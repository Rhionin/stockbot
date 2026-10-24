-- The same work now runs in Go, in the transaction that deletes the item
-- or inserts the stocked unit. DSQL has no triggers.

DROP TRIGGER IF EXISTS delete_item_target_with_item;
DROP TRIGGER IF EXISTS unshelve_item_on_stock_in;
