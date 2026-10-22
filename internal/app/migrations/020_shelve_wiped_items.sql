-- A wipe keeps the item row so its target can reattach, and hides that row
-- until the next stock-in. Inventory and the planner both skip shelved rows.
ALTER TABLE items ADD COLUMN shelved INTEGER NOT NULL DEFAULT 0;

-- Every stock-in path inserts an instance. Putting the item back on the
-- shelf here covers scan commit, corrections, and direct adds.
CREATE TRIGGER unshelve_item_on_stock_in
AFTER INSERT ON item_instances
FOR EACH ROW
BEGIN
    UPDATE items SET shelved = 0 WHERE id = NEW.item_id;
END;
