// Package logging builds the platform's structured JSON logger (log/slog) and carries
// per-request data (correlation ID, actor) through context.Context. Records written inside an
// OpenTelemetry span also get its trace_id and span_id.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// ParseLevel turns "debug", "info", "warn" or "error" into a slog.Level.
// Unknown values mean info
func ParseLevel(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.TrimSpace(s))); err != nil {
		return slog.LevelInfo
	}
	return l
}

// sensitiveKey are substring of attribute key whose values are never written
var sensitiveKey = []string{
	"password", "passwd", "secret", "token", "session",
	"cookie", "authorization", "dsn", "credential",
}

func isSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKey {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// New returns a JSON logger writing to w. The message key is renamed to "event", sensitive
// attributes are redacted, and correlation_id/actor are added from the context.
func New(w io.Writer, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.MessageKey {
				a.Key = "event"
			}
			if isSensitive(a.Key) {
				return slog.String(a.Key, "[REDACTED]")
			}
			return a
		},
	}
	return slog.New(&contextHandler{Handler: slog.NewJSONHandler(w, opts)})
}

type ctxkey struct{}

// RequestInfo is filled during one request. The middleware creates it; handlers set Actor
// once the session is known. Only the request's own goroutine may touch it.
type RequestInfo struct {
	CorrelationID string
	Actor         string
}

// WithRequestInfo stores info in ctx
func WithRequestInfo(ctx context.Context, info *RequestInfo) context.Context {
	return context.WithValue(ctx, ctxkey{}, info)
}

// Info returns the RequestInfo in ctx, or nil
func Info(ctx context.Context) *RequestInfo {
	info, _ := ctx.Value(ctxkey{}).(*RequestInfo)
	return info
}

// SetActor record the user acting in this request
func SetActor(ctx context.Context, userID string) {
	if info := Info(ctx); info != nil {
		info.Actor = userID
	}
}

// contextHandler adds correlation_id and actor from the context, and trace_id/span_id of the
// current span, to every record.
type contextHandler struct{ slog.Handler }

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	if info := Info(ctx); info != nil {
		if info.CorrelationID != "" {
			r.AddAttrs(slog.String("correlation_id", info.CorrelationID))
		}
		if info.Actor != "" {
			r.AddAttrs(slog.String("actor", info.Actor))
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attr []slog.Attr) slog.Handler {
	return &contextHandler{h.Handler.WithAttrs(attr)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{h.Handler.WithGroup(name)}
}

func fatal(event string, err error) {
	slog.Error(event, slog.String("error", err.Error()))
	os.Exit(1)
}
