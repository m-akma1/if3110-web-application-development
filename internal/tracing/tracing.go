// Package tracing sets up OpenTelemetry tracing for the platform and carries trace context
// through the migration job queue (W3C traceparent stored on the job row).
//
// One trace follows a migration from the HTTP request that queues it, through the worker that
// claims the job, the database lock, and every batch and SQL statement on the target.
package tracing

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// InstrumentationName names the tracer every package of the platform uses.
const InstrumentationName = "dbplatform"

// Exporters accepted in OTEL_TRACES_EXPORTER (console, otlp, none) and OTEL_LOGS_EXPORTER
// (otlp, none).
const (
	ExporterConsole = "console" // one JSON object per span on stdout
	ExporterNone    = "none"    // spans are recorded (so logs carry trace IDs) but not exported
	// ExporterOTLP sends spans or log records over OTLP/HTTP to OTEL_EXPORTER_OTLP_ENDPOINT
	// (e.g. http://otel-collector:4318), read by the exporter itself, like the other standard
	// OTEL_EXPORTER_OTLP_* variables.
	ExporterOTLP = "otlp"
)

// Config selects the exporters and names the service on every span and log record.
type Config struct {
	Exporter     string // OTEL_TRACES_EXPORTER: console (default), otlp or none
	LogsExporter string // OTEL_LOGS_EXPORTER: none (default; stdout only) or otlp (stdout and OTLP)
	ServiceName  string // OTEL_SERVICE_NAME, default "dbplatform"
	// Writer receives console spans; nil means os.Stdout, next to the JSON logs.
	Writer io.Writer
}

// DefaultConfig exports spans to the console.
func DefaultConfig() Config {
	return Config{Exporter: ExporterConsole, ServiceName: InstrumentationName}
}

// propagator is the W3C Trace Context format used for HTTP headers and the job queue.
var propagator = propagation.TraceContext{}

// Setup installs the global tracer provider and propagator. The returned shutdown flushes
// buffered spans; call it after the last span ends (after the worker stops).
func Setup(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	res, err := newResource(cfg)
	if err != nil {
		return nil, err
	}
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	}
	switch cfg.Exporter {
	case "", ExporterConsole:
		w := cfg.Writer
		if w == nil {
			w = os.Stdout
		}
		// Compact output: one line per span, so `jq -R 'fromjson?'` reads logs and spans alike.
		exp, err := stdouttrace.New(stdouttrace.WithWriter(w))
		if err != nil {
			return nil, err
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	case ExporterOTLP:
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, err
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	case ExporterNone:
	default:
		return nil, fmt.Errorf("unknown trace exporter %q", cfg.Exporter)
	}
	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagator)
	return tp.Shutdown, nil
}

func newResource(cfg Config) (*resource.Resource, error) {
	if cfg.ServiceName == "" {
		cfg.ServiceName = InstrumentationName
	}
	return resource.Merge(resource.Default(),
		resource.NewSchemaless(attribute.String("service.name", cfg.ServiceName)))
}

// SetupLogs returns a slog.Handler that exports log records over OTLP (OTEL_LOGS_EXPORTER=otlp)
// through the otelslog bridge, for logging.New to use next to stdout. Records keep the trace and
// span of their context, so a log backend can link them to the trace. With none it returns a nil
// handler. The returned shutdown flushes buffered records.
func SetupLogs(ctx context.Context, cfg Config) (slog.Handler, func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	switch cfg.LogsExporter {
	case "", ExporterNone:
		return nil, noop, nil
	case ExporterOTLP:
	default:
		return nil, noop, fmt.Errorf("unknown logs exporter %q", cfg.LogsExporter)
	}
	res, err := newResource(cfg)
	if err != nil {
		return nil, noop, err
	}
	exp, err := otlploghttp.New(ctx)
	if err != nil {
		return nil, noop, err
	}
	lp := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
	return otelslog.NewHandler(InstrumentationName, otelslog.WithLoggerProvider(lp)), lp.Shutdown, nil
}

// Tracer returns the platform tracer from the global provider (a no-op one until Setup runs).
func Tracer() trace.Tracer {
	return otel.Tracer(InstrumentationName)
}

// Inject returns the W3C traceparent of the span in ctx, or "" when there is none.
// The job queue stores it so the worker can continue the trace.
func Inject(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagator.Inject(ctx, carrier)
	return carrier.Get("traceparent")
}

// Extract returns ctx with the remote span described by traceparent as its parent.
// An empty or malformed traceparent leaves ctx unchanged (the worker starts a new trace).
func Extract(ctx context.Context, traceparent string) context.Context {
	if traceparent == "" {
		return ctx
	}
	return propagator.Extract(ctx, propagation.MapCarrier{"traceparent": traceparent})
}
