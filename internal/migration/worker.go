package migration

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"dbplatform/internal/ids"
	"dbplatform/internal/logging"
)

// WorkerConfig tunes the worker pool. Zero fields take the DefaultWorkerConfig value.
type WorkerConfig struct {
	Concurrency  int           // jobs processed at the same time (on different databases)
	PollInterval time.Duration // wait between claims when the queue is empty
	Lease        time.Duration // how long a claimed job belongs to its worker without a heartbeat
	BaseBackoff  time.Duration // first retry delay; doubles every attempt
	MaxBackoff   time.Duration // retry delay cap
	ReapInterval time.Duration // how often expired leases are recovered
}

// DefaultWorkerConfig suits the demo deployment.
func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		Concurrency:  4,
		PollInterval: 500 * time.Millisecond,
		Lease:        30 * time.Second,
		BaseBackoff:  2 * time.Second,
		MaxBackoff:   time.Minute,
		ReapInterval: 10 * time.Second,
	}
}

func (c WorkerConfig) normalized() WorkerConfig {
	d := DefaultWorkerConfig()
	if c.Concurrency <= 0 {
		c.Concurrency = d.Concurrency
	}
	if c.PollInterval <= 0 {
		c.PollInterval = d.PollInterval
	}
	if c.Lease <= 0 {
		c.Lease = d.Lease
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = d.BaseBackoff
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = d.MaxBackoff
	}
	if c.ReapInterval <= 0 {
		c.ReapInterval = d.ReapInterval
	}
	return c
}

// Worker takes jobs from migration_jobs and runs them with the Engine.
type Worker struct {
	repo   *Repository
	engine *Engine
	cfg    WorkerConfig
	logger *slog.Logger
}

// NewWorker builds a worker pool and registers the engine hook that records when SQL starts.
func NewWorker(repo *Repository, engine *Engine, cfg WorkerConfig) *Worker {
	engine.SetRunStartHook(repo.MarkSQLStarted)
	return &Worker{repo: repo, engine: engine, cfg: cfg.normalized(), logger: slog.Default()}
}

// SetLogger replaces the worker's logger. Nil is ignored.
func (w *Worker) SetLogger(l *slog.Logger) {
	if l != nil {
		w.logger = l
	}
}

// Run starts cfg.Concurrency workers and the lease reaper, and blocks until ctx is cancelled
// and every job in progress has finished.
func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 1; i <= w.cfg.Concurrency; i++ {
		id := fmt.Sprintf("worker-%d-%s", i, ids.New()[:8])
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.loop(ctx, id)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.reapLoop(ctx)
	}()
	w.logger.InfoContext(ctx, "worker.started", slog.Int("concurrency", w.cfg.Concurrency))
	wg.Wait()
	w.logger.Info("worker.stopped")
}

func (w *Worker) loop(ctx context.Context, workerID string) {
	for ctx.Err() == nil {
		worked, err := w.RunOnce(ctx, workerID)
		if err != nil && ctx.Err() == nil {
			w.logger.ErrorContext(ctx, "worker.claim_failed", slog.String("worker_id", workerID), slog.String("error", err.Error()))
		}
		if worked {
			continue // there may be more work: claim again right away
		}
		select {
		case <-ctx.Done():
		case <-time.After(w.cfg.PollInterval):
		}
	}
}

// RunOnce claims one due job and processes it. It reports whether a job was processed.
// Tests call it directly to run the queue one step at a time.
func (w *Worker) RunOnce(ctx context.Context, workerID string) (bool, error) {
	j, err := w.repo.ClaimNextJob(ctx, workerID, w.cfg.Lease)
	if err != nil || j == nil {
		return false, err
	}
	w.process(ctx, workerID, j)
	return true, nil
}

// process runs one claimed job to a final or retry state.
func (w *Worker) process(ctx context.Context, workerID string, j *Job) {
	// A started job finishes even during shutdown: the engine already ignores cancellation once
	// it holds the lock, and the job row must be updated to match. A batch run is the exception:
	// it stops at the next batch boundary and the job goes back to the queue.
	shutdown := ctx.Done()
	ctx = WithInterrupt(context.WithoutCancel(ctx), shutdown)
	// The job keeps the request's correlation ID and actor, so its logs join the HTTP request's.
	ctx = logging.WithRequestInfo(ctx, &logging.RequestInfo{CorrelationID: j.CorrelationID, Actor: j.ExecutorID})
	// The engine stores the job and attempt on the execution row it writes.
	ctx = WithRun(ctx, j.ID, j.Attempt)
	log := w.logger.With(
		slog.String("job_id", j.ID), slog.String("migration_id", j.MigrationID),
		slog.String("database_id", j.DatabaseID), slog.String("job_type", string(j.Type)),
		slog.Int("attempt", j.Attempt), slog.String("worker_id", workerID))
	log.InfoContext(ctx, "job.claimed")

	stop := w.heartbeat(ctx, log, j.ID, workerID)
	defer stop()

	// Idempotency: a duplicate delivery or a recovered job whose result is already in the
	// history is closed without running the SQL again.
	if done, err := w.repo.OutcomeRecorded(ctx, j); err == nil && done {
		w.finish(ctx, log, j, JobSuccess, "hasil sudah tercatat di riwayat; SQL tidak dijalankan ulang")
		return
	}

	res, err := w.execute(ctx, j)
	switch {
	case res != nil && res.OK && err == nil:
		w.finish(ctx, log, j, JobSuccess, "")
	case res != nil && res.Interrupted:
		// Shutdown between batches: committed batches are checkpointed, so the next run of this
		// job continues after them. Waiting is not an attempt.
		w.requeue(ctx, log, j, errors.New("worker berhenti di antara batch; dilanjutkan dari checkpoint"), 0, false)
	case res != nil && res.Partial:
		// Batches were committed, then one failed: the migration is PARTIAL until a user resumes
		// or aborts it. Not retried automatically: the failure is usually in the data or script.
		w.finish(ctx, log, j, JobPartial, res.Err.String())
	case res != nil && !res.OK:
		// The script failed and its transaction was rolled back: a final result, never retried.
		msg := "script gagal"
		if res.Err != nil {
			msg = res.Err.String()
		}
		w.finish(ctx, log, j, JobFailed, msg)
	case res != nil:
		// COMMIT happened but something after it failed (usually the history write).
		if done, cerr := w.repo.OutcomeRecorded(ctx, j); cerr == nil && done {
			w.finish(ctx, log, j, JobSuccess, "")
			return
		}
		w.recordAttempt(ctx, log, j, ExecUncertain, true, err.Error())
		w.finish(ctx, log, j, JobUncertain, err.Error())
	case errors.Is(err, ErrTargetBusy):
		// Not the job's fault and not an attempt: give it back and try again soon. No execution
		// row either: the job only waited (job.retry_scheduled is logged).
		w.requeue(ctx, log, j, err, w.cfg.PollInterval, false)
	case isTransient(err) && j.Attempt < j.MaxAttempts:
		w.recordAttempt(ctx, log, j, ExecFailed, false, err.Error())
		w.requeue(ctx, log, j, err, w.backoff(j.Attempt), true)
	default:
		// Validation errors (no longer the latest migration, not approved, ...) and transient
		// errors after the last attempt. No SQL ran, so the migration can be tried again later.
		w.recordAttempt(ctx, log, j, ExecFailed, false, err.Error())
		w.finish(ctx, log, j, JobFailed, err.Error())
		if rerr := w.repo.RestoreMigrationStatus(ctx, j); rerr != nil {
			log.ErrorContext(ctx, "job.restore_failed", slog.String("error", rerr.Error()))
		}
	}
}

// execute calls the engine method for the job type. The engine re-reads and revalidates the
// migration under the database lock, so a stale job cannot break LIFO/linear ordering.
// A nil RunResult means no script was sent to the target.
func (w *Worker) execute(ctx context.Context, j *Job) (*RunResult, error) {
	var res *RunResult
	var err error
	switch j.Type {
	case JobExecute:
		_, res, err = w.engine.Execute(ctx, j.MigrationID, j.ExecutorID)
	case JobRollback:
		_, res, err = w.engine.ExecuteRollback(ctx, j.MigrationID, j.ExecutorID)
	case JobForward:
		_, res, err = w.engine.ExecuteForward(ctx, j.MigrationID, j.ExecutorID)
	case JobResume:
		_, res, err = w.engine.Execute(ctx, j.MigrationID, j.ExecutorID)
	case JobAbort:
		_, res, err = w.engine.ExecuteAbort(ctx, j.MigrationID, j.ExecutorID)
	default:
		err = fmt.Errorf("job_type tidak dikenal: %s", j.Type)
	}
	return res, err
}

// heartbeat extends the job's lease every Lease/3 until stop is called, so a long migration is
// never taken over by the reaper while its worker is alive.
func (w *Worker) heartbeat(ctx context.Context, log *slog.Logger, jobID, workerID string) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(w.cfg.Lease / 3)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				ok, err := w.repo.ExtendLease(ctx, jobID, workerID, w.cfg.Lease)
				if err != nil {
					log.WarnContext(ctx, "job.heartbeat_failed", slog.String("error", err.Error()))
				} else if !ok {
					log.ErrorContext(ctx, "job.lease_lost")
					return
				}
			}
		}
	}()
	return func() { close(done); <-finished }
}

// recordAttempt appends the execution row of an attempt the engine wrote no row for (see
// Repository.RecordJobAttempt) and writes its audit event. A failure here is logged and does
// not change the job's outcome.
func (w *Worker) recordAttempt(ctx context.Context, log *slog.Logger, j *Job, status ExecStatus, sqlSent bool, cause string) {
	ex, err := w.repo.RecordJobAttempt(ctx, j, status, sqlSent, cause)
	if err != nil {
		log.ErrorContext(ctx, "job.history_failed", slog.String("error", err.Error()))
		return
	}
	auditExecution(ctx, w.logger, ex)
}

func (w *Worker) finish(ctx context.Context, log *slog.Logger, j *Job, status JobStatus, msg string) {
	if err := w.repo.FinishJob(ctx, j.ID, status, msg); err != nil {
		log.ErrorContext(ctx, "job.update_failed", slog.String("error", err.Error()))
		return
	}
	attrs := []any{slog.String("status", string(status))}
	if msg != "" {
		attrs = append(attrs, slog.String("error", msg))
	}
	switch status {
	case JobSuccess:
		log.InfoContext(ctx, "job.finished", attrs...)
	case JobFailed, JobPartial:
		log.WarnContext(ctx, "job.finished", attrs...)
	default:
		// UNCERTAIN holds every later job on this database until an operator checks the target.
		log.ErrorContext(ctx, "job.uncertain", attrs...)
	}
}

func (w *Worker) requeue(ctx context.Context, log *slog.Logger, j *Job, cause error, delay time.Duration, countAttempt bool) {
	if err := w.repo.RequeueJob(ctx, j.ID, cause.Error(), delay, countAttempt); err != nil {
		log.ErrorContext(ctx, "job.update_failed", slog.String("error", err.Error()))
		return
	}
	log.WarnContext(ctx, "job.retry_scheduled",
		slog.Int64("delay_ms", delay.Milliseconds()), slog.Bool("counts_as_attempt", countAttempt),
		slog.String("error", cause.Error()))
}

// isBatchJob reports whether j runs per batch (resume and abort jobs always do).
func (w *Worker) isBatchJob(ctx context.Context, j *Job) bool {
	if j.Type == JobResume || j.Type == JobAbort {
		return true
	}
	m, err := w.repo.Get(ctx, j.MigrationID)
	return err == nil && m.IsBatch()
}

// backoff is BaseBackoff * 2^(attempt-1), capped at MaxBackoff.
func (w *Worker) backoff(attempt int) time.Duration {
	d := w.cfg.BaseBackoff
	for i := 1; i < attempt && d < w.cfg.MaxBackoff; i++ {
		d *= 2
	}
	return min(d, w.cfg.MaxBackoff)
}

// isTransient reports errors that happen before any SQL reaches the target and may succeed on
// a later attempt: the target is down or a connection broke.
func isTransient(err error) bool {
	var netErr net.Error
	return errors.Is(err, ErrTargetUnavailable) || errors.Is(err, driver.ErrBadConn) || errors.As(err, &netErr)
}

func (w *Worker) reapLoop(ctx context.Context) {
	t := time.NewTicker(w.cfg.ReapInterval)
	defer t.Stop()
	for {
		// Also runs once at start: jobs left RUNNING by a crashed process are recovered on restart.
		if n, err := w.RecoverStaleJobs(ctx); err != nil && ctx.Err() == nil {
			w.logger.ErrorContext(ctx, "worker.reap_failed", slog.String("error", err.Error()))
		} else if n > 0 {
			w.logger.WarnContext(ctx, "worker.reaped", slog.Int("jobs", n))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RecoverStaleJobs handles RUNNING jobs whose lease expired (their worker died):
//   - outcome already in the history: SUCCESS, nothing runs again;
//   - no SQL was sent yet: back to the queue;
//   - SQL may have run but no outcome was recorded: UNCERTAIN, and the database is held.
func (w *Worker) RecoverStaleJobs(ctx context.Context) (int, error) {
	stale, err := w.repo.StaleJobIDs(ctx)
	if err != nil {
		return 0, err
	}
	reaperID := "reaper-" + ids.New()[:8]
	n := 0
	for _, id := range stale {
		j, err := w.repo.TakeStaleJob(ctx, id, reaperID, w.cfg.Lease)
		if err != nil {
			return n, err
		}
		if j == nil {
			continue // its worker extended the lease in the meantime
		}
		jctx := logging.WithRequestInfo(ctx, &logging.RequestInfo{CorrelationID: j.CorrelationID, Actor: j.ExecutorID})
		log := w.logger.With(slog.String("job_id", j.ID), slog.String("migration_id", j.MigrationID),
			slog.String("database_id", j.DatabaseID), slog.String("job_type", string(j.Type)), slog.Int("attempt", j.Attempt))
		done, err := w.repo.OutcomeRecorded(jctx, j)
		switch {
		case err != nil:
			return n, err
		case done:
			w.finish(jctx, log, j, JobSuccess, "dipulihkan: hasil sudah tercatat sebelum worker berhenti")
		case j.SQLStartedAt != nil && w.isBatchJob(jctx, j):
			// Every batch commits together with its checkpoint, so nothing is uncertain: run the
			// job again and it continues after the last checkpoint.
			const cause = "worker berhenti di tengah batch; dilanjutkan dari checkpoint"
			w.recordAttempt(jctx, log, j, ExecPartial, true, cause)
			if j.Attempt < j.MaxAttempts {
				w.requeue(jctx, log, j, errors.New(cause), 0, true)
			} else if status, err := w.repo.StopBatchJob(jctx, j); err != nil {
				log.ErrorContext(jctx, "job.update_failed", slog.String("error", err.Error()))
			} else {
				w.finish(jctx, log, j, status, cause)
			}
		case j.SQLStartedAt == nil && j.Attempt < j.MaxAttempts:
			const cause = "lease worker habis sebelum SQL dijalankan"
			w.recordAttempt(jctx, log, j, ExecFailed, false, cause)
			w.requeue(jctx, log, j, errors.New(cause), 0, true)
		case j.SQLStartedAt == nil:
			const cause = "lease worker habis setelah percobaan terakhir"
			w.recordAttempt(jctx, log, j, ExecFailed, false, cause)
			w.finish(jctx, log, j, JobFailed, cause)
			_ = w.repo.RestoreMigrationStatus(jctx, j)
		default:
			const cause = "worker berhenti setelah SQL dikirim; hasil di database target belum pasti"
			w.recordAttempt(jctx, log, j, ExecUncertain, true, cause)
			w.finish(jctx, log, j, JobUncertain, cause)
		}
		n++
	}
	return n, nil
}
