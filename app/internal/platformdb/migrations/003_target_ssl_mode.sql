-- 003_target_ssl_mode.sql
-- TLS mode used when the platform connects to a target database.
-- 'disable' suits the bundled docker network.
-- real deployments should use 'require' or 'verify-full'.
ALTER TABLE target_databases
    ADD COLUMN ssl_mode VARCHAR(16) NOT NULL DEFAULT 'disable'
        CHECK (ssl_mode IN ('disable', 'require', 'verify-full'));
