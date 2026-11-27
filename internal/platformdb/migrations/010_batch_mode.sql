-- 010_batch_mode.sql
-- Per-batch execution: one UPDATE/DELETE runs over the target table in key ranges of
-- batch_size rows, one transaction per batch, with a checkpoint per committed batch.

ALTER TABLE migrations
    ADD COLUMN execution_mode  VARCHAR(16) NOT NULL DEFAULT 'STANDARD',
    ADD COLUMN batch_attribute VARCHAR(63) NOT NULL DEFAULT '',
    ADD COLUMN batch_size      INTEGER     NOT NULL DEFAULT 0,
    -- Estimated number of batches, refreshed when a run starts (committed + remaining).
    ADD COLUMN batch_total     INTEGER     NOT NULL DEFAULT 0 CHECK (batch_total >= 0),
    ADD CONSTRAINT migrations_execution_mode_check CHECK (execution_mode IN ('STANDARD', 'BATCH')),
    -- The cursor column is written into SQL, so only plain lower-case identifiers are allowed.
    ADD CONSTRAINT migrations_batch_config CHECK (
        execution_mode = 'STANDARD'
        OR (batch_attribute ~ '^[a-z_][a-z0-9_]*$' AND batch_size BETWEEN 1 AND 100000));

-- A PARTIAL migration has run already, and resume/abort queue it again: QUEUED and RUNNING may
-- now have executed_at set (first run) or not (never run).
ALTER TABLE migrations DROP CONSTRAINT migrations_executed_iff_not_draft;
ALTER TABLE migrations ADD CONSTRAINT migrations_executed_iff_not_draft CHECK (
    CASE
        WHEN status IN ('DRAFT', 'PENDING_REVIEW', 'APPROVED', 'REJECTED') THEN executed_at IS NULL
        WHEN status IN ('QUEUED', 'RUNNING') THEN TRUE
        ELSE executed_at IS NOT NULL
    END);

-- A batch run that stopped after committing some batches is PARTIAL (resume or abort it).
ALTER TABLE migration_executions DROP CONSTRAINT migration_executions_status_check;
ALTER TABLE migration_executions ADD CONSTRAINT migration_executions_status_check
    CHECK (status IN ('SUCCESS', 'FAILED', 'UNCERTAIN', 'PARTIAL'));

ALTER TABLE migration_jobs DROP CONSTRAINT migration_jobs_job_type_check;
ALTER TABLE migration_jobs ADD CONSTRAINT migration_jobs_job_type_check
    CHECK (job_type IN ('EXECUTE', 'ROLLBACK', 'FORWARD', 'RESUME', 'ABORT'));
ALTER TABLE migration_jobs DROP CONSTRAINT migration_jobs_status_check;
ALTER TABLE migration_jobs ADD CONSTRAINT migration_jobs_status_check
    CHECK (status IN ('QUEUED', 'RUNNING', 'SUCCESS', 'FAILED', 'UNCERTAIN', 'PARTIAL'));

-- One row per committed batch, copied from the checkpoint the batch wrote on the target in its
-- own transaction (the target copy is the source of truth; see engine_batch.go).
-- DOWN batch k undoes UP batch k (abort with rollback runs them newest first).
CREATE TABLE migration_batches (
    migration_id  UUID        NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    direction     VARCHAR(4)  NOT NULL CHECK (direction IN ('UP', 'DOWN')),
    batch_no      INTEGER     NOT NULL CHECK (batch_no >= 1),
    -- The run that committed the batch. No foreign key: the execution row is written when
    -- the run ends, after its batches.
    execution_id  UUID,
    cursor_from   BIGINT,               -- exclusive lower bound; NULL for the first batch
    cursor_to     BIGINT      NOT NULL, -- inclusive upper bound
    rows_affected BIGINT      NOT NULL CHECK (rows_affected >= 0),
    committed_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (migration_id, direction, batch_no)
);

-- Append-only like migration_executions; rows only go away with their migration (which can
-- only be deleted together with its target database once it has history).
CREATE FUNCTION protect_migration_batches() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' AND NOT EXISTS (SELECT 1 FROM migrations WHERE id = OLD.migration_id) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'migration batch %/%/% is history and cannot be changed',
        OLD.migration_id, OLD.direction, OLD.batch_no USING ERRCODE = 'check_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER migration_batches_append_only
BEFORE UPDATE OR DELETE ON migration_batches
FOR EACH ROW EXECUTE FUNCTION protect_migration_batches();
