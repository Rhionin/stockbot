-- A backfill stock-in is a history row with source backfill. SQLite cannot
-- change a CHECK with ALTER TABLE, so stock_moves is rebuilt. Existing rows
-- are copied first. The copy and the schema_migrations row commit together.

CREATE TABLE stock_moves_next (
    id              TEXT PRIMARY KEY,
    item_id         TEXT NOT NULL,
    product_id      TEXT NOT NULL,
    direction       TEXT NOT NULL CHECK (direction IN ('in', 'out')),
    quantity        INTEGER NOT NULL CHECK (quantity >= 1),
    at              DATETIME NOT NULL,
    source          TEXT NOT NULL CHECK (source IN ('scan', 'manual', 'backfill')),
    scan_entry_id   TEXT,
    tracked         INTEGER NOT NULL DEFAULT 0 CHECK (tracked IN (0, 1)),
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO stock_moves_next (
    id, item_id, product_id, direction, quantity, at, source, scan_entry_id, tracked, created_at
)
SELECT
    id, item_id, product_id, direction, quantity, at, source, scan_entry_id, tracked, created_at
FROM stock_moves;

DROP TABLE stock_moves;
ALTER TABLE stock_moves_next RENAME TO stock_moves;

CREATE INDEX stock_moves_item_at ON stock_moves (item_id, at);
CREATE INDEX stock_moves_product_at ON stock_moves (product_id, at);
