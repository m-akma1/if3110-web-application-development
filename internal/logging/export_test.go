package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
)

// captureHandler keeps every record it is given, with the attributes added by WithAttrs.
type captureHandler struct {
	mu      *sync.Mutex
	records *[]map[string]string
	attrs   []slog.Attr
}

func newCapture() *captureHandler {
	return &captureHandler{mu: &sync.Mutex{}, records: &[]map[string]string{}}
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	m := map[string]string{"event": r.Message, "level": r.Level.String()}
	for _, a := range h.attrs {
		m[a.Key] = a.Value.String()
	}
	r.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	*h.records = append(*h.records, m)
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &captureHandler{mu: h.mu, records: h.records, attrs: append(append([]slog.Attr{}, h.attrs...), attrs...)}
}

func (h *captureHandler) WithGroup(string) slog.Handler { return h }

func TestExportGetsTheSameRedactedRecords(t *testing.T) {
	recordSpans(t)
	export := newCapture()
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo, export).With(slog.String("component", "worker"), slog.String("api_token", "t0k"))
	ctx := WithRequestInfo(context.Background(), &RequestInfo{CorrelationID: "req-7", Actor: "user-1"})
	ctx, span := otel.Tracer("test").Start(ctx, "work")
	defer span.End()

	l.InfoContext(ctx, "job.claimed", slog.String("password", "rahasia"), slog.Group("db", slog.String("dsn", "postgres://u:p@h")))
	l.DebugContext(ctx, "debug.hidden")

	if len(*export.records) != 1 {
		t.Fatalf("export got %d records, want 1 (debug is below the level)", len(*export.records))
	}
	got := (*export.records)[0]
	if got["event"] != "job.claimed" || got["correlation_id"] != "req-7" || got["actor"] != "user-1" || got["component"] != "worker" {
		t.Fatalf("export record = %v, want the event and the context attributes", got)
	}
	for _, k := range []string{"password", "api_token"} {
		if got[k] != "[REDACTED]" {
			t.Errorf("%s = %q in the exported record, want [REDACTED]", k, got[k])
		}
	}
	if got["db"] == "" || strings.Contains(got["db"], "postgres://") {
		t.Errorf("group attribute db = %q, want the dsn inside it redacted", got["db"])
	}
	if _, dup := got["trace_id"]; dup {
		t.Error("trace_id must not be copied into exported records: OTel records carry it natively")
	}
	if lines := decodeLines(t, &buf); len(lines) != 1 || lines[0]["trace_id"] == nil {
		t.Fatalf("stdout = %v, want one record with trace_id", lines)
	}
}
