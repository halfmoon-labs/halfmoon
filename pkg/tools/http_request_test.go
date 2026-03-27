package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/halfmoon-labs/halfmoon/pkg/config"
)

// testServerHost extracts just the hostname (without port) from a test server URL.
func testServerHost(serverURL string) string {
	u, _ := url.Parse(serverURL)
	return u.Hostname()
}

// newTestHTTPRequestTool creates an HTTPRequestTool for testing without full config loading.
func newTestHTTPRequestTool(
	t *testing.T, domains []string, profiles map[string]config.HTTPAuthProfile,
) *HTTPRequestTool {
	t.Helper()

	cfg := config.DefaultConfig()
	cfg.Tools.HTTPRequest.AllowedDomains = domains
	cfg.Tools.HTTPRequest.MaxResponseBytes = 1 << 20
	cfg.Tools.HTTPRequest.TimeoutSeconds = 5
	if profiles != nil {
		cfg.Tools.HTTPRequest.SetAuthProfiles(profiles)
	}

	tool, err := NewHTTPRequestTool(cfg)
	require.NoError(t, err)
	return tool
}

func TestHTTPRequest_GET_Success(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer server.Close()

	host := testServerHost(server.URL)
	tool := newTestHTTPRequestTool(t, []string{host}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    server.URL + "/api/test",
	})

	assert.False(t, result.IsError, "expected success, got error: %s", result.ForLLM)
	assert.True(t, result.Silent, "expected SilentResult")
	assert.Empty(t, result.ForUser, "expected no ForUser output")
	assert.Contains(t, result.ForLLM, "Status: 200 OK")
	assert.Contains(t, result.ForLLM, `{"status":"ok"}`)
	assert.Contains(t, result.ForLLM, "Content-Type: application/json")
}

func TestHTTPRequest_POST_WithBody(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	var receivedBody string
	var receivedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":1}`)
	}))
	defer server.Close()

	host := testServerHost(server.URL)
	tool := newTestHTTPRequestTool(t, []string{host}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method":  "POST",
		"url":     server.URL + "/api/items",
		"headers": map[string]any{"Content-Type": "application/json"},
		"body":    `{"name":"test"}`,
	})

	assert.False(t, result.IsError, "expected success, got error: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "Status: 201")
	assert.Equal(t, `{"name":"test"}`, receivedBody)
	assert.Equal(t, "application/json", receivedContentType)
}

func TestHTTPRequest_AllMethods(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			var receivedMethod string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedMethod = r.Method
				w.WriteHeader(http.StatusOK)
				if r.Method != "HEAD" {
					fmt.Fprint(w, "ok")
				}
			}))
			defer server.Close()

			host := testServerHost(server.URL)
			tool := newTestHTTPRequestTool(t, []string{host}, nil)

			result := tool.Execute(context.Background(), map[string]any{
				"method": method,
				"url":    server.URL,
			})

			assert.False(t, result.IsError, "expected success for %s, got: %s", method, result.ForLLM)
			assert.Equal(t, method, receivedMethod)
		})
	}
}

func TestHTTPRequest_DomainDenied(t *testing.T) {
	tool := newTestHTTPRequestTool(t, []string{"api.github.com"}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    "https://evil.com/steal",
	})

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "domain not in allowed list")
}

func TestHTTPRequest_NoDomains_DenyAll(t *testing.T) {
	tool := newTestHTTPRequestTool(t, nil, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    "https://httpbin.org/get",
	})

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "domain not in allowed list")
}

func TestHTTPRequest_WildcardDomain(t *testing.T) {
	tool := newTestHTTPRequestTool(t, []string{"*.example.com"}, nil)

	tests := []struct {
		name    string
		host    string
		allowed bool
	}{
		{"subdomain matches", "api.example.com", true},
		{"deep subdomain matches", "a.b.example.com", true},
		{"bare domain does not match", "example.com", false},
		{"unrelated domain denied", "example.org", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.allowed, tool.isDomainAllowed(tt.host))
		})
	}
}

func TestHTTPRequest_DomainCaseInsensitive(t *testing.T) {
	tool := newTestHTTPRequestTool(t, []string{"api.github.com"}, nil)

	assert.True(t, tool.isDomainAllowed("API.GitHub.COM"))
	assert.True(t, tool.isDomainAllowed("api.github.com"))
	assert.True(t, tool.isDomainAllowed("Api.GitHub.Com"))
}

func TestHTTPRequest_DomainWithPort(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	host := testServerHost(server.URL)
	tool := newTestHTTPRequestTool(t, []string{host}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    server.URL,
	})

	assert.False(t, result.IsError, "expected success when port is stripped for domain match: %s", result.ForLLM)
}

func TestHTTPRequest_Auth_Header(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	host := testServerHost(server.URL)
	profiles := map[string]config.HTTPAuthProfile{
		"github": {Type: "header", Key: "Authorization", Value: "Bearer ghp_test123"},
	}
	tool := newTestHTTPRequestTool(t, []string{host}, profiles)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    server.URL,
		"auth":   "github",
	})

	assert.False(t, result.IsError, "expected success, got: %s", result.ForLLM)
	assert.Equal(t, "Bearer ghp_test123", receivedAuth)
}

func TestHTTPRequest_Auth_Query(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	var receivedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.Query().Get("appid")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	host := testServerHost(server.URL)
	profiles := map[string]config.HTTPAuthProfile{
		"weather": {Type: "query", Key: "appid", Value: "abc123"},
	}
	tool := newTestHTTPRequestTool(t, []string{host}, profiles)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    server.URL + "/weather",
		"auth":   "weather",
	})

	assert.False(t, result.IsError, "expected success, got: %s", result.ForLLM)
	assert.Equal(t, "abc123", receivedQuery)
}

func TestHTTPRequest_Auth_NotFound(t *testing.T) {
	tool := newTestHTTPRequestTool(t, []string{"example.com"}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    "https://example.com/api",
		"auth":   "nonexistent",
	})

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "auth profile not found: nonexistent")
}

func TestHTTPRequest_MissingMethod(t *testing.T) {
	tool := newTestHTTPRequestTool(t, []string{"example.com"}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"url": "https://example.com",
	})

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "method is required")
}

func TestHTTPRequest_MissingURL(t *testing.T) {
	tool := newTestHTTPRequestTool(t, []string{"example.com"}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
	})

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "url is required")
}

func TestHTTPRequest_InvalidMethod(t *testing.T) {
	tool := newTestHTTPRequestTool(t, []string{"example.com"}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "TRACE",
		"url":    "https://example.com",
	})

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "unsupported HTTP method")
}

func TestHTTPRequest_InvalidURL(t *testing.T) {
	tool := newTestHTTPRequestTool(t, []string{"example.com"}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    "://bad-url",
	})

	assert.True(t, result.IsError)
}

func TestHTTPRequest_NonHTTPScheme(t *testing.T) {
	tool := newTestHTTPRequestTool(t, []string{"example.com"}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    "ftp://example.com/file",
	})

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "only http and https")
}

func TestHTTPRequest_SSRF_PrivateIP(t *testing.T) {
	// Ensure private hosts are NOT allowed (default production behavior).
	previous := allowPrivateWebFetchHosts.Load()
	allowPrivateWebFetchHosts.Store(false)
	t.Cleanup(func() { allowPrivateWebFetchHosts.Store(previous) })

	tool := newTestHTTPRequestTool(t, []string{"127.0.0.1", "localhost"}, nil)

	tests := []struct {
		name string
		url  string
	}{
		{"loopback IP", "http://127.0.0.1/admin"},
		{"localhost", "http://localhost/admin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.Execute(context.Background(), map[string]any{
				"method": "GET",
				"url":    tt.url,
			})
			assert.True(t, result.IsError, "expected SSRF block for %s", tt.url)
		})
	}
}

func TestHTTPRequest_ResponseSizeLimit(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	// Server returns 2KB response.
	largeBody := strings.Repeat("x", 2048)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, largeBody)
	}))
	defer server.Close()

	host := testServerHost(server.URL)
	cfg := config.DefaultConfig()
	cfg.Tools.HTTPRequest.AllowedDomains = []string{host}
	cfg.Tools.HTTPRequest.MaxResponseBytes = 1024 // 1KB limit
	cfg.Tools.HTTPRequest.TimeoutSeconds = 5

	tool, err := NewHTTPRequestTool(cfg)
	require.NoError(t, err)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    server.URL,
	})

	// Should succeed but with truncation note.
	assert.False(t, result.IsError, "expected success with truncation, got error: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "[Response truncated")
}

func TestHTTPRequest_Timeout(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	host := testServerHost(server.URL)
	cfg := config.DefaultConfig()
	cfg.Tools.HTTPRequest.AllowedDomains = []string{host}
	cfg.Tools.HTTPRequest.MaxResponseBytes = 1 << 20
	cfg.Tools.HTTPRequest.TimeoutSeconds = 1 // 1 second timeout

	tool, err := NewHTTPRequestTool(cfg)
	require.NoError(t, err)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    server.URL,
	})

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "request failed")
}

func TestHTTPRequest_RedirectToBlockedDomain(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.com/steal", http.StatusFound)
	}))
	defer server.Close()

	host := testServerHost(server.URL)
	tool := newTestHTTPRequestTool(t, []string{host}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    server.URL,
	})

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "redirect target domain not in allowed list")
}

func TestHTTPRequest_RedirectToPrivateIP(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1/internal", http.StatusFound)
	}))
	defer server.Close()

	host := testServerHost(server.URL)
	tool := newTestHTTPRequestTool(t, []string{host}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    server.URL,
	})

	assert.True(t, result.IsError)
}

func TestHTTPRequest_ResponseFormat(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"key":"value"}`)
	}))
	defer server.Close()

	host := testServerHost(server.URL)
	tool := newTestHTTPRequestTool(t, []string{host}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    server.URL,
	})

	assert.False(t, result.IsError)

	lines := strings.SplitN(result.ForLLM, "\n", 4)
	require.GreaterOrEqual(t, len(lines), 3, "expected at least status + header + blank + body")
	assert.True(t, strings.HasPrefix(lines[0], "Status: 200"), "first line should be status")
	// There should be a blank line separating headers from body.
	assert.Contains(t, result.ForLLM, "\n\n")
	assert.Contains(t, result.ForLLM, `{"key":"value"}`)
}

func TestHTTPRequest_HEAD_NoBody(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "42")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	host := testServerHost(server.URL)
	tool := newTestHTTPRequestTool(t, []string{host}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "HEAD",
		"url":    server.URL,
	})

	assert.False(t, result.IsError, "expected success, got: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "Status: 200 OK")
	assert.Contains(t, result.ForLLM, "Content-Type: application/json")

	// After the blank line separator, there should be no body content.
	parts := strings.SplitN(result.ForLLM, "\n\n", 2)
	require.Len(t, parts, 2)
	assert.Empty(t, parts[1], "HEAD response should have no body")
}

func TestHTTPRequest_IsDomainAllowed(t *testing.T) {
	tests := []struct {
		name     string
		domains  []string
		hostname string
		want     bool
	}{
		{"exact match", []string{"api.github.com"}, "api.github.com", true},
		{"exact no match", []string{"api.github.com"}, "api.gitlab.com", false},
		{"wildcard match", []string{"*.github.com"}, "api.github.com", true},
		{"wildcard deep subdomain", []string{"*.github.com"}, "a.b.github.com", true},
		{"wildcard bare domain no match", []string{"*.github.com"}, "github.com", false},
		{"empty domains", nil, "anything.com", false},
		{"empty domain entry", []string{""}, "anything.com", false},
		{"case insensitive", []string{"API.GitHub.COM"}, "api.github.com", true},
		{"multiple domains first match", []string{"a.com", "b.com"}, "b.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &HTTPRequestTool{allowedDomains: tt.domains}
			assert.Equal(t, tt.want, tool.isDomainAllowed(tt.hostname))
		})
	}
}

func TestHTTPRequest_BlockedHeaders(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	host := testServerHost(server.URL)
	tool := newTestHTTPRequestTool(t, []string{host}, nil)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    server.URL,
		"headers": map[string]any{
			"Host":              "evil.com",
			"Authorization":     "Bearer stolen",
			"Transfer-Encoding": "chunked",
			"Content-Length":    "999",
			"X-Custom":          "allowed",
		},
	})

	assert.False(t, result.IsError, "got error: %s", result.ForLLM)
	assert.Equal(t, "allowed", receivedHeaders.Get("X-Custom"))
	assert.Empty(t, receivedHeaders.Get("Authorization"))
	assert.NotEqual(t, "evil.com", receivedHeaders.Get("Host"))
}

func TestHTTPRequest_Auth_EmptyKey(t *testing.T) {
	profiles := map[string]config.HTTPAuthProfile{
		"bad": {Type: "header", Key: "", Value: "token"},
	}
	tool := newTestHTTPRequestTool(t, []string{"example.com"}, profiles)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    "https://example.com/api",
		"auth":   "bad",
	})

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "empty key")
}

func TestHTTPRequest_Auth_UnsupportedType(t *testing.T) {
	profiles := map[string]config.HTTPAuthProfile{
		"bad": {Type: "bearer", Key: "Authorization", Value: "token"},
	}
	tool := newTestHTTPRequestTool(t, []string{"example.com"}, profiles)

	result := tool.Execute(context.Background(), map[string]any{
		"method": "GET",
		"url":    "https://example.com/api",
		"auth":   "bad",
	})

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "unsupported auth type")
}

func TestRedactQueryParam(t *testing.T) {
	tests := []struct {
		name string
		s    string
		key  string
		want string
	}{
		{
			"redacts value",
			"https://api.example.com?appid=secret123&foo=bar",
			"appid",
			"https://api.example.com?appid=[REDACTED]&foo=bar",
		},
		{
			"redacts at end of string",
			"https://api.example.com?appid=secret123",
			"appid",
			"https://api.example.com?appid=[REDACTED]",
		},
		{
			"no match returns unchanged",
			"https://api.example.com?other=val",
			"appid",
			"https://api.example.com?other=val",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, redactQueryParam(tt.s, tt.key))
		})
	}
}
