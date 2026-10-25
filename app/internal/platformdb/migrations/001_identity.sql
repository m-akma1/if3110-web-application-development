-- 001_identity.sql
-- Principals (users), session lifecycle, and groups.
-- Every user is always a member of the system "Public" group (enforced by triggers below).

CREATE TABLE users (
    id            UUID PRIMARY KEY,
    username      VARCHAR(32)  NOT NULL CHECK (username = LOWER(username) AND LENGTH(username) >= 3),
    email         VARCHAR(254) CHECK (email IS NULL OR email = LOWER(email)),
    password_hash TEXT         NOT NULL,
    role          VARCHAR(32)  NOT NULL DEFAULT 'USER' CHECK (role IN ('USER', 'PLATFORM_ADMIN')),
    is_active     BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX users_username_key ON users (username);
CREATE UNIQUE INDEX users_email_key ON users (email) WHERE email IS NOT NULL;

CREATE TABLE sessions (
    id                      UUID PRIMARY KEY,
    user_id                 UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash              CHAR(64)    NOT NULL UNIQUE,
    previous_token_hash     CHAR(64),
    grace_period_expires_at TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_activity_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_rotated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    idle_expires_at         TIMESTAMPTZ NOT NULL,
    absolute_expires_at     TIMESTAMPTZ NOT NULL,
    is_revoked              BOOLEAN     NOT NULL DEFAULT FALSE
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_previous_token_hash_idx ON sessions (previous_token_hash);

CREATE TABLE groups (
    id          UUID PRIMARY KEY,
    name        VARCHAR(64) NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    is_system   BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX groups_name_key ON groups (LOWER(name));

CREATE TABLE group_members (
    group_id UUID        NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX group_members_user_id_idx ON group_members (user_id);

-- The Public group has a fixed id so code can refer to it (authn.PublicGroupID).
INSERT INTO groups (id, name, description, is_system)
VALUES ('00000000-0000-0000-0000-000000000001', 'Public',
        'Every user is a member. Has no database access unless the Platform Admin grants it.', TRUE);

-- New users join Public automatically, whoever creates them (signup, admin, bootstrap, seed).
CREATE FUNCTION add_user_to_public_group() RETURNS trigger AS $$
BEGIN
    INSERT INTO group_members (group_id, user_id)
    VALUES ('00000000-0000-0000-0000-000000000001', NEW.id)
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_join_public_group
AFTER INSERT ON users
FOR EACH ROW EXECUTE FUNCTION add_user_to_public_group();

-- Public membership can only disappear together with the user (ON DELETE CASCADE).
CREATE FUNCTION protect_public_membership() RETURNS trigger AS $$
BEGIN
    IF OLD.group_id = '00000000-0000-0000-0000-000000000001'
       AND EXISTS (SELECT 1 FROM users WHERE id = OLD.user_id) THEN
        RAISE EXCEPTION 'users cannot leave the Public group' USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER group_members_protect_public
BEFORE DELETE ON group_members
FOR EACH ROW EXECUTE FUNCTION protect_public_membership();

-- System groups cannot be deleted.
CREATE FUNCTION protect_system_groups() RETURNS trigger AS $$
BEGIN
    IF OLD.is_system THEN
        RAISE EXCEPTION 'system group "%" cannot be deleted', OLD.name USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER groups_protect_system
BEFORE DELETE ON groups
FOR EACH ROW EXECUTE FUNCTION protect_system_groups();
