-- Child rows take the household of their parent. Rows with no parent stay
-- on household 1, which the column default already set. actor_user_id keeps
-- the legacy user id beside the new household column.

UPDATE items SET actor_user_id = user_id WHERE actor_user_id IS NULL AND user_id != '';
UPDATE barcodes SET actor_user_id = user_id WHERE actor_user_id IS NULL AND user_id != '';
UPDATE brand_preferences SET actor_user_id = user_id WHERE actor_user_id IS NULL AND user_id != '';
UPDATE group_suggestion_dismissals SET actor_user_id = user_id WHERE actor_user_id IS NULL AND user_id != '';
UPDATE group_suggestions SET actor_user_id = user_id WHERE actor_user_id IS NULL AND user_id != '';
UPDATE item_deals SET actor_user_id = user_id WHERE actor_user_id IS NULL AND user_id != '';
UPDATE product_groups SET actor_user_id = user_id WHERE actor_user_id IS NULL AND user_id != '';
UPDATE provider_app_credentials SET actor_user_id = user_id WHERE actor_user_id IS NULL AND user_id != '';
UPDATE provider_connections SET actor_user_id = user_id WHERE actor_user_id IS NULL AND user_id != '';
UPDATE scan_entries SET actor_user_id = user_id WHERE actor_user_id IS NULL AND user_id != '';
UPDATE shopping_list_items SET actor_user_id = user_id WHERE actor_user_id IS NULL AND user_id != '';
UPDATE staged_cart_skips SET actor_user_id = user_id WHERE actor_user_id IS NULL AND user_id != '';

UPDATE item_instances
SET household_id = (SELECT items.household_id FROM items WHERE items.id = item_instances.item_id)
WHERE item_id IN (SELECT id FROM items);

UPDATE consumption_events
SET household_id = (SELECT items.household_id FROM items WHERE items.id = consumption_events.item_id)
WHERE item_id IN (SELECT id FROM items);

UPDATE fulfillment_ledger
SET household_id = (SELECT items.household_id FROM items WHERE items.id = fulfillment_ledger.item_id)
WHERE item_id IN (SELECT id FROM items);

UPDATE stock_moves
SET household_id = (SELECT items.household_id FROM items WHERE items.id = stock_moves.item_id)
WHERE item_id IN (SELECT id FROM items);

UPDATE shopping_list_entry_adjustments
SET household_id = (
    SELECT shopping_list_items.household_id
    FROM shopping_list_items
    WHERE shopping_list_items.id = shopping_list_entry_adjustments.entry_id
)
WHERE entry_id IN (SELECT id FROM shopping_list_items);

UPDATE group_suggestion_members
SET household_id = (
    SELECT group_suggestions.household_id
    FROM group_suggestions
    WHERE group_suggestions.id = group_suggestion_members.suggestion_id
)
WHERE suggestion_id IN (SELECT id FROM group_suggestions);

UPDATE product_group_members
SET household_id = (
    SELECT product_groups.household_id
    FROM product_groups
    WHERE product_groups.id = product_group_members.group_id
)
WHERE group_id IN (SELECT id FROM product_groups);
