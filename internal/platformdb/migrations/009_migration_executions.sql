-- 009_migration_executions.sql
-- Append-only execution history. Every attempt to run a migration (execute, rollback, forward),
-- including attempts that failed before any SQL reached the target, is one row here. Rows are
-- never changed: a rollback appends a row that reverts an earlier execution instead of
-- rewriting the original migration from SUCCESS to ROLLED_BACK.

CREATE TABLE migration_executions (
    id                   UUID PRIMARY KEY,
    -- The migration row this run belongs to: the migration itself, or the rollback/forward row
    -- the run created. A failed direct rollback/forward creates no row and points at the
    -- migration it was asked for.
    migration_id         UUID         NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    database_id          UUID         NOT NULL REFERENCES target_databases(id) ON DELETE CASCADE,
    -- No foreign key: queue rows are operational data and may be cleaned up; history stays.
    job_id               UUID,
    attempt              INTEGER      NOT NULL DEFAULT 1 CHECK (attempt >= 1),
    execution_type       VARCHAR(16)  NOT NULL CHECK (execution_type IN ('EXECUTE', 'ROLLBACK', 'FORWARD')),
    -- UNCERTAIN: SQL was sent but the outcome could not be confirmed (see migration_jobs).
    status               VARCHAR(16)  NOT NULL CHECK (status IN ('SUCCESS', 'FAILED', 'UNCERTAIN')),
    -- false: the attempt stopped before any SQL reached the target (target down, validation).
    sql_sent             BOOLEAN      NOT NULL,
    -- The exact script sent to the target; empty when sql_sent is false.
    sql_executed         TEXT         NOT NULL DEFAULT '',
    -- ROLLBACK: the EXECUTE/FORWARD execution it undid. FORWARD: the ROLLBACK it undid.
    reverts_execution_id UUID         REFERENCES migration_executions(id),
    executor_id          UUID         REFERENCES users(id) ON DELETE SET NULL,
    error_message        TEXT         NOT NULL DEFAULT '',
    sqlstate             VARCHAR(5)   NOT NULL DEFAULT '',
    correlation_id       VARCHAR(64)  NOT NULL DEFAULT '',
    started_at           TIMESTAMPTZ  NOT NULL,
    finished_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    duration_ms          BIGINT       NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),

    CONSTRAINT migration_executions_success_sent CHECK (status <> 'SUCCESS' OR sql_sent),
    CONSTRAINT migration_executions_sql_iff_sent CHECK (sql_sent = (sql_executed <> '')),
    CONSTRAINT migration_executions_execute_reverts_nothing
        CHECK (execution_type <> 'EXECUTE' OR reverts_execution_id IS NULL),
    CONSTRAINT migration_executions_success_has_target
        CHECK (status <> 'SUCCESS' OR execution_type = 'EXECUTE' OR reverts_execution_id IS NOT NULL)
);

-- A migration row succeeds at most once, and an execution is undone at most once.
-- These two indexes keep the chain a simple line, so the joins in migration_chain never fan out.
CREATE UNIQUE INDEX migration_executions_one_success_per_migration
    ON migration_executions (migration_id) WHERE status = 'SUCCESS';
CREATE UNIQUE INDEX migration_executions_reverted_once
    ON migration_executions (reverts_execution_id) WHERE status = 'SUCCESS';
CREATE INDEX migration_executions_migration_idx ON migration_executions (migration_id, finished_at);
CREATE INDEX migration_executions_job_idx ON migration_executions (job_id) WHERE job_id IS NOT NULL;
CREATE INDEX migration_executions_database_idx ON migration_executions (database_id, finished_at DESC);

-- Backfill: until now one executed migrations row was one execution, so the row's id is reused
-- as its execution id (it is also what migration.attempt logged as execution_id).
INSERT INTO migration_executions (
    id, migration_id, database_id, attempt, execution_type, status, sql_sent, sql_executed,
    reverts_execution_id, executor_id, error_message, started_at, finished_at, duration_ms)
SELECT m.id, m.id, m.database_id, 1,
       CASE WHEN m.execution_type = 'ROLLBACK' THEN 'ROLLBACK'
            WHEN m.source_migration_id IS NOT NULL THEN 'FORWARD'
            ELSE 'EXECUTE' END,
       CASE WHEN m.status = 'FAILED' THEN 'FAILED' ELSE 'SUCCESS' END,
       TRUE,
       CASE WHEN m.sql_script <> '' THEN m.sql_script ELSE '-- (script tidak tersimpan)' END,
       CASE WHEN m.status = 'FAILED' THEN NULL
            WHEN m.execution_type = 'ROLLBACK' THEN m.rollback_target_id
            WHEN m.source_migration_id IS NOT NULL THEN (
                SELECT r.id FROM migrations r
                WHERE r.execution_type = 'ROLLBACK' AND r.status = 'SUCCESS'
                  AND r.rollback_target_id = m.source_migration_id
                ORDER BY r.executed_at DESC LIMIT 1)
       END,
       m.executed_by, m.error_message, m.executed_at,
       m.executed_at + COALESCE(m.duration_ms, 0) * INTERVAL '1 millisecond',
       COALESCE(m.duration_ms, 0)
FROM migrations m
WHERE m.status IN ('SUCCESS', 'FAILED', 'ROLLED_BACK') AND m.executed_at IS NOT NULL;

-- ROLLED_BACK is no longer stored: it is derived from the chain (migration_chain below).
ALTER TABLE migrations DISABLE TRIGGER migrations_protect_executed;
UPDATE migrations SET status = 'SUCCESS' WHERE status = 'ROLLED_BACK';
ALTER TABLE migrations ENABLE TRIGGER migrations_protect_executed;

ALTER TABLE migrations DROP CONSTRAINT IF EXISTS migrations_status_check;
ALTER TABLE migrations ADD CONSTRAINT migrations_status_check
    CHECK (status IN (
        'DRAFT', 'PENDING_REVIEW', 'APPROVED', 'REJECTED',
        'QUEUED', 'RUNNING', 'PARTIAL', 'SUCCESS', 'FAILED', 'ABORTED'
    ));
ALTER TABLE migrations DROP CONSTRAINT IF EXISTS migrations_version_iff_success;
ALTER TABLE migrations ADD CONSTRAINT migrations_version_iff_success
    CHECK ((status = 'SUCCESS' AND execution_type <> 'ROLLBACK') = (version IS NOT NULL));

-- An executed migration never changes again (006 allowed SUCCESS <-> ROLLED_BACK; not any more).
CREATE OR REPLACE FUNCTION protect_executed_migrations() RETURNS trigger AS $$
BEGIN
    IF OLD.status IN ('SUCCESS', 'FAILED', 'ABORTED') AND (
           (to_jsonb(NEW) - 'created_by' - 'executed_by') IS DISTINCT FROM (to_jsonb(OLD) - 'created_by' - 'executed_by')
        OR (NEW.created_by IS NOT NULL AND NEW.created_by IS DISTINCT FROM OLD.created_by)
        OR (NEW.executed_by IS NOT NULL AND NEW.executed_by IS DISTINCT FROM OLD.executed_by)
    ) THEN
        RAISE EXCEPTION 'migration % is already executed and cannot be changed', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Append-only: no UPDATE except executor_id becoming NULL when that user is deleted
-- (ON DELETE SET NULL), and no DELETE except when the whole target database is removed.
CREATE FUNCTION protect_migration_executions() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.executor_id IS NULL
           AND (to_jsonb(NEW) - 'executor_id') = (to_jsonb(OLD) - 'executor_id') THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION 'migration execution % is history and cannot be changed', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (SELECT 1 FROM target_databases WHERE id = OLD.database_id) THEN
        RAISE EXCEPTION 'migration execution % is history and cannot be deleted', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER migration_executions_append_only
BEFORE UPDATE OR DELETE ON migration_executions
FOR EACH ROW EXECUTE FUNCTION protect_migration_executions();

-- The chain of one migration row: its successful run, the rollback that undid it, and the
-- forward that undid that rollback. A migration is rolled back when rollback_execution_id is set.
CREATE VIEW migration_chain AS
SELECT m.id AS migration_id,
       a.id AS applied_execution_id,
       r.id AS rollback_execution_id,
       f.id AS forward_execution_id
FROM migrations m
LEFT JOIN migration_executions a
       ON a.migration_id = m.id AND a.status = 'SUCCESS' AND a.execution_type IN ('EXECUTE', 'FORWARD')
LEFT JOIN migration_executions r
       ON r.reverts_execution_id = a.id AND r.status = 'SUCCESS' AND r.execution_type = 'ROLLBACK'
LEFT JOIN migration_executions f
       ON f.reverts_execution_id = r.id AND f.status = 'SUCCESS' AND f.execution_type = 'FORWARD';
