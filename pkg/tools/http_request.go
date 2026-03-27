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
	"github.com/halfmoon-labs/halfmoon/pkg/utils"
)

const (
	httpRequestMaxRedirects    = 10
	httpRequestDefaultTimeout  = 30 * time.Second
	httpRequestDefaultMaxBytes = int64(1 << 20) // 1MB
)

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
	timeout          time.Duration
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
		timeout:          timeout,
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

	// Apply auth profile if specified.
	authName, _ := args["auth"].(string)
	if authName != "" {
		profile, ok := t.authProfiles[authName]
		if !ok {
			return ErrorResult(fmt.Sprintf("auth profile not found: %s", authName))
		}
		switch strings.ToLower(profile.Type) {
		case "header":
			// Will be set on the request below.
		case "query":
			q := parsedURL.Query()
			q.Set(profile.Key, profile.Value)
			parsedURL.RawQuery = q.Encode()
			urlStr = parsedURL.String()
		default:
			return ErrorResult(fmt.Sprintf("unsupported auth type: %s (allowed: header, query)", profile.Type))
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

	// Apply custom headers.
	if headers, ok := args["headers"].(map[string]any); ok {
		for k, v := range headers {
			if s, ok := v.(string); ok {
				req.Header.Set(k, s)
			}
		}
	}

	// Apply header auth after custom headers (auth takes precedence).
	if authName != "" {
		if profile, ok := t.authProfiles[authName]; ok && strings.EqualFold(profile.Type, "header") {
			req.Header.Set(profile.Key, profile.Value)
		}
	}

	// Execute request.
	resp, err := t.client.Do(req)
	if err != nil {
		return ErrorResult(fmt.Sprintf("request failed: %v", err))
	}
	defer resp.Body.Close()

	// Read response with size cap.
	limitedBody := http.MaxBytesReader(nil, resp.Body, t.maxResponseBytes)
	bodyBytes, readErr := io.ReadAll(limitedBody)

	// Format response for LLM.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Status: %s\n", resp.Status))

	// Include useful response headers.
	for _, h := range []string{"Content-Type", "Content-Length", "Location", "Retry-After"} {
		if v := resp.Header.Get(h); v != "" {
			sb.WriteString(fmt.Sprintf("%s: %s\n", h, v))
		}
	}
	sb.WriteString("\n")

	if method != "HEAD" {
		if readErr != nil && len(bodyBytes) > 0 {
			sb.Write(bodyBytes)
			sb.WriteString("\n\n[Response truncated: exceeded size limit]")
		} else if readErr != nil {
			sb.WriteString(fmt.Sprintf("[Error reading response body: %v]", readErr))
		} else {
			sb.Write(bodyBytes)
		}
	}

	return SilentResult(sb.String())
}

// isDomainAllowed checks if hostname is in the allowed domains list.
// Returns false if no domains are configured (deny-by-default).
func (t *HTTPRequestTool) isDomainAllowed(hostname string) bool {
	if len(t.allowedDomains) == 0 {
		return false
	}

	// Strip port if present.
	h := strings.ToLower(strings.TrimSpace(hostname))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}

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
