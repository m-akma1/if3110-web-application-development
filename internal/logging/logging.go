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
//
// Every record also goes to each handler in export (for example the OpenTelemetry log bridge,
// see tracing.SetupLogs), at the same level, with the same context attributes and the same
// redaction, so nothing reaches an exporter that stdout would not show.
func New(w io.Writer, level slog.Level, export ...slog.Handler) *slog.Logger {
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
	var h slog.Handler = slog.NewJSONHandler(w, opts)
	if len(export) > 0 {
		f := &fanout{handlers: []slog.Handler{h}}
		for _, e := range export {
			f.handlers = append(f.handlers, &redactHandler{Handler: e, level: level})
		}
		h = f
	}
	return slog.New(&contextHandler{Handler: h})
}

// fanout sends each record to every handler that accepts its level.
type fanout struct{ handlers []slog.Handler }

func (f *fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f *fanout) Handle(ctx context.Context, r slog.Record) error {
	var first error
	for _, h := range f.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		// An exporter that fails must not stop the stdout log (or the other way round).
		if err := h.Handle(ctx, r.Clone()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (f *fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := &fanout{handlers: make([]slog.Handler, len(f.handlers))}
	for i, h := range f.handlers {
		out.handlers[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f *fanout) WithGroup(name string) slog.Handler {
	out := &fanout{handlers: make([]slog.Handler, len(f.handlers))}
	for i, h := range f.handlers {
		out.handlers[i] = h.WithGroup(name)
	}
	return out
}

// redactHandler applies the JSON logger's level and redaction to a handler that has neither.
type redactHandler struct {
	slog.Handler
	level slog.Level
}

func (h *redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.level && h.Handler.Enabled(ctx, l)
}

func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		// An OpenTelemetry log record carries the trace and span of ctx in its own fields; the
		// copies contextHandler adds for stdout would only be duplicates there.
		if a.Key != "trace_id" && a.Key != "span_id" {
			out.AddAttrs(redact(a))
		}
		return true
	})
	return h.Handler.Handle(ctx, out)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = redact(a)
	}
	return &redactHandler{Handler: h.Handler.WithAttrs(clean), level: h.level}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{Handler: h.Handler.WithGroup(name), level: h.level}
}

// redact replaces the value of a sensitive attribute, also inside groups.
func redact(a slog.Attr) slog.Attr {
	if isSensitive(a.Key) {
		return slog.String(a.Key, "[REDACTED]")
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		clean := make([]any, len(group))
		for i, g := range group {
			clean[i] = redact(g)
		}
		return slog.Group(a.Key, clean...)
	}
	return a
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
