-- One-trip staged-cart skips and trip boundaries (#571 W1/D4).
ALTER TABLE households ADD COLUMN trip_seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE households ADD COLUMN trip_closed_at TEXT;
ALTER TABLE households ADD COLUMN trip_closed_cause TEXT CHECK (trip_closed_cause IS NULL OR trip_closed_cause IN ('new_cart', 'put_away'));
ALTER TABLE households ADD COLUMN stock_in_cluster_min INTEGER NOT NULL DEFAULT 5 CHECK (stock_in_cluster_min BETWEEN 2 AND 50);
ALTER TABLE households ADD COLUMN stock_in_cluster_min_updated_at TEXT;
ALTER TABLE households ADD COLUMN stock_in_cluster_min_updated_by TEXT;

ALTER TABLE staged_cart_skips ADD COLUMN expires_trip INTEGER NOT NULL DEFAULT 1;
ALTER TABLE staged_cart_skips ADD COLUMN removed_at TEXT;

ALTER TABLE stock_in_events ADD COLUMN store_id TEXT;

ALTER TABLE shopping_list_entry_adjustments ADD COLUMN actor_user_id TEXT;
ALTER TABLE shopping_list_entry_adjustments ADD COLUMN set_at TEXT;
ALTER TABLE shopping_list_entry_adjustments ADD COLUMN expires_trip INTEGER;

CREATE TABLE IF NOT EXISTS stock_in_clusters (
    household_id TEXT NOT NULL,
    started_at TEXT NOT NULL,
    closed_at TEXT NOT NULL,
    product_count INTEGER NOT NULL,
    k_used INTEGER NOT NULL,
    trip_seq INTEGER NOT NULL,
    PRIMARY KEY (household_id, started_at)
);

CREATE TABLE IF NOT EXISTS list_line_changes (
    household_id TEXT NOT NULL,
    line_key TEXT NOT NULL,
    effect TEXT NOT NULL,
    reason TEXT,
    cause TEXT NOT NULL,
    from_qty INTEGER,
    to_qty INTEGER,
    n INTEGER,
    actor_user_id TEXT,
    at TEXT NOT NULL,
    trip_seq INTEGER NOT NULL,
    PRIMARY KEY (household_id, line_key)
);

CREATE TABLE IF NOT EXISTS cart_line_results (
    household_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    item_id TEXT NOT NULL,
    entry_id TEXT,
    status TEXT NOT NULL,
    reason TEXT,
    quantity INTEGER,
    attempted_at TEXT NOT NULL,
    PRIMARY KEY (household_id, provider_id, item_id)
);

CREATE TABLE IF NOT EXISTS attention_marks (
    household_id TEXT NOT NULL,
    line_key TEXT NOT NULL,
    flagged_trip INTEGER NOT NULL,
    PRIMARY KEY (household_id, line_key)
);

CREATE INDEX IF NOT EXISTS idx_stock_moves_household_at ON stock_moves (household_id, at);
