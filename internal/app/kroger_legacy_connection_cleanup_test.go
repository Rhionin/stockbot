package app

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestKrogerLegacyCleanupNilDBIsNoOp(t *testing.T) {
	if err := RunKrogerLegacyConnectionCleanup(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestKrogerLegacyCleanupEmptyHouseholdIDFails(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', '', 'kroger', 'connected', ?)`, legacyKrogerConnectionID, when); err != nil {
		t.Fatal(err)
	}
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err == nil {
		t.Fatal("expected error when legacy row has empty household_id")
	}
}

func TestKrogerLegacyCleanupFailsOnInvalidLegacyHouseholdID(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', 'not-a-uuid', 'kroger', 'connected', ?)`, legacyKrogerConnectionID, when); err != nil {
		t.Fatal(err)
	}
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err == nil {
		t.Fatal("expected error for invalid household id on legacy row")
	}
}

func TestKrogerLegacyCleanupFailsWhenLegacyHouseholdMissing(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	unknown := "01907777-7777-7000-8000-000000000077"
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, unknown, when); err != nil {
		t.Fatal(err)
	}
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err == nil {
		t.Fatal("expected error when legacy row names an unknown household")
	}
}

func TestKrogerLegacyCleanupReadErrorSurfaces(t *testing.T) {
	db := openCutoverDB(t)
	_ = db.Close()
	err := RunKrogerLegacyConnectionCleanup(context.Background(), db)
	if err == nil {
		t.Fatal("expected error from closed database")
	}
}

func TestKrogerLegacyCleanupCommitHookFailureNoFlagNoLegacyPath(t *testing.T) {
	t.Cleanup(func() {
		krogerLegacyCleanupCommit = func(tx *sql.Tx) error { return tx.Commit() }
		krogerLegacyCleanupLogSuccess = func(msg string) { log.Print(msg) }
	})
	var successCalls []string
	krogerLegacyCleanupLogSuccess = func(msg string) { successCalls = append(successCalls, msg) }
	db := openCutoverDB(t)
	ctx := context.Background()
	krogerLegacyCleanupCommit = func(*sql.Tx) error { return fmt.Errorf("inject commit failure") }
	err := RunKrogerLegacyConnectionCleanup(ctx, db)
	if err == nil || !strings.Contains(err.Error(), "inject commit failure") {
		t.Fatalf("err = %v", err)
	}
	var flag sql.NullString
	if err := db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, krogerLegacyConnectionCleanupKey).Scan(&flag); err != sql.ErrNoRows {
		if err == nil && flag.Valid {
			t.Fatalf("flag should be absent, got %q", flag.String)
		}
		if err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
	}
	if len(successCalls) != 0 {
		t.Fatalf("expected no success logs before commit, got %v", successCalls)
	}
}

func TestKrogerLegacyCleanupCommitHookFailureNoFlagDuplicatePathRetrySucceeds(t *testing.T) {
	t.Cleanup(func() {
		krogerLegacyCleanupCommit = func(tx *sql.Tx) error { return tx.Commit() }
		krogerLegacyCleanupLogSuccess = func(msg string) { log.Print(msg) }
	})
	var successCalls []string
	var mu sync.Mutex
	krogerLegacyCleanupLogSuccess = func(msg string) {
		mu.Lock()
		successCalls = append(successCalls, msg)
		mu.Unlock()
	}
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', 'canon', ?)`, hid+":kroger", hid, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	var commitCalls int
	krogerLegacyCleanupCommit = func(tx *sql.Tx) error {
		commitCalls++
		if commitCalls == 1 {
			return fmt.Errorf("inject commit failure at cleanup commit")
		}
		return tx.Commit()
	}
	err = RunKrogerLegacyConnectionCleanup(ctx, db)
	if err == nil || !strings.Contains(err.Error(), "inject commit failure") {
		t.Fatalf("first run err = %v", err)
	}
	mu.Lock()
	logN := len(successCalls)
	mu.Unlock()
	if logN != 0 {
		t.Fatalf("expected no success logs on rolled-back commit, got %v", successCalls)
	}
	var flag sql.NullString
	_ = db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, krogerLegacyConnectionCleanupKey).Scan(&flag)
	if flag.Valid {
		t.Fatal("flag must not be set when commit fails")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`, hid).Scan(&n); err != nil || n != 2 {
		t.Fatalf("expected 2 rows after failed commit, got %d err=%v", n, err)
	}
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	assertLegacyCleanupFlag(t, db)
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`, hid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("after retry want 1 row, got %d err=%v", n, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(successCalls) != 1 || !strings.Contains(successCalls[0], "removed duplicate") {
		t.Fatalf("expected one post-commit success log, got %v", successCalls)
	}
}

func TestKrogerLegacyCleanupNoLegacyRowSetsFlag(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	assertLegacyCleanupFlag(t, db)
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func TestKrogerLegacyCleanupDuplicateDeletesLegacyPreservesCanonical(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-03-01T12:00:00Z"
	const (
		wantAccess  = "access-canonical-xx"
		wantRefresh = "refresh-canonical-yy"
		wantScope   = "cart.basic:write product.compact"
	)
	canonID := hid + ":kroger"
	if _, err := db.Exec(`
		INSERT INTO provider_connections
			(id, user_id, household_id, provider_id, state, access_token, refresh_token,
			 access_token_expires_at, auth_state, auth_state_at, app_key, auth_app_key, auth_return, granted_scope, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', ?, ?, ?, NULL, NULL, 'appkey-canon', 'aak', 'shop', ?, ?)`,
		canonID, hid, hid, wantAccess, wantRefresh, when, wantScope, when); err != nil {
		t.Fatal(err)
	}
	const legacyAccess = "access-legacy-should-drop"
	if _, err := db.Exec(`
		INSERT INTO provider_connections
			(id, user_id, household_id, provider_id, state, access_token, refresh_token, granted_scope, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', ?, 'legacy-refresh', ?, ?)`,
		legacyKrogerConnectionID, hid, legacyAccess, wantScope, when); err != nil {
		t.Fatal(err)
	}
	canonBefore := connectionSnapshot(t, db, canonID)

	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	assertLegacyCleanupFlag(t, db)

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE provider_id = 'kroger' AND household_id = ?`, hid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("want 1 kroger row, got n=%d err=%v", n, err)
	}
	if connectionSnapshot(t, db, canonID) != canonBefore {
		t.Fatal("canonical row payload changed when deleting duplicate legacy")
	}
}

func TestKrogerLegacyCleanupLegacyOnlyRekeysPreservesPayload(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-03-02T15:00:00Z"
	const (
		wantAccess  = "legacy-only-access"
		wantRefresh = "legacy-only-refresh"
		wantState   = "reauth_required"
		wantScope   = "cart.basic:write"
	)
	wantKey := appcred.AppKey("legacy-client")
	if _, err := db.Exec(`
		INSERT INTO provider_connections
			(id, user_id, household_id, provider_id, state, access_token, refresh_token,
			 access_token_expires_at, auth_state, auth_state_at, app_key, auth_app_key, auth_return, granted_scope, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', ?, ?, ?, ?, 'pend', ?, ?, 'ak1', 'ret-shop', ?, ?)`,
		legacyKrogerConnectionID, hid, wantState, wantAccess, wantRefresh, when, when, wantKey, wantScope, when); err != nil {
		t.Fatal(err)
	}
	legacyBefore := connectionSnapshot(t, db, legacyKrogerConnectionID)

	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	assertLegacyCleanupFlag(t, db)

	scope, err := tenant.New(hid)
	if err != nil {
		t.Fatal(err)
	}
	dir := connection.NewDirectory(db)
	conn, err := dir.Read(ctx, scope, "kroger")
	if err != nil {
		t.Fatal(err)
	}
	if conn == nil {
		t.Fatal("expected connection after rekey")
	}
	if conn.State != connection.ConnectionState(wantState) || conn.AccessToken != wantAccess || conn.RefreshToken != wantRefresh {
		t.Fatalf("read after cleanup: state=%s access=%q refresh=%q", conn.State, conn.AccessToken, conn.RefreshToken)
	}
	if conn.AppKey != wantKey || conn.AuthAppKey != "ak1" || conn.AuthReturn != "ret-shop" || conn.GrantedScope != wantScope {
		t.Fatalf("auth fields mismatch after rekey")
	}
	rowAfter := connectionSnapshot(t, db, hid+":kroger")
	if connectionPayloadOnly(legacyBefore) != connectionPayloadOnly(rowAfter) {
		t.Fatalf("rekey changed non-id columns: before=\n%s\nafter=\n%s", legacyBefore, rowAfter)
	}
}

func TestKrogerLegacyCleanupDoesNotTouchOtherHouseholdsOrProviders(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	pHID := primary.HouseholdID()
	secondary := "01909999-9999-7000-8000-000000000099"
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Secondary', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, secondary); err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)

	// Primary: duplicate kroger (legacy + canonical) — cleanup fixes this household only.
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', 'p-canon', ?)`, pHID+":kroger", pHID, pHID, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', 'p-legacy', ?)`, legacyKrogerConnectionID, pHID, when); err != nil {
		t.Fatal(err)
	}

	// Secondary: single canonical Kroger + Instacart row (must be unchanged).
	const secKR = "sec-kr-access"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', ?, ?)`, secondary+":kroger", secondary, secondary, secKR, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, ?, ?, 'instacart', 'connected', 'inst-1', ?)`, secondary+":instacart", secondary, secondary, when); err != nil {
		t.Fatal(err)
	}
	secKRSnap := connectionSnapshot(t, db, secondary+":kroger")
	secInstSnap := connectionSnapshot(t, db, secondary+":instacart")

	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}

	var pCount, sKR, sInst int
	_ = db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`, pHID).Scan(&pCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`, secondary).Scan(&sKR)
	_ = db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE household_id = ? AND provider_id = 'instacart'`, secondary).Scan(&sInst)
	if pCount != 1 || sKR != 1 || sInst != 1 {
		t.Fatalf("counts primary_kroger=%d secondary_kroger=%d instacart=%d", pCount, sKR, sInst)
	}
	if connectionSnapshot(t, db, secondary+":kroger") != secKRSnap {
		t.Fatal("secondary kroger row changed")
	}
	if connectionSnapshot(t, db, secondary+":instacart") != secInstSnap {
		t.Fatal("secondary instacart row changed")
	}
}

func TestKrogerLegacyCleanupCanonicalOnlyUnchanged(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections
			(id, user_id, household_id, provider_id, state, access_token, refresh_token, access_token_expires_at,
			 auth_state, auth_state_at, app_key, auth_app_key, auth_return, granted_scope, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', 'only-access', 'only-ref', ?, NULL, NULL, 'k1', 'k2', 'ret', 'scope-x', ?)`,
		hid+":kroger", hid, hid, when, when); err != nil {
		t.Fatal(err)
	}
	row2 := connectionSnapshot(t, db, hid+":kroger")
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	row3 := connectionSnapshot(t, db, hid+":kroger")
	if row3 != row2 {
		t.Fatalf("canonical-only row changed by cleanup")
	}
}

func TestKrogerLegacyCleanupFailsOnMultipleCanonicalRows(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES ('extra-kroger', ?, ?, 'kroger', 'connected', ?)`, hid, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	err = RunKrogerLegacyConnectionCleanup(ctx, db)
	if err == nil {
		t.Fatal("expected error for multiple canonical rows")
	}
	var flag sql.NullString
	_ = db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, krogerLegacyConnectionCleanupKey).Scan(&flag)
	if flag.Valid {
		t.Fatal("flag should not be set on failure")
	}
}

func TestKrogerLegacyCleanupFailsClosedUnknownHousehold(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	unknown := "01908888-8888-7000-8000-000000000088"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, unknown, when); err != nil {
		t.Fatal(err)
	}
	err = RunKrogerLegacyConnectionCleanup(ctx, db)
	if err == nil {
		t.Fatal("expected error for unknown household on legacy row")
	}
	// Primary household should not gain a spurious row from a failed attempt.
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE household_id = ?`, hid).Scan(&n)
	if n != 0 {
		t.Fatalf("unexpected rows for primary: %d", n)
	}
}

func TestKrogerLegacyCleanupFailureAtFlagRollsBack(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', 'keep-me', ?)`, hid+":kroger", hid, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TRIGGER block_legacy_cleanup_flag
		BEFORE INSERT ON app_settings
		WHEN NEW.key = 'kroger_legacy_connection_cleanup_v1'
		BEGIN
			SELECT RAISE(ABORT, 'inject legacy cleanup failure');
		END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP TRIGGER IF EXISTS block_legacy_cleanup_flag`) })

	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err == nil {
		t.Fatal("expected cleanup failure")
	}
	var cnt int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE provider_id = 'kroger' AND household_id = ?`, hid).Scan(&cnt); err != nil || cnt != 2 {
		t.Fatalf("expected both rows still present, got %d err=%v", cnt, err)
	}
	if _, err := db.Exec(`DROP TRIGGER block_legacy_cleanup_flag`); err != nil {
		t.Fatal(err)
	}
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	assertLegacyCleanupFlag(t, db)
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE provider_id = 'kroger' AND household_id = ?`, hid).Scan(&cnt); err != nil || cnt != 1 {
		t.Fatalf("after retry want 1 row, got %d err=%v", cnt, err)
	}
}

func TestKrogerLegacyCleanupPostCheckDuplicateHouseholdsFails(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', 'canon', ?)`, hid+":kroger", hid, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	injectedUser := "01907777-7777-7000-8000-000000000088"
	triggerSQL := fmt.Sprintf(`
		CREATE TRIGGER kroger_cleanup_inject_dup AFTER DELETE ON provider_connections
		WHEN OLD.id = 'user-1:kroger' AND OLD.provider_id = 'kroger'
		BEGIN
			INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
			VALUES ('inj-krog', '%s', OLD.household_id, 'kroger', 'connected', '%s');
		END`, injectedUser, when)
	if _, err := db.Exec(triggerSQL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP TRIGGER IF EXISTS kroger_cleanup_inject_dup`) })

	err = RunKrogerLegacyConnectionCleanup(ctx, db)
	if err == nil || !strings.Contains(err.Error(), "post-check found multiple kroger rows for a household") {
		t.Fatalf("err = %v", err)
	}
	var flag sql.NullString
	_ = db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, krogerLegacyConnectionCleanupKey).Scan(&flag)
	if flag.Valid {
		t.Fatal("flag must not be set when post-check fails")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`, hid).Scan(&n); err != nil || n != 2 {
		t.Fatalf("expected transaction rollback preserving 2 rows, got %d err=%v", n, err)
	}
}

func TestKrogerLegacyCleanupFailureOnDeleteRollsBack(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', ?)`, hid+":kroger", hid, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TRIGGER block_legacy_delete
		BEFORE DELETE ON provider_connections
		WHEN OLD.id = 'user-1:kroger'
		BEGIN
			SELECT RAISE(ABORT, 'inject delete failure');
		END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP TRIGGER IF EXISTS block_legacy_delete`) })

	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err == nil {
		t.Fatal("expected failure on delete")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE id = ?`, legacyKrogerConnectionID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("legacy row should remain: n=%d err=%v", n, err)
	}
}

func TestKrogerLegacyCleanupIdempotentAfterSuccess(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', 'x', ?)`, hid+":kroger", hid, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	before := connectionSnapshot(t, db, hid+":kroger")
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	after := connectionSnapshot(t, db, hid+":kroger")
	if before != after {
		t.Fatalf("second run changed data")
	}
}

func TestKrogerLegacyCleanupNoOpWhenFlagAlreadySet(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', ?)`, hid+":kroger", hid, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES (?, '1')`, krogerLegacyConnectionCleanupKey); err != nil {
		t.Fatal(err)
	}
	before := connectionSnapshot(t, db, hid+":kroger")
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	after := connectionSnapshot(t, db, hid+":kroger")
	if before != after {
		t.Fatal("cleanup must not run when flag already set")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE provider_id = 'kroger' AND household_id = ?`, hid).Scan(&n); err != nil || n != 2 {
		t.Fatalf("expected duplicate rows unchanged when flag preset, got %d", n)
	}
}

func TestKrogerLegacyCleanupLoneLegacyUnexpectedUserFails(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-wrong', ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	err = RunKrogerLegacyConnectionCleanup(ctx, db)
	if err == nil {
		t.Fatal("expected failure when lone legacy row user_id is not user-1")
	}
}

func TestKrogerLegacyCleanupDuplicateRejectsMismatchedLegacyUser(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	canonID := hid + ":kroger"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', 'canon', ?)`, canonID, hid, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, 'user-wrong', ?, 'kroger', 'connected', 'legacy', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	legacySnap := connectionSnapshot(t, db, legacyKrogerConnectionID)
	canonSnap := connectionSnapshot(t, db, canonID)
	err = RunKrogerLegacyConnectionCleanup(ctx, db)
	if err == nil {
		t.Fatal("expected failure when legacy user_id is not user-1 on duplicate path")
	}
	if connectionSnapshot(t, db, legacyKrogerConnectionID) != legacySnap {
		t.Fatal("legacy row mutated on rejection")
	}
	if connectionSnapshot(t, db, canonID) != canonSnap {
		t.Fatal("canonical row mutated on rejection")
	}
	var flag sql.NullString
	_ = db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, krogerLegacyConnectionCleanupKey).Scan(&flag)
	if flag.Valid {
		t.Fatal("flag must not be set on failure")
	}
}

func TestKrogerLegacyCleanupDuplicateRejectsBadCanonicalIdentity(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES ('wrong-id', ?, ?, 'kroger', 'connected', 'tok', ?)`, hid, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	beforeLegacy := connectionSnapshot(t, db, legacyKrogerConnectionID)
	beforeNonLegacy := connectionSnapshot(t, db, "wrong-id")
	err = RunKrogerLegacyConnectionCleanup(ctx, db)
	if err == nil {
		t.Fatal("expected failure when canonical id is not household:kroger")
	}
	if connectionSnapshot(t, db, legacyKrogerConnectionID) != beforeLegacy {
		t.Fatal("legacy row changed")
	}
	if connectionSnapshot(t, db, "wrong-id") != beforeNonLegacy {
		t.Fatal("non-legacy row changed")
	}
	var flag sql.NullString
	_ = db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, krogerLegacyConnectionCleanupKey).Scan(&flag)
	if flag.Valid {
		t.Fatal("flag must not be set")
	}
}

func TestKrogerLegacyCleanupFailsClosedThreeKrogerRowsSameHousehold(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	// Third row must use a different user_id — UNIQUE(user_id, provider_id).
	otherUser := "01907777-7777-7000-8000-000000000099"
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', 'a', ?)`, hid+":kroger", hid, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES ('extra-kroger-2', ?, ?, 'kroger', 'connected', 'b', ?)`, otherUser, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	snaps := map[string]string{
		legacyKrogerConnectionID: connectionSnapshot(t, db, legacyKrogerConnectionID),
		hid + ":kroger":          connectionSnapshot(t, db, hid+":kroger"),
		"extra-kroger-2":         connectionSnapshot(t, db, "extra-kroger-2"),
	}
	err = RunKrogerLegacyConnectionCleanup(ctx, db)
	if err == nil {
		t.Fatal("expected error with three kroger rows for one household")
	}
	for id, want := range snaps {
		if connectionSnapshot(t, db, id) != want {
			t.Fatalf("row %q mutated on failure", id)
		}
	}
	var flag sql.NullString
	_ = db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, krogerLegacyConnectionCleanupKey).Scan(&flag)
	if flag.Valid {
		t.Fatal("flag must not be set")
	}
}

func TestKrogerLegacyCleanupCommitHookFailureRekeyRollsBackAllColumns(t *testing.T) {
	t.Cleanup(func() {
		krogerLegacyCleanupCommit = func(tx *sql.Tx) error { return tx.Commit() }
		krogerLegacyCleanupLogSuccess = func(msg string) { log.Print(msg) }
	})
	var successCalls []string
	krogerLegacyCleanupLogSuccess = func(msg string) { successCalls = append(successCalls, msg) }
	var commitCalls int
	krogerLegacyCleanupCommit = func(tx *sql.Tx) error {
		commitCalls++
		if commitCalls == 1 {
			return fmt.Errorf("inject commit failure rekey")
		}
		return tx.Commit()
	}
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-03-02T15:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections
			(id, user_id, household_id, provider_id, state, access_token, refresh_token,
			 access_token_expires_at, auth_state, auth_state_at, app_key, auth_app_key, auth_return, granted_scope, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', 'acc', 'ref', ?, 'pend', ?, 'apk', 'aak', 'ret', 'scope', ?)`,
		legacyKrogerConnectionID, hid, when, when, when); err != nil {
		t.Fatal(err)
	}
	wantSnap := connectionSnapshot(t, db, legacyKrogerConnectionID)
	err = RunKrogerLegacyConnectionCleanup(ctx, db)
	if err == nil || !strings.Contains(err.Error(), "inject commit failure rekey") {
		t.Fatalf("err = %v", err)
	}
	if connectionSnapshot(t, db, legacyKrogerConnectionID) != wantSnap {
		t.Fatal("legacy row changed after failed commit (expected full rollback)")
	}
	if len(successCalls) != 0 {
		t.Fatalf("unexpected success logs: %v", successCalls)
	}
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	assertLegacyCleanupFlag(t, db)
	if connectionSnapshot(t, db, hid+":kroger") == wantSnap {
		t.Fatal("rekey should have changed id/user_id after success")
	}
	wantPayload := connectionPayloadOnly(wantSnap)
	gotPayload := connectionPayloadOnly(connectionSnapshot(t, db, hid+":kroger"))
	if wantPayload != gotPayload {
		t.Fatalf("payload not preserved after successful rekey retry: want %q got %q", wantPayload, gotPayload)
	}
}

func TestKrogerLegacyCleanupDuplicateDisconnectedDeletesLegacy(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', 'canon-tok', ?)`, hid+":kroger", hid, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'disconnected', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	var gotState, gotTok sql.NullString
	if err := db.QueryRow(`
		SELECT state, access_token FROM provider_connections WHERE id = ?`, hid+":kroger").Scan(&gotState, &gotTok); err != nil {
		t.Fatal(err)
	}
	if gotState.String != "connected" || !gotTok.Valid || gotTok.String != "canon-tok" {
		t.Fatalf("canonical row changed: state=%s tok=%v", gotState.String, gotTok)
	}
}

func TestMutationLegacyCleanupMustNotMergeTokensFromLegacyIntoCanonical(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hid := primary.HouseholdID()
	when := "2026-04-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', 'CANON-ACCESS', ?)`, hid+":kroger", hid, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, 'user-1', ?, 'kroger', 'connected', 'LEGACY-ACCESS', ?)`, legacyKrogerConnectionID, hid, when); err != nil {
		t.Fatal(err)
	}
	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	var tok string
	if err := db.QueryRow(`SELECT access_token FROM provider_connections WHERE id = ?`, hid+":kroger").Scan(&tok); err != nil {
		t.Fatal(err)
	}
	if tok != "CANON-ACCESS" {
		t.Fatalf("mutation: canonical access_token became %q", tok)
	}
}

func connectionSnapshot(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var (
		userID, householdID, providerID, state string
		accessToken, refreshToken              sql.NullString
		expiresAt                              sql.NullString
		authState, authStateAt                 sql.NullString
		appKey, authAppKey, authReturn         sql.NullString
		grantedScope                           sql.NullString
		updatedAt                              string
	)
	err := db.QueryRow(`
		SELECT user_id, household_id, provider_id, state, access_token, refresh_token, access_token_expires_at,
		       auth_state, auth_state_at, app_key, auth_app_key, auth_return, granted_scope, updated_at
		FROM provider_connections WHERE id = ?`, id).Scan(
		&userID, &householdID, &providerID, &state, &accessToken, &refreshToken, &expiresAt,
		&authState, &authStateAt, &appKey, &authAppKey, &authReturn, &grantedScope, &updatedAt)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s",
		userID, householdID, providerID, state,
		nullable(accessToken), nullable(refreshToken), nullable(expiresAt),
		nullable(authState), nullable(authStateAt),
		nullable(appKey), nullable(authAppKey), nullable(authReturn), nullable(grantedScope),
		updatedAt)
}

func nullable(ns sql.NullString) string {
	if !ns.Valid {
		return "\\N"
	}
	return ns.String
}

// connectionPayloadOnly returns the pipe-delimited snapshot with id and user_id removed
// (first two fields), so rekey can be checked without hand-editing strings.
func connectionPayloadOnly(snapshot string) string {
	parts := strings.Split(snapshot, "|")
	if len(parts) < 3 {
		return snapshot
	}
	return strings.Join(parts[2:], "|")
}

func assertLegacyCleanupFlag(t *testing.T, db *sql.DB) {
	t.Helper()
	var v string
	if err := db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, krogerLegacyConnectionCleanupKey).Scan(&v); err != nil || v != "1" {
		t.Fatalf("cleanup flag = %q err=%v", v, err)
	}
}
