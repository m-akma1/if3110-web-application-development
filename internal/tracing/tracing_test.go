package tracing

import (
	"context"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestInjectExtractRoundTrip(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	ctx, span := tp.Tracer("test").Start(context.Background(), "enqueue")
	defer span.End()

	tp1 := Inject(ctx)
	if tp1 == "" {
		t.Fatal("Inject returned an empty traceparent for a recording span")
	}
	remote := trace.SpanContextFromContext(Extract(context.Background(), tp1))
	if !remote.IsValid() || !remote.IsRemote() {
		t.Fatalf("extracted span context = %+v, want a valid remote context", remote)
	}
	if remote.TraceID() != span.SpanContext().TraceID() || remote.SpanID() != span.SpanContext().SpanID() {
		t.Fatalf("extracted %s/%s, want %s/%s", remote.TraceID(), remote.SpanID(),
			span.SpanContext().TraceID(), span.SpanContext().SpanID())
	}
}

func TestInjectWithoutSpan(t *testing.T) {
	if got := Inject(context.Background()); got != "" {
		t.Fatalf("Inject without a span = %q, want empty", got)
	}
}

func TestExtractIgnoresBadInput(t *testing.T) {
	for _, in := range []string{"", "not-a-traceparent", "00-zzzz-0000-01"} {
		if sc := trace.SpanContextFromContext(Extract(context.Background(), in)); sc.IsValid() {
			t.Errorf("Extract(%q) gave a valid span context", in)
		}
	}
}

func TestSetupRejectsUnknownExporter(t *testing.T) {
	if _, err := Setup(context.Background(), Config{Exporter: "zipkin"}); err == nil {
		t.Fatal("Setup accepted an unknown exporter")
	}
}

func TestSetupLogs(t *testing.T) {
	ctx := context.Background()
	if h, _, err := SetupLogs(ctx, Config{}); err != nil || h != nil {
		t.Fatalf("default = %v, %v; want no handler (stdout only)", h, err)
	}
	if _, _, err := SetupLogs(ctx, Config{LogsExporter: "loki"}); err == nil {
		t.Fatal("SetupLogs accepted an unknown exporter")
	}
	// Creating the OTLP exporter does not connect; records are sent in the background.
	h, shutdown, err := SetupLogs(ctx, Config{LogsExporter: ExporterOTLP})
	if err != nil || h == nil {
		t.Fatalf("otlp = %v, %v; want a handler", h, err)
	}
	sctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_ = shutdown(sctx)
}
