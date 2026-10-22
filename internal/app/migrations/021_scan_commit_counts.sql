-- A batch retry of a committed stock-out reports the counts from the first
-- commit. Those figures are not recomputed from the shelf.
--
-- requested_count is the card's current unit_count, and removed_count is the
-- latest stock-out move's current quantity. A history correction rewrites
-- stock_moves.quantity in place, and a later edit of the card can change
-- unit_count. Neither table keeps the numbers from the moment of commit, so
-- the current figures are the only ones left to store.
ALTER TABLE scan_entries ADD COLUMN removed_count INTEGER;
ALTER TABLE scan_entries ADD COLUMN requested_count INTEGER;

UPDATE scan_entries
SET removed_count = (
        SELECT sm.quantity
        FROM stock_moves sm
        WHERE sm.scan_entry_id = scan_entries.id AND sm.direction = 'out'
        ORDER BY sm.at DESC, sm.id DESC
        LIMIT 1
    ),
    requested_count = unit_count
WHERE status = 'committed'
  AND direction = 'stock_out'
  AND EXISTS (
        SELECT 1
        FROM stock_moves sm
        WHERE sm.scan_entry_id = scan_entries.id AND sm.direction = 'out'
    );
