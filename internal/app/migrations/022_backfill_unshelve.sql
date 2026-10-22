-- A backfill that puts a wiped item back on the shelf marks its stock-in.
-- Undoing that pair hides the item again. Other stock-ins leave unshelved at 0.
ALTER TABLE stock_moves ADD COLUMN unshelved INTEGER NOT NULL DEFAULT 0;
