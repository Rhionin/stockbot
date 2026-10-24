-- Stage 1 household tables. Existing rows move in the later 022 steps.
-- This file does not copy the pantry file. The pre-deploy backup lives off the Pi.

CREATE TABLE households (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    restock_mode    TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    deleted_at      TEXT,
    purge_after     TEXT,
    channel_key     TEXT NOT NULL,
    is_primary      INTEGER NOT NULL CHECK (is_primary IN (0, 1)),
    legacy_user_id  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE users (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    email_norm   TEXT NOT NULL UNIQUE,
    pw_hash      TEXT NOT NULL,
    idp          TEXT NOT NULL DEFAULT 'local',
    idp_subject  TEXT,
    operator     INTEGER NOT NULL DEFAULT 0 CHECK (operator IN (0, 1)),
    created_at   TEXT NOT NULL,
    deleted_at   TEXT
);

CREATE TABLE memberships (
    household_id TEXT NOT NULL,
    user_id      TEXT NOT NULL,
    role         TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    created_at   TEXT NOT NULL,
    PRIMARY KEY (household_id, user_id)
);

CREATE TABLE sessions (
    id_hash              TEXT PRIMARY KEY,
    user_id              TEXT,
    kind                 TEXT NOT NULL CHECK (kind IN ('cookie', 'bearer')),
    origin               TEXT NOT NULL CHECK (origin IN ('personal', 'household_password')),
    active_household_id  TEXT NOT NULL,
    created_at           TEXT NOT NULL,
    last_seen_at         TEXT NOT NULL,
    expires_at           TEXT NOT NULL,
    revoked_at           TEXT
);

CREATE TABLE invites (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL CHECK (kind IN ('start_household', 'join_household')),
    household_id TEXT,
    code_hash    TEXT NOT NULL,
    created_by   TEXT,
    expires_at   TEXT NOT NULL,
    used_at      TEXT,
    cancelled_at TEXT,
    attempts     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE reset_links (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    code_hash  TEXT NOT NULL,
    created_by TEXT,
    expires_at TEXT NOT NULL,
    used_at    TEXT
);

CREATE TABLE devices (
    id              TEXT PRIMARY KEY,
    household_id    TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('direct', 'pi_hub', 'box_hub')),
    label           TEXT NOT NULL,
    paired_by       TEXT,
    created_at      TEXT NOT NULL,
    first_seen_at   TEXT,
    last_seen_at    TEXT,
    revoked_at      TEXT,
    hub_version     TEXT,
    config_version  INTEGER NOT NULL DEFAULT 0,
    dropped_total   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE device_tokens (
    id            TEXT PRIMARY KEY,
    device_id     TEXT NOT NULL,
    household_id  TEXT NOT NULL,
    token_hash    TEXT NOT NULL,
    prefix        TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    superseded_at TEXT,
    grace_until   TEXT
);

CREATE TABLE pairing_codes (
    id                   TEXT PRIMARY KEY,
    household_id         TEXT NOT NULL,
    code_hash            TEXT NOT NULL,
    created_by           TEXT,
    expires_at           TEXT NOT NULL,
    attempts             INTEGER NOT NULL DEFAULT 0,
    completed_device_id  TEXT
);

CREATE TABLE audit_log (
    id           TEXT PRIMARY KEY,
    household_id TEXT,
    actor_kind   TEXT NOT NULL,
    actor_id     TEXT,
    action       TEXT NOT NULL,
    target_kind  TEXT,
    target_id    TEXT,
    ip_hash      TEXT,
    at           TEXT NOT NULL
);

CREATE TABLE household_password (
    household_id  TEXT PRIMARY KEY,
    login_name    TEXT NOT NULL DEFAULT '',
    pw_hash       TEXT,
    state         TEXT NOT NULL DEFAULT 'on' CHECK (state IN ('on', 'off')),
    session_key   TEXT,
    retired_at    TEXT,
    restore_until TEXT
);

CREATE TABLE change_versions (
    household_id TEXT NOT NULL,
    scope        TEXT NOT NULL CHECK (scope IN ('scans', 'stock', 'list', 'devices')),
    version      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (household_id, scope)
);

CREATE TABLE household_settings (
    household_id TEXT NOT NULL,
    key          TEXT NOT NULL,
    value        TEXT NOT NULL,
    PRIMARY KEY (household_id, key)
);

-- Setup codes are server-wide. The row is the source of truth; the display
-- file is only a copy. code_hash is argon2id. mode is setup or recover.
CREATE TABLE setup_codes (
    id         TEXT PRIMARY KEY,
    code_hash  TEXT NOT NULL,
    mode       TEXT NOT NULL CHECK (mode IN ('setup', 'recover')),
    issued_at  TEXT NOT NULL,
    used       INTEGER NOT NULL DEFAULT 0 CHECK (used IN (0, 1))
);

INSERT INTO households (
    id, name, restock_mode, created_at, channel_key, is_primary, legacy_user_id
) VALUES (
    '{{household}}', 'Primary', '', '{{issued}}', '{{channel}}', 1, 'user-1'
);

INSERT INTO app_settings (key, value) VALUES ('primary_household_id', '{{household}}')
ON CONFLICT(key) DO UPDATE SET value = excluded.value;
