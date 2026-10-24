-- Device queue depth, inferred scan clocks, and operator alerts.
-- Alerts store a household id and a kind. They do not store item names or barcodes.

ALTER TABLE devices ADD COLUMN queued INTEGER NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN boot_id TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN uptime_s INTEGER NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN clock_synced INTEGER NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN rollback INTEGER NOT NULL DEFAULT 0;

ALTER TABLE scan_entries ADD COLUMN time_approx INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scan_entries ADD COLUMN scan_group INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scan_entries ADD COLUMN boot_id TEXT NOT NULL DEFAULT '';

CREATE TABLE operator_alerts (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL,
    kind         TEXT NOT NULL,
    created_at   TEXT NOT NULL
);
