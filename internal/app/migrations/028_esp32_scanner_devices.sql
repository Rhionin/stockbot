-- ESP32 scanner device kind, per-device settings, battery telemetry, device-scan
-- LCD fields on scan_entries, and device_scan_receipts for per-client_scan_id replay.

CREATE TABLE devices_new (
    id                  TEXT PRIMARY KEY,
    household_id        TEXT NOT NULL,
    kind                TEXT NOT NULL CHECK (kind IN ('direct', 'pi_hub', 'box_hub', 'esp32_scanner')),
    label               TEXT NOT NULL,
    paired_by           TEXT,
    created_at          TEXT NOT NULL,
    first_seen_at       TEXT,
    last_seen_at        TEXT,
    revoked_at          TEXT,
    hub_version         TEXT,
    config_version      INTEGER NOT NULL DEFAULT 0,
    dropped_total       INTEGER NOT NULL DEFAULT 0,
    queued              INTEGER NOT NULL DEFAULT 0,
    boot_id             TEXT NOT NULL DEFAULT '',
    uptime_s            INTEGER NOT NULL DEFAULT 0,
    clock_synced        INTEGER NOT NULL DEFAULT 0,
    rollback            INTEGER NOT NULL DEFAULT 0,
    default_direction   TEXT NOT NULL DEFAULT 'stock_out' CHECK (default_direction IN ('stock_in', 'stock_out')),
    inactivity_revert_s INTEGER NOT NULL DEFAULT 300 CHECK (inactivity_revert_s >= 30 AND inactivity_revert_s <= 3600),
    commit_mode         TEXT NOT NULL DEFAULT 'queue' CHECK (commit_mode IN ('queue', 'immediate')),
    battery_pct         INTEGER CHECK (battery_pct IS NULL OR (battery_pct >= 0 AND battery_pct <= 100)),
    charging            INTEGER CHECK (charging IS NULL OR charging IN (0, 1))
);

INSERT INTO devices_new (
    id, household_id, kind, label, paired_by, created_at, first_seen_at, last_seen_at,
    revoked_at, hub_version, config_version, dropped_total, queued, boot_id, uptime_s,
    clock_synced, rollback
)
SELECT
    id, household_id, kind, label, paired_by, created_at, first_seen_at, last_seen_at,
    revoked_at, hub_version, config_version, dropped_total, queued, boot_id, uptime_s,
    clock_synced, rollback
FROM devices;

DROP TABLE devices;
ALTER TABLE devices_new RENAME TO devices;

ALTER TABLE scan_entries ADD COLUMN device_result_status TEXT;
ALTER TABLE scan_entries ADD COLUMN device_on_hand INTEGER;
ALTER TABLE scan_entries ADD COLUMN device_queued_count INTEGER;
ALTER TABLE scan_entries ADD COLUMN device_delta INTEGER;
ALTER TABLE scan_entries ADD COLUMN device_backfilled INTEGER NOT NULL DEFAULT 0;

CREATE TABLE device_scan_receipts (
    household_id          TEXT NOT NULL,
    device_id             TEXT NOT NULL,
    client_scan_id        TEXT NOT NULL,
    scan_entry_id         TEXT NOT NULL,
    barcode               TEXT NOT NULL,
    direction             TEXT,
    unit_count            INTEGER NOT NULL,
    scanned_at            TEXT NOT NULL,
    device_result_status  TEXT,
    device_on_hand        INTEGER,
    device_queued_count   INTEGER,
    device_delta          INTEGER,
    device_backfilled     INTEGER NOT NULL DEFAULT 0,
    product_name          TEXT,
    PRIMARY KEY (household_id, device_id, client_scan_id)
);

CREATE INDEX idx_device_scan_receipts_entry ON device_scan_receipts (scan_entry_id);
