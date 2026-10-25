-- 002_access.sql
-- Target databases managed by the Platform Admin, per-group access grants, and invite codes.
--
-- Access model: a user's effective level on a database is the most permissive level granted
-- to any group they belong to (VIEW_EDIT > VIEW_ONLY > none). The rule itself lives in
-- internal/rbac; these tables only store the grants. "No access" is the absence of a row.

CREATE TABLE target_databases (
    id                 UUID PRIMARY KEY,
    name               VARCHAR(100) NOT NULL,
    host               VARCHAR(255) NOT NULL,
    port               INTEGER      NOT NULL CHECK (port BETWEEN 1 AND 65535),
    db_name            VARCHAR(63)  NOT NULL,
    username           VARCHAR(63)  NOT NULL,
    -- AES-256-GCM sealed password (nonce || ciphertext); never stored in plaintext.
    encrypted_password BYTEA        NOT NULL,
    status             VARCHAR(16)  NOT NULL DEFAULT 'UNKNOWN' CHECK (status IN ('UNKNOWN', 'CONNECTED', 'ERROR')),
    last_error         TEXT         NOT NULL DEFAULT '',
    last_checked_at    TIMESTAMPTZ,
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX target_databases_name_key ON target_databases (LOWER(name));

CREATE TABLE group_db_grants (
    group_id     UUID        NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    database_id  UUID        NOT NULL REFERENCES target_databases(id) ON DELETE CASCADE,
    access_level VARCHAR(16) NOT NULL CHECK (access_level IN ('VIEW_ONLY', 'VIEW_EDIT')),
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, database_id)
);
CREATE INDEX group_db_grants_database_id_idx ON group_db_grants (database_id);

-- Signing up with an active code adds the new user to the code's group (in addition to Public).
CREATE TABLE invite_codes (
    code       VARCHAR(64) PRIMARY KEY CHECK (code = UPPER(code) AND code ~ '^[A-Z0-9][A-Z0-9_-]{3,63}$'),
    group_id   UUID        NOT NULL REFERENCES groups(id) ON DELETE CASCADE
                           CHECK (group_id <> '00000000-0000-0000-0000-000000000001'),
    is_active  BOOLEAN     NOT NULL DEFAULT TRUE,
    created_by UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX invite_codes_group_id_idx ON invite_codes (group_id);
