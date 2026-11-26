-- 008_migration_jobs.sql
-- Persistent queue for asynchronous migration runs (execute, rollback, forward).

-- QUEUED and RUNNING migrations have not been executed yet, so executed_at is still NULL.
ALTER TABLE migrations DROP CONSTRAINT IF EXISTS migrations_executed_iff_not_draft;
ALTER TABLE migrations ADD CONSTRAINT migrations_executed_iff_not_draft
    CHECK ((status IN ('DRAFT', 'PENDING_REVIEW', 'APPROVED', 'REJECTED', 'QUEUED', 'RUNNING')) = (executed_at IS NULL));

CREATE TABLE migration_jobs (
    id               UUID PRIMARY KEY,
    migration_id     UUID         NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    database_id      UUID         NOT NULL REFERENCES target_databases(id) ON DELETE CASCADE,
    job_type         VARCHAR(16)  NOT NULL CHECK (job_type IN ('EXECUTE', 'ROLLBACK', 'FORWARD')),
    executor_id      UUID         REFERENCES users(id) ON DELETE SET NULL,
    -- UNCERTAIN: the worker stopped after SQL was sent and the outcome is unknown.
    -- It holds every later job on the same database until an operator resolves it.
    status           VARCHAR(16)  NOT NULL DEFAULT 'QUEUED'
                     CHECK (status IN ('QUEUED', 'RUNNING', 'SUCCESS', 'FAILED', 'UNCERTAIN')),
    -- migration status before enqueue (DRAFT or APPROVED), restored when the job fails before any SQL ran
    from_status      VARCHAR(16)  NOT NULL DEFAULT '',
    attempt          INTEGER      NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    max_attempts     INTEGER      NOT NULL DEFAULT 3 CHECK (max_attempts >= 1),
    next_run_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    locked_by        VARCHAR(128) NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    -- set right before the script is sent to the target: proof that SQL may have run
    sql_started_at   TIMESTAMPTZ,
    last_error       TEXT         NOT NULL DEFAULT '',
    correlation_id   VARCHAR(64)  NOT NULL DEFAULT '',
    trace_parent     VARCHAR(255) NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    finished_at      TIMESTAMPTZ
);

-- At most one unfinished job per migration: a double click or a retried request cannot enqueue twice.
CREATE UNIQUE INDEX migration_jobs_one_active_per_migration
    ON migration_jobs (migration_id)
    WHERE status IN ('QUEUED', 'RUNNING', 'UNCERTAIN');

CREATE INDEX migration_jobs_queue_idx ON migration_jobs (status, next_run_at, created_at);
CREATE INDEX migration_jobs_database_idx ON migration_jobs (database_id, status);
