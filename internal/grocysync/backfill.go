package grocysync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/syncsnap"
	"modernc.org/sqlite"
)

// SQLite primary result codes. Extended codes keep the same low byte.
const (
	sqlitePrimaryBusy   = 5
	sqlitePrimaryLocked = 6
)

// ErrNotConfirmed is returned when backfill is invoked without --confirm.
var ErrNotConfirmed = fmt.Errorf("pass --confirm to write past uses into Grocy. Nothing was changed")

// HistoryResult is what one backfill run did.
type HistoryResult struct {
	BackupPath string
	Inserted   int
	Updated    int
	Undone     int
	Unchanged  int
}

// HistoryOptions is the one-time stock_log write.
// Products maps a Pantry product id to the Grocy product id already created by Reconcile.
// The stock table is not modified, so current on-hand counts stay with the API sync.
type HistoryOptions struct {
	DBPath     string
	LocationID int
	Products   map[string]int
	Snap       syncsnap.Snapshot
	Now        time.Time
	Loc        *time.Location
	Confirm    bool
}

// ApplyHistory backs up grocy.db, then upserts one stock_log row per Pantry stock move.
// Rows are keyed by note pantry-history:<move id>, so running it again does not duplicate them.
func ApplyHistory(ctx context.Context, opts HistoryOptions) (HistoryResult, error) {
	if !opts.Confirm {
		return HistoryResult{}, ErrNotConfirmed
	}
	if strings.TrimSpace(opts.DBPath) == "" {
		return HistoryResult{}, fmt.Errorf("pass --db with the path to grocy.db")
	}
	if strings.Contains(opts.DBPath, "'") {
		return HistoryResult{}, fmt.Errorf("the database path cannot contain a quote")
	}
	if err := journalInUse(opts.DBPath); err != nil {
		return HistoryResult{}, err
	}
	if err := grocyDBWritable(opts.DBPath); err != nil {
		return HistoryResult{}, err
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if opts.Loc == nil {
		opts.Loc = time.UTC
	}
	db, err := app.OpenSQLite(opts.DBPath)
	if err != nil {
		return HistoryResult{}, fmt.Errorf("could not open grocy.db: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout = 200`); err != nil {
		return HistoryResult{}, fmt.Errorf("could not open grocy.db: %w", err)
	}
	if err := lockAvailable(ctx, db); err != nil {
		return HistoryResult{}, err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		return HistoryResult{}, fmt.Errorf("could not open grocy.db: %w", err)
	}
	cols, err := tableColumns(ctx, db, "stock_log")
	if err != nil {
		return HistoryResult{}, err
	}
	for _, need := range []string{"product_id", "amount", "stock_id", "transaction_id", "transaction_type", "undone", "note", "row_created_timestamp"} {
		if !cols[need] {
			return HistoryResult{}, fmt.Errorf("this Grocy database is missing stock_log.%s", need)
		}
	}
	planned, err := planHistory(opts)
	if err != nil {
		return HistoryResult{}, err
	}
	existing, err := loadHistory(ctx, db, cols)
	if err != nil {
		return HistoryResult{}, err
	}
	var result HistoryResult
	type change struct {
		kind string
		row  historyRow
		id   int
	}
	var changes []change
	want := map[string]bool{}
	for _, row := range planned {
		want[row.Note] = true
		ex, ok := existing[row.Note]
		if !ok {
			changes = append(changes, change{kind: "insert", row: row})
			continue
		}
		if !cols["best_before_date"] {
			ex.Best = row.BestBefore
		}
		if !cols["purchased_date"] {
			ex.Purchased = row.Purchased
		}
		if !cols["spoiled"] {
			ex.Spoiled = row.Spoiled
		}
		if historySame(ex, row) {
			result.Unchanged++
			continue
		}
		changes = append(changes, change{kind: "update", row: row, id: ex.ID})
	}
	for note, ex := range existing {
		if want[note] || ex.Undone == 1 {
			continue
		}
		changes = append(changes, change{kind: "undo", id: ex.ID, row: historyRow{Note: note}})
	}
	if len(changes) == 0 {
		return result, nil
	}
	backup, err := nextBackupPath(opts.DBPath, opts.Now)
	if err != nil {
		return HistoryResult{}, err
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO '`+backup+`'`); err != nil {
		return HistoryResult{}, fmt.Errorf("could not back up grocy.db (%w). Stop Grocy and try again. Nothing was changed", err)
	}
	result.BackupPath = backup
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("could not write stock history. Restore from %s. %s: %w", backup, RestoreSteps(backup, opts.DBPath), err)
	}
	defer tx.Rollback()
	for _, change := range changes {
		switch change.kind {
		case "insert":
			if err := insertHistory(ctx, tx, cols, change.row); err != nil {
				return result, fmt.Errorf("could not write stock history. Restore from %s. %s: %w", backup, RestoreSteps(backup, opts.DBPath), err)
			}
			result.Inserted++
		case "update":
			if err := updateHistory(ctx, tx, cols, change.id, change.row); err != nil {
				return result, fmt.Errorf("could not write stock history. Restore from %s. %s: %w", backup, RestoreSteps(backup, opts.DBPath), err)
			}
			result.Updated++
		case "undo":
			if err := undoHistory(ctx, tx, cols, change.id, opts.Now); err != nil {
				return result, fmt.Errorf("could not write stock history. Restore from %s. %s: %w", backup, RestoreSteps(backup, opts.DBPath), err)
			}
			result.Undone++
		}
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("could not write stock history. Restore from %s. %s: %w", backup, RestoreSteps(backup, opts.DBPath), err)
	}
	return result, nil
}

func grocyDBWritable(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err == nil {
		f.Close()
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if os.IsPermission(err) {
		return fmt.Errorf("grocy.db is not writable by this process. Backfill must run as Grocy's data owner (uid 1001). The grocy-sync image runs as uid 65532. Nothing was changed: %w", err)
	}
	return fmt.Errorf("could not open grocy.db: %w", err)
}

func journalInUse(path string) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		_, err := os.Stat(path + suffix)
		if err == nil {
			return fmt.Errorf("grocy.db looks in use (grocy.db%s is present). Stop Grocy with docker stop grocy, then run backfill again. Nothing was changed", suffix)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("could not check whether grocy.db is in use. Nothing was changed: %w", err)
		}
	}
	return nil
}

func lockAvailable(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("could not open grocy.db: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		if sqliteBusy(err) {
			return fmt.Errorf("grocy.db looks in use. Stop Grocy with docker stop grocy, then run backfill again. Nothing was changed: %w", err)
		}
		return fmt.Errorf("could not open grocy.db: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `ROLLBACK`); err != nil {
		return fmt.Errorf("could not open grocy.db: %w", err)
	}
	return nil
}

func sqliteBusy(err error) bool {
	if err == nil {
		return false
	}
	var driverErr *sqlite.Error
	if errors.As(err, &driverErr) {
		// A driver error is classified by its code. The text is only a
		// fallback for an error that did not come from the driver.
		switch driverErr.Code() & 0xff {
		case sqlitePrimaryBusy, sqlitePrimaryLocked:
			return true
		default:
			return false
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "database is locked")
}

// nextBackupPath picks a free grocy.db.bak-* name. A second run in the same
// instant must not reuse the previous backup file.
func nextBackupPath(dbPath string, now time.Time) (string, error) {
	base := dbPath + ".bak-" + now.UTC().Format("20060102T150405.000000000Z")
	candidate := base
	for n := 2; n < 1000; n++ {
		if strings.Contains(candidate, "'") {
			return "", fmt.Errorf("the backup path cannot contain a quote")
		}
		_, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("could not back up grocy.db (%w). Stop Grocy and try again. Nothing was changed", err)
		}
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
	return "", fmt.Errorf("could not back up grocy.db. Stop Grocy and try again. Nothing was changed")
}

// RestoreSteps is how to put grocy.db back after a failed or unwanted backfill.
func RestoreSteps(backup, live string) string {
	return fmt.Sprintf("Stop Grocy (docker stop grocy). Move %s aside, copy %s to %s, delete %s-wal and %s-shm if they exist, then start Grocy.", live, backup, live, live, live)
}

type historyRow struct {
	Note         string
	GrocyProduct int
	Amount       float64
	Type         string
	Stamp        string
	Purchased    string
	BestBefore   string
	Used         string
	Spoiled      int
	LocationID   int
	StockID      string
	TxID         string
}

type existingHistory struct {
	ID        int
	Amount    float64
	Stamp     string
	Spoiled   int
	Undone    int
	Type      string
	Best      string
	Purchased string
}

func planHistory(opts HistoryOptions) ([]historyRow, error) {
	var rows []historyRow
	for _, move := range opts.Snap.Moves {
		grocyID := opts.Products[move.ProductID]
		if grocyID == 0 {
			return nil, fmt.Errorf("sync once before backfilling so Grocy has every product")
		}
		if opts.LocationID == 0 {
			return nil, fmt.Errorf("sync once before backfilling so Grocy has the Pantry location")
		}
		row := historyRow{
			Note:         historyPrefix + move.ID,
			GrocyProduct: grocyID,
			Stamp:        stamp(move.At, opts.Loc),
			LocationID:   opts.LocationID,
			StockID:      "pantry-stock-" + move.ID,
			TxID:         "pantry-history-" + move.ID,
			BestBefore:   neverExpires,
		}
		switch move.Direction {
		case "in":
			row.Type = "purchase"
			row.Amount = float64(move.Quantity)
			row.Purchased = day(move.At, opts.Loc)
			row.BestBefore = expiryForMove(move.ID, opts.Snap, opts.Loc)
		case "out":
			row.Type = "consume"
			row.Amount = -float64(move.Quantity)
			row.Spoiled = spoiledMove(move.ID, opts.Snap.Removals)
			row.Used = day(move.At, opts.Loc)
			row.Purchased, row.BestBefore = consumeDates(move.ID, opts.Snap, opts.Loc)
		default:
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func expiryForMove(moveID string, snap syncsnap.Snapshot, loc *time.Location) string {
	var earliest *time.Time
	consider := func(t *time.Time) {
		if t == nil || t.IsZero() {
			return
		}
		if earliest == nil || t.Before(*earliest) {
			c := t.UTC()
			earliest = &c
		}
	}
	for _, inst := range snap.Instances {
		if inst.StockMoveID == moveID {
			consider(inst.ExpiresAt)
		}
	}
	for _, rm := range snap.Removals {
		if rm.StockMoveID == moveID {
			consider(rm.ExpiresAt)
		}
	}
	return bestBefore(earliest, loc)
}

func consumeDates(moveID string, snap syncsnap.Snapshot, loc *time.Location) (purchased, best string) {
	best = neverExpires
	for _, rm := range snap.Removals {
		if rm.RemovedByMoveID != moveID {
			continue
		}
		if purchased == "" && !rm.StockInAt.IsZero() {
			purchased = day(rm.StockInAt, loc)
		}
		if rm.ExpiresAt != nil {
			candidate := day(*rm.ExpiresAt, loc)
			if best == neverExpires || candidate < best {
				best = candidate
			}
		}
	}
	return purchased, best
}

func spoiledMove(moveID string, removals []syncsnap.Removal) int {
	n := 0
	for _, rm := range removals {
		if rm.RemovedByMoveID != moveID {
			continue
		}
		n++
		if rm.Reason != "expired" {
			return 0
		}
	}
	if n == 0 {
		return 0
	}
	return 1
}

func historySame(ex existingHistory, row historyRow) bool {
	return ex.Undone == 0 &&
		ex.Type == row.Type &&
		ex.Amount == row.Amount &&
		canonicalStamp(ex.Stamp) == canonicalStamp(row.Stamp) &&
		ex.Spoiled == row.Spoiled &&
		sameDay(ex.Best, row.BestBefore) &&
		sameDay(ex.Purchased, row.Purchased)
}

// canonicalStamp compares journal times when the sqlite driver rewrites a
// datetime column as RFC3339. The clock time is the value Grocy stored.
func canonicalStamp(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "T", " ")
	s = strings.TrimSuffix(s, "Z")
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		s = s[:dot]
	}
	if len(s) >= 19 {
		return s[:19]
	}
	return s
}

func loadHistory(ctx context.Context, db *sql.DB, cols map[string]bool) (map[string]existingHistory, error) {
	selects := []string{"id", "note", "amount", "row_created_timestamp", "transaction_type", "undone"}
	optional := []string{"spoiled", "best_before_date", "purchased_date"}
	for _, col := range optional {
		if cols[col] {
			selects = append(selects, col)
		}
	}
	q := "SELECT " + strings.Join(selects, ", ") + " FROM stock_log WHERE note LIKE ?"
	rows, err := db.QueryContext(ctx, q, historyPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("could not read Grocy's stock journal: %w", err)
	}
	defer rows.Close()
	out := map[string]existingHistory{}
	for rows.Next() {
		var ex existingHistory
		var note string
		dest := []any{&ex.ID, &note, &ex.Amount, &ex.Stamp, &ex.Type, &ex.Undone}
		var spoiled sql.NullInt64
		var best, purchased sql.NullString
		if cols["spoiled"] {
			dest = append(dest, &spoiled)
		}
		if cols["best_before_date"] {
			dest = append(dest, &best)
		}
		if cols["purchased_date"] {
			dest = append(dest, &purchased)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("could not read Grocy's stock journal: %w", err)
		}
		if spoiled.Valid && spoiled.Int64 != 0 {
			ex.Spoiled = 1
		}
		if best.Valid {
			ex.Best = best.String
		}
		if purchased.Valid {
			ex.Purchased = purchased.String
		}
		out[note] = ex
	}
	return out, rows.Err()
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	if table != "stock_log" {
		return nil, fmt.Errorf("unexpected table")
	}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(stock_log)`)
	if err != nil {
		return nil, fmt.Errorf("could not read Grocy's stock journal: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("could not read Grocy's stock journal: %w", err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("grocy.db has no stock_log table. This command is for Grocy's own database.")
	}
	return out, nil
}

func insertHistory(ctx context.Context, tx *sql.Tx, cols map[string]bool, row historyRow) error {
	fields, args := historyAssignments(cols, row, true)
	q := "INSERT INTO stock_log (" + strings.Join(fields, ", ") + ") VALUES (" + placeholders(len(fields)) + ")"
	_, err := tx.ExecContext(ctx, q, args...)
	return err
}

func updateHistory(ctx context.Context, tx *sql.Tx, cols map[string]bool, id int, row historyRow) error {
	fields, args := historyAssignments(cols, row, false)
	sets := make([]string, len(fields))
	for i, field := range fields {
		sets[i] = field + " = ?"
	}
	if cols["undone"] {
		sets = append(sets, "undone = ?")
		args = append(args, 0)
	}
	args = append(args, id)
	_, err := tx.ExecContext(ctx, "UPDATE stock_log SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	return err
}

func historyAssignments(cols map[string]bool, row historyRow, insert bool) ([]string, []any) {
	var fields []string
	var args []any
	add := func(col string, val any) {
		if cols[col] {
			fields = append(fields, col)
			args = append(args, val)
		}
	}
	if insert {
		add("product_id", row.GrocyProduct)
		add("stock_id", row.StockID)
		add("transaction_id", row.TxID)
		add("note", row.Note)
		add("user_id", 1)
		add("correlation_id", "pantry-history")
		add("undone", 0)
	}
	add("amount", row.Amount)
	add("transaction_type", row.Type)
	add("row_created_timestamp", row.Stamp)
	add("best_before_date", row.BestBefore)
	add("purchased_date", emptyNull(row.Purchased))
	add("used_date", emptyNull(row.Used))
	add("spoiled", row.Spoiled)
	add("location_id", row.LocationID)
	return fields, args
}

func emptyNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func undoHistory(ctx context.Context, tx *sql.Tx, cols map[string]bool, id int, now time.Time) error {
	if cols["undone_timestamp"] {
		_, err := tx.ExecContext(ctx, `UPDATE stock_log SET undone = 1, undone_timestamp = ? WHERE id = ?`, stamp(now, time.UTC), id)
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE stock_log SET undone = 1 WHERE id = ?`, id)
	return err
}

func placeholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ", ")
}
