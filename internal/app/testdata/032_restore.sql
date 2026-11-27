-- Row-level undo of 032 data changes. Not shipped as a migration unless
-- Manager/CJ choose to; kept as a tested constant. Skips rows a person has
-- changed since 032 ran.
INSERT INTO item_targets (household_id, item_id, kind, value)
SELECT b.household_id, b.item_id, b.kind, b.value
FROM migration_backup_item_targets b
WHERE b.migration_id = '032'
  AND EXISTS (SELECT 1 FROM items i WHERE i.id = b.item_id AND i.household_id = b.household_id)
ON CONFLICT (household_id, item_id) DO NOTHING;

UPDATE product_groups
SET goal_items = NULL, window_months = NULL
WHERE EXISTS (
    SELECT 1 FROM migration_backup_group_goals bg
    WHERE bg.migration_id = '032' AND bg.household_id = product_groups.household_id
      AND bg.group_id = product_groups.id
      AND ((bg.new_kind = 'items' AND product_groups.goal_items = bg.new_value AND product_groups.window_months IS NULL)
        OR (bg.new_kind = 'months' AND product_groups.window_months = bg.new_value AND product_groups.goal_items IS NULL))
      AND product_groups.quantity_base_value IS NULL);

UPDATE shopping_list_items
SET touched = 1
WHERE touched = 0 AND EXISTS (
    SELECT 1 FROM migration_backup_list_edits bl
    WHERE bl.migration_id = '032' AND bl.household_id = shopping_list_items.household_id
      AND bl.entry_id = shopping_list_items.id AND bl.old_touched = 1);