package migration

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// recordSpans installs an in-memory tracer provider for one test and restores the old one.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(old) })
	return rec
}

// spansOf returns the ended spans of one trace, by name (the last one wins).
func spansOf(rec *tracetest.SpanRecorder, id trace.TraceID) map[string]sdktrace.ReadOnlySpan {
	out := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		if s.SpanContext().TraceID() == id {
			out[s.Name()] = s
		}
	}
	return out
}

func requireChild(t *testing.T, spans map[string]sdktrace.ReadOnlySpan, child, parent string) {
	t.Helper()
	c, p := spans[child], spans[parent]
	if c == nil || p == nil {
		t.Fatalf("missing span %q or %q; have %v", child, parent, names(spans))
	}
	if c.Parent().SpanID() != p.SpanContext().SpanID() {
		t.Fatalf("%s parent = %s, want %s (%s)", child, c.Parent().SpanID(), parent, p.SpanContext().SpanID())
	}
}

func names(spans map[string]sdktrace.ReadOnlySpan) []string {
	var out []string
	for n := range spans {
		out = append(out, n)
	}
	return out
}

// The trace crosses the queue: the worker's span continues the enqueue span of the request.
func TestTraceFollowsJobFromEnqueueToSQL(t *testing.T) {
	rec := recordSpans(t)
	f := newFixture(t, Limits{})
	d := f.draft(t, "orders", "CREATE TABLE orders (id int)")

	reqCtx, req := otel.Tracer("test").Start(context.Background(), "POST /api/migration/{id}/execute")
	j, err := f.repo.EnqueueJob(reqCtx, EnqueueInput{
		MigrationID: d.ID, DatabaseID: d.DatabaseID, Type: JobExecute, ExecutorID: f.userID, MarkQueued: true,
	})
	req.End()
	if err != nil {
		t.Fatal(err)
	}
	if j.TraceParent == "" {
		t.Fatal("job has no trace_parent")
	}

	// The worker runs later, with a context that knows nothing about the request.
	if worked, err := fastWorker(f).RunOnce(context.Background(), "w1"); err != nil || !worked {
		t.Fatalf("RunOnce = %v, %v", worked, err)
	}

	spans := spansOf(rec, req.SpanContext().TraceID())
	requireChild(t, spans, spanEnqueue, "POST /api/migration/{id}/execute")
	requireChild(t, spans, spanProcess, spanEnqueue)
	requireChild(t, spans, spanLockWait, spanProcess)
	requireChild(t, spans, spanRun, spanProcess)
	requireChild(t, spans, "sql migration", spanRun)

	if k := spans[spanEnqueue].SpanKind(); k != trace.SpanKindProducer {
		t.Errorf("enqueue kind = %v, want producer", k)
	}
	if k := spans[spanProcess].SpanKind(); k != trace.SpanKindConsumer {
		t.Errorf("process kind = %v, want consumer", k)
	}
	if s := spans[spanProcess].Status().Code; s == codes.Error {
		t.Errorf("successful job span has error status")
	}
	var committed bool
	for _, ev := range spans[spanRun].Events() {
		committed = committed || ev.Name == "tx.commit"
	}
	if !committed {
		t.Error("migration.run has no tx.commit event")
	}
}

func TestTraceHasOneSpanPerBatch(t *testing.T) {
	rec := recordSpans(t)
	f := newFixture(t, Limits{})
	f.items(t, 2500)
	m := f.batchDraft(t, 1000)

	reqCtx, req := otel.Tracer("test").Start(context.Background(), "request")
	if _, err := f.repo.EnqueueJob(reqCtx, EnqueueInput{
		MigrationID: m.ID, DatabaseID: m.DatabaseID, Type: JobExecute, ExecutorID: f.userID, MarkQueued: true,
	}); err != nil {
		t.Fatal(err)
	}
	req.End()
	if _, err := fastWorker(f).RunOnce(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	var process trace.SpanID
	var batches, batchSQL int
	for _, s := range rec.Ended() {
		if s.SpanContext().TraceID() != req.SpanContext().TraceID() {
			continue
		}
		switch s.Name() {
		case spanProcess:
			process = s.SpanContext().SpanID()
		case spanBatch:
			batches++
		case "sql migration_batch":
			batchSQL++
		}
	}
	// 3 batches commit; a 4th finds no keys left and ends the run without a statement.
	if batches != 4 || batchSQL != 3 {
		t.Fatalf("batch spans = %d, batch statements = %d; want 4 and 3", batches, batchSQL)
	}
	for _, s := range rec.Ended() {
		if s.Name() == spanBatch && s.Parent().SpanID() != process {
			t.Fatalf("batch span parent = %s, want job.process %s", s.Parent().SpanID(), process)
		}
	}
}

// A retried job keeps its trace: every attempt is a job.process span under the same enqueue.
func TestTraceKeepsRetriesInOneTrace(t *testing.T) {
	rec := recordSpans(t)
	f := newFixture(t, Limits{})
	d := f.draft(t, "orders", "CREATE TABLE orders (id int)")
	f.pools.Remove(f.dbID) // the target is down: retried with backoff, then FAILED

	reqCtx, req := otel.Tracer("test").Start(context.Background(), "request")
	j, err := f.repo.EnqueueJob(reqCtx, EnqueueInput{
		MigrationID: d.ID, DatabaseID: d.DatabaseID, Type: JobExecute, ExecutorID: f.userID, MarkQueued: true,
	})
	req.End()
	if err != nil {
		t.Fatal(err)
	}
	w := fastWorker(f)
	for i := 0; i < 20 && !f.job(t, j.ID).Done(); i++ {
		if _, err := w.RunOnce(context.Background(), "w1"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond) // wait for next_run_at
	}
	if got := f.job(t, j.ID); got.Status != JobFailed {
		t.Fatalf("job = %s, want FAILED after its retries", got.Status)
	}

	enqueue := spansOf(rec, req.SpanContext().TraceID())[spanEnqueue]
	attempts := map[int64]bool{}
	for _, s := range rec.Ended() {
		if s.Name() != spanProcess || s.SpanContext().TraceID() != req.SpanContext().TraceID() {
			continue
		}
		if s.Parent().SpanID() != enqueue.SpanContext().SpanID() {
			t.Errorf("attempt span parent = %s, want job.enqueue", s.Parent().SpanID())
		}
		if s.Status().Code != codes.Error {
			t.Errorf("failed attempt span status = %v, want error", s.Status().Code)
		}
		for _, kv := range s.Attributes() {
			if kv.Key == "attempt" {
				attempts[kv.Value.AsInt64()] = true
			}
		}
	}
	if len(attempts) != j.MaxAttempts {
		t.Fatalf("attempt spans = %v, want %d distinct attempts in one trace", attempts, j.MaxAttempts)
	}
}
