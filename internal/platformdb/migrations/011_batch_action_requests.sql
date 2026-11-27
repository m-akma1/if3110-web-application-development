-- 011_batch_action_requests.sql
-- Leader approval for actions on a batch migration that stopped half-way (PARTIAL): resume,
-- abort with rollback, and abort without rollback. Like a migration, the action needs one
-- approval per group with access to the database before it may run.
--
-- These are separate from migration_approvals: those rows belong to the migration's own review
-- (and are replaced when a draft is resubmitted), while a PARTIAL migration can get several
-- action requests over its life. Every request and approval is kept.

CREATE TABLE batch_action_requests (
    id               UUID PRIMARY KEY,
    migration_id     UUID        NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    database_id      UUID        NOT NULL REFERENCES target_databases(id) ON DELETE CASCADE,
    action           VARCHAR(16) NOT NULL CHECK (action IN ('RESUME', 'ABORT_ROLLBACK', 'ABORT_MARK')),
    -- PENDING -> APPROVED -> DONE, or PENDING -> REJECTED. An APPROVED request that did not run
    -- is CANCELLED when a request for another action replaces it.
    status           VARCHAR(16) NOT NULL DEFAULT 'PENDING'
                     CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED', 'DONE', 'CANCELLED')),
    requested_by     UUID        REFERENCES users(id) ON DELETE SET NULL,
    rejection_reason TEXT        NOT NULL DEFAULT '',
    job_id           UUID        REFERENCES migration_jobs(id) ON DELETE SET NULL, -- the job that ran it
    executed_by      UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at       TIMESTAMPTZ, -- APPROVED or REJECTED
    done_at          TIMESTAMPTZ
);

-- One open (PENDING or APPROVED) request per migration.
CREATE UNIQUE INDEX batch_action_requests_one_open
    ON batch_action_requests (migration_id) WHERE status IN ('PENDING', 'APPROVED');
CREATE INDEX batch_action_requests_migration_idx ON batch_action_requests (migration_id, created_at DESC);

CREATE TABLE batch_action_approvals (
    id               UUID PRIMARY KEY,
    request_id       UUID        NOT NULL REFERENCES batch_action_requests(id) ON DELETE CASCADE,
    group_id         UUID        NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    leader_id        UUID        REFERENCES users(id) ON DELETE SET NULL, -- who reviewed it
    status           VARCHAR(16) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED')),
    note             TEXT        NOT NULL DEFAULT '',
    rejection_reason TEXT        NOT NULL DEFAULT '',
    reviewed_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT batch_action_approvals_request_group_key UNIQUE (request_id, group_id)
);
