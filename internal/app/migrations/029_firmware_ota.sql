CREATE TABLE firmware_releases (
    id          TEXT PRIMARY KEY,                 -- fwr_<ulid>
    product     TEXT NOT NULL DEFAULT 'esp32_scanner',
    target      TEXT NOT NULL,
    version     TEXT NOT NULL,
    size        INTEGER NOT NULL,
    sha256      TEXT NOT NULL,
    key_id      TEXT NOT NULL,
    sig         TEXT NOT NULL,
    channel     TEXT NOT NULL DEFAULT 'beta' CHECK (channel IN ('beta','stable')),
    path        TEXT NOT NULL,                    -- under /data/firmware
    created_at  TEXT NOT NULL,
    promoted_at TEXT,
    yanked_at   TEXT,
    UNIQUE (target, version)
);
ALTER TABLE devices ADD COLUMN fw_target TEXT;
ALTER TABLE devices ADD COLUMN fw_channel TEXT NOT NULL DEFAULT 'stable' CHECK (fw_channel IN ('beta','stable'));
ALTER TABLE devices ADD COLUMN fw_pinned_release_id TEXT;
ALTER TABLE devices ADD COLUMN ota_state TEXT;
ALTER TABLE devices ADD COLUMN ota_release_id TEXT;
ALTER TABLE devices ADD COLUMN ota_last_error TEXT;
ALTER TABLE devices ADD COLUMN ota_updated_at TEXT;
ALTER TABLE devices ADD COLUMN ota_retry_release_id TEXT;
