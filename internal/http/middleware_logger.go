package http

import (
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"dbplatform/internal/ids"
	"dbplatform/internal/logging"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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

func SanitizedLoggerMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			// lanjutkan ke middleware/handler berikutnya
			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			switch {
			case rec.statusCode >= 500:
				level = slog.LevelError
			case rec.statusCode >= 400:
				level = slog.LevelWarn
			}
			logger.LogAttrs(r.Context(), level, "http.request",
				slog.String("method", stripLogNewlines(r.Method)),
				slog.String("path", stripLogNewlines(SanitizeURL(r.URL))),
				slog.Int("status", rec.statusCode),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
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

func stripLogNewlines(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// RequestContextMiddleware gives every request a correlation ID (reusing a well-formed
// X-Request-ID from the client) and a logging.RequestInfo in its context.
func RequestContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID.MatchString(id) {
			id = ids.New()
		}
		w.Header().Set("X-Request-ID", id)
		// The correlation ID is also on the request's span, so a trace can be found from a log line.
		trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("correlation_id", id))
		ctx := logging.WithRequestInfo(r.Context(), &logging.RequestInfo{CorrelationID: id})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
