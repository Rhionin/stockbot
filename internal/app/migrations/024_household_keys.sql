-- Household-owned uniqueness leads with household_id, so two households can
-- store the same product, barcode, endpoint, direction, or repair row.
-- {{household}} is the primary household, the same default 022b baked in.

CREATE TABLE supply_overrides_next (
    household_id  TEXT NOT NULL DEFAULT '{{household}}',
    product_id    TEXT NOT NULL,
    window_months INTEGER,
    quantity      INTEGER,
    PRIMARY KEY (household_id, product_id),
    CHECK ((window_months IS NOT NULL) <> (quantity IS NOT NULL)),
    CHECK (window_months IS NULL OR window_months BETWEEN 1 AND 12),
    CHECK (quantity IS NULL OR quantity BETWEEN 1 AND 999)
);

INSERT INTO supply_overrides_next (household_id, product_id, window_months, quantity)
SELECT household_id, product_id, window_months, quantity FROM supply_overrides;

DROP TABLE supply_overrides;
ALTER TABLE supply_overrides_next RENAME TO supply_overrides;

CREATE TABLE product_group_members_next (
    household_id TEXT NOT NULL DEFAULT '{{household}}',
    product_id   TEXT NOT NULL,
    group_id     TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    no_restock   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (household_id, product_id),
    CHECK (no_restock IN (0, 1))
);

INSERT INTO product_group_members_next (household_id, product_id, group_id, created_at, no_restock)
SELECT household_id, product_id, group_id, created_at, no_restock FROM product_group_members;

DROP TABLE product_group_members;
ALTER TABLE product_group_members_next RENAME TO product_group_members;

CREATE INDEX idx_product_group_members_group ON product_group_members (group_id);

CREATE TABLE scan_alert_schedule_next (
    household_id TEXT NOT NULL DEFAULT '{{household}}',
    direction    TEXT NOT NULL CHECK (direction IN ('stock_in', 'stock_out')),
    due_at       TEXT NOT NULL,
    scan_ids     TEXT NOT NULL,
    PRIMARY KEY (household_id, direction)
);

INSERT INTO scan_alert_schedule_next (household_id, direction, due_at, scan_ids)
SELECT household_id, direction, due_at, scan_ids FROM scan_alert_schedule;

DROP TABLE scan_alert_schedule;
ALTER TABLE scan_alert_schedule_next RENAME TO scan_alert_schedule;

CREATE TABLE push_subscriptions_next (
    household_id TEXT NOT NULL DEFAULT '{{household}}',
    endpoint     TEXT NOT NULL,
    p256dh       TEXT NOT NULL,
    auth         TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    origin       TEXT NOT NULL DEFAULT '',
    user_id      TEXT NOT NULL DEFAULT '',
    device_id    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (household_id, endpoint)
);

INSERT INTO push_subscriptions_next (
    household_id, endpoint, p256dh, auth, created_at, updated_at, origin, user_id, device_id
)
SELECT household_id, endpoint, p256dh, auth, created_at, updated_at, origin, user_id, device_id
FROM push_subscriptions;

DROP TABLE push_subscriptions;
ALTER TABLE push_subscriptions_next RENAME TO push_subscriptions;

CREATE TABLE fulfillment_ledger_next (
    household_id       TEXT NOT NULL DEFAULT '{{household}}',
    provider_id        TEXT NOT NULL,
    item_id            TEXT NOT NULL REFERENCES items(id),
    requested_quantity INTEGER NOT NULL DEFAULT 0,
    ledger_boundary    DATETIME NOT NULL,
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (household_id, provider_id, item_id)
);

INSERT INTO fulfillment_ledger_next (
    household_id, provider_id, item_id, requested_quantity, ledger_boundary, updated_at
)
SELECT household_id, provider_id, item_id, requested_quantity, ledger_boundary, updated_at
FROM fulfillment_ledger;

DROP TABLE fulfillment_ledger;
ALTER TABLE fulfillment_ledger_next RENAME TO fulfillment_ledger;

CREATE TABLE barcodes_next (
    barcode        TEXT NOT NULL,
    product_id     TEXT NOT NULL REFERENCES products(id),
    source         TEXT NOT NULL CHECK (source IN ('global', 'user_override')),
    user_id        TEXT NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    household_id   TEXT NOT NULL DEFAULT '{{household}}',
    actor_user_id  TEXT,
    UNIQUE (household_id, barcode, source, user_id)
);

INSERT INTO barcodes_next (barcode, product_id, source, user_id, created_at, household_id, actor_user_id)
SELECT barcode, product_id, source, user_id, created_at, household_id, actor_user_id FROM barcodes;

DROP TABLE barcodes;
ALTER TABLE barcodes_next RENAME TO barcodes;

CREATE TABLE history_repair_next (
    household_id TEXT NOT NULL DEFAULT '{{household}}',
    id           INTEGER NOT NULL CHECK (id = 1),
    PRIMARY KEY (household_id, id)
);

INSERT INTO history_repair_next (household_id, id)
SELECT household_id, id FROM history_repair;

DROP TABLE history_repair;
ALTER TABLE history_repair_next RENAME TO history_repair;

CREATE TABLE stock_in_events_next (
    household_id  TEXT NOT NULL DEFAULT '{{household}}',
    id            INTEGER NOT NULL,
    product_id    TEXT NOT NULL,
    at            TIMESTAMP NOT NULL,
    stock_move_id TEXT,
    PRIMARY KEY (household_id, id)
);

INSERT INTO stock_in_events_next (household_id, id, product_id, at, stock_move_id)
SELECT household_id, id, product_id, at, stock_move_id FROM stock_in_events;

DROP TABLE stock_in_events;
ALTER TABLE stock_in_events_next RENAME TO stock_in_events;

CREATE INDEX stock_in_events_product_at ON stock_in_events (product_id, at);

-- user_id no longer defaults to the retired account. Callers name the account.
CREATE TABLE product_groups_next (
    id                  TEXT PRIMARY KEY,
    user_id             TEXT NOT NULL,
    name                TEXT NOT NULL,
    name_key            TEXT NOT NULL,
    rule                TEXT NOT NULL DEFAULT 'same_as_ran_out',
    rule_confirmed      INTEGER NOT NULL DEFAULT 0,
    pinned_product_id   TEXT,
    window_months       INTEGER,
    quantity_base_value REAL,
    quantity_dimension  TEXT,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    household_id        TEXT NOT NULL DEFAULT '{{household}}',
    actor_user_id       TEXT,
    UNIQUE (user_id, name_key),
    CHECK (rule IN ('same_as_ran_out', 'favorite', 'best_deal', 'favor_variety')),
    CHECK (rule_confirmed IN (0, 1)),
    CHECK (window_months IS NULL OR (quantity_base_value IS NULL AND quantity_dimension IS NULL)),
    CHECK (
        (quantity_base_value IS NULL AND quantity_dimension IS NULL)
        OR (quantity_base_value IS NOT NULL AND quantity_dimension IS NOT NULL)
    ),
    CHECK (quantity_dimension IS NULL OR quantity_dimension IN ('mass', 'volume')),
    CHECK (window_months IS NULL OR window_months BETWEEN 0 AND 12),
    CHECK (quantity_base_value IS NULL OR quantity_base_value >= 0)
);

INSERT INTO product_groups_next (
    id, user_id, name, name_key, rule, rule_confirmed, pinned_product_id,
    window_months, quantity_base_value, quantity_dimension, created_at,
    household_id, actor_user_id
)
SELECT
    id, user_id, name, name_key, rule, rule_confirmed, pinned_product_id,
    window_months, quantity_base_value, quantity_dimension, created_at,
    household_id, actor_user_id
FROM product_groups;

DROP TABLE product_groups;
ALTER TABLE product_groups_next RENAME TO product_groups;

CREATE UNIQUE INDEX product_groups_household_name ON product_groups (household_id, name_key);

CREATE TABLE group_suggestions_next (
    id                 TEXT PRIMARY KEY,
    user_id            TEXT NOT NULL,
    kind               TEXT NOT NULL,
    title              TEXT NOT NULL,
    proposed_rule      TEXT,
    pinned_product_id  TEXT,
    existing_group_id  TEXT,
    status             TEXT NOT NULL DEFAULT 'open',
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    household_id       TEXT NOT NULL DEFAULT '{{household}}',
    actor_user_id      TEXT,
    CHECK (kind IN ('looks_alike', 'from_scan', 'from_old_plan')),
    CHECK (status IN ('open', 'accepted', 'dismissed')),
    CHECK (proposed_rule IS NULL OR proposed_rule IN ('same_as_ran_out', 'favorite', 'best_deal', 'favor_variety'))
);

INSERT INTO group_suggestions_next (
    id, user_id, kind, title, proposed_rule, pinned_product_id, existing_group_id, status, created_at,
    household_id, actor_user_id
)
SELECT
    id, user_id, kind, title, proposed_rule, pinned_product_id, existing_group_id, status, created_at,
    household_id, actor_user_id
FROM group_suggestions;

DROP TABLE group_suggestions;
ALTER TABLE group_suggestions_next RENAME TO group_suggestions;
