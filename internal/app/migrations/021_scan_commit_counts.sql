-- A batch retry of a committed stock-out reports the counts from the first
-- commit. Those figures are not the card's live unit_count, which a later
-- edit must not change, and they are not recomputed from the shelf.
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
