package auth

import (
	"context"
	"database/sql"
)

func (s *Service) claimDeviceScanReceipt(ctx context.Context, tx *sql.Tx, p Principal, item preparedScan) (bool, error) {
	var dir any
	if item.item.Direction == "" {
		dir = nil
	} else {
		dir = item.item.Direction
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO device_scan_receipts (
			household_id, device_id, client_scan_id, scan_entry_id, barcode, direction, unit_count, scanned_at
		) VALUES (?, ?, ?, '', ?, ?, ?, ?)
		ON CONFLICT(household_id, device_id, client_scan_id) DO NOTHING`,
		p.HouseholdID, p.DeviceID, item.item.ID, item.barcode, dir, item.unit, stamp(item.at),
	)
	if err != nil {
		return false, AsError(err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Service) recordDeviceScanReceipt(ctx context.Context, q queryer, p Principal, item preparedScan, scanEntryID string, lcd deviceLCD) error {
	var productName sql.NullString
	if lcd.ProductName != nil && *lcd.ProductName != "" {
		productName = sql.NullString{String: *lcd.ProductName, Valid: true}
	}
	var dir any
	if item.item.Direction == "" {
		dir = nil
	} else {
		dir = item.item.Direction
	}
	res, err := q.ExecContext(ctx, `
		UPDATE device_scan_receipts SET
			scan_entry_id = ?, device_result_status = ?, device_on_hand = ?, device_queued_count = ?,
			device_delta = ?, device_backfilled = ?, product_name = ?
		WHERE household_id = ? AND device_id = ? AND client_scan_id = ?`,
		scanEntryID, lcd.ScanStatus, lcd.OnHand, lcd.QueuedCount, lcd.Delta, boolToInt(lcd.Backfilled), productName,
		p.HouseholdID, p.DeviceID, item.item.ID,
	)
	if err != nil {
		return AsError(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		_, err = q.ExecContext(ctx, `
			INSERT INTO device_scan_receipts (
				household_id, device_id, client_scan_id, scan_entry_id, barcode, direction, unit_count,
				scanned_at, device_result_status, device_on_hand, device_queued_count, device_delta,
				device_backfilled, product_name
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.HouseholdID, p.DeviceID, item.item.ID, scanEntryID, item.barcode, dir, item.unit,
			stamp(item.at), lcd.ScanStatus, lcd.OnHand, lcd.QueuedCount, lcd.Delta, boolToInt(lcd.Backfilled), productName,
		)
		if err != nil {
			return AsError(err)
		}
	}
	_, err = q.ExecContext(ctx, `
		UPDATE scan_entries SET device_id = ?, device_result_status = ?, device_on_hand = ?,
			device_queued_count = ?, device_delta = ?, device_backfilled = ?
		WHERE household_id = ? AND id = ?`,
		p.DeviceID, lcd.ScanStatus, lcd.OnHand, lcd.QueuedCount, lcd.Delta, boolToInt(lcd.Backfilled),
		p.HouseholdID, scanEntryID,
	)
	if err != nil {
		return AsError(err)
	}
	if item.approx {
		_, err = q.ExecContext(ctx, `UPDATE scan_entries SET time_approx = 1 WHERE household_id = ? AND id = ?`, p.HouseholdID, scanEntryID)
	}
	return AsError(err)
}
