package tracing

import (
	"context"
	"testing"

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
