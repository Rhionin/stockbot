-- Optional brand label for household-typed products (barcodeless items).

ALTER TABLE household_product_overrides ADD COLUMN brand TEXT NOT NULL DEFAULT '';
