package connection_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func insertProviderConnection(t *testing.T, db *sql.DB, id, userID, householdID, provider, state, accessToken, updatedAt string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, userID, householdID, provider, state, accessToken, updatedAt); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func TestReadPrefersHouseholdKeyedRowOverLegacyEvenWhenLegacyIsNewer(t *testing.T) {
	dir, db, scope := newTestDirectory(t)
	ctx := context.Background()
	house := scope.HouseholdID()
	old := "2026-01-01T00:00:00Z"
	newer := "2026-06-01T00:00:00Z"

	insertProviderConnection(t, db, "user-1:kroger", "user-1", house, "kroger", "connected", "legacy-token", newer)
	insertProviderConnection(t, db, house+":kroger", house, house, "kroger", "disconnected", "household-token", old)

	got, err := dir.Read(ctx, scope, "kroger")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "household-token" {
		t.Fatalf("AccessToken = %q want household-token", got.AccessToken)
	}
	if got.State != connection.StateDisconnected {
		t.Fatalf("State = %q want disconnected household-keyed row", got.State)
	}
}

func TestReadReturnsNewestUpdatedAtAmongNonCanonicalRows(t *testing.T) {
	dir, db, scope := newTestDirectory(t)
	ctx := context.Background()
	house := scope.HouseholdID()

	insertProviderConnection(t, db, "legacy-a:kroger", "legacy-a", house, "kroger", "connected", "older", "2026-01-01T00:00:00Z")
	insertProviderConnection(t, db, "legacy-b:kroger", "legacy-b", house, "kroger", "connected", "newer", "2026-06-01T00:00:00Z")

	got, err := dir.Read(ctx, scope, "kroger")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "newer" {
		t.Fatalf("AccessToken = %q want newer legacy row", got.AccessToken)
	}
}

func TestReadAfterWriteReturnsWrittenRow(t *testing.T) {
	dir, _, scope := newTestDirectory(t)
	ctx := context.Background()

	if err := dir.Write(ctx, scope, &connection.Connection{
		Provider: "kroger", State: connection.StateConnected, AccessToken: "written",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := dir.Read(ctx, scope, "kroger")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "written" || got.State != connection.StateConnected {
		t.Fatalf("Read after Write = %+v", got)
	}
}

func TestReadDoesNotReturnOtherHouseholdRows(t *testing.T) {
	dir, db, scope := newTestDirectory(t)
	ctx := context.Background()
	other := "01909999-9999-7000-8000-000000000099"
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Other', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, other); err != nil {
		t.Fatal(err)
	}
	insertProviderConnection(t, db, other+":kroger", other, other, "kroger", "connected", "foreign", when)

	got, err := dir.Read(ctx, scope, "kroger")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("primary Read should be nil, got %+v", got)
	}
}

func TestReadSingleCanonicalRowUnchanged(t *testing.T) {
	dir, _, scope := newTestDirectory(t)
	ctx := context.Background()
	if err := dir.Write(ctx, scope, &connection.Connection{
		Provider: "kroger", State: connection.StateConnected, AccessToken: "only",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := dir.Read(ctx, scope, "kroger")
	if err != nil || got.AccessToken != "only" {
		t.Fatalf("Read = %+v err=%v", got, err)
	}
}

func TestConsumeAuthStateUsesSameRowPreferenceAsRead(t *testing.T) {
	dir, db, scope := newTestDirectory(t)
	ctx := context.Background()
	house := scope.HouseholdID()
	storedAt := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, auth_state, auth_state_at, updated_at)
		VALUES ('user-1:kroger', 'user-1', ?, 'kroger', 'connected', 'legacy-state', ?, '2026-06-01T00:00:00Z')`,
		house, storedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, auth_state, auth_state_at, updated_at)
		VALUES (?, ?, ?, 'kroger', 'disconnected', 'household-state', ?, '2026-01-01T00:00:00Z')`,
		house+":kroger", house, house, storedAt); err != nil {
		t.Fatal(err)
	}

	ok, err := dir.ConsumeAuthState(ctx, scope, "kroger", "household-state")
	if err != nil || !ok {
		t.Fatalf("ConsumeAuthState ok=%v err=%v", ok, err)
	}
}

func TestReadLegacyConnectedDoesNotMaskDisconnectedHouseholdKeyedRow(t *testing.T) {
	dir, db, scope := newTestDirectory(t)
	ctx := context.Background()
	house := scope.HouseholdID()
	insertProviderConnection(t, db, "user-1:kroger", "user-1", house, "kroger", "connected", "legacy-live", "2026-06-01T00:00:00Z")
	insertProviderConnection(t, db, house+":kroger", house, house, "kroger", "disconnected", "", "2026-01-01T00:00:00Z")

	got, err := dir.Read(ctx, scope, "kroger")
	if err != nil {
		t.Fatal(err)
	}
	// Read intentionally returns the household-keyed row even when a legacy row is still connected.
	if got.State != connection.StateDisconnected {
		t.Fatalf("State = %q want disconnected on canonical row", got.State)
	}
}

func TestReadMissingProviderReturnsNilWithDuplicateRows(t *testing.T) {
	dir, db, scope := newTestDirectory(t)
	got, err := dir.Read(context.Background(), scope, "missing-provider")
	if err != nil || got != nil {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	_ = db
}

func TestReadUsesPrimaryScopeNotLegacyUserHousehold(t *testing.T) {
	dir, db, _ := newTestDirectory(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	other, err := tenant.New("01909999-9999-7000-8000-000000000099")
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Other', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, other.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	insertProviderConnection(t, db, other.HouseholdID()+":kroger", other.HouseholdID(), other.HouseholdID(), "kroger", "connected", "other-house", when)

	got, err := dir.Read(ctx, primary, "kroger")
	if err != nil || got != nil {
		t.Fatalf("primary Read leaked other household: %+v err=%v", got, err)
	}
}
