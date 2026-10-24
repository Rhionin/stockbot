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
