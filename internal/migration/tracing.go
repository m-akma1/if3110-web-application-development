package migration

import (
	"context"

	"dbplatform/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Span names of the migration path. One trace reads:
//
//	HTTP request -> job.enqueue -> job.process (one per attempt)
//	  -> migration.lock_wait, migration.run | migration.batch (xN) -> sql <kind>
const (
	spanEnqueue  = "job.enqueue"
	spanProcess  = "job.process"
	spanRecover  = "job.recover" // the reaper settling a job whose lease expired
	spanLockWait = "migration.lock_wait"
	spanRun      = "migration.run"
	spanDryRun   = "migration.dry_run"
	spanBatch    = "migration.batch"
)

func startSpan(ctx context.Context, name string, kind trace.SpanKind, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracing.Tracer().Start(ctx, name, trace.WithSpanKind(kind), trace.WithAttributes(attrs...))
}

// failSpan marks span as failed with err (nil leaves it unset).
func failSpan(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
