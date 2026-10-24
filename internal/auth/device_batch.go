package auth

import (
	"context"
	"time"
)

const (
	scanPastLimit = 30 * 24 * time.Hour
	scanFuture    = 5 * time.Minute
	scanGroupGap  = 5 * time.Minute
)

type preparedScan struct {
	item    ScanInput
	reject  ScanResult
	at      time.Time
	approx  bool
	unit    int
	barcode string
	ok      bool
}

// IngestScans writes one batch. A conflict or a rejected item does not roll
// back the other items. Nothing is written when the device is over 600 scans
// in the last minute, or when the batch is empty or larger than 50.
func (s *Service) IngestScans(ctx context.Context, p Principal, items []ScanInput, dropped int) ([]ScanResult, error) {
	if len(items) == 0 || len(items) > 50 {
		return nil, validation()
	}
	if retry, blocked := s.limits.Blocked(s.clock(), "scan:"+p.DeviceID, scanLimit, scanWindow); blocked {
		return nil, rateLimited(retry)
	}
	if _, blocked := s.limits.Hit(s.clock(), "scan:"+p.DeviceID, scanLimit, scanWindow); blocked {
		return nil, rateLimited(time.Second)
	}
	for i := 1; i < len(items); i++ {
		if _, blocked := s.limits.Hit(s.clock(), "scan:"+p.DeviceID, scanLimit, scanWindow); blocked {
			return nil, rateLimited(time.Second)
		}
	}
	now := s.clock()
	prepared := make([]preparedScan, len(items))
	for i, item := range items {
		prepared[i] = prepareScan(item, now)
	}
	order := make([]int, 0, len(items))
	for i, item := range prepared {
		if item.ok {
			order = append(order, i)
		}
	}
	sortScanOrder(order, prepared)
	groups := map[int]int{}
	group := 1
	var prev time.Time
	for n, idx := range order {
		if n > 0 && prepared[idx].at.Sub(prev) > scanGroupGap {
			group++
		}
		groups[idx] = group
		prev = prepared[idx].at
	}

	touch := s.presence.note(p.DeviceID, p.HouseholdID, now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, AsError(err)
	}
	defer tx.Rollback()
	if touch.write {
		if _, err := tx.ExecContext(ctx, `
			UPDATE devices SET last_seen_at = ? WHERE id = ? AND household_id = ?`,
			stamp(now), p.DeviceID, p.HouseholdID); err != nil {
			return nil, AsError(err)
		}
	}
	if dropped > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE devices SET dropped_total = dropped_total + ?
			WHERE id = ? AND household_id = ?`,
			dropped, p.DeviceID, p.HouseholdID); err != nil {
			return nil, AsError(err)
		}
	}
	out := make([]ScanResult, len(items))
	var created []createdScan
	for _, idx := range order {
		result, row, err := s.insertScan(ctx, tx, p, prepared[idx], groups[idx])
		if err != nil {
			return nil, err
		}
		out[idx] = result
		if row.ScanID != "" {
			created = append(created, row)
		}
	}
	for i, item := range prepared {
		if !item.ok {
			out[i] = item.reject
		}
	}
	freshDrop := false
	if dropped > 0 {
		freshDrop, err = s.insertAlert(ctx, tx, p.HouseholdID, alertScansDropped, true)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, AsError(err)
	}
	if touch.cameOnline && s.hints != nil {
		s.hints.PublishHint(p.HouseholdID, "devices")
	}
	if len(created) > 0 && s.hints != nil {
		s.hints.PublishHint(p.HouseholdID, "scans")
	}
	if s.barcodeLookup != nil {
		for _, row := range created {
			if productDigits(row.Barcode) {
				s.barcodeLookup(ctx, p.HouseholdID, row.ScanID, row.Barcode)
			}
		}
	}
	if freshDrop {
		s.announce(ctx, p.HouseholdID, alertScansDropped)
	}
	return out, nil
}

type createdScan struct {
	ScanID  string
	Barcode string
}

func prepareScan(item ScanInput, now time.Time) preparedScan {
	if item.ID == "" {
		return preparedScan{reject: ScanResult{Status: "rejected", Code: CodeValidation}}
	}
	if item.Heartbeat && !item.HasBarcode {
		return preparedScan{reject: ScanResult{ID: item.ID, Status: "accepted"}}
	}
	if item.ScannedAtInvalid {
		return preparedScan{reject: ScanResult{ID: item.ID, Status: "rejected", Code: CodeValidation}}
	}
	if !item.HasBarcode {
		return preparedScan{reject: ScanResult{ID: item.ID, Status: "rejected", Code: CodeValidation}}
	}
	barcode, ok := deviceBarcode(item.Barcode)
	if !ok {
		return preparedScan{reject: ScanResult{ID: item.ID, Status: "rejected", Code: CodeValidation}}
	}
	if item.Direction != "" && item.Direction != "stock_in" && item.Direction != "stock_out" {
		return preparedScan{reject: ScanResult{ID: item.ID, Status: "rejected", Code: CodeValidation}}
	}
	unit := item.UnitCount
	if unit == 0 {
		unit = 1
	}
	if unit < 1 || unit > 999 {
		return preparedScan{reject: ScanResult{ID: item.ID, Status: "rejected", Code: CodeValidation}}
	}
	at, approx, bad := resolveScanTime(now, item)
	if bad {
		return preparedScan{reject: ScanResult{ID: item.ID, Status: "rejected", Code: CodeValidation}}
	}
	item.Barcode = barcode
	return preparedScan{item: item, at: at, approx: approx, unit: unit, barcode: barcode, ok: true}
}

func resolveScanTime(now time.Time, item ScanInput) (time.Time, bool, bool) {
	if item.ScannedAtInvalid {
		return time.Time{}, false, true
	}
	if item.HasScannedAt && withinScanWindow(now, item.ScannedAt) {
		return item.ScannedAt.UTC(), false, false
	}
	if item.AgeMs != nil && *item.AgeMs >= 0 {
		at := now.Add(-time.Duration(*item.AgeMs) * time.Millisecond)
		if withinScanWindow(now, at) {
			return at.UTC(), false, false
		}
	}
	return now.UTC(), true, false
}

func withinScanWindow(now, at time.Time) bool {
	if at.Before(now.Add(-scanPastLimit)) {
		return false
	}
	if at.After(now.Add(scanFuture)) {
		return false
	}
	return true
}

func deviceBarcode(raw string) (string, bool) {
	barcode := trimASCII(raw)
	if barcode == "" || len(barcode) > 64 || !printableASCII(barcode) {
		return "", false
	}
	if _, well, _ := NormalizePairingCode(raw); well {
		return "", false
	}
	return barcode, true
}

func productDigits(raw string) bool {
	value := trimASCII(raw)
	if value == "" || len(value) > 14 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func printableASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7E {
			return false
		}
	}
	return true
}

func sortScanOrder(order []int, prepared []preparedScan) {
	for i := 1; i < len(order); i++ {
		j := i
		for j > 0 {
			a, b := order[j-1], order[j]
			if prepared[a].at.Before(prepared[b].at) || (prepared[a].at.Equal(prepared[b].at) && a < b) {
				break
			}
			order[j-1], order[j] = order[j], order[j-1]
			j--
		}
	}
}

func (s *Service) insertScan(ctx context.Context, q queryer, p Principal, item preparedScan, group int) (ScanResult, createdScan, error) {
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
		id, p.DeviceID, p.HouseholdID, p.DeviceID, item.item.ID, item.barcode, stamp(item.at),
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
