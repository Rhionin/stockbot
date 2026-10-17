-- Restock targets by level. A target of 0 means don't restock.
-- household_id is the tenant key (today the same id as items.user_id).
-- Item targets are keyed by the household's item, not the shared product.
-- The migrator writes a pantry-pre-targets backup, then applies this file
-- in one transaction so a failure leaves the database unchanged.

-- A group may set an explicit 0. NULL still means the group sets nothing.
-- SQLite cannot change a CHECK, so product_groups is rebuilt.
CREATE TABLE product_groups_next (
    id                  TEXT PRIMARY KEY,
    user_id             TEXT NOT NULL DEFAULT 'user-1',
    name                TEXT NOT NULL,
    name_key            TEXT NOT NULL,
    rule                TEXT NOT NULL DEFAULT 'same_as_ran_out',
    rule_confirmed      INTEGER NOT NULL DEFAULT 0,
    pinned_product_id   TEXT,
    window_months       INTEGER,
    quantity_base_value REAL,
    quantity_dimension  TEXT,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
    window_months, quantity_base_value, quantity_dimension, created_at
)
SELECT
    id, user_id, name, name_key, rule, rule_confirmed, pinned_product_id,
    window_months, quantity_base_value, quantity_dimension, created_at
FROM product_groups;

DROP TABLE product_groups;
ALTER TABLE product_groups_next RENAME TO product_groups;

CREATE TABLE categories (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL,
    name         TEXT NOT NULL,
    name_key     TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (household_id, name_key)
);

CREATE TABLE category_targets (
    household_id TEXT NOT NULL,
    category_id  TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('months', 'amount')),
    value        REAL NOT NULL CHECK (value >= 0 AND value <= 999),
    PRIMARY KEY (household_id, category_id),
    CHECK (kind != 'months' OR value <= 12)
);

CREATE TABLE item_targets (
    household_id TEXT NOT NULL,
    item_id      TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('months', 'amount')),
    value        REAL NOT NULL CHECK (value >= 0 AND value <= 999),
    PRIMARY KEY (household_id, item_id),
    CHECK (kind != 'months' OR value <= 12)
);

CREATE INDEX idx_item_targets_item ON item_targets (item_id);

CREATE TABLE item_categories (
    household_id TEXT NOT NULL,
    item_id      TEXT NOT NULL,
    category_id  TEXT NOT NULL,
    source       TEXT NOT NULL CHECK (source IN ('off', 'suggested', 'user')),
    confirmed_at DATETIME,
    removed_at   DATETIME,
    PRIMARY KEY (household_id, item_id, category_id)
);

CREATE INDEX idx_item_categories_category ON item_categories (category_id);

CREATE TABLE category_suggestion_dismissals (
    household_id TEXT NOT NULL,
    item_id      TEXT NOT NULL,
    category_key TEXT NOT NULL,
    PRIMARY KEY (household_id, item_id, category_key)
);

-- Raw Open Food Facts tags travel with the shared product, not the household.
CREATE TABLE product_off_tags (
    product_id        TEXT PRIMARY KEY,
    categories_tags   TEXT NOT NULL DEFAULT '[]',
    food_groups_tags  TEXT NOT NULL DEFAULT '[]'
);

-- Ungrouped supply overrides become item targets. Overrides on a grouped
-- product were ignored by the planner, so they are left in supply_overrides.
INSERT INTO item_targets (household_id, item_id, kind, value)
SELECT i.user_id, i.id,
    CASE WHEN o.window_months IS NOT NULL THEN 'months' ELSE 'amount' END,
    CASE WHEN o.window_months IS NOT NULL THEN o.window_months ELSE MIN(o.quantity, 999) END
FROM supply_overrides o
JOIN items i ON i.product_id = o.product_id
WHERE NOT EXISTS (SELECT 1 FROM product_group_members m WHERE m.product_id = o.product_id)
  AND NOT EXISTS (SELECT 1 FROM item_targets t WHERE t.household_id = i.user_id AND t.item_id = i.id)
  AND (o.window_months IS NOT NULL OR (o.quantity IS NOT NULL AND o.quantity > 0));

-- A member marked don't restock becomes an item target of 0.
INSERT INTO item_targets (household_id, item_id, kind, value)
SELECT i.user_id, i.id, 'months', 0
FROM product_group_members m
JOIN items i ON i.product_id = m.product_id
WHERE m.no_restock = 1
  AND NOT EXISTS (SELECT 1 FROM item_targets t WHERE t.household_id = i.user_id AND t.item_id = i.id);

-- A fixed quantity in target mode becomes an item amount, when nothing else
-- already set a target and the product is not in a group.
-- A stored count of 0 was ignored by the planner, so it is not "don't restock".
-- Counts above 999 are clamped to the amount the screens can store.
INSERT INTO item_targets (household_id, item_id, kind, value)
SELECT i.user_id, i.id, 'amount', MIN(i.target_quantity, 999)
FROM items i
WHERE i.target_quantity > 0
  AND COALESCE(i.replenishment_mode, 'target') = 'target'
  AND NOT EXISTS (SELECT 1 FROM item_targets t WHERE t.household_id = i.user_id AND t.item_id = i.id)
  AND NOT EXISTS (SELECT 1 FROM product_group_members m WHERE m.product_id = i.product_id)
  AND NOT EXISTS (SELECT 1 FROM supply_overrides o WHERE o.product_id = i.product_id);
