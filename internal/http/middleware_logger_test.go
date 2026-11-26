package http

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dbplatform/internal/logging"
)

// decodeLogLines parses one JSON object per line; a line that is not JSON fails the test.
func decodeLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if ln == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("log line is not valid JSON: %q (%v)", ln, err)
		}
		out = append(out, m)
	}
	return out
}

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
	logger := logging.New(&buf, slog.LevelInfo)

	handler := SanitizedLoggerMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/dashboard?token=supersecret", nil)
	// Simulate an attacker injecting CRLF into method or path
	req.Method = "GET\r\nFAKE_LOG_ENTRY"
	req.URL.Path = "/dashboard\r\nFAKE_PATH"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	lines := decodeLogLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 log line (no injected line), got %d: %q", len(lines), buf.String())
	}
	got := lines[0]
	if got["event"] != "http.request" {
		t.Errorf("event = %v, want http.request", got["event"])
	}
	// encoding/json decodes every JSON number as float64.
	if got["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want %d (key must be lowercase \"status\")", got["status"], http.StatusOK)
	}
	if _, ok := got["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms missing or not a number: %v", got["duration_ms"])
	}
	if m, _ := got["method"].(string); strings.ContainsAny(m, "\r\n") {
		t.Errorf("method still contains CR/LF: %q", m)
	}
	if strings.Contains(buf.String(), "supersecret") {
		t.Errorf("Logged output leaked sensitive token: %q", buf.String())
	}
}

func TestSanitizedLoggerMiddleware_LevelFromStatus(t *testing.T) {
	tests := []struct {
		status int
		level  string
	}{
		{http.StatusOK, "INFO"},
		{http.StatusNotFound, "WARN"},
		{http.StatusInternalServerError, "ERROR"},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		handler := SanitizedLoggerMiddleware(logging.New(&buf, slog.LevelInfo))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
		}))
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))

		lines := decodeLogLines(t, &buf)
		if len(lines) != 1 || lines[0]["level"] != tt.level {
			t.Errorf("status %d: want level %s, got %v", tt.status, tt.level, lines)
		}
	}
}

func TestRequestContextMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, slog.LevelInfo)

	// Same order as NewServer: RequestContext (outermost) -> access log -> handler.
	var inner http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logging.SetActor(r.Context(), "user-1")
		logger.InfoContext(r.Context(), "handler.ran")
		w.WriteHeader(http.StatusOK)
	})
	handler := RequestContextMiddleware(SanitizedLoggerMiddleware(logger)(inner))

	tests := []struct {
		name     string
		headerID string
		wantSame bool
	}{
		{"valid client id is reused", "abc-123.X_y", true},
		{"missing id gets generated", "", false},
		{"invalid id is replaced", "bad id\r\nFAKE", false},
		{"too long id is replaced", strings.Repeat("a", 65), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			req := httptest.NewRequest("GET", "/api/x", nil)
			if tt.headerID != "" {
				req.Header.Set("X-Request-ID", tt.headerID)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			id := rec.Header().Get("X-Request-ID")
			if id == "" {
				t.Fatal("response has no X-Request-ID header")
			}
			if tt.wantSame && id != tt.headerID {
				t.Errorf("X-Request-ID = %q, want client value %q", id, tt.headerID)
			}
			if !tt.wantSame && id == tt.headerID {
				t.Errorf("invalid client id %q was accepted", tt.headerID)
			}

			lines := decodeLogLines(t, &buf)
			if len(lines) != 2 {
				t.Fatalf("want 2 log lines (handler + access log), got %d: %q", len(lines), buf.String())
			}
			// Both lines carry the same correlation ID; the access log is written after the
			// handler, so it also sees the actor set inside the handler (shared *RequestInfo).
			for _, ln := range lines {
				if ln["correlation_id"] != id {
					t.Errorf("event %v: correlation_id = %v, want %q", ln["event"], ln["correlation_id"], id)
				}
				if ln["actor"] != "user-1" {
					t.Errorf("event %v: actor = %v, want user-1", ln["event"], ln["actor"])
				}
			}
		})
	}
}
