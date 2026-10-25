-- 004_migrations.sql
-- User-written migrations for target databases: drafts, executed runs, and their history.

-- Lifecycle: DRAFT (editable) -> SUCCESS or FAILED (immutable).
-- Only SUCCESS runs get a version: 1, 2, 3, ... per target database, assigned by the engine
-- while it holds that target's advisory lock. FAILED runs stay as history without a version.

CREATE TABLE migrations (
    id            UUID PRIMARY KEY,
    -- Removing a target database also removes its migration history.
    database_id   UUID         NOT NULL REFERENCES target_databases(id) ON DELETE CASCADE,
    version       INTEGER      CHECK (version > 0),
    name          VARCHAR(200) NOT NULL CHECK (LENGTH(BTRIM(name)) > 0),
    sql_script    TEXT         NOT NULL,
    status        VARCHAR(16)  NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'SUCCESS', 'FAILED')),
    created_by    UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    executed_by   UUID         REFERENCES users(id) ON DELETE SET NULL,
    executed_at   TIMESTAMPTZ,
    duration_ms   BIGINT       CHECK (duration_ms >= 0),
    error_message TEXT         NOT NULL DEFAULT '',
    -- A version exists exactly when the run succeeded.
    CONSTRAINT migrations_version_iff_success CHECK ((status = 'SUCCESS') = (version IS NOT NULL)),
    -- Drafts have no execution data; executed runs always do.
    CONSTRAINT migrations_executed_iff_not_draft CHECK ((status = 'DRAFT') = (executed_at IS NULL)),
    CONSTRAINT migrations_database_version_key UNIQUE (database_id, version)
);
CREATE INDEX migrations_database_created_idx ON migrations (database_id, created_at DESC);

-- Executed migrations are history: once a row leaves DRAFT, nothing may change it, except
-- created_by/executed_by becoming NULL when that user is deleted (ON DELETE SET NULL).
-- (Deleting is still allowed so a target database can be removed together with its history.)
CREATE FUNCTION protect_executed_migrations() RETURNS trigger AS $$
BEGIN
    IF OLD.status <> 'DRAFT' AND (
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

CREATE TRIGGER migrations_protect_executed
BEFORE UPDATE ON migrations
FOR EACH ROW EXECUTE FUNCTION protect_executed_migrations();
