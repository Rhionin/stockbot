-- One-trip staged-cart skips. trip_seq advances on start-new-cart and on mass stock-in close.
ALTER TABLE households ADD COLUMN trip_seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE households ADD COLUMN trip_closed_at TEXT;
ALTER TABLE households ADD COLUMN trip_closed_cause TEXT CHECK (trip_closed_cause IS NULL OR trip_closed_cause IN ('new_cart', 'put_away'));

ALTER TABLE staged_cart_skips ADD COLUMN expires_trip INTEGER NOT NULL DEFAULT 1;
ALTER TABLE staged_cart_skips ADD COLUMN removed_at TEXT;

ALTER TABLE stock_in_events ADD COLUMN store_id TEXT;

-- Marks a mass stock-in cluster that already advanced trip_seq (idempotent close).
CREATE TABLE IF NOT EXISTS stock_in_trip_closures (
    household_id TEXT NOT NULL,
    cluster_started_at TEXT NOT NULL,
    PRIMARY KEY (household_id, cluster_started_at)
);
