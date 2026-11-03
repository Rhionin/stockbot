-- PROPOSED: CJ approves this migration at the PR for #493 / #481 B1.
-- Design reference: docs/design/multi-store-sourcing.md §3.

CREATE TABLE IF NOT EXISTS stores (
    id              TEXT PRIMARY KEY,
    household_id    TEXT NOT NULL,
    name            TEXT NOT NULL,                     -- trimmed, 1-40 chars (Go)
    name_key        TEXT NOT NULL,                     -- case-folded trimmed name (Go)
    kind            TEXT NOT NULL DEFAULT 'physical' CHECK (kind IN ('physical','online')),
    linked_provider TEXT CHECK (linked_provider IS NULL OR linked_provider IN ('kroger')),
    location_id     TEXT,                              -- Kroger 8-char locationId; only when linked
    location_chain  TEXT,                              -- Kroger chain name of that location (cart URL, banner)
    position        INTEGER NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (household_id, name_key)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_stores_one_per_provider
    ON stores (household_id, linked_provider) WHERE linked_provider IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_stores_household_position ON stores (household_id, position);

-- What people set. Never written by automation (set_by_user_id NOT NULL).
CREATE TABLE IF NOT EXISTS product_stores (
    household_id    TEXT NOT NULL,
    product_id      TEXT NOT NULL,
    store_id        TEXT NOT NULL,
    state           TEXT NOT NULL CHECK (state IN ('carries','excluded')),
    is_usual        INTEGER NOT NULL DEFAULT 0 CHECK (is_usual IN (0,1)),
    set_by_user_id  TEXT NOT NULL,
    set_at          TEXT NOT NULL,
    PRIMARY KEY (household_id, product_id, store_id),
    CHECK (NOT (is_usual = 1 AND state = 'excluded'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_product_stores_one_usual
    ON product_stores (household_id, product_id) WHERE is_usual = 1;
CREATE INDEX IF NOT EXISTS idx_product_stores_store ON product_stores (household_id, store_id);

-- Learned from trips. Household history; survives store removal (DEP-4) and Kroger disconnect.
CREATE TABLE IF NOT EXISTS store_trips (
    id              TEXT PRIMARY KEY,
    household_id    TEXT NOT NULL,
    product_id      TEXT NOT NULL,
    store_id        TEXT NOT NULL,                     -- soft reference
    store_name      TEXT NOT NULL,                     -- snapshot at write time
    store_name_key  TEXT NOT NULL,                     -- snapshot, for relink on re-add
    kind            TEXT NOT NULL CHECK (kind IN ('bought','this_time')),
    line_id         TEXT,
    via_run         INTEGER NOT NULL DEFAULT 0 CHECK (via_run IN (0,1)),
    actor_user_id   TEXT,
    occurred_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_store_trips_product ON store_trips (household_id, product_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_store_trips_store ON store_trips (household_id, store_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_store_trips_one_this_time ON store_trips (line_id) WHERE kind = 'this_time';
CREATE UNIQUE INDEX IF NOT EXISTS idx_store_trips_one_bought ON store_trips (line_id) WHERE kind = 'bought';

-- Kroger check cache (vendor content), per household only. Expired rows are never read and are purged.
-- Deleted for the household when its Kroger connection is removed.
CREATE TABLE IF NOT EXISTS store_availability (
    household_id       TEXT NOT NULL,
    store_id           TEXT NOT NULL,
    product_id         TEXT NOT NULL,
    source             TEXT NOT NULL DEFAULT 'kroger_api' CHECK (source IN ('kroger_api')),
    location_id        TEXT NOT NULL,
    upc                TEXT,
    status             TEXT NOT NULL CHECK (status IN ('carried','not_found','error')),
    stock_level        TEXT,
    price_cents        INTEGER,
    promo_price_cents  INTEGER,
    checked_at         TEXT NOT NULL,
    status_expires_at  TEXT NOT NULL,
    detail_expires_at  TEXT,
    PRIMARY KEY (household_id, store_id, product_id)
);
CREATE INDEX IF NOT EXISTS idx_store_availability_expiry ON store_availability (status_expires_at);

CREATE TABLE IF NOT EXISTS store_checks (
    household_id   TEXT NOT NULL,
    store_id       TEXT NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('running','done','failed')),
    trigger        TEXT NOT NULL CHECK (trigger IN ('daily','product_added','store_setup','manual','send')),
    started_at     TEXT NOT NULL,
    finished_at    TEXT,
    lease_until    TEXT NOT NULL,
    checked_count  INTEGER NOT NULL DEFAULT 0,
    found_count    INTEGER NOT NULL DEFAULT 0,
    total_count    INTEGER NOT NULL DEFAULT 0,
    failure        TEXT CHECK (failure IS NULL OR failure IN ('budget','kroger_unavailable','rate_limited','auth')),
    PRIMARY KEY (household_id, store_id)
);

CREATE TABLE IF NOT EXISTS kroger_api_window (
    app_key            TEXT NOT NULL,
    endpoint           TEXT NOT NULL CHECK (endpoint IN ('products','locations')),
    window_started_at  TEXT NOT NULL,
    calls              INTEGER NOT NULL DEFAULT 0,
    blocked_until      TEXT,
    PRIMARY KEY (app_key, endpoint)
);
CREATE TABLE IF NOT EXISTS kroger_household_usage (
    household_id       TEXT NOT NULL,
    endpoint           TEXT NOT NULL CHECK (endpoint IN ('products','locations')),
    app_key            TEXT NOT NULL,
    window_started_at  TEXT NOT NULL,
    job_calls          INTEGER NOT NULL DEFAULT 0,
    on_demand_calls    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (household_id, endpoint)
);

-- Last statements (transactional runner).
ALTER TABLE shopping_list_items ADD COLUMN run_store_id TEXT;
CREATE INDEX IF NOT EXISTS idx_shopping_list_items_run_store
    ON shopping_list_items (household_id, run_store_id) WHERE run_store_id IS NOT NULL;
-- Scopes granted at the last code exchange. NULL = connected before #481
-- (cart.basic:write only): no product checks, no error (checkReason no_product_access).
ALTER TABLE provider_connections ADD COLUMN granted_scope TEXT;
-- Q4 = B (§4.6): the household app that issued this connection, and the app a
-- pending sign-in started with. app_key = first 16 hex chars of SHA-256(client_id).
-- NULL app_key = connected before B11a; the cutover (§4.6.5) sets the primary household's.
ALTER TABLE provider_connections ADD COLUMN app_key TEXT;
ALTER TABLE provider_connections ADD COLUMN auth_app_key TEXT;
-- Where the callback sends the browser after a sign-in: 'shopping' (NULL) or 'household' (F9, §4.6.7).
ALTER TABLE provider_connections ADD COLUMN auth_return TEXT;
-- Set only by test-and-save (§4.6.3): NULL = unknown, 0 = no Products, 1 = can check products.
ALTER TABLE provider_app_credentials ADD COLUMN product_access INTEGER;
ALTER TABLE provider_app_credentials ADD COLUMN product_access_at TEXT;
