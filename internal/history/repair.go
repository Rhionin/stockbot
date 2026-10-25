package history

import (
	"context"
	"database/sql"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
)

// moveRow is one stored stock move.
type moveRow struct {
	ID          string
	ItemID      string
	ProductID   string
	Direction   string
	Quantity    int
	At          time.Time
	Source      string
	ScanEntryID sql.NullString
	Tracked     bool
	Unshelved   bool
}

// ensure groups older shelf rows that were written before moves existed.
// The history_repair row is updated first so two readers cannot both insert.
// That update takes the write lock. Preview uses repair on a throwaway copy
// and does not call ensure.
func ensure(ctx context.Context, scope tenant.Scope, tx *sql.Tx) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE history_repair SET id = 1 WHERE id = 1 AND household_id = ?`, scope.HouseholdID()); err != nil {
		return err
	}
	return repair(ctx, scope, tx)
}

// repair is the grouping work inside ensure, without the history_repair update.
// On a shelf that is already grouped it only reads. When older rows still need
// a move, it writes those moves. Preview runs it on a copy that is thrown away.
func repair(ctx context.Context, scope tenant.Scope, tx *sql.Tx) error {
	if err := scope.Require(); err != nil {
		return err
	}
	scans, err := loadCommittedScans(ctx, scope, tx)
	if err != nil {
		return err
	}
	if err := ensureStockIns(ctx, scope, tx, scans); err != nil {
		return err
	}
	if err := ensureStockOuts(ctx, scope, tx); err != nil {
		return err
	}
	if err := ensureRemoved(ctx, scope, tx, scans); err != nil {
		return err
	}
	return relabelManualMoves(ctx, scope, tx, scans)
}

type instanceRow struct {
	ID        string
	ItemID    string
	ProductID string
	UserID    string
	At        time.Time
	Removed   sql.NullTime
	Reason    string
}

func ensureStockIns(ctx context.Context, scope tenant.Scope, tx *sql.Tx, scans scanIndex) error {
	if err := scope.Require(); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT ii.id, ii.item_id, i.product_id, i.user_id, ii.stock_in_at
		FROM item_instances ii
		JOIN items i ON i.id = ii.item_id AND i.household_id = ii.household_id
		WHERE ii.household_id = ? AND ii.stock_move_id IS NULL
		ORDER BY ii.item_id, ii.stock_in_at, ii.id`, scope.HouseholdID())
	if err != nil {
		return err
	}
	var loaded []instanceRow
	for rows.Next() {
		var row instanceRow
		if err := rows.Scan(&row.ID, &row.ItemID, &row.ProductID, &row.UserID, &row.At); err != nil {
			rows.Close()
			return err
		}
		loaded = append(loaded, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	var current []instanceRow
	flush := func() error {
		if len(current) == 0 {
			return nil
		}
		head := current[0]
		ids := make([]string, len(current))
		for i, row := range current {
			ids[i] = row.ID
		}
		scanID, source := scans.first(head.UserID, head.ProductID, "stock_in", head.At)
		tracked, err := stockInTracked(ctx, scope, tx, head.ProductID, head.At)
		if err != nil {
			return err
		}
		current = nil
		_, err = AttachStockIn(ctx, scope, tx, InNote{
			ItemID: head.ItemID, ProductID: head.ProductID, At: head.At,
			Source: source, ScanEntryID: scanID, Tracked: tracked, InstanceIDs: ids,
		})
		return err
	}
	for _, row := range loaded {
		if len(current) > 0 {
			prev := current[0]
			if prev.ItemID != row.ItemID || !prev.At.Equal(row.At) {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		current = append(current, row)
	}
	return flush()
}

func ensureStockOuts(ctx context.Context, scope tenant.Scope, tx *sql.Tx) error {
	if err := scope.Require(); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT c.id, c.item_id, i.product_id, i.user_id, c.consumed_at, c.scan_entry_id
		FROM consumption_events c
		JOIN items i ON i.id = c.item_id AND i.household_id = c.household_id
		WHERE c.household_id = ? AND c.stock_move_id IS NULL
		ORDER BY c.item_id, c.consumed_at, c.scan_entry_id, c.id`, scope.HouseholdID())
	if err != nil {
		return err
	}
	type useRow struct {
		ID      string
		ItemID  string
		Product string
		UserID  string
		At      time.Time
		ScanID  sql.NullString
	}
	var loaded []useRow
	for rows.Next() {
		var row useRow
		if err := rows.Scan(&row.ID, &row.ItemID, &row.Product, &row.UserID, &row.At, &row.ScanID); err != nil {
			rows.Close()
			return err
		}
		loaded = append(loaded, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	var current []useRow
	flush := func() error {
		if len(current) == 0 {
			return nil
		}
		head := current[0]
		id := uuid.NewString()
		source := "manual"
		var scan any
		if head.ScanID.Valid && head.ScanID.String != "" {
			source = "scan"
			scan = head.ScanID.String
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, scan_entry_id, tracked, household_id)
			VALUES (?, ?, ?, 'out', ?, ?, ?, ?, 1, ?)`,
			id, head.ItemID, head.Product, len(current), head.At, source, scan, scope.HouseholdID(),
		); err != nil {
			return err
		}
		for _, row := range current {
			if _, err := tx.ExecContext(ctx, `
				UPDATE consumption_events SET stock_move_id = ? WHERE id = ? AND household_id = ?`, id, row.ID, scope.HouseholdID()); err != nil {
				return err
			}
		}
		if err := linkRemoved(ctx, scope, tx, head.ItemID, id, head.At, len(current)); err != nil {
			return err
		}
		current = nil
		return nil
	}
	for _, row := range loaded {
		if len(current) > 0 {
			prev := current[0]
			sameScan := prev.ScanID.String == row.ScanID.String && prev.ScanID.Valid == row.ScanID.Valid
			if prev.ItemID != row.ItemID || !prev.At.Equal(row.At) || !sameScan {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		current = append(current, row)
	}
	return flush()
}

func linkRemoved(ctx context.Context, scope tenant.Scope, tx *sql.Tx, itemID, moveID string, at time.Time, quantity int) error {
	if err := scope.Require(); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, removed_at FROM item_instances
		WHERE household_id = ? AND item_id = ? AND removed_at IS NOT NULL AND removed_by_move_id IS NULL
			AND (removal_reason IS NULL OR removal_reason = 'consumed')`,
		scope.HouseholdID(), itemID)
	if err != nil {
		return err
	}
	type candidate struct {
		id string
		at time.Time
	}
	var candidates []candidate
	for rows.Next() {
		var id string
		var removed time.Time
		if err := rows.Scan(&id, &removed); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, candidate{id: id, at: removed})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i].at.Sub(at)
		if left < 0 {
			left = -left
		}
		right := candidates[j].at.Sub(at)
		if right < 0 {
			right = -right
		}
		if left == right {
			return candidates[i].id < candidates[j].id
		}
		return left < right
	})
	n := quantity
	if n > len(candidates) {
		n = len(candidates)
	}
	for i := 0; i < n; i++ {
		if _, err := tx.ExecContext(ctx, `
			UPDATE item_instances SET removed_by_move_id = ? WHERE id = ? AND household_id = ?`,
			moveID, candidates[i].id, scope.HouseholdID(),
		); err != nil {
			return err
		}
	}
	return nil
}

func ensureRemoved(ctx context.Context, scope tenant.Scope, tx *sql.Tx, scans scanIndex) error {
	if err := scope.Require(); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT ii.id, ii.item_id, i.product_id, i.user_id, ii.removed_at
		FROM item_instances ii
		JOIN items i ON i.id = ii.item_id AND i.household_id = ii.household_id
		WHERE ii.household_id = ? AND ii.removed_at IS NOT NULL AND ii.removed_by_move_id IS NULL
		ORDER BY ii.removed_at, ii.id`, scope.HouseholdID())
	if err != nil {
		return err
	}
	type removedRow struct {
		id, itemID, productID, userID string
		at                            time.Time
	}
	var loaded []removedRow
	for rows.Next() {
		var row removedRow
		if err := rows.Scan(&row.id, &row.itemID, &row.productID, &row.userID, &row.at); err != nil {
			rows.Close()
			return err
		}
		loaded = append(loaded, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, row := range loaded {
		scanID, source := scans.first(row.userID, row.productID, "stock_out", row.at)
		if _, err := AttachStockOut(ctx, scope, tx, OutNote{
			ItemID: row.itemID, ProductID: row.productID, At: row.at,
			Source: source, ScanEntryID: scanID, Tracked: false, InstanceID: row.id,
		}); err != nil {
			return err
		}
	}
	return nil
}

// scanKey groups one user's committed scans so a repair matches instants in memory.
// "Exactly one" is one scan for that user, product, and direction.
// Move rows say "in" and "out"; scan rows say "stock_in" and "stock_out".
type scanKey struct {
	userID    string
	productID string
	direction string
}

type storedScan struct {
	id string
	at time.Time
}

type scanIndex struct {
	byKey map[scanKey][]storedScan
}

func loadCommittedScans(ctx context.Context, scope tenant.Scope, tx *sql.Tx) (scanIndex, error) {
	if err := scope.Require(); err != nil {
		return scanIndex{}, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, user_id, product_id, direction, CAST(scanned_at AS TEXT)
		FROM scan_entries
		WHERE household_id = ? AND status = 'committed' AND product_id IS NOT NULL AND direction IS NOT NULL
		ORDER BY id`, scope.HouseholdID())
	if err != nil {
		return scanIndex{}, err
	}
	defer rows.Close()
	idx := scanIndex{byKey: map[scanKey][]storedScan{}}
	for rows.Next() {
		var id, userID, productID, direction, scannedAt string
		if err := rows.Scan(&id, &userID, &productID, &direction, &scannedAt); err != nil {
			return scanIndex{}, err
		}
		at, err := parseStoredTime(scannedAt)
		if err != nil {
			log.Printf("history repair: skipped a committed scan whose scanned_at could not be parsed")
			continue
		}
		if id == "" {
			continue
		}
		key := scanKey{userID: userID, productID: productID, direction: direction}
		idx.byKey[key] = append(idx.byKey[key], storedScan{id: id, at: at})
	}
	if err := rows.Err(); err != nil {
		return scanIndex{}, err
	}
	return idx, nil
}

// parseStoredTime reads a scanned_at value stored as text, including a legacy
// monotonic " m=" suffix. Unreadable text is not a match.
func parseStoredTime(raw string) (time.Time, error) {
	raw = stripMonotonic(raw)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
	}
	var last error
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed.UTC(), nil
		}
		last = err
	}
	return time.Time{}, last
}

func stripMonotonic(raw string) string {
	if i := strings.Index(raw, " m="); i >= 0 {
		return raw[:i]
	}
	return raw
}

// first is the earliest committed scan this user recorded for the product,
// direction, and instant. Another user's scan at the same instant is not a match.
func (idx scanIndex) first(userID, productID, direction string, at time.Time) (*string, string) {
	for _, scan := range idx.byKey[scanKey{userID: userID, productID: productID, direction: direction}] {
		if scan.at.Equal(at) {
			id := scan.id
			return &id, "scan"
		}
	}
	return nil, "manual"
}

func moveScanDirection(direction string) (string, bool) {
	switch direction {
	case "in":
		return "stock_in", true
	case "out":
		return "stock_out", true
	default:
		return "", false
	}
}

// relabelManualMoves points an existing manual move at a committed scan when
// that user has exactly one match for the product, direction, and instant,
// and no other move already uses it. Two moves that share one scan are
// taken in id order: the first is updated, then the scan is marked linked,
// so the rest of the pass leaves it alone. Only source and scan_entry_id
// change. A later pass finds nothing to update.
func relabelManualMoves(ctx context.Context, scope tenant.Scope, tx *sql.Tx, scans scanIndex) error {
	if err := scope.Require(); err != nil {
		return err
	}
	linked, err := linkedScanIDs(ctx, scope, tx)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT m.id, i.user_id, m.product_id, m.direction, m.at
		FROM stock_moves m
		JOIN items i ON i.id = m.item_id AND i.household_id = m.household_id
		WHERE m.household_id = ? AND m.source = 'manual' AND m.scan_entry_id IS NULL
		ORDER BY m.id`, scope.HouseholdID())
	if err != nil {
		return err
	}
	type manualMove struct {
		id, userID, productID, direction string
		at                               time.Time
	}
	var pending []manualMove
	for rows.Next() {
		var move manualMove
		if err := rows.Scan(&move.id, &move.userID, &move.productID, &move.direction, &move.at); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, move)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, move := range pending {
		direction, ok := moveScanDirection(move.direction)
		if !ok {
			continue
		}
		var matched *storedScan
		ambiguous := false
		key := scanKey{userID: move.userID, productID: move.productID, direction: direction}
		for _, scan := range scans.byKey[key] {
			if !scan.at.Equal(move.at) {
				continue
			}
			if matched != nil {
				ambiguous = true
				break
			}
			copy := scan
			matched = &copy
		}
		if ambiguous || matched == nil {
			continue
		}
		if _, taken := linked[matched.id]; taken {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE stock_moves
			SET source = 'scan', scan_entry_id = ?
			WHERE id = ? AND household_id = ? AND source = 'manual' AND scan_entry_id IS NULL`,
			matched.id, move.id, scope.HouseholdID(),
		); err != nil {
			return err
		}
		linked[matched.id] = struct{}{}
	}
	return nil
}

func linkedScanIDs(ctx context.Context, scope tenant.Scope, tx *sql.Tx) (map[string]struct{}, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT scan_entry_id FROM stock_moves
		WHERE household_id = ? AND scan_entry_id IS NOT NULL`, scope.HouseholdID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	linked := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id != "" {
			linked[id] = struct{}{}
		}
	}
	return linked, rows.Err()
}

func stockInTracked(ctx context.Context, scope tenant.Scope, tx *sql.Tx, productID string, at time.Time) (bool, error) {
	if err := scope.Require(); err != nil {
		return false, err
	}
	var one int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM stock_in_events
		WHERE household_id = ? AND product_id = ? AND at = ? AND stock_move_id IS NULL
		LIMIT 1`, scope.HouseholdID(), productID, at).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
