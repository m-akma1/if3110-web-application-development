package http

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"dbplatform/internal/logging"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	oldTP, oldProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(oldTP)
		otel.SetTextMapPropagator(oldProp)
	})
	return rec
}

func TestAPIRequestSpanIsNamedByRouteAndLogged(t *testing.T) {
	rec := recordSpans(t)
	var buf bytes.Buffer
	srv := NewServer(&buf, Deps{Logger: logging.New(&buf, slog.LevelInfo)})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("X-Request-ID", "trace-test-1")
	// A client-supplied parent must not be trusted on a public API.
	const clientParent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	req.Header.Set("traceparent", clientParent)
	srv.ServeHTTP(httptest.NewRecorder(), req)

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	sp := spans[0]
	if sp.Name() != "GET /api/health" {
		t.Fatalf("span name = %q, want the route pattern", sp.Name())
	}
	if sp.Parent().IsValid() || sp.SpanContext().TraceID().String() == "0af7651916cd43dd8448eb211c80319c" {
		t.Fatal("client traceparent was used as parent")
	}
	if len(sp.Links()) != 1 {
		t.Fatalf("links = %d, want the client span as a link", len(sp.Links()))
	}
	attrs := map[string]string{}
	for _, kv := range sp.Attributes() {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}
	if attrs["correlation_id"] != "trace-test-1" || attrs["http.route"] != "GET /api/health" {
		t.Fatalf("span attributes = %v", attrs)
	}

	var access map[string]any
	for _, l := range decodeLogLines(t, &buf) {
		if l["event"] == "http.request" {
			access = l
		}
	}
	if access == nil || access["trace_id"] != sp.SpanContext().TraceID().String() {
		t.Fatalf("http.request record = %v, want trace_id %s", access, sp.SpanContext().TraceID())
	}
}

func TestOnlyAPIRequestsAreTraced(t *testing.T) {
	rec := recordSpans(t)
	var buf bytes.Buffer
	srv := NewServer(&buf, Deps{Logger: logging.New(&buf, slog.LevelInfo)})
	for _, path := range []string{"/", "/dashboard", "/static/img/auth-bg.svg"} {
		srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if n := len(rec.Ended()); n != 0 {
		t.Fatalf("non-API requests produced %d spans", n)
	}
}
