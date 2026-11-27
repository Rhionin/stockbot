-- #771: one goal per group, item-count group goals, one-trip list edits.
-- Applied in one transaction (applyMigrationTx). No in-volume snapshot
-- (multi-household-staged.md 2.5); the pre-deploy R2 backup is the full
-- rollback, and the three backup tables below are the row-level undo.

-- A group goal is exactly one of: window_months, quantity_base_value +
-- quantity_dimension, or goal_items. goal_items counts units on hand
-- across all members.
ALTER TABLE product_groups ADD COLUMN goal_items INTEGER
    CHECK (goal_items IS NULL OR (
        goal_items BETWEEN 1 AND 999
        AND window_months IS NULL
        AND quantity_base_value IS NULL
        AND quantity_dimension IS NULL));

-- An edit (touched = 1) lasts while touched_expires_trip > households.trip_seq.
-- touched_by_user_id / touched_at say who made the edit and when (UX A8).
ALTER TABLE shopping_list_items ADD COLUMN touched_expires_trip INTEGER;
ALTER TABLE shopping_list_items ADD COLUMN touched_by_user_id TEXT;
ALTER TABLE shopping_list_items ADD COLUMN touched_at TEXT;

CREATE TABLE IF NOT EXISTS migration_backup_item_targets (
    migration_id TEXT NOT NULL,
    household_id TEXT NOT NULL,
    item_id      TEXT NOT NULL,
    product_id   TEXT NOT NULL,
    group_id     TEXT NOT NULL,
    kind         TEXT NOT NULL,
    value        REAL NOT NULL,
    backed_up_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (migration_id, household_id, item_id)
);

CREATE TABLE IF NOT EXISTS migration_backup_group_goals (
    migration_id        TEXT NOT NULL,
    household_id        TEXT NOT NULL,
    group_id            TEXT NOT NULL,
    old_window_months   INTEGER,
    old_quantity_base   REAL,
    old_quantity_dim    TEXT,
    old_goal_items      INTEGER,
    new_kind            TEXT NOT NULL CHECK (new_kind IN ('items', 'months')),
    new_value           INTEGER NOT NULL,
    backed_up_at        TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (migration_id, household_id, group_id)
);

CREATE TABLE IF NOT EXISTS migration_backup_list_edits (
    migration_id TEXT NOT NULL,
    household_id TEXT NOT NULL,
    entry_id     TEXT NOT NULL,
    old_touched  INTEGER NOT NULL,
    backed_up_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (migration_id, household_id, entry_id)
);

-- 1. Back up every item target above 0 on a grouped product (rule 7).
INSERT INTO migration_backup_item_targets (migration_id, household_id, item_id, product_id, group_id, kind, value)
SELECT '032', t.household_id, t.item_id, i.product_id, m.group_id, t.kind, t.value
FROM item_targets t
JOIN items i ON i.id = t.item_id AND i.household_id = t.household_id
JOIN product_group_members m ON m.product_id = i.product_id AND m.household_id = i.household_id
WHERE t.value > 0
ON CONFLICT (migration_id, household_id, item_id) DO NOTHING;

-- 2. Decide the goal for groups with no goal (rules 3 and 4).
-- Any amount member: items = largest amount. Months only: months = largest.
INSERT INTO migration_backup_group_goals (
    migration_id, household_id, group_id,
    old_window_months, old_quantity_base, old_quantity_dim, old_goal_items,
    new_kind, new_value)
SELECT '032', g.household_id, g.id,
       g.window_months, g.quantity_base_value, g.quantity_dimension, g.goal_items,
       CASE WHEN SUM(CASE WHEN b.kind = 'amount' THEN 1 ELSE 0 END) > 0 THEN 'items' ELSE 'months' END,
       CAST(ROUND(CASE WHEN SUM(CASE WHEN b.kind = 'amount' THEN 1 ELSE 0 END) > 0
            THEN MAX(CASE WHEN b.kind = 'amount' THEN b.value END)
            ELSE MAX(CASE WHEN b.kind = 'months' THEN b.value END) END) AS INTEGER)
FROM migration_backup_item_targets b
JOIN product_groups g ON g.id = b.group_id AND g.household_id = b.household_id
WHERE b.migration_id = '032'
  AND g.window_months IS NULL AND g.quantity_base_value IS NULL AND g.goal_items IS NULL
GROUP BY g.household_id, g.id
ON CONFLICT (migration_id, household_id, group_id) DO NOTHING;

-- 3. Apply those goals. Only a group that still has no goal is changed.
UPDATE product_groups
SET goal_items = (
    SELECT MIN(999, MAX(1, bg.new_value)) FROM migration_backup_group_goals bg
    WHERE bg.migration_id = '032' AND bg.household_id = product_groups.household_id
      AND bg.group_id = product_groups.id AND bg.new_kind = 'items')
WHERE window_months IS NULL AND quantity_base_value IS NULL AND goal_items IS NULL
  AND EXISTS (
    SELECT 1 FROM migration_backup_group_goals bg
    WHERE bg.migration_id = '032' AND bg.household_id = product_groups.household_id
      AND bg.group_id = product_groups.id AND bg.new_kind = 'items');

UPDATE product_groups
SET window_months = (
    SELECT MIN(12, MAX(1, bg.new_value)) FROM migration_backup_group_goals bg
    WHERE bg.migration_id = '032' AND bg.household_id = product_groups.household_id
      AND bg.group_id = product_groups.id AND bg.new_kind = 'months')
WHERE window_months IS NULL AND quantity_base_value IS NULL AND goal_items IS NULL
  AND EXISTS (
    SELECT 1 FROM migration_backup_group_goals bg
    WHERE bg.migration_id = '032' AND bg.household_id = product_groups.household_id
      AND bg.group_id = product_groups.id AND bg.new_kind = 'months');

-- 4. Remove member targets above 0 that were backed up (rules 2-4).
-- Item target 0 (don't restock) is never touched (rule 1).
DELETE FROM item_targets
WHERE value > 0
  AND EXISTS (
    SELECT 1 FROM migration_backup_item_targets b
    WHERE b.migration_id = '032' AND b.household_id = item_targets.household_id
      AND b.item_id = item_targets.item_id);

-- 5. List edits (rule 6). Back up, then un-mark lines whose only possible
-- edit was a per-store quantity: SwapAutoLine refuses a line with no group.
INSERT INTO migration_backup_list_edits (migration_id, household_id, entry_id, old_touched)
SELECT '032', household_id, id, touched FROM shopping_list_items
WHERE source = 'auto' AND touched = 1
ON CONFLICT (migration_id, household_id, entry_id) DO NOTHING;

UPDATE shopping_list_items SET touched = 0
WHERE source = 'auto' AND touched = 1 AND COALESCE(group_id, '') = '';

UPDATE shopping_list_items
SET touched_expires_trip = (SELECT h.trip_seq + 1 FROM households h WHERE h.id = shopping_list_items.household_id)
WHERE source = 'auto' AND touched = 1 AND touched_expires_trip IS NULL;
