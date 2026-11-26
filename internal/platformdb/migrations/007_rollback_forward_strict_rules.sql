-- 007_rollback_forward_strict_rules.sql
-- Support append-only forward execution tracking and strict LIFO/linear ordering.

ALTER TABLE migrations ADD COLUMN IF NOT EXISTS source_migration_id UUID REFERENCES migrations(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS migrations_db_status_version_idx ON migrations (database_id, status, version);
