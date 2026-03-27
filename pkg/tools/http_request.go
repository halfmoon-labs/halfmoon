package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/halfmoon-labs/halfmoon/pkg/config"
	"github.com/halfmoon-labs/halfmoon/pkg/logger"
	"github.com/halfmoon-labs/halfmoon/pkg/utils"
)

const (
	httpRequestMaxRedirects    = 10
	httpRequestDefaultTimeout  = 30 * time.Second
	httpRequestDefaultMaxBytes = int64(1 << 20) // 1MB
)

// blockedRequestHeaders are headers the LLM must not set directly via
// the "headers" parameter. Auth must go through profiles; Host and
// transport headers could enable request smuggling or domain bypass.
var blockedRequestHeaders = map[string]bool{
	"host":              true,
	"authorization":     true,
	"transfer-encoding": true,
	"content-length":    true,
}

var allowedHTTPMethods = map[string]bool{
	"GET":    true,
	"POST":   true,
	"PUT":    true,
	"PATCH":  true,
	"DELETE": true,
	"HEAD":   true,
}

// HTTPRequestTool makes raw HTTP requests to external APIs.
// Deny-by-default: requests are only allowed to explicitly configured domains.
type HTTPRequestTool struct {
	allowedDomains   []string
	maxResponseBytes int64
	authProfiles     map[string]config.HTTPAuthProfile
	client           *http.Client
	whitelist        *privateHostWhitelist
}

// NewHTTPRequestTool creates an HTTPRequestTool from config.
func NewHTTPRequestTool(cfg *config.Config) (*HTTPRequestTool, error) {
	httpCfg := cfg.Tools.HTTPRequest

	timeout := time.Duration(httpCfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = httpRequestDefaultTimeout
	}

	maxBytes := httpCfg.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = httpRequestDefaultMaxBytes
	}

	client, err := utils.CreateHTTPClient(cfg.Tools.Web.Proxy, timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client for http_request: %w", err)
	}

	whitelist, err := newPrivateHostWhitelist(cfg.Tools.Web.PrivateHostWhitelist)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private host whitelist: %w", err)
	}

	// Inject SSRF-safe dialer at transport level.
	if transport, ok := client.Transport.(*http.Transport); ok {
		dialer := &net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		transport.DialContext = newSafeDialContext(dialer, whitelist)
	}

	tool := &HTTPRequestTool{
		allowedDomains:   httpCfg.AllowedDomains,
		maxResponseBytes: maxBytes,
		authProfiles:     httpCfg.AuthProfiles(),
		client:           client,
		whitelist:        whitelist,
	}

	// Block redirects to private hosts or non-allowed domains.
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= httpRequestMaxRedirects {
			return fmt.Errorf("stopped after %d redirects", httpRequestMaxRedirects)
		}
		if isObviousPrivateHost(req.URL.Hostname(), whitelist) {
			return fmt.Errorf("redirect target is private or local network host")
		}
		if !tool.isDomainAllowed(req.URL.Hostname()) {
			return fmt.Errorf("redirect target domain not in allowed list: %s", req.URL.Hostname())
		}
		return nil
	}

	return tool, nil
}

func (t *HTTPRequestTool) Name() string { return "http_request" }

func (t *HTTPRequestTool) Description() string {
	return "Make HTTP requests to external APIs. Supports GET, POST, PUT, PATCH, DELETE, HEAD. " +
		"Use the auth parameter to apply a configured authentication profile by name. " +
		"Returns the raw response. Only works with explicitly allowed domains."
}

func (t *HTTPRequestTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"method": map[string]any{
				"type":        "string",
				"description": "HTTP method",
				"enum":        []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"},
			},
			"url": map[string]any{
				"type":        "string",
				"description": "Full URL to request",
			},
			"headers": map[string]any{
				"type":        "object",
				"description": "Custom request headers as key-value pairs",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "Request body (for POST, PUT, PATCH)",
			},
			"auth": map[string]any{
				"type":        "string",
				"description": "Name of an authentication profile configured in .security.yml",
			},
		},
		"required": []string{"method", "url"},
	}
}

func (t *HTTPRequestTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	method, _ := args["method"].(string)
	if method == "" {
		return ErrorResult("method is required")
	}
	method = strings.ToUpper(method)
	if !allowedHTTPMethods[method] {
		return ErrorResult(fmt.Sprintf(
			"unsupported HTTP method: %s (allowed: GET, POST, PUT, PATCH, DELETE, HEAD)", method,
		))
	}

	urlStr, _ := args["url"].(string)
	if urlStr == "" {
		return ErrorResult("url is required")
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid URL: %v", err))
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return ErrorResult("only http and https URLs are allowed")
	}
	if parsedURL.Host == "" {
		return ErrorResult("missing host in URL")
	}

	hostname := parsedURL.Hostname()

	// Domain allowlist check (deny-by-default).
	if !t.isDomainAllowed(hostname) {
		return ErrorResult(fmt.Sprintf("domain not in allowed list: %s", hostname))
	}

	// SSRF pre-flight check.
	if isObviousPrivateHost(hostname, t.whitelist) {
		return ErrorResult("requests to private or local network hosts are not allowed")
	}

	// Resolve auth profile if specified.
	var authProfile *config.HTTPAuthProfile
	if authName, _ := args["auth"].(string); authName != "" {
		profile, ok := t.authProfiles[authName]
		if !ok {
			return ErrorResult(fmt.Sprintf("auth profile not found: %s", authName))
		}
		if profile.Key == "" {
			return ErrorResult(fmt.Sprintf(
				"auth profile %q has an empty key", authName,
			))
		}
		authType := strings.ToLower(profile.Type)
		if authType != "header" && authType != "query" {
			return ErrorResult(fmt.Sprintf(
				"unsupported auth type: %s (allowed: header, query)", profile.Type,
			))
		}
		authProfile = &profile
		if authType == "query" {
			q := parsedURL.Query()
			q.Set(profile.Key, profile.Value)
			parsedURL.RawQuery = q.Encode()
			urlStr = parsedURL.String()
		}
	}

	// Build request.
	var bodyReader io.Reader
	if body, ok := args["body"].(string); ok && body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, bodyReader)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create request: %v", err))
	}

	req.Header.Set("User-Agent", fmt.Sprintf(userAgentHonest, config.Version))

	// Apply custom headers, blocking sensitive ones.
	if headers, ok := args["headers"].(map[string]any); ok {
		for k, v := range headers {
			if blockedRequestHeaders[strings.ToLower(k)] {
				continue
			}
			if s, ok := v.(string); ok {
				req.Header.Set(k, s)
			}
		}
	}

	// Apply header auth after custom headers (auth takes precedence).
	if authProfile != nil && strings.EqualFold(authProfile.Type, "header") {
		req.Header.Set(authProfile.Key, authProfile.Value)
	}

	// Audit log: request initiated.
	logFields := map[string]any{
		"method": method,
		"host":   hostname,
		"path":   parsedURL.Path,
	}
	if authProfile != nil {
		logFields["auth_profile"] = args["auth"]
	}
	logger.DebugCF("tool", "http_request executing", logFields)

	// Execute request.
	resp, err := t.client.Do(req)
	if err != nil {
		errMsg := err.Error()
		// Redact query-auth token from error messages if present.
		if authProfile != nil &&
			strings.EqualFold(authProfile.Type, "query") {
			errMsg = redactQueryParam(errMsg, authProfile.Key)
		}
		logger.WarnCF("tool", "http_request failed", map[string]any{
			"method": method,
			"host":   hostname,
		})
		return ErrorResult(fmt.Sprintf("request failed: %s", errMsg))
	}
	defer resp.Body.Close()

	logger.InfoCF("tool", "http_request completed", map[string]any{
		"method": method,
		"host":   hostname,
		"status": resp.StatusCode,
	})

	// Read response with size cap.
	limitedBody := http.MaxBytesReader(nil, resp.Body, t.maxResponseBytes)
	bodyBytes, readErr := io.ReadAll(limitedBody)

	// Format response for LLM.
	var sb strings.Builder
	fmt.Fprintf(&sb, "Status: %s\n", resp.Status)

	// Include useful response headers.
	for _, h := range []string{
		"Content-Type", "Content-Length", "Location", "Retry-After",
	} {
		if v := resp.Header.Get(h); v != "" {
			fmt.Fprintf(&sb, "%s: %s\n", h, v)
		}
	}
	sb.WriteString("\n")

	if method != "HEAD" {
		if readErr != nil && len(bodyBytes) > 0 {
			sb.Write(bodyBytes)
			sb.WriteString("\n\n[Response truncated: exceeded size limit]")
		} else if readErr != nil {
			fmt.Fprintf(&sb, "[Error reading response body: %v]", readErr)
		} else {
			sb.Write(bodyBytes)
		}
	}

	return SilentResult(sb.String())
}

// isDomainAllowed checks if hostname is in the allowed domains list.
// Returns false if no domains are configured (deny-by-default).
// Callers must pass a bare hostname without port (e.g. from url.URL.Hostname()).
func (t *HTTPRequestTool) isDomainAllowed(hostname string) bool {
	if len(t.allowedDomains) == 0 {
		return false
	}

	h := strings.ToLower(strings.TrimSpace(hostname))

	for _, domain := range t.allowedDomains {
		d := strings.ToLower(strings.TrimSpace(domain))
		if d == "" {
			continue
		}

		if strings.HasPrefix(d, "*.") {
			// Wildcard: *.example.com matches sub.example.com but not example.com
			suffix := d[1:] // ".example.com"
			if strings.HasSuffix(h, suffix) && h != suffix[1:] {
				return true
			}
		} else if h == d {
			return true
		}
	}

	return false
}

// redactQueryParam replaces the value of a specific query parameter
// in a string (typically an error message) with "[REDACTED]".
// This prevents auth tokens injected via query-type auth profiles
// from leaking into LLM-visible error messages.
func redactQueryParam(s, key string) string {
	encoded := url.QueryEscape(key) + "="
	idx := strings.Index(s, encoded)
	if idx == -1 {
		return s
	}
	start := idx + len(encoded)
	end := strings.IndexAny(s[start:], "&# ")
	if end == -1 {
		return s[:start] + "[REDACTED]"
	}
	return s[:start] + "[REDACTED]" + s[start+end:]
}
