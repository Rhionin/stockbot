-- Household-typed product names live beside the shared catalog.
-- A public lookup name stays on products. A name a household types does not.

CREATE TABLE household_product_overrides (
    household_id TEXT NOT NULL,
    product_id   TEXT NOT NULL,
    display_name TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    PRIMARY KEY (household_id, product_id)
);

INSERT INTO household_product_overrides (household_id, product_id, display_name, updated_at)
SELECT h.id, p.id, p.name, p.created_at
FROM products p
JOIN households h ON h.is_primary = 1
WHERE (p.source = 'user' OR COALESCE(p.name_overridden, 0) = 1)
  AND p.name <> '';

-- A product a household typed is not a shared catalog name.
UPDATE products
SET name = ''
WHERE source = 'user';

-- Per-household keys used to live in app_settings. Keep the primary
-- household's values. The stored default-rule key is default_group_rule.
INSERT INTO household_settings (household_id, key, value)
SELECT h.id, s.key, s.value
FROM households h
JOIN app_settings s ON s.key IN (
    'onboarding_started_at',
    'supply_target_kind',
    'supply_target_value',
    'default_group_rule',
    'group_default_rule',
    'group_suggestions_seeded_v1',
    'product_contributions_enabled'
)
WHERE h.is_primary = 1
  AND NOT EXISTS (
    SELECT 1 FROM household_settings hs
    WHERE hs.household_id = h.id AND hs.key = s.key
  );
