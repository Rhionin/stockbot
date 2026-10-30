package auth

import (
	"context"
	"database/sql"
	"log/slog"
)

func (s *Service) insertScan(ctx context.Context, q queryer, p Principal, item preparedScan, group int) (ScanResult, createdScan, error) {
	if s.deviceScanProcessor != nil {
		return s.insertScanWithProcessor(ctx, p, item, group)
	}
	if dup, ok, err := s.loadDeviceScanReplay(ctx, q, p, item); err != nil {
		return ScanResult{}, createdScan{}, err
	} else if ok {
		return dup, createdScan{}, nil
	}
	return s.insertScanLegacy(ctx, q, p, item, group)
}

func (s *Service) insertScanWithProcessor(ctx context.Context, p Principal, item preparedScan, group int) (ScanResult, createdScan, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScanResult{}, createdScan{}, AsError(err)
	}
	defer tx.Rollback()

	if dup, ok, err := s.claimOrReplayDeviceScan(ctx, tx, p, item); err != nil {
		return ScanResult{}, createdScan{}, err
	} else if ok {
		return dup, createdScan{}, nil
	}

	out, created, err := s.deviceScanProcessor(ctx, tx, s, p, preparedItem(item), group)
	if err != nil {
		return ScanResult{}, createdScan{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScanResult{}, createdScan{}, AsError(err)
	}
	if s.deviceIngestEvents != nil && created.ScanID != "" {
		if err := s.deviceIngestEvents.PublishAfterIngest(ctx, p.HouseholdID, created.ScanID, created.Stock); err != nil {
			slog.Error("device ingest live publish failed",
				"err", err,
				"household_id", p.HouseholdID,
				"device_id", p.DeviceID,
				"scan_entry_id", created.ScanID,
			)
		}
	}
	return out, createdScan{ScanID: created.ScanID, Barcode: created.Barcode, stock: created.Stock}, nil
}

func (s *Service) claimOrReplayDeviceScan(ctx context.Context, tx *sql.Tx, p Principal, item preparedScan) (ScanResult, bool, error) {
	claimed, err := s.claimDeviceScanReceipt(ctx, tx, p, item)
	if err != nil {
		return ScanResult{}, false, err
	}
	if claimed {
		return ScanResult{}, false, nil
	}
	return s.loadDeviceScanReplay(ctx, tx, p, item)
}

func (s *Service) loadDeviceScanReplay(ctx context.Context, q queryer, p Principal, item preparedScan) (ScanResult, bool, error) {
	var barcode, direction, resultStatus sql.NullString
	var units int
	var onHand, queued, delta, backfilled sql.NullInt64
	var productName sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT barcode, COALESCE(direction, ''), unit_count,
		       device_result_status, device_on_hand, device_queued_count, device_delta, device_backfilled,
		       product_name
		FROM device_scan_receipts
		WHERE household_id = ? AND device_id = ? AND client_scan_id = ?`,
		p.HouseholdID, p.DeviceID, item.item.ID,
	).Scan(&barcode, &direction, &units, &resultStatus, &onHand, &queued, &delta, &backfilled, &productName)
	if err == sql.ErrNoRows {
		return ScanResult{}, false, nil
	}
	if err != nil {
		return ScanResult{}, false, AsError(err)
	}
	if barcode.String != item.barcode || direction.String != item.item.Direction || units != item.unit {
		return ScanResult{ID: item.item.ID, Status: "conflict"}, true, nil
	}
	if !resultStatus.Valid || resultStatus.String == "" {
		return ScanResult{ID: item.item.ID, Status: "conflict"}, true, nil
	}
	result := ScanResult{ID: item.item.ID, Status: "duplicate", Cursor: item.item.ID}
	known := resultStatus.String != "flagged"
	lcd := deviceLCD{
		ScanStatus:  resultStatus.String,
		Known:       known,
		Barcode:     item.barcode,
		Direction:   item.item.Direction,
		QueuedCount: int(queued.Int64),
		OnHand:      int(onHand.Int64),
		Delta:       int(delta.Int64),
		Backfilled:  backfilled.Int64 != 0,
	}
	if productName.Valid {
		name := productName.String
		lcd.ProductName = &name
	} else if name := displayProductName(item.match); name != nil {
		lcd.ProductName = name
	}
	lcd.apply(&result)
	return result, true, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Service) insertScanLegacy(ctx context.Context, q queryer, p Principal, item preparedScan, group int) (ScanResult, createdScan, error) {
	approx := 0
	if item.approx {
		approx = 1
	}
	var dir any
	if item.item.Direction == "" {
		dir = nil
	} else {
		dir = item.item.Direction
	}
	id := newID()
	_, err := q.ExecContext(ctx, `
		INSERT INTO scan_entries (
			id, user_id, household_id, device_id, client_scan_id, barcode, scanned_at,
			direction, unit_count, status, time_approx, scan_group, boot_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)`,
		id, p.DeviceID, p.HouseholdID, p.DeviceID, item.item.ID, item.barcode, item.at,
		dir, item.unit, approx, group, item.item.BootID)
	if err == nil {
		return ScanResult{ID: item.item.ID, Status: "created", Cursor: id, ScanID: id}, createdScan{ScanID: id, Barcode: item.barcode}, nil
	}
	var barcode, direction string
	var units int
	qerr := q.QueryRowContext(ctx, `
		SELECT barcode, COALESCE(direction, ''), unit_count FROM scan_entries
		WHERE household_id = ? AND device_id = ? AND client_scan_id = ?`,
		p.HouseholdID, p.DeviceID, item.item.ID).Scan(&barcode, &direction, &units)
	if qerr != nil {
		return ScanResult{}, createdScan{}, AsError(err)
	}
	if barcode == item.barcode && direction == item.item.Direction && units == item.unit {
		return ScanResult{ID: item.item.ID, Status: "duplicate", Cursor: item.item.ID}, createdScan{}, nil
	}
	return ScanResult{ID: item.item.ID, Status: "conflict"}, createdScan{}, nil
}

type deviceLCD = DeviceLCD

func (lcd DeviceLCD) apply(result *ScanResult) {
	result.ScanStatus = lcd.ScanStatus
	result.Known = &lcd.Known
	result.ProductName = lcd.ProductName
	result.Barcode = lcd.Barcode
	result.Direction = lcd.Direction
	result.QueuedCount = intPtr(lcd.QueuedCount)
	result.OnHand = intPtr(lcd.OnHand)
	result.Delta = intPtr(lcd.Delta)
	result.Backfilled = &lcd.Backfilled
}
