package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"dbplatform/internal/logging"
	"dbplatform/internal/metrics"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Batch directions: UP runs the migration's script, DOWN runs its SQL Down over the same key
// ranges, newest batch first (abort with rollback).
const (
	DirUp   = "UP"
	DirDown = "DOWN"
)

// checkpointTable lives on the target, in the first schema of the connection's search_path.
// Each batch inserts its row in the same transaction as its data change, so a batch is either
// committed together with its checkpoint or not at all. The platform's migration_batches is a
// copy, synchronised from this table at the start of every run.
const checkpointTable = "_dbplatform_batch_checkpoints"

// recoveryTable lives next to checkpointTable. Before an UP batch of an UPDATE changes its rows,
// the batch stores their before-image (to_jsonb of the whole row) here, in the same transaction
// (see BatchPlan.Capture). An abort with rollback uses it to check, batch by batch, that SQL Down
// really put every row back; a DOWN batch deletes its rows when it commits, and the rest are
// purged when the migration is final (SUCCESS or ABORTED).
const recoveryTable = "_dbplatform_batch_rows"

// checkpointDDL creates (or upgrades) both tables. One script, sent with the simple protocol.
const checkpointDDL = `CREATE TABLE IF NOT EXISTS ` + checkpointTable + ` (
	migration_id  UUID        NOT NULL,
	direction     TEXT        NOT NULL,
	batch_no      INTEGER     NOT NULL,
	cursor_from   BIGINT,
	cursor_to     BIGINT      NOT NULL,
	rows_affected BIGINT      NOT NULL,
	committed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	PRIMARY KEY (migration_id, direction, batch_no));
ALTER TABLE ` + checkpointTable + ` ADD COLUMN IF NOT EXISTS recovery_rows BIGINT;
CREATE TABLE IF NOT EXISTS ` + recoveryTable + ` (
	migration_id UUID    NOT NULL,
	batch_no     INTEGER NOT NULL,
	row_key      BIGINT  NOT NULL,
	before       JSONB   NOT NULL);
CREATE INDEX IF NOT EXISTS ` + recoveryTable + `_batch_idx ON ` + recoveryTable + ` (migration_id, batch_no)`

var (
	// ErrBatchRowsChanged means rows started to match the batch between its capture and its
	// statement (a concurrent write). The batch was rolled back; running it again is safe.
	ErrBatchRowsChanged = errors.New("baris pada rentang batch berubah oleh transaksi lain selama batch berjalan; batch di-ROLLBACK, jalankan lagi")
	// ErrUnsafeAbort means SQL Down did not put the rows of a batch back as they were before the
	// migration. That batch was rolled back and the abort stopped (still PARTIAL).
	ErrUnsafeAbort = errors.New("pembatalan batch dihentikan karena tidak aman")
)

// Batch is one committed batch (a row of migration_batches).
type Batch struct {
	Direction   string
	No          int
	ExecutionID string
	From        *int64 // exclusive; nil for the first batch
	To          int64  // inclusive
	Rows        int64
	CommittedAt time.Time
	// RecoveryRows is how many before-images an UP batch stored in recoveryTable; nil when it
	// kept none (irreversible migration, DOWN batch, or committed before recovery data existed).
	RecoveryRows *int64
}

type interruptKey struct{}

// WithInterrupt lets a batch run stop between batches when done is closed: the worker passes
// its shutdown signal, so a long migration pauses at a checkpoint instead of delaying shutdown.
func WithInterrupt(ctx context.Context, done <-chan struct{}) context.Context {
	return context.WithValue(ctx, interruptKey{}, done)
}

func interrupted(ctx context.Context) bool {
	done, _ := ctx.Value(interruptKey{}).(<-chan struct{})
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// ExecuteAbort undoes the committed batches of a PARTIAL batch migration with its SQL Down,
// newest batch first. When every batch is undone the migration becomes ABORTED; when it stops
// early it stays PARTIAL and reads as PARTIALLY_REVERTED.
func (e *Engine) ExecuteAbort(ctx context.Context, migrationID, executorID string) (*Migration, *RunResult, error) {
	return e.executeBatch(ctx, migrationID, executorID, DirDown)
}

// executeBatch runs a batch migration (UP, also used to resume) or its abort (DOWN) while
// holding the database's advisory lock for the whole run. Each batch is one transaction on the
// target. The outcome is written like a standard run: the migration row and one execution row
// in one platform transaction.
//
// A failing batch is a normal result (nil error): FAILED when nothing was ever committed,
// PARTIAL otherwise. Errors mean nothing ran in this call; a transient error before the first
// batch commits lets the worker retry.
func (e *Engine) executeBatch(ctx context.Context, migrationID, executorID, dir string) (*Migration, *RunResult, error) {
	m, err := getMigration(ctx, e.platform, migrationID)
	if err != nil {
		return nil, nil, err
	}
	pool, ok := e.pools.GetPool(m.DatabaseID)
	if !ok {
		return nil, nil, ErrTargetUnavailable
	}
	lockConn, err := e.platform.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer lockConn.Close()
	if err := e.lock(ctx, lockConn, m.DatabaseID); err != nil {
		return nil, nil, err
	}
	defer e.unlock(lockConn, m.DatabaseID)

	// No deadline for the whole run: it can take many batches. Every batch has its own.
	runCtx := context.WithoutCancel(ctx)

	m, err = getMigration(runCtx, lockConn, migrationID)
	if err != nil {
		return nil, nil, err
	}
	script, kind, err := batchScript(m, dir)
	if err != nil {
		return nil, nil, err
	}
	// The first run is covered by the migration's review; a resume or abort by its request.
	approvals := ApprovalsOfBatchRequest
	if dir == DirUp && m.ExecutedAt.IsZero() {
		approvals = ApprovalsOfMigration
	}
	if err := e.revalidate(runCtx, m, executorID, approvals); err != nil {
		return nil, nil, err
	}
	plan, err := PrepareBatch(script, m.BatchAttribute)
	if err != nil {
		return nil, nil, err
	}
	// rec holds the recovery queries: UP stores before-images with them, DOWN checks against
	// them (they come from SQL Up, whose rows SQL Down puts back).
	var rec *BatchPlan
	if dir == DirUp && plan.Capture != "" && !m.IsIrreversible {
		rec = plan
	} else if dir == DirDown {
		if rec, err = PrepareBatch(m.SQL, m.BatchAttribute); err != nil {
			return nil, nil, err
		}
		if rec.Verify == "" {
			return nil, nil, ErrNoRollbackScript
		}
	}
	if err := e.ensureCheckpointTable(runCtx, pool, m.DatabaseID); err != nil {
		return nil, nil, err
	}
	if err := e.syncCheckpoints(runCtx, pool, lockConn, m.DatabaseID, m.ID); err != nil {
		return nil, nil, err
	}
	batches, err := listBatches(runCtx, lockConn, m.ID)
	if err != nil {
		return nil, nil, err
	}
	up, down := splitBatches(batches)
	if dir == DirUp && len(down) > 0 {
		return nil, nil, ErrBatchReverting
	}
	if dir == DirDown && !recoveryComplete(batches) {
		return nil, nil, ErrRecoveryMissing
	}

	ex := newExecution(runCtx, kind, m.ID, m.DatabaseID, executorID)
	if dir == DirDown {
		if ex.RevertsExecutionID, err = latestRunExecution(runCtx, lockConn, m.ID); err != nil {
			return nil, nil, err
		}
	} else if err := e.updateBatchTotal(runCtx, pool, lockConn, m, plan, up); err != nil {
		return nil, nil, err
	}
	if err := e.runStarted(runCtx, m.ID); err != nil {
		return nil, nil, err
	}

	start := e.now()
	res := &RunResult{OK: true, Statements: 1}
	var runErr error
	for {
		if interrupted(ctx) {
			res.Interrupted = true
			break
		}
		var b Batch
		if dir == DirUp {
			b = Batch{Direction: DirUp, No: len(up) + 1}
			if n := len(up); n > 0 {
				last := up[n-1].To
				b.From = &last
			}
		} else {
			target := nextToUndo(up, down)
			if target == nil {
				break // every committed batch is undone
			}
			b = Batch{Direction: DirDown, No: target.No, From: target.From, To: target.To}
		}
		b.ExecutionID = ex.ID

		done, err := e.runBatch(runCtx, pool, plan, rec, m, &b)
		if err != nil {
			runErr = err
			break
		}
		if done {
			break // UP: no keys left
		}
		if err := insertBatch(runCtx, lockConn, m.ID, b); err != nil {
			// The target checkpoint is the truth; the next run copies it again.
			e.logger.ErrorContext(runCtx, "migration.batch_record_failed", slog.String("migration_id", m.ID), slog.String("error", err.Error()))
		}
		res.Batches++
		res.Rows += b.Rows
		if dir == DirUp {
			up = append(up, b)
		} else {
			down[b.No] = true
		}
	}
	res.Duration = e.now().Sub(start)

	committedOverall := len(up) > 0
	if dir == DirDown {
		committedOverall = len(down) > 0
	}
	if runErr != nil {
		res.OK = false
		var pgErr *pgconn.PgError
		// A refused batch (rows changed meanwhile, or an abort that is not safe) is a result,
		// like an SQL error: the batch was rolled back and the run is recorded.
		refused := errors.Is(runErr, ErrBatchRowsChanged) || errors.Is(runErr, ErrUnsafeAbort)
		if !errors.As(runErr, &pgErr) && !refused && !committedOverall {
			// Nothing committed and not an SQL error (the target went away): retry like any
			// error before SQL. Only a rolled-back batch can have been sent.
			return nil, nil, runErr
		}
		res.Err = baseScriptError(runErr)
		// A DOWN run always leaves a PARTIAL migration: its UP batches are still committed.
		res.Partial = committedOverall || dir == DirDown
	}
	if res.Interrupted {
		res.OK = false
	}

	finished := runErr == nil && !res.Interrupted
	if res.Interrupted && res.Batches == 0 {
		// Stopped before the first batch of this run: nothing to record; the job is requeued.
		m, err = getMigration(runCtx, lockConn, migrationID)
		return m, res, err
	}
	ex = ex.withResult(script, start, res)
	switch {
	case finished:
		ex.Status = ExecSuccess
	case res.Partial || res.Interrupted:
		ex.Status = ExecPartial
		if ex.Error == "" {
			ex.Error = "dihentikan di antara batch karena worker berhenti; dilanjutkan dari checkpoint"
		}
	}

	if err := withTx(runCtx, lockConn, func(tx *sql.Tx) error {
		if err := e.recordBatchOutcome(runCtx, tx, m, dir, finished, res, executorID, start); err != nil {
			return err
		}
		return insertExecution(runCtx, tx, ex)
	}); err != nil {
		return nil, res, fmt.Errorf("batch sudah di-COMMIT pada database target, tetapi riwayat gagal disimpan: %w", err)
	}
	if finished {
		// SUCCESS or ABORTED: an abort is no longer possible, so the before-images go.
		_ = e.purgeRecovery(runCtx, pool, m.DatabaseID, m.ID)
	}
	execType := "UP"
	if dir == DirDown {
		execType = "ROLLBACK"
	}
	m, err = getMigration(runCtx, lockConn, migrationID)
	version := 0
	if m != nil {
		version = m.Version
	}
	e.logAttempt(runCtx, execType, migrationID, ex.ID, ex.DatabaseID, version, string(ex.Status), res.Duration, script, res)
	auditExecution(runCtx, e.logger, ex)
	return m, res, err
}

// batchScript returns the script and execution kind for a run in dir, after checking that the
// migration may run in that direction.
func batchScript(m *Migration, dir string) (string, ExecKind, error) {
	if dir == DirUp {
		if !m.IsExecutable() {
			return "", "", ErrNotDraft
		}
		return m.SQL, ExecExecute, nil
	}
	switch m.Status {
	case StatusPartial, StatusPartiallyReverted, StatusQueued, StatusRunning:
	default:
		return "", "", ErrNotPartial
	}
	if m.ExecutedAt.IsZero() {
		return "", "", ErrNotPartial
	}
	if m.IsIrreversible || m.SQLDown == "" {
		return "", "", ErrNoRollbackScript
	}
	return m.SQLDown, ExecRollback, nil
}

// runBatch runs one batch in its own transaction on the target: find the key range (UP), run
// the statement over it, write the checkpoint, commit. done is true when UP has no keys left.
// The batch is one migration.batch span; each of its statements is a child "sql <kind>" span.
//
// With rec (see BatchPlan.Capture), an UP batch first stores the before-image of its rows and
// a DOWN batch checks, after SQL Down, that every stored row is back as it was; otherwise the
// batch is rolled back with ErrUnsafeAbort.
func (e *Engine) runBatch(ctx context.Context, pool *sql.DB, plan, rec *BatchPlan, m *Migration, b *Batch) (done bool, err error) {
	ctx, span := startSpan(ctx, spanBatch, trace.SpanKindInternal,
		attribute.String("migration_id", m.ID), attribute.String("database_id", m.DatabaseID),
		attribute.String("direction", b.Direction), attribute.Int("batch_no", b.No))
	defer func() {
		span.SetAttributes(attribute.Int64("rows", b.Rows), attribute.Bool("done", done))
		if b.From != nil {
			span.SetAttributes(attribute.Int64("cursor_from", *b.From))
		}
		if b.To != 0 {
			span.SetAttributes(attribute.Int64("cursor_to", b.To))
		}
		failSpan(span, err)
		span.End()
	}()

	bctx, cancel := context.WithTimeout(ctx, e.runTimeout())
	defer cancel()
	txStart := time.Now()
	conn, tx, err := e.begin(bctx, pool)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	committed := false
	defer func() {
		_ = tx.Rollback()
		if !committed && !done {
			span.AddEvent("tx.rollback")
			e.logger.WarnContext(ctx, "tx.rollback", e.batchTxAttrs(m, b, slog.String("error", errString(err)))...)
		}
	}()

	from := nullInt(b.From)
	if b.Direction == DirUp {
		var count int64
		err := e.batchSQL(bctx, m, b, "batch_bounds", plan.Bounds, []any{from, m.BatchSize}, func(ctx context.Context) (int64, error) {
			err := tx.QueryRowContext(ctx, plan.Bounds, from, m.BatchSize).Scan(&b.To, &count)
			if errors.Is(err, sql.ErrNoRows) {
				return 0, nil
			}
			return count, err
		})
		if err != nil {
			return false, err
		}
		if count == 0 {
			return true, nil
		}
	}

	started := time.Now()
	recParams := []any{from, b.To, m.ID, b.No}
	if b.Direction == DirUp && rec != nil {
		err = e.captureAndRun(bctx, tx, rec, m, b, recParams)
	} else {
		err = e.batchSQL(bctx, m, b, "migration_batch", plan.Statement, []any{from, b.To}, func(ctx context.Context) (int64, error) {
			r, err := tx.ExecContext(ctx, plan.Statement, from, b.To)
			if err != nil {
				return -1, err
			}
			b.Rows, _ = r.RowsAffected()
			return b.Rows, nil
		})
		if err == nil && b.Direction == DirDown && rec != nil {
			err = e.verifyAndRelease(bctx, tx, rec, m, b, recParams)
		}
	}
	if err != nil {
		e.logger.ErrorContext(ctx, "migration.batch_failed", e.batchAttrs(m, b, started, slog.String("error", err.Error()))...)
		return false, err
	}
	const insertCheckpoint = `INSERT INTO ` + checkpointTable + `
		(migration_id, direction, batch_no, cursor_from, cursor_to, rows_affected, recovery_rows) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	params := []any{m.ID, b.Direction, b.No, from, b.To, b.Rows, nullInt(b.RecoveryRows)}
	if err := e.batchSQL(bctx, m, b, "checkpoint", insertCheckpoint, params, func(ctx context.Context) (int64, error) {
		_, err := tx.ExecContext(ctx, insertCheckpoint, params...)
		return 1, err
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		// The commit may or may not have happened; the next run reads the checkpoint to know.
		return false, err
	}
	committed = true
	b.CommittedAt = time.Now()
	metrics.Batches.WithLabelValues(b.Direction).Inc()
	metrics.BatchRows.WithLabelValues(b.Direction).Add(float64(b.Rows))
	metrics.BatchDuration.WithLabelValues(b.Direction).Observe(b.CommittedAt.Sub(txStart).Seconds())
	span.AddEvent("tx.commit")
	e.logger.InfoContext(ctx, "tx.commit", e.batchTxAttrs(m, b)...)
	e.logger.InfoContext(ctx, "migration.batch", e.batchAttrs(m, b, started)...)
	return false, nil
}

// captureAndRun is an UP batch with recovery data: store the before-images (locking those
// rows), then run the statement and check that it changed no row that was not stored.
func (e *Engine) captureAndRun(ctx context.Context, tx *sql.Tx, rec *BatchPlan, m *Migration, b *Batch, params []any) error {
	var stored int64
	keys := make(map[int64]struct{}, m.BatchSize)
	if err := e.batchSQL(ctx, m, b, "recovery_capture", rec.Capture, params, func(ctx context.Context) (int64, error) {
		return scanKeys(ctx, tx, rec.Capture, params, func(k int64) {
			keys[k] = struct{}{}
			stored++
		})
	}); err != nil {
		return err
	}
	var missed int64
	if err := e.batchSQL(ctx, m, b, "migration_batch", rec.Returning, params[:2], func(ctx context.Context) (int64, error) {
		n, err := scanKeys(ctx, tx, rec.Returning, params[:2], func(k int64) {
			if _, ok := keys[k]; !ok {
				missed++
			}
		})
		b.Rows = max(n, 0)
		return n, err
	}); err != nil {
		return err
	}
	if missed > 0 {
		return fmt.Errorf("%w (batch %d: %d baris tidak tercatat)", ErrBatchRowsChanged, b.No, missed)
	}
	b.RecoveryRows = &stored
	return nil
}

// verifyAndRelease runs after SQL Down in a DOWN batch: every stored before-image must be found
// unchanged in the key range, and SQL Down must not have changed more rows than were stored.
// Then the batch's recovery rows are deleted in the same transaction.
func (e *Engine) verifyAndRelease(ctx context.Context, tx *sql.Tx, rec *BatchPlan, m *Migration, b *Batch, params []any) error {
	var stored, differ int64
	if err := e.batchSQL(ctx, m, b, "recovery_verify", rec.Verify, params, func(ctx context.Context) (int64, error) {
		return 1, tx.QueryRowContext(ctx, rec.Verify, params...).Scan(&stored, &differ)
	}); err != nil {
		return err
	}
	switch {
	case differ > 0:
		return fmt.Errorf("%w: batch %d (kunci %s): %d dari %d baris tidak kembali ke keadaan sebelum migration, "+
			"karena diubah lagi setelah batch dijalankan atau SQL Down tidak membalik perubahannya dengan tepat; batch ini di-ROLLBACK",
			ErrUnsafeAbort, b.No, keyRange(b), differ, stored)
	case b.Rows > stored:
		return fmt.Errorf("%w: batch %d (kunci %s): SQL Down mengubah %d baris, padahal batch ini hanya mengubah %d; "+
			"baris lain akan ikut berubah, jadi batch ini di-ROLLBACK", ErrUnsafeAbort, b.No, keyRange(b), b.Rows, stored)
	}
	const release = `DELETE FROM ` + recoveryTable + ` WHERE migration_id = $1::uuid AND batch_no = $2::int`
	return e.batchSQL(ctx, m, b, "recovery_release", release, []any{m.ID, b.No}, func(ctx context.Context) (int64, error) {
		r, err := tx.ExecContext(ctx, release, m.ID, b.No)
		if err != nil {
			return -1, err
		}
		n, _ := r.RowsAffected()
		return n, nil
	})
}

// scanKeys runs a query that returns one bigint per row and calls fn for each. It returns the
// number of rows (-1 on error).
func scanKeys(ctx context.Context, tx *sql.Tx, query string, params []any, fn func(int64)) (int64, error) {
	rows, err := tx.QueryContext(ctx, query, params...)
	if err != nil {
		return -1, err
	}
	defer rows.Close()
	var n int64
	for rows.Next() {
		var k int64
		if err := rows.Scan(&k); err != nil {
			return -1, err
		}
		fn(k)
		n++
	}
	if err := rows.Err(); err != nil {
		return -1, err
	}
	return n, nil
}

func keyRange(b *Batch) string {
	if b.From == nil {
		return fmt.Sprintf("<= %d", b.To)
	}
	return fmt.Sprintf("%d < k <= %d", *b.From, b.To)
}

// PurgeRecovery deletes the stored before-images of a migration that became final outside a
// run (marked ABORTED without rollback). Failures are logged; nothing depends on the rows.
func (e *Engine) PurgeRecovery(ctx context.Context, databaseID, migrationID string) error {
	pool, ok := e.pools.GetPool(databaseID)
	if !ok {
		return ErrTargetUnavailable
	}
	return e.purgeRecovery(ctx, pool, databaseID, migrationID)
}

func (e *Engine) purgeRecovery(ctx context.Context, pool *sql.DB, databaseID, migrationID string) error {
	ctx, cancel := context.WithTimeout(ctx, e.runTimeout())
	defer cancel()
	const purge = `DELETE FROM ` + recoveryTable + ` WHERE migration_id = $1::uuid`
	sqlCtx, end := logging.StartSQL(ctx, e.logger, logging.SQLLog{
		DatabaseID: databaseID, Kind: "recovery_purge", Query: purge, Params: []any{migrationID},
		Extra: []slog.Attr{slog.String("migration_id", migrationID)},
	})
	n := int64(-1)
	r, err := pool.ExecContext(sqlCtx, purge, migrationID)
	if err == nil {
		n, _ = r.RowsAffected()
	}
	end(n, err)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
		return nil // the table was never created: nothing to purge
	}
	if err != nil {
		e.logger.WarnContext(ctx, "migration.recovery_purge_failed",
			slog.String("migration_id", migrationID), slog.String("error", err.Error()))
	}
	return err
}

// recoveryComplete reports whether every committed UP batch that is not undone yet has its
// recovery data, which an abort with rollback needs.
func recoveryComplete(all []Batch) bool {
	up, down := splitBatches(all)
	for _, b := range up {
		if !down[b.No] && b.RecoveryRows == nil {
			return false
		}
	}
	return true
}

// batchSQL runs fn between sql.start and sql.result records (in an "sql <kind>" span) for one
// statement of batch b on the target.
func (e *Engine) batchSQL(ctx context.Context, m *Migration, b *Batch, kind, query string, params []any, fn func(ctx context.Context) (int64, error)) error {
	ctx, end := logging.StartSQL(ctx, e.logger, logging.SQLLog{
		DatabaseID: m.DatabaseID, Kind: kind, Query: query, Params: params,
		Extra: []slog.Attr{
			slog.String("migration_id", m.ID), slog.String("direction", b.Direction), slog.Int("batch_no", b.No),
		},
	})
	rows, err := fn(ctx)
	end(rows, err)
	return err
}

// batchTxAttrs are the attributes of a batch's tx.commit / tx.rollback record.
func (e *Engine) batchTxAttrs(m *Migration, b *Batch, extra ...any) []any {
	return append([]any{
		slog.String("database_id", m.DatabaseID), slog.String("migration_id", m.ID),
		slog.String("execution_type", "BATCH_"+b.Direction), slog.Int("batch_no", b.No),
	}, extra...)
}

func (e *Engine) batchAttrs(m *Migration, b *Batch, started time.Time, extra ...any) []any {
	attrs := []any{
		slog.String("migration_id", m.ID), slog.String("database_id", m.DatabaseID),
		slog.String("direction", b.Direction), slog.Int("batch_no", b.No),
		slog.Any("cursor_from", b.From), slog.Int64("cursor_to", b.To),
		slog.Int64("rows", b.Rows), slog.Int64("duration_ms", time.Since(started).Milliseconds()),
	}
	return append(attrs, extra...)
}

// ensureCheckpointTable creates the checkpoint table on the target if it does not exist.
func (e *Engine) ensureCheckpointTable(ctx context.Context, pool *sql.DB, databaseID string) error {
	bctx, cancel := context.WithTimeout(ctx, e.runTimeout())
	defer cancel()
	conn, tx, err := e.begin(bctx, pool)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer func() { _ = tx.Rollback() }()
	sqlCtx, end := logging.StartSQL(bctx, e.logger, logging.SQLLog{DatabaseID: databaseID, Kind: "checkpoint", Query: checkpointDDL})
	_, err = tx.ExecContext(sqlCtx, checkpointDDL)
	end(-1, err)
	if err != nil {
		return fmt.Errorf("membuat tabel checkpoint di database target: %w", err)
	}
	return tx.Commit()
}

// syncCheckpoints copies target checkpoints that the platform does not have yet: batches that
// committed right before the process died, before their platform row was written. This is
// what makes a crash safe: those batches are never run again.
func (e *Engine) syncCheckpoints(ctx context.Context, pool *sql.DB, q querier, databaseID, migrationID string) error {
	bctx, cancel := context.WithTimeout(ctx, e.runTimeout())
	defer cancel()
	const readCheckpoints = `
		SELECT direction, batch_no, cursor_from, cursor_to, rows_affected, committed_at, recovery_rows
		FROM ` + checkpointTable + ` WHERE migration_id = $1`
	sqlCtx, end := logging.StartSQL(bctx, e.logger, logging.SQLLog{
		DatabaseID: databaseID, Kind: "checkpoint", Query: readCheckpoints, Params: []any{migrationID},
		Extra: []slog.Attr{slog.String("migration_id", migrationID)},
	})
	found, err := readTargetCheckpoints(sqlCtx, pool, readCheckpoints, migrationID)
	end(int64(len(found)), err)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTargetUnavailable, err)
	}
	recovered := 0
	for _, b := range found {
		res, err := q.ExecContext(ctx, `
			INSERT INTO migration_batches (migration_id, direction, batch_no, cursor_from, cursor_to, rows_affected, committed_at, recovery_rows)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT DO NOTHING`,
			migrationID, b.Direction, b.No, nullInt(b.From), b.To, b.Rows, b.CommittedAt, nullInt(b.RecoveryRows))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			recovered++
		}
	}
	if recovered > 0 {
		e.logger.WarnContext(ctx, "migration.batch_recovered", slog.String("migration_id", migrationID), slog.Int("batches", recovered))
	}
	return nil
}

// readTargetCheckpoints reads the checkpoints one migration wrote on the target.
func readTargetCheckpoints(ctx context.Context, pool *sql.DB, query, migrationID string) ([]Batch, error) {
	rows, err := pool.QueryContext(ctx, query, migrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []Batch
	for rows.Next() {
		var b Batch
		var from, recovery sql.NullInt64
		if err := rows.Scan(&b.Direction, &b.No, &from, &b.To, &b.Rows, &b.CommittedAt, &recovery); err != nil {
			return nil, err
		}
		if from.Valid {
			b.From = &from.Int64
		}
		if recovery.Valid {
			b.RecoveryRows = &recovery.Int64
		}
		found = append(found, b)
	}
	return found, rows.Err()
}

// updateBatchTotal estimates the number of batches (committed + remaining) for the progress bar.
func (e *Engine) updateBatchTotal(ctx context.Context, pool *sql.DB, q querier, m *Migration, plan *BatchPlan, up []Batch) error {
	var from *int64
	if n := len(up); n > 0 {
		from = &up[n-1].To
	}
	bctx, cancel := context.WithTimeout(ctx, e.runTimeout())
	defer cancel()
	var remaining int64
	sqlCtx, end := logging.StartSQL(bctx, e.logger, logging.SQLLog{
		DatabaseID: m.DatabaseID, Kind: "batch_estimate", Query: plan.Remaining, Params: []any{nullInt(from)},
		Extra: []slog.Attr{slog.String("migration_id", m.ID)},
	})
	err := pool.QueryRowContext(sqlCtx, plan.Remaining, nullInt(from)).Scan(&remaining)
	end(1, err)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			// e.g. the cursor column does not exist: reported when the first batch runs
			return nil
		}
		return fmt.Errorf("%w: %v", ErrTargetUnavailable, err)
	}
	size := int64(m.BatchSize)
	total := int64(len(up)) + (remaining+size-1)/size
	_, err = q.ExecContext(ctx, `UPDATE migrations SET batch_total = $2, updated_at = NOW()
		WHERE id = $1 AND status IN ('DRAFT', 'APPROVED', 'QUEUED', 'RUNNING', 'PARTIAL')`, m.ID, total)
	return err
}

// recordBatchOutcome writes the migration row at the end of a run.
func (e *Engine) recordBatchOutcome(ctx context.Context, q querier, m *Migration, dir string, finished bool, res *RunResult, executorID string, start time.Time) error {
	msg := ""
	if res.Err != nil {
		msg = res.Err.String()
	}
	switch {
	case res.Interrupted && res.Err == nil:
		return nil // still RUNNING; the worker puts the job back in the queue
	case dir == DirUp && finished:
		return recordExecution(ctx, q, executionRecord{ID: m.ID, OK: true, SQL: m.SQL, ExecutedBy: executorID, ExecutedAt: start, Duration: res.Duration})
	case dir == DirUp && !res.Partial:
		return recordExecution(ctx, q, executionRecord{ID: m.ID, SQL: m.SQL, ExecutedBy: executorID, ExecutedAt: start, Duration: res.Duration, Error: msg})
	case dir == DirDown && finished:
		_, err := q.ExecContext(ctx, `
			UPDATE migrations SET status = 'ABORTED', error_message = '', duration_ms = $2, updated_at = NOW()
			WHERE id = $1 AND status IN ('PARTIAL', 'QUEUED', 'RUNNING')`, m.ID, res.Duration.Milliseconds())
		return err
	}
	// Stopped with committed batches (UP), or an abort that stopped early (DOWN).
	_, err := q.ExecContext(ctx, `
		UPDATE migrations SET status = 'PARTIAL', executed_by = $2, executed_at = COALESCE(executed_at, $3),
		       duration_ms = $4, error_message = $5, updated_at = NOW()
		WHERE id = $1 AND status IN ('DRAFT', 'APPROVED', 'QUEUED', 'RUNNING', 'PARTIAL')`,
		m.ID, nullID(executorID), start, res.Duration.Milliseconds(), msg)
	return err
}

func listBatches(ctx context.Context, q querier, migrationID string) ([]Batch, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT direction, batch_no, COALESCE(execution_id::text, ''), cursor_from, cursor_to, rows_affected, committed_at, recovery_rows
		FROM migration_batches WHERE migration_id = $1 ORDER BY direction DESC, batch_no`, migrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Batch
	for rows.Next() {
		var b Batch
		var from, recovery sql.NullInt64
		if err := rows.Scan(&b.Direction, &b.No, &b.ExecutionID, &from, &b.To, &b.Rows, &b.CommittedAt, &recovery); err != nil {
			return nil, err
		}
		if from.Valid {
			b.From = &from.Int64
		}
		if recovery.Valid {
			b.RecoveryRows = &recovery.Int64
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListBatches returns the committed batches of a migration: UP in order, then DOWN.
func (r *Repository) ListBatches(ctx context.Context, migrationID string) ([]Batch, error) {
	return listBatches(ctx, r.db, migrationID)
}

func insertBatch(ctx context.Context, q querier, migrationID string, b Batch) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO migration_batches (migration_id, direction, batch_no, execution_id, cursor_from, cursor_to, rows_affected, committed_at, recovery_rows)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT DO NOTHING`,
		migrationID, b.Direction, b.No, nullID(b.ExecutionID), nullInt(b.From), b.To, b.Rows, b.CommittedAt, nullInt(b.RecoveryRows))
	return err
}

// splitBatches returns the UP batches in order and the set of UP batch numbers already undone.
func splitBatches(all []Batch) (up []Batch, down map[int]bool) {
	down = map[int]bool{}
	for _, b := range all {
		if b.Direction == DirUp {
			up = append(up, b)
		} else {
			down[b.No] = true
		}
	}
	return up, down
}

// nextToUndo is the newest UP batch that is not undone yet.
func nextToUndo(up []Batch, down map[int]bool) *Batch {
	for i := len(up) - 1; i >= 0; i-- {
		if !down[up[i].No] {
			return &up[i]
		}
	}
	return nil
}

// latestRunExecution is the newest EXECUTE execution of a migration that sent SQL: the run an
// abort reverts.
func latestRunExecution(ctx context.Context, q querier, migrationID string) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `
		SELECT id FROM migration_executions
		WHERE migration_id = $1 AND execution_type = 'EXECUTE' AND sql_sent
		ORDER BY finished_at DESC LIMIT 1`, migrationID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotPartial
	}
	return id, err
}

func nullInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
