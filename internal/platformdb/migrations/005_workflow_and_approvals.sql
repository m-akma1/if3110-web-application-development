-- 005_workflow_and_approvals.sql
-- Group leader and migration review/approval workflow.

-- Add leader_id to groups
ALTER TABLE groups ADD COLUMN IF NOT EXISTS leader_id UUID REFERENCES users(id) ON DELETE SET NULL;

-- Backfill leader_id for any existing non-system groups that have members
UPDATE groups g
SET leader_id = (
    SELECT gm.user_id
    FROM group_members gm
    WHERE gm.group_id = g.id
    ORDER BY gm.added_at ASC
    LIMIT 1
)
WHERE g.leader_id IS NULL AND g.is_system = FALSE AND EXISTS (
    SELECT 1 FROM group_members gm WHERE gm.group_id = g.id
);

-- Relax status constraints on migrations table to support review workflow and batch/async statuses
ALTER TABLE migrations DROP CONSTRAINT IF EXISTS migrations_status_check;
ALTER TABLE migrations ADD CONSTRAINT migrations_status_check
    CHECK (status IN (
        'DRAFT', 'PENDING_REVIEW', 'APPROVED', 'REJECTED',
        'QUEUED', 'RUNNING', 'PARTIAL', 'SUCCESS', 'FAILED', 'ABORTED', 'ROLLED_BACK'
    ));

ALTER TABLE migrations DROP CONSTRAINT IF EXISTS migrations_executed_iff_not_draft;
ALTER TABLE migrations ADD CONSTRAINT migrations_executed_iff_not_draft
    CHECK ((status IN ('DRAFT', 'PENDING_REVIEW', 'APPROVED', 'REJECTED')) = (executed_at IS NULL));

-- Add rejection_reason column to migrations table
ALTER TABLE migrations ADD COLUMN IF NOT EXISTS rejection_reason TEXT NOT NULL DEFAULT '';

-- Update trigger to allow updating drafts in review / rejected state
CREATE OR REPLACE FUNCTION protect_executed_migrations() RETURNS trigger AS $$
BEGIN
    IF OLD.status IN ('SUCCESS', 'FAILED', 'ROLLED_BACK', 'ABORTED') AND (
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

-- Create migration_approvals table
CREATE TABLE IF NOT EXISTS migration_approvals (
    id               UUID PRIMARY KEY,
    migration_id     UUID        NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
    group_id         UUID        NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    leader_id        UUID        REFERENCES users(id) ON DELETE SET NULL,
    status           VARCHAR(16) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED')),
    reviewed_at      TIMESTAMPTZ,
    note             TEXT        NOT NULL DEFAULT '',
    rejection_reason TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT migration_approvals_migration_group_key UNIQUE (migration_id, group_id)
);
CREATE INDEX IF NOT EXISTS migration_approvals_migration_idx ON migration_approvals (migration_id);
