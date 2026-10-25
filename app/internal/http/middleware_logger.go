package http

import (
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Daftar kata sensitif yang muncul di URL query log
var sensitiveKeys = []string{
	"password", "pass", "pwd",
	"session", "sid", "session_id", "token",
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.statusCode = code
	rec.ResponseWriter.WriteHeader(code)
}

func SanitizedLoggerMiddleware(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			// lanjutkan ke middleware/handler berikutnya
			next.ServeHTTP(rec, r)

			// bersihkan URl dari kemungkinan kebocoran password atau session ID di Query String
			safePath := SanitizeURL(r.URL)

			// catat informasi aman tidak ada cookie, set cookie, body
			logger.Printf("[HTTP] method=%s path=%s status=%d duration=%s",
				r.Method,
				safePath,
				rec.statusCode,
				time.Since(start).Round(time.Microsecond),
			)
		})
	}
}

func SanitizeURL(u *url.URL) string {
	if u.RawQuery == "" {
		return u.Path
	}

	q := u.Query()
	for key := range q {
		lowerKey := strings.ToLower(key)
		for _, sensitive := range sensitiveKeys {
			if strings.Contains(lowerKey, sensitive) {
				q.Set(key, "[REDACTED]")
				break
			}
		}
	}

	// kembalikan path yang sudah di sensor
	sanitizeQuery := strings.ReplaceAll(q.Encode(), "%5BREDACTED%5D", "[REDACTED]")
	return u.Path + "?" + sanitizeQuery

}
