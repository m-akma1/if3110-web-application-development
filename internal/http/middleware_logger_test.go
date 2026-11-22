package http

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		contains string
		excludes string
	}{
		{
			name:     "plain url without query",
			rawURL:   "/api/health",
			contains: "/api/health",
		},
		{
			name:     "query with password redacted",
			rawURL:   "/login?username=alice&password=secretpassword123",
			contains: "password=[REDACTED]",
			excludes: "secretpassword123",
		},
		{
			name:     "query with token and session_id redacted",
			rawURL:   "/callback?token=jwt.token.here&session_id=sess_456&page=2",
			contains: "page=2",
			excludes: "jwt.token.here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("parse url: %v", err)
			}
			res := SanitizeURL(u)
			if tt.contains != "" && !strings.Contains(res, tt.contains) {
				t.Errorf("SanitizeURL(%s) = %s, want containing %s", tt.rawURL, res, tt.contains)
			}
			if tt.excludes != "" && strings.Contains(res, tt.excludes) {
				t.Errorf("SanitizeURL(%s) = %s, should NOT contain %s", tt.rawURL, res, tt.excludes)
			}
		})
	}
}

func TestStripLogNewlines(t *testing.T) {
	in := "GET\r\nINJECTED_LINE: true\n"
	got := stripLogNewlines(in)
	if strings.Contains(got, "\r") || strings.Contains(got, "\n") {
		t.Errorf("stripLogNewlines failed to strip CRLF: %q", got)
	}
	if got != "GETINJECTED_LINE: true" {
		t.Errorf("stripLogNewlines = %q, want %q", got, "GETINJECTED_LINE: true")
	}
}

func TestSanitizedLoggerMiddleware_LogInjectionMitigation(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	handler := SanitizedLoggerMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/dashboard?token=supersecret", nil)
	// Simulate an attacker injecting CRLF into method or path
	req.Method = "GET\r\nFAKE_LOG_ENTRY"
	req.URL.Path = "/dashboard\r\nFAKE_PATH"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	logged := buf.String()
	trimmed := strings.TrimRight(logged, "\r\n")
	if strings.Contains(trimmed, "\n") || strings.Contains(trimmed, "\r") {
		t.Errorf("Logged output contains unescaped newline (log injection): %q", logged)
	}
	if strings.Contains(logged, "supersecret") {
		t.Errorf("Logged output leaked sensitive token: %q", logged)
	}
}
