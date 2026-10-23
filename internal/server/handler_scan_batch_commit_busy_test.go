package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/Rhionin/stockbot/internal/scan"
	"modernc.org/sqlite"
)

// batchLockWait is the busy_timeout on the file used by the locked-batch
// tests. One item waits about this long. N items must not.
const batchLockWait = 500 * time.Millisecond

func TestBatchCommitLockedDatabaseIsBusyOnce(t *testing.T) {
	var slogBuf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&slogBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	const items = 4
	path := filepath.Join(t.TempDir(), "pantry.db")
	handler, env := setupTestOnDSN(t, path+"?_pragma=busy_timeout(500)", nil)
	mustProduct(env, "prod-lock", "Locked Oats")
	ids := make([]string, items)
	for i := range ids {
		ids[i] = fmt.Sprintf("scan-lock-%d", i+1)
		insertPendingScanUnits(env, ids[i], "prod-lock", scan.StockIn, 1)
	}

	release := holdSQLiteLock(t, path, "BEGIN IMMEDIATE")
	started := time.Now()
	slogBuf.Reset()
	res := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody(ids))
	elapsed := time.Since(started)
	release()

	body := decodeBatchCommit(t, res)
	var problems []string
	if res.code != http.StatusOK {
		problems = append(problems, fmt.Sprintf("status %d", res.code))
	}
	if !sameIDs(body.FailedIDs, ids) || len(body.CommittedIDs) != 0 || strings.Contains(res.body, "committedIds") {
		problems = append(problems, fmt.Sprintf("ids committed=%v failed=%v", body.CommittedIDs, body.FailedIDs))
	}
	if !allEqual(body.FailedCodes, "busy") || !allEqual(body.FailedReasons, clientmsg.Busy) {
		problems = append(problems, fmt.Sprintf("codes=%v reasons=%v", body.FailedCodes, body.FailedReasons))
	}
	if strings.Contains(res.body, clientmsg.Generic) || strings.Contains(res.body, "internal") ||
		strings.Contains(strings.ToLower(res.body), "sqlite") || strings.Contains(res.body, "database is locked") {
		problems = append(problems, "body leaked a generic or driver failure")
	}
	// One busy_timeout for the batch, not one per item.
	if elapsed < batchLockWait/2 || elapsed >= 2*batchLockWait {
		problems = append(problems, fmt.Sprintf("elapsed %s for %d items, timeout %s", elapsed, items, batchLockWait))
	}
	if strings.Count(slogBuf.String(), "msg=db_busy") != 1 {
		problems = append(problems, fmt.Sprintf("db_busy lines = %q", slogBuf.String()))
	} else {
		line := busyLogLine(t, slogBuf.String())
		for _, part := range []string{"level=WARN", "event=db_busy", "method=POST", "sqlite_code=5", "household_id=user-1"} {
			if !strings.Contains(line, part) {
				problems = append(problems, fmt.Sprintf("log line missing %s: %s", part, line))
			}
		}
		if strings.Contains(strings.ToLower(line), "locked oats") || strings.Contains(line, "database is locked") || strings.Contains(strings.ToLower(line), "sqlite_busy") {
			problems = append(problems, fmt.Sprintf("log line leaked: %s", line))
		}
	}
	if len(problems) > 0 {
		t.Fatalf("%s\nelapsed %s\nbody %s", strings.Join(problems, "; "), elapsed, res.body)
	}
	t.Logf("N=%d timeout=%s elapsed=%s", items, batchLockWait, elapsed)
	slogBuf.Reset()

	if pending := scanIDs(t, handler, "pending"); !sameIDs(pending, ids) {
		t.Fatalf("pending after the lock = %v", pending)
	}
	if got := inventoryUnits(t, handler); got != 0 {
		t.Fatalf("inventory units while locked = %d", got)
	}

	again := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody(ids))
	retried := decodeBatchCommit(t, again)
	if again.code != http.StatusOK || !sameIDs(retried.CommittedIDs, ids) || len(retried.FailedIDs) != 0 {
		t.Fatalf("retry = %d %s", again.code, again.body)
	}
	if got := inventoryUnits(t, handler); got != items {
		t.Fatalf("inventory units after retry = %d, want %d", got, items)
	}
	third := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody(ids))
	repeated := decodeBatchCommit(t, third)
	if !sameIDs(repeated.CommittedIDs, ids) || len(repeated.FailedIDs) != 0 {
		t.Fatalf("second retry = %s", third.body)
	}
	if got := inventoryUnits(t, handler); got != items {
		t.Fatalf("inventory units after a second retry = %d, want %d", got, items)
	}
	if strings.Contains(slogBuf.String(), "db_busy") {
		t.Fatalf("retry logged db_busy: %s", slogBuf.String())
	}
}

func TestBatchCommitStopsOnBusyAndLeavesEarlierResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	handler, env := setupTestOnDSN(t, path+"?_pragma=busy_timeout(500)", nil)
	mustProduct(env, "prod-mix", "Mixed Oats")
	insertPendingScanUnits(env, "scan-done", "prod-mix", scan.StockIn, 1)
	insertPendingScanUnits(env, "scan-echo", "prod-mix", scan.StockIn, 1)
	insertPendingScanUnits(env, "scan-a", "prod-mix", scan.StockIn, 1)
	insertPendingScanUnits(env, "scan-b", "prod-mix", scan.StockIn, 1)
	first := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody([]string{"scan-done", "scan-echo"}))
	if first.code != http.StatusOK || !strings.Contains(first.body, "scan-done") || !strings.Contains(first.body, "scan-echo") {
		t.Fatalf("seed commit = %d %s", first.code, first.body)
	}
	flagged := scan.Flagged
	in := scan.StockIn
	productID := "prod-mix"
	if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), scan.ScanEntry{
		ID: "scan-flag", UserID: "user-1", Barcode: "scan-flag",
		ScannedAt: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		Direction: &in, UnitCount: 1, Status: flagged, ProductID: &productID,
	}); err != nil {
		t.Fatalf("flagged scan: %v", err)
	}

	ids := []string{"missing", "scan-done", "scan-a", "scan-echo", "scan-b", "scan-flag"}
	release := holdSQLiteLock(t, path, "BEGIN IMMEDIATE")
	started := time.Now()
	res := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody(ids))
	elapsed := time.Since(started)
	release()

	body := decodeBatchCommit(t, res)
	if res.code != http.StatusOK {
		t.Fatalf("status %d %s", res.code, res.body)
	}
	if !sameIDs(body.CommittedIDs, []string{"scan-done"}) {
		t.Fatalf("committed = %v", body.CommittedIDs)
	}
	if !sameIDs(body.FailedIDs, []string{"missing", "scan-a", "scan-echo", "scan-b", "scan-flag"}) {
		t.Fatalf("failed = %v", body.FailedIDs)
	}
	reasons := map[string]string{}
	codes := map[string]string{}
	for i, id := range body.FailedIDs {
		reasons[id] = body.FailedReasons[i]
		codes[id] = body.FailedCodes[i]
	}
	if codes["missing"] != "scan_not_found" || reasons["missing"] != "This scan is no longer in the queue." {
		t.Fatalf("missing = %s %s", codes["missing"], reasons["missing"])
	}
	for _, id := range []string{"scan-a", "scan-echo", "scan-b", "scan-flag"} {
		if codes[id] != "busy" || reasons[id] != clientmsg.Busy {
			t.Fatalf("%s = %s %s", id, codes[id], reasons[id])
		}
	}
	if elapsed >= 2*batchLockWait {
		t.Fatalf("elapsed %s for one locked write and two untried items", elapsed)
	}
	if pending := scanIDs(t, handler, "pending"); !sameIDs(pending, []string{"scan-a", "scan-b"}) {
		t.Fatalf("pending = %v", pending)
	}
	if flaggedIDs := scanIDs(t, handler, "flagged"); !sameIDs(flaggedIDs, []string{"scan-flag"}) {
		t.Fatalf("flagged = %v", flaggedIDs)
	}
	if got := inventoryUnits(t, handler); got != 2 {
		t.Fatalf("inventory = %d", got)
	}
	if committed := scanIDs(t, handler, "committed"); !sameIDs(committed, []string{"scan-done", "scan-echo"}) {
		t.Fatalf("committed rows = %v", committed)
	}

	again := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody(ids))
	retried := decodeBatchCommit(t, again)
	if !sameIDs(retried.CommittedIDs, []string{"scan-done", "scan-a", "scan-echo", "scan-b"}) {
		t.Fatalf("retry committed = %v body %s", retried.CommittedIDs, again.body)
	}
	if len(retried.FailedIDs) != 2 {
		t.Fatalf("retry failed = %v", retried.FailedIDs)
	}
	retryCodes := map[string]string{}
	for i, id := range retried.FailedIDs {
		retryCodes[id] = retried.FailedCodes[i]
	}
	if retryCodes["missing"] != "scan_not_found" || retryCodes["scan-flag"] != "not_pending" {
		t.Fatalf("retry codes = %v", retryCodes)
	}
	if got := inventoryUnits(t, handler); got != 4 {
		t.Fatalf("inventory after retry = %d, want 4", got)
	}
	if flaggedIDs := scanIDs(t, handler, "flagged"); !sameIDs(flaggedIDs, []string{"scan-flag"}) {
		t.Fatalf("flagged after retry = %v", flaggedIDs)
	}
}

func TestBatchCommitFullDiskIsNoSpaceOnce(t *testing.T) {
	var slogBuf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&slogBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	handler, env := setupTestWithDB(t)
	mustProduct(env, "prod-full", "Full Oats")
	insertPendingScanUnits(env, "scan-kept", "prod-full", scan.StockIn, 1)
	insertPendingScanUnits(env, "scan-full-a", "prod-full", scan.StockIn, 1)
	insertPendingScanUnits(env, "scan-full-b", "prod-full", scan.StockIn, 1)
	seed := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody([]string{"scan-kept"}))
	if seed.code != http.StatusOK {
		t.Fatalf("seed = %d %s", seed.code, seed.body)
	}
	fillDatabase(t, env)
	filled := inventoryUnits(t, handler)
	if filled < 2 {
		t.Fatalf("fill added no rows, inventory = %d", filled)
	}

	slogBuf.Reset()
	ids := []string{"scan-kept", "scan-full-a", "scan-full-b"}
	res := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody(ids))
	body := decodeBatchCommit(t, res)
	if res.code != http.StatusOK {
		t.Fatalf("status %d %s", res.code, res.body)
	}
	if !sameIDs(body.CommittedIDs, []string{"scan-kept"}) {
		t.Fatalf("committed = %v", body.CommittedIDs)
	}
	if !sameIDs(body.FailedIDs, []string{"scan-full-a", "scan-full-b"}) ||
		!allEqual(body.FailedCodes, "no_space") || !allEqual(body.FailedReasons, clientmsg.NoSpace) {
		t.Fatalf("failures = %v %v %v", body.FailedIDs, body.FailedCodes, body.FailedReasons)
	}
	if strings.Contains(res.body, clientmsg.Generic) || strings.Contains(res.body, "internal") ||
		strings.Contains(strings.ToLower(res.body), "sqlite") {
		t.Fatalf("body = %s", res.body)
	}
	line := noSpaceLogLine(t, slogBuf.String())
	for _, part := range []string{"level=WARN", "msg=db_no_space", "event=db_no_space", "method=POST", "sqlite_code=13"} {
		if !strings.Contains(line, part) {
			t.Fatalf("log line missing %s: %s", part, line)
		}
	}
	if strings.Count(slogBuf.String(), "msg=db_no_space") != 1 {
		t.Fatalf("log lines = %q", slogBuf.String())
	}
	assertNoSpaceLogIsSafe(t, line)
	if pending := scanIDs(t, handler, "pending"); !sameIDs(pending, []string{"scan-full-a", "scan-full-b"}) {
		t.Fatalf("pending = %v", pending)
	}
	if got := inventoryUnits(t, handler); got != filled {
		t.Fatalf("inventory = %d, want %d", got, filled)
	}

	if _, err := env.DB.Exec(`PRAGMA max_page_count = 1073741823`); err != nil {
		t.Fatalf("raise page limit: %v", err)
	}
	slogBuf.Reset()
	again := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody(ids))
	retried := decodeBatchCommit(t, again)
	if !sameIDs(retried.CommittedIDs, ids) || len(retried.FailedIDs) != 0 {
		t.Fatalf("retry = %s", again.body)
	}
	if strings.Contains(slogBuf.String(), "db_no_space") {
		t.Fatalf("retry logged no_space: %s", slogBuf.String())
	}
	if got := inventoryUnits(t, handler); got != filled+2 {
		t.Fatalf("inventory after retry = %d, want %d", got, filled+2)
	}
	onceMore := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody(ids))
	repeated := decodeBatchCommit(t, onceMore)
	if !sameIDs(repeated.CommittedIDs, ids) || len(repeated.FailedIDs) != 0 {
		t.Fatalf("second retry = %s", onceMore.body)
	}
	if got := inventoryUnits(t, handler); got != filled+2 {
		t.Fatalf("inventory after a second retry = %d, want %d", got, filled+2)
	}
}

func TestExplainCommitFailureClassifiesWrappedStorageErrors(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		message string
		code    string
	}{
		{"busy", fmt.Errorf("begin transaction: %w", realDriverBusy(t)), clientmsg.Busy, "busy"},
		{"locked", fmt.Errorf("begin transaction: %w", realDriverLocked(t)), clientmsg.Busy, "busy"},
		{"full", fmt.Errorf("commit transaction: %w", realDriverFull(t)), clientmsg.NoSpace, "no_space"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message, code := explainCommitFailure(tc.err)
			if message != tc.message || code != tc.code {
				t.Fatalf("got %q %q, want %q %q", message, code, tc.message, tc.code)
			}
		})
	}
}

func TestBatchCommitStopsOnNoSpaceWithoutReadingLaterItems(t *testing.T) {
	var slogBuf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&slogBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	handler, env := setupTestWithDB(t)
	mustProduct(env, "prod-stop", "Stop Oats")
	insertPendingScanUnits(env, "scan-kept", "prod-stop", scan.StockIn, 1)
	insertPendingScanUnits(env, "scan-echo", "prod-stop", scan.StockIn, 1)
	insertPendingScanUnits(env, "scan-full", "prod-stop", scan.StockIn, 1)
	seed := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody([]string{"scan-kept", "scan-echo"}))
	if seed.code != http.StatusOK {
		t.Fatalf("seed = %d %s", seed.code, seed.body)
	}
	flagged := scan.Flagged
	in := scan.StockIn
	productID := "prod-stop"
	if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), scan.ScanEntry{
		ID: "scan-flag", UserID: "user-1", Barcode: "scan-flag",
		ScannedAt: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		Direction: &in, UnitCount: 1, Status: flagged, ProductID: &productID,
	}); err != nil {
		t.Fatalf("flagged scan: %v", err)
	}
	fillDatabase(t, env)
	filled := inventoryUnits(t, handler)

	slogBuf.Reset()
	ids := []string{"scan-kept", "scan-full", "scan-echo", "scan-flag"}
	res := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody(ids))
	body := decodeBatchCommit(t, res)
	if res.code != http.StatusOK {
		t.Fatalf("status %d %s", res.code, res.body)
	}
	// scan-echo was committed earlier, and scan-flag is not pending. Both sit
	// after the full-disk write, so they are reported no_space without a read.
	// Trying them would list scan-echo as committed and scan-flag as not_pending.
	if !sameIDs(body.CommittedIDs, []string{"scan-kept"}) {
		t.Fatalf("committed = %v", body.CommittedIDs)
	}
	if !sameIDs(body.FailedIDs, []string{"scan-full", "scan-echo", "scan-flag"}) ||
		!allEqual(body.FailedCodes, "no_space") || !allEqual(body.FailedReasons, clientmsg.NoSpace) {
		t.Fatalf("failures = %v %v %v", body.FailedIDs, body.FailedCodes, body.FailedReasons)
	}
	if strings.Count(slogBuf.String(), "msg=db_no_space") != 1 {
		t.Fatalf("log lines = %q", slogBuf.String())
	}
	if pending := scanIDs(t, handler, "pending"); !sameIDs(pending, []string{"scan-full"}) {
		t.Fatalf("pending = %v", pending)
	}
	if committed := scanIDs(t, handler, "committed"); !sameIDs(committed, []string{"scan-kept", "scan-echo"}) {
		t.Fatalf("committed rows = %v", committed)
	}
	if flaggedIDs := scanIDs(t, handler, "flagged"); !sameIDs(flaggedIDs, []string{"scan-flag"}) {
		t.Fatalf("flagged = %v", flaggedIDs)
	}
	if got := inventoryUnits(t, handler); got != filled {
		t.Fatalf("inventory = %d, want %d", got, filled)
	}

	if _, err := env.DB.Exec(`PRAGMA max_page_count = 1073741823`); err != nil {
		t.Fatalf("raise page limit: %v", err)
	}
	slogBuf.Reset()
	again := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody(ids))
	retried := decodeBatchCommit(t, again)
	if !sameIDs(retried.CommittedIDs, []string{"scan-kept", "scan-full", "scan-echo"}) {
		t.Fatalf("retry committed = %v body %s", retried.CommittedIDs, again.body)
	}
	if len(retried.FailedIDs) != 1 || retried.FailedIDs[0] != "scan-flag" || retried.FailedCodes[0] != "not_pending" {
		t.Fatalf("retry failed = %v %v", retried.FailedIDs, retried.FailedCodes)
	}
	if strings.Contains(slogBuf.String(), "db_no_space") {
		t.Fatalf("retry logged no_space: %s", slogBuf.String())
	}
	if got := inventoryUnits(t, handler); got != filled+1 {
		t.Fatalf("inventory after retry = %d, want %d", got, filled+1)
	}
	onceMore := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitBody(ids))
	repeated := decodeBatchCommit(t, onceMore)
	if !sameIDs(repeated.CommittedIDs, []string{"scan-kept", "scan-full", "scan-echo"}) || len(repeated.FailedIDs) != 1 {
		t.Fatalf("second retry = %s", onceMore.body)
	}
	if got := inventoryUnits(t, handler); got != filled+1 {
		t.Fatalf("inventory after a second retry = %d, want %d", got, filled+1)
	}
}

func TestBatchCommitFieldsUnderLockStayPerItem(t *testing.T) {
	var slogBuf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&slogBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	const items = 2
	path := filepath.Join(t.TempDir(), "pantry.db")
	handler, env := setupTestOnDSN(t, path+"?_pragma=busy_timeout(500)", nil)
	mustProduct(env, "prod-field", "Field Oats")
	ids := []string{"scan-field-a", "scan-field-b"}
	for _, id := range ids {
		insertPendingScanUnits(env, id, "prod-field", scan.StockIn, 1)
	}

	release := holdSQLiteLock(t, path, "BEGIN IMMEDIATE")
	started := time.Now()
	slogBuf.Reset()
	res := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitFieldsBody(ids))
	elapsed := time.Since(started)
	release()

	body := decodeBatchCommit(t, res)
	if res.code != http.StatusOK {
		t.Fatalf("status %d %s", res.code, res.body)
	}
	if !sameIDs(body.FailedIDs, ids) || len(body.CommittedIDs) != 0 || strings.Contains(res.body, "committedIds") {
		t.Fatalf("ids = %s", res.body)
	}
	if !allEqual(body.FailedCodes, "busy") || !allEqual(body.FailedReasons, clientmsg.Busy) {
		t.Fatalf("codes = %v reasons = %v", body.FailedCodes, body.FailedReasons)
	}
	if elapsed < batchLockWait/2 || elapsed >= 2*batchLockWait {
		t.Fatalf("elapsed %s", elapsed)
	}
	if strings.Count(slogBuf.String(), "msg=db_busy") != 1 {
		t.Fatalf("db_busy lines = %q", slogBuf.String())
	}
	rows := listedScans(t, handler, "pending")
	if len(rows) != items {
		t.Fatalf("pending = %+v", rows)
	}
	for _, row := range rows {
		if row.UnitCount != 1 || row.Direction == nil || *row.Direction != string(scan.StockIn) || row.ExpiresAt != nil {
			t.Fatalf("card changed under the lock: %+v", row)
		}
	}
	if got := inventoryUnits(t, handler); got != 0 {
		t.Fatalf("inventory = %d", got)
	}

	slogBuf.Reset()
	again := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitFieldsBody(ids))
	retried := decodeBatchCommit(t, again)
	if again.code != http.StatusOK || !sameIDs(retried.CommittedIDs, ids) || len(retried.FailedIDs) != 0 {
		t.Fatalf("retry = %d %s", again.code, again.body)
	}
	if got := inventoryUnits(t, handler); got != items*4 {
		t.Fatalf("inventory after retry = %d, want %d", got, items*4)
	}
	committed := listedScans(t, handler, "committed")
	if len(committed) != items {
		t.Fatalf("committed cards = %+v", committed)
	}
	for _, row := range committed {
		if row.UnitCount != 4 {
			t.Fatalf("committed count = %+v", row)
		}
	}
	onceMore := serveJSON(handler, http.MethodPost, "/api/scans/batch-commit", batchCommitFieldsBody(ids))
	repeated := decodeBatchCommit(t, onceMore)
	if !sameIDs(repeated.CommittedIDs, ids) || len(repeated.FailedIDs) != 0 {
		t.Fatalf("second retry = %s", onceMore.body)
	}
	if got := inventoryUnits(t, handler); got != items*4 {
		t.Fatalf("inventory after a second retry = %d, want %d", got, items*4)
	}
	if strings.Contains(slogBuf.String(), "db_busy") {
		t.Fatalf("retry logged db_busy: %s", slogBuf.String())
	}
}

func TestBatchCommitUpdateFullDiskIsNoSpacePerItem(t *testing.T) {
	var slogBuf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&slogBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	full := realDriverFull(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scans/batch-commit", HandleJSON((&ScanBatchCommitHandler{Queue: updateFailQueue{t: t, err: full}}).Handle))
	slogBuf.Reset()
	ids := []string{"scan-field-a", "scan-field-b"}
	res := serveJSON(mux, http.MethodPost, "/api/scans/batch-commit", batchCommitFieldsBody(ids))
	body := decodeBatchCommit(t, res)
	if res.code != http.StatusOK {
		t.Fatalf("status %d %s", res.code, res.body)
	}
	if !sameIDs(body.FailedIDs, ids) || !allEqual(body.FailedCodes, "no_space") || !allEqual(body.FailedReasons, clientmsg.NoSpace) {
		t.Fatalf("body = %s", res.body)
	}
	if strings.Contains(res.body, "committedIds") || strings.Count(slogBuf.String(), "msg=db_no_space") != 1 {
		t.Fatalf("body %s\nlog %s", res.body, slogBuf.String())
	}
}

func TestBatchCommitCanceledLogsOnce(t *testing.T) {
	var slogBuf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&slogBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scans/batch-commit", HandleJSON((&ScanBatchCommitHandler{Queue: cancelQueue{}}).Handle))
	res := serveJSON(mux, http.MethodPost, "/api/scans/batch-commit", batchCommitBody([]string{"scan-cancel-a", "scan-cancel-b"}))
	body := decodeBatchCommit(t, res)
	if res.code != http.StatusOK {
		t.Fatalf("status %d %s", res.code, res.body)
	}
	if !sameIDs(body.FailedIDs, []string{"scan-cancel-a", "scan-cancel-b"}) || !allEqual(body.FailedCodes, "internal") || !allEqual(body.FailedReasons, clientmsg.Generic) {
		t.Fatalf("body = %s", res.body)
	}
	if strings.Count(slogBuf.String(), "request canceled") != 1 || strings.Contains(logs.String(), "scan commit failed") {
		t.Fatalf("slog %q\nlog %q", slogBuf.String(), logs.String())
	}
	if strings.Contains(slogBuf.String(), "scan-cancel") {
		t.Fatalf("cancel log named an item: %s", slogBuf.String())
	}
}

type cancelQueue struct{}

func (cancelQueue) GetScanEntry(context.Context, string) (*scan.ScanEntry, error) {
	return nil, context.Canceled
}
func (cancelQueue) BatchUpdateScanEntries(context.Context, []string, *scan.ScanDirection, *int, *time.Time) error {
	return nil
}
func (cancelQueue) CommitStockIn(context.Context, *scan.ScanEntry) error { return nil }
func (cancelQueue) CommitStockOut(context.Context, *scan.ScanEntry, *string) (int, error) {
	return 0, nil
}

type updateFailQueue struct {
	t   *testing.T
	err error
}

func (q updateFailQueue) GetScanEntry(context.Context, string) (*scan.ScanEntry, error) {
	q.t.Fatal("batch read an item after the update failed")
	return nil, nil
}
func (q updateFailQueue) BatchUpdateScanEntries(context.Context, []string, *scan.ScanDirection, *int, *time.Time) error {
	return q.err
}
func (q updateFailQueue) CommitStockIn(context.Context, *scan.ScanEntry) error {
	q.t.Fatal("batch committed an item after the update failed")
	return nil
}
func (q updateFailQueue) CommitStockOut(context.Context, *scan.ScanEntry, *string) (int, error) {
	q.t.Fatal("batch committed an item after the update failed")
	return 0, nil
}

func batchCommitBody(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = `"` + id + `"`
	}
	return `{"scanEntryIds":[` + strings.Join(quoted, ",") + `],"commit":true}`
}

func batchCommitFieldsBody(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = `"` + id + `"`
	}
	return `{"scanEntryIds":[` + strings.Join(quoted, ",") + `],"direction":"stock_in","unitCount":4,"expiresAt":"2026-12-01T00:00:00Z","commit":true}`
}

func busyLogLine(t *testing.T, logged string) string {
	t.Helper()
	var line string
	for _, candidate := range strings.Split(logged, "\n") {
		if strings.Contains(candidate, "level=WARN") && strings.Contains(candidate, "msg=db_busy") {
			if line != "" {
				t.Fatalf("more than one busy line: %s", logged)
			}
			line = candidate
		}
	}
	if line == "" {
		t.Fatalf("no busy line: %s", logged)
	}
	return line
}

func listedScans(t *testing.T, handler http.Handler, status string) []struct {
	ID        string  `json:"id"`
	UnitCount int     `json:"unitCount"`
	Direction *string `json:"direction"`
	ExpiresAt *string `json:"expiresAt"`
} {
	t.Helper()
	res := serveJSON(handler, http.MethodGet, "/api/scans?userId=user-1&status="+status, "")
	if res.code != http.StatusOK {
		t.Fatalf("list %s = %d %s", status, res.code, res.body)
	}
	var rows []struct {
		ID        string  `json:"id"`
		UnitCount int     `json:"unitCount"`
		Direction *string `json:"direction"`
		ExpiresAt *string `json:"expiresAt"`
	}
	if err := json.Unmarshal([]byte(res.body), &rows); err != nil {
		t.Fatalf("list %s: %v\n%s", status, err, res.body)
	}
	return rows
}

type batchCommitBodyJSON struct {
	CommittedIDs  []string `json:"committedIds"`
	FailedIDs     []string `json:"failedIds"`
	FailedReasons []string `json:"failedReasons"`
	FailedCodes   []string `json:"failedCodes"`
}

func decodeBatchCommit(t *testing.T, res lockedHTTPResult) batchCommitBodyJSON {
	t.Helper()
	var body batchCommitBodyJSON
	if err := json.Unmarshal([]byte(res.body), &body); err != nil {
		t.Fatalf("decode: %v\n%s", err, res.body)
	}
	if len(body.FailedIDs) != len(body.FailedReasons) || len(body.FailedIDs) != len(body.FailedCodes) {
		t.Fatalf("parallel arrays = %s", res.body)
	}
	return body
}

func scanIDs(t *testing.T, handler http.Handler, status string) []string {
	t.Helper()
	res := serveJSON(handler, http.MethodGet, "/api/scans?userId=user-1&status="+status, "")
	if res.code != http.StatusOK {
		t.Fatalf("list %s = %d %s", status, res.code, res.body)
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(res.body), &rows); err != nil {
		t.Fatalf("list %s: %v", status, err)
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

func inventoryUnits(t *testing.T, handler http.Handler) int {
	t.Helper()
	res := serveJSON(handler, http.MethodGet, "/api/inventory", "")
	if res.code != http.StatusOK {
		t.Fatalf("inventory = %d %s", res.code, res.body)
	}
	var rows []struct {
		InstanceCount int `json:"instanceCount"`
	}
	if err := json.Unmarshal([]byte(res.body), &rows); err != nil {
		t.Fatalf("inventory: %v\n%s", err, res.body)
	}
	total := 0
	for _, row := range rows {
		total += row.InstanceCount
	}
	return total
}

func sameIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	for _, id := range want {
		seen[id]--
		if seen[id] < 0 {
			return false
		}
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func allEqual(values []string, want string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if value != want {
			return false
		}
	}
	return true
}

// fillDatabase lowers the page cap and writes until SQLite returns SQLITE_FULL
// for a one-byte insert, so the next commit cannot grow the file.
func fillDatabase(t *testing.T, env testEnv) {
	t.Helper()
	db := env.DB
	var itemID string
	if err := db.QueryRow(`SELECT id FROM items LIMIT 1`).Scan(&itemID); err != nil {
		t.Fatalf("item: %v", err)
	}
	var pages int
	if err := db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatalf("page_count: %v", err)
	}
	var limit int
	if err := db.QueryRow(fmt.Sprintf(`PRAGMA max_page_count = %d`, pages)).Scan(&limit); err != nil {
		t.Fatalf("max_page_count: %v", err)
	}
	if limit != pages {
		t.Fatalf("max_page_count = %d, page_count = %d", limit, pages)
	}
	// Pack the same table a stock-in inserts into. A different table can be
	// full while this one still has room on its last page.
	var last error
	for i := 0; i < 10000; i++ {
		_, err := db.Exec(
			`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES (?, ?, ?)`,
			fmt.Sprintf("fill-%04d", i), itemID, "2026-06-01 12:00:00",
		)
		if err == nil {
			continue
		}
		last = err
		break
	}
	var driverErr *sqlite.Error
	if last == nil || !errors.As(last, &driverErr) || driverErr.Code()&0xff != sqlitePrimaryFull {
		t.Fatalf("fill = %v", last)
	}
	if _, err := db.Exec(
		`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES (?, ?, ?)`,
		"fill-probe", itemID, "2026-06-01 12:00:00",
	); err == nil || !sqliteNoSpace(err) {
		t.Fatalf("database still accepted an instance after fill: %v", err)
	}
}
