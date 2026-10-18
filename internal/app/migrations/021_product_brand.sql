-- A leading word is a brand only when one was recorded. Open Food Facts
-- publishes that on the product, and a household can record the same field.
ALTER TABLE products ADD COLUMN brand TEXT;
