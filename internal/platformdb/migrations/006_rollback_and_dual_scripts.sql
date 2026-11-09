-- 006_rollback_and_dual_scripts.sql
-- Support SQL Up & Down (rollback), irreversible options, and execution types.

ALTER TABLE migrations ADD COLUMN IF NOT EXISTS sql_up TEXT NOT NULL DEFAULT '';
ALTER TABLE migrations ADD COLUMN IF NOT EXISTS sql_down TEXT NOT NULL DEFAULT '';
ALTER TABLE migrations ADD COLUMN IF NOT EXISTS is_irreversible BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE migrations ADD COLUMN IF NOT EXISTS irreversible_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE migrations ADD COLUMN IF NOT EXISTS execution_type VARCHAR(16) NOT NULL DEFAULT 'FORWARD' CHECK (execution_type IN ('FORWARD', 'ROLLBACK', 'UP'));
ALTER TABLE migrations ADD COLUMN IF NOT EXISTS rollback_target_id UUID REFERENCES migrations(id) ON DELETE SET NULL;

-- Backfill existing rows: sql_up = sql_script
ALTER TABLE migrations DISABLE TRIGGER migrations_protect_executed;
UPDATE migrations SET sql_up = sql_script WHERE sql_up = '' AND sql_script <> '';
ALTER TABLE migrations ENABLE TRIGGER migrations_protect_executed;

-- Relax version constraint:
-- 1. Successful forward runs have a version.
-- 2. Rolled back runs keep their version.
-- 3. Rollback runs or unexecuted drafts have version IS NULL.
ALTER TABLE migrations DROP CONSTRAINT IF EXISTS migrations_version_iff_success;
ALTER TABLE migrations ADD CONSTRAINT migrations_version_iff_success
    CHECK ((status IN ('SUCCESS', 'ROLLED_BACK') AND execution_type <> 'ROLLBACK') = (version IS NOT NULL));

-- Update trigger to allow transitioning status from SUCCESS to ROLLED_BACK on rollback,
-- and from ROLLED_BACK to SUCCESS on forward re-apply.
CREATE OR REPLACE FUNCTION protect_executed_migrations() RETURNS trigger AS $$
BEGIN
    -- Allow status transition from SUCCESS to ROLLED_BACK (and vice versa)
    IF OLD.status = 'SUCCESS' AND NEW.status = 'ROLLED_BACK' THEN
        RETURN NEW;
    END IF;
    IF OLD.status = 'ROLLED_BACK' AND NEW.status = 'SUCCESS' THEN
        RETURN NEW;
    END IF;

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
