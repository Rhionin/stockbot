-- household_id on every household-owned table. The default is household 1,
-- so a writer that does not name the column yet still lands on that household.
-- Tables that already store household_id are rewritten in 022d.

ALTER TABLE barcodes ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE barcodes ADD COLUMN actor_user_id TEXT;

ALTER TABLE brand_preferences ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE brand_preferences ADD COLUMN actor_user_id TEXT;

ALTER TABLE group_suggestion_dismissals ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE group_suggestion_dismissals ADD COLUMN actor_user_id TEXT;

ALTER TABLE group_suggestions ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE group_suggestions ADD COLUMN actor_user_id TEXT;

ALTER TABLE item_deals ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE item_deals ADD COLUMN actor_user_id TEXT;

ALTER TABLE items ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE items ADD COLUMN actor_user_id TEXT;
ALTER TABLE items ADD COLUMN device_id TEXT;
ALTER TABLE items ADD COLUMN client_scan_id TEXT;

ALTER TABLE product_groups ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE product_groups ADD COLUMN actor_user_id TEXT;

ALTER TABLE provider_app_credentials ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE provider_app_credentials ADD COLUMN actor_user_id TEXT;

ALTER TABLE provider_connections ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE provider_connections ADD COLUMN actor_user_id TEXT;

ALTER TABLE scan_entries ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE scan_entries ADD COLUMN actor_user_id TEXT;
ALTER TABLE scan_entries ADD COLUMN device_id TEXT;
ALTER TABLE scan_entries ADD COLUMN client_scan_id TEXT;

ALTER TABLE shopping_list_items ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE shopping_list_items ADD COLUMN actor_user_id TEXT;

ALTER TABLE staged_cart_skips ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE staged_cart_skips ADD COLUMN actor_user_id TEXT;

ALTER TABLE consumption_events ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE fulfillment_ledger ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE group_suggestion_members ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE history_repair ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE item_instances ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE product_contributions ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE product_group_members ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE shopping_list_entry_adjustments ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE stock_in_events ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE stock_moves ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE supply_overrides ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE push_subscriptions ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';
ALTER TABLE scan_alert_schedule ADD COLUMN household_id TEXT NOT NULL DEFAULT '{{household}}';

CREATE UNIQUE INDEX items_household_client_scan ON items (household_id, device_id, client_scan_id);
CREATE UNIQUE INDEX scan_entries_household_client_scan ON scan_entries (household_id, device_id, client_scan_id);
CREATE UNIQUE INDEX product_groups_household_name ON product_groups (household_id, name_key);
