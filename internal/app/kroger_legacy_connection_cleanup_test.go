package app

import (
	"context"
	"database/sql"
	"fmt"
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

func TestKrogerLegacyCleanupCommitFailureNoFlag(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`PRAGMA query_only = ON`); err != nil {
		t.Fatal(err)
	}
	err := RunKrogerLegacyConnectionCleanup(ctx, db)
	if err == nil {
		t.Fatal("expected error when commit cannot write (query_only)")
	}
	var flag sql.NullString
	_ = db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, krogerLegacyConnectionCleanupKey).Scan(&flag)
	if flag.Valid {
		t.Fatal("flag must not be set when commit fails")
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

	if err := RunKrogerLegacyConnectionCleanup(ctx, db); err != nil {
		t.Fatal(err)
	}
	assertLegacyCleanupFlag(t, db)

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE provider_id = 'kroger' AND household_id = ?`, hid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("want 1 kroger row, got n=%d err=%v", n, err)
	}
	var gotAccess, gotRefresh string
	if err := db.QueryRow(`
		SELECT access_token, refresh_token FROM provider_connections WHERE id = ?`, canonID).Scan(&gotAccess, &gotRefresh); err != nil {
		t.Fatal(err)
	}
	if gotAccess != wantAccess || gotRefresh != wantRefresh {
		t.Fatalf("canonical tokens overwritten: access=%q refresh=%q", gotAccess, gotRefresh)
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
	var rowID, rowUser string
	if err := db.QueryRow(`SELECT id, user_id FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`, hid).Scan(&rowID, &rowUser); err != nil {
		t.Fatal(err)
	}
	if rowID != hid+":kroger" || rowUser != hid {
		t.Fatalf("id/user_id not canonical: id=%q user_id=%q", rowID, rowUser)
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
	var gotSec string
	if err := db.QueryRow(`SELECT access_token FROM provider_connections WHERE id = ?`, secondary+":kroger").Scan(&gotSec); err != nil || gotSec != secKR {
		t.Fatalf("secondary kroger token changed: %q err=%v", gotSec, err)
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
		VALUES (?, ?, ?, 'kroger', 'connected', ?)`, legacyKrogerConnectionID, "user-wrong", hid, when); err != nil {
		t.Fatal(err)
	}
	err = RunKrogerLegacyConnectionCleanup(ctx, db)
	if err == nil {
		t.Fatal("expected failure when lone legacy row user_id is not user-1")
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

func assertLegacyCleanupFlag(t *testing.T, db *sql.DB) {
	t.Helper()
	var v string
	if err := db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, krogerLegacyConnectionCleanupKey).Scan(&v); err != nil || v != "1" {
		t.Fatalf("cleanup flag = %q err=%v", v, err)
	}
}
