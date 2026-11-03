package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func newTestCatalog(t *testing.T) (*Catalog, tenant.Scope, *sql.DB) {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	return NewCatalog(conn), scope, conn
}

func TestNameKeyRejectsEmptyAndLongNames(t *testing.T) {
	if _, _, err := NameKey("   "); err == nil {
		t.Fatal("expected empty name error")
	}
	long := strings.Repeat("x", 41)
	if _, _, err := NameKey(long); err == nil {
		t.Fatal("expected long name error")
	}
	display, key, err := NameKey("  Fresh Market  ")
	if err != nil || display != "Fresh Market" || key != "fresh market" {
		t.Fatalf("got %q %q err %v", display, key, err)
	}
}

func TestCatalogCreateLinkedAndValidation(t *testing.T) {
	catalog, scope, _ := newTestCatalog(t)
	ctx := context.Background()

	kroger := "kroger"
	loc := "12345678"
	chain := "KROGER"
	_, err := catalog.Create(ctx, scope, CreateInput{
		Name:           "Cart",
		LinkedProvider: &kroger,
		LocationID:     &loc,
		LocationChain:  &chain,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = catalog.Create(ctx, scope, CreateInput{Name: ""})
	if err == nil {
		t.Fatal("expected validation on empty name")
	}
	var ve *Error
	if !errors.As(err, &ve) || ve.Code != CodeInvalid {
		t.Fatalf("got %v", err)
	}
	badKind := "warehouse"
	_, err = catalog.Create(ctx, scope, CreateInput{Name: "Other", Kind: badKind})
	if err == nil {
		t.Fatal("expected validation on kind")
	}
	if !errors.As(err, &ve) || ve.Code != CodeInvalid {
		t.Fatalf("got %v", err)
	}
	_, err = catalog.Create(ctx, scope, CreateInput{
		Name:           "Second link",
		LinkedProvider: &kroger,
		LocationID:     &loc,
		LocationChain:  &chain,
	})
	if err == nil {
		t.Fatal("expected already linked")
	}
	var le *Error
	if !errors.As(err, &le) || le.Code != CodeAlreadyLinked {
		t.Fatalf("got %v", err)
	}
}

func TestCatalogPatchRenameUnlinkAndNotFound(t *testing.T) {
	catalog, scope, _ := newTestCatalog(t)
	ctx := context.Background()
	kroger := "kroger"
	loc := "12345678"
	chain := "KROGER"
	st, err := catalog.Create(ctx, scope, CreateInput{
		Name:           "Linked",
		LinkedProvider: &kroger,
		LocationID:     &loc,
		LocationChain:  &chain,
	})
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = catalog.db.ExecContext(ctx, `
		INSERT INTO store_availability (
			household_id, store_id, product_id, location_id, status, checked_at, status_expires_at
		) VALUES (?, ?, 'prod-1', ?, 'carried', ?, ?)`,
		scope.HouseholdID(), st.ID, loc, when, when)
	if err != nil {
		t.Fatal(err)
	}

	name := "Renamed Store"
	updated, err := catalog.Patch(ctx, scope, st.ID, PatchInput{Name: &name})
	if err != nil || updated.Name != "Renamed Store" {
		t.Fatalf("patch name: %v %q", err, updated.Name)
	}

	updated, err = catalog.Patch(ctx, scope, st.ID, PatchInput{SetLinked: true, LinkedProvider: nil})
	if err != nil {
		t.Fatal(err)
	}
	if updated.LinkedProvider != nil {
		t.Fatalf("expected unlinked, got %v", updated.LinkedProvider)
	}
	var avail int
	if err := catalog.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM store_availability WHERE household_id = ? AND store_id = ?`,
		scope.HouseholdID(), st.ID).Scan(&avail); err != nil || avail != 0 {
		t.Fatalf("availability after unlink %d err %v", avail, err)
	}

	_, err = catalog.Patch(ctx, scope, "missing-id", PatchInput{Name: &name})
	if err == nil {
		t.Fatal("expected not found")
	}
}

func TestCatalogSetDefaultValidationAndClear(t *testing.T) {
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	st, err := catalog.Create(ctx, scope, CreateInput{Name: "Default Target"})
	if err != nil {
		t.Fatal(err)
	}
	bad := "not-a-store"
	_, err = catalog.SetDefault(ctx, scope, &bad, CheckingContext{})
	if err == nil {
		t.Fatal("expected not found for unknown store id")
	}
	var nf *Error
	if !errors.As(err, &nf) || nf.Code != CodeNotFound {
		t.Fatalf("got %v", err)
	}
	_, err = catalog.SetDefault(ctx, scope, &st.ID, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRow(`
		SELECT value FROM household_settings WHERE household_id = ? AND key = ?`,
		scope.HouseholdID(), settingDefaultStoreID).Scan(&stored); err != nil || stored != st.ID {
		t.Fatalf("default setting %q err %v", stored, err)
	}
	_, err = catalog.SetDefault(ctx, scope, nil, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM household_settings WHERE household_id = ? AND key = ?`,
		scope.HouseholdID(), settingDefaultStoreID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("expected default cleared, count %d err %v", remaining, err)
	}
}

func TestCatalogListDEP7Fields(t *testing.T) {
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	kroger := "kroger"
	loc := "12345678"
	chain := "KROGER"
	_, err := catalog.Create(ctx, scope, CreateInput{
		Name:           "Kroger linked",
		LinkedProvider: &kroger,
		LocationID:     &loc,
		LocationChain:  &chain,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := catalog.List(ctx, scope, CheckingContext{CredentialsConfigured: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.CanConnect {
		t.Fatal("expected canConnect when credentials configured")
	}
	if len(out.Stores) != 1 || out.Stores[0].CheckReason == nil || *out.Stores[0].CheckReason != "not_configured" {
		t.Fatalf("checkReason: %+v", out.Stores[0].CheckReason)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`
		INSERT INTO provider_app_credentials (
			id, user_id, household_id, provider_id, client_id, client_secret, redirect_uri, product_access, product_access_at, updated_at
		) VALUES ('pac-1', 'user-1', ?, 'kroger', 'cid', 'sec', 'https://example.test/cb', 1, ?, ?)`,
		scope.HouseholdID(), when, when)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, granted_scope, updated_at)
		VALUES ('conn-1', 'user-1', ?, 'kroger', 'connected', 'product.compact cart.basic:write', ?)`, scope.HouseholdID(), when)
	if err != nil {
		t.Fatal(err)
	}
	out, err = catalog.List(ctx, scope, CheckingContext{})
	if err != nil || !out.LookupsEnabled {
		t.Fatalf("lookupsEnabled want true got %v err %v", out.LookupsEnabled, err)
	}
}

func TestCatalogSetOrderStalePermutation(t *testing.T) {
	catalog, scope, _ := newTestCatalog(t)
	ctx := context.Background()
	a, err := catalog.Create(ctx, scope, CreateInput{Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := catalog.Create(ctx, scope, CreateInput{Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Create(ctx, scope, CreateInput{Name: "C"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.SetOrder(ctx, scope, []string{b.ID, a.ID}, CheckingContext{})
	if err == nil {
		t.Fatal("expected conflict on stale permutation")
	}
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != CodeConflict {
		t.Fatalf("got %v", err)
	}
	_, err = catalog.SetOrder(ctx, scope, []string{c.ID, a.ID, b.ID}, CheckingContext{})
	if err != nil {
		t.Fatalf("valid reorder: %v", err)
	}
}

func TestCatalogSetOrderForeignID(t *testing.T) {
	catalog, scope, _ := newTestCatalog(t)
	ctx := context.Background()
	if _, err := catalog.Create(ctx, scope, CreateInput{Name: "Only"}); err != nil {
		t.Fatal(err)
	}
	_, err := catalog.SetOrder(ctx, scope, []string{"00000000-0000-0000-0000-000000000099"}, CheckingContext{})
	if err == nil {
		t.Fatal("expected not found")
	}
	var nf *Error
	if !errors.As(err, &nf) || nf.Code != CodeNotFound {
		t.Fatalf("got %v", err)
	}
}

func TestCatalogSetOrderValidation(t *testing.T) {
	catalog, scope, _ := newTestCatalog(t)
	ctx := context.Background()
	a, err := catalog.Create(ctx, scope, CreateInput{Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := catalog.Create(ctx, scope, CreateInput{Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.SetOrder(ctx, scope, []string{}, CheckingContext{})
	if err == nil {
		t.Fatal("expected validation on empty order")
	}
	var ve *Error
	if !errors.As(err, &ve) || ve.Code != CodeInvalid {
		t.Fatalf("got %v", err)
	}
	_, err = catalog.SetOrder(ctx, scope, []string{b.ID}, CheckingContext{})
	if err == nil {
		t.Fatal("expected not found when order omits a store")
	}
	var orderNF *Error
	if !errors.As(err, &orderNF) || orderNF.Code != CodeConflict {
		t.Fatalf("stale partial order want conflict got %v", err)
	}
	out, err := catalog.SetOrder(ctx, scope, []string{b.ID, a.ID}, CheckingContext{})
	if err != nil || len(out.Stores) != 2 || out.Stores[0].ID != b.ID {
		t.Fatalf("order: %v %+v", err, out.Stores)
	}
	_, err = catalog.SetOrder(ctx, scope, []string{a.ID, a.ID}, CheckingContext{})
	if err == nil {
		t.Fatal("expected validation on duplicate store ids")
	}
	if !errors.As(err, &ve) || ve.Code != CodeInvalid {
		t.Fatalf("duplicate ids want invalid got %v", err)
	}
}

func TestCatalogSetOrderEmptyWhenNoStores(t *testing.T) {
	catalog, scope, _ := newTestCatalog(t)
	ctx := context.Background()
	out, err := catalog.SetOrder(ctx, scope, []string{}, CheckingContext{})
	if err != nil || len(out.Stores) != 0 {
		t.Fatalf("empty order with no stores: err=%v stores=%d", err, len(out.Stores))
	}
}

func TestCatalogPatchLocationRequiresLinkedStore(t *testing.T) {
	catalog, scope, _ := newTestCatalog(t)
	ctx := context.Background()
	st, err := catalog.Create(ctx, scope, CreateInput{Name: "Plain"})
	if err != nil {
		t.Fatal(err)
	}
	loc := "12345678"
	chain := "KROGER"
	_, err = catalog.Patch(ctx, scope, st.ID, PatchInput{
		SetLocation: true, LocationID: &loc, LocationChain: &chain,
	})
	if err == nil {
		t.Fatal("expected validation when setting location on unlinked store")
	}
	var ve *Error
	if !errors.As(err, &ve) || ve.Code != CodeInvalid {
		t.Fatalf("got %v", err)
	}
}

func TestCatalogCreateValidationFields(t *testing.T) {
	catalog, scope, _ := newTestCatalog(t)
	ctx := context.Background()
	kroger := "kroger"
	_, err := catalog.Create(ctx, scope, CreateInput{Name: "No loc", LinkedProvider: &kroger})
	if err == nil {
		t.Fatal("expected location validation")
	}
	var ve *Error
	if !errors.As(err, &ve) || ve.Code != CodeInvalid {
		t.Fatalf("got %v", err)
	}
	bad := "target"
	_, err = catalog.Create(ctx, scope, CreateInput{Name: "Bad provider", LinkedProvider: &bad})
	if err == nil {
		t.Fatal("expected linkedProvider validation")
	}
	if !errors.As(err, &ve) || ve.Code != CodeInvalid {
		t.Fatalf("got %v", err)
	}
	online := "online"
	_, err = catalog.Create(ctx, scope, CreateInput{Name: "Web shop", Kind: online})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCatalogCheckReasonVariants(t *testing.T) {
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	kroger := "kroger"
	loc := "12345678"
	chain := "KROGER"
	st, err := catalog.Create(ctx, scope, CreateInput{
		Name: "K", LinkedProvider: &kroger, LocationID: &loc, LocationChain: &chain,
	})
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`
		INSERT INTO provider_app_credentials (
			id, user_id, household_id, provider_id, client_id, client_secret, redirect_uri, product_access, product_access_at, updated_at
		) VALUES ('pac-2', 'user-1', ?, 'kroger', 'cid', 'sec', 'https://example.test/cb', 0, ?, ?)`,
		scope.HouseholdID(), when, when)
	if err != nil {
		t.Fatal(err)
	}
	out, err := catalog.List(ctx, scope, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Stores[0].CheckReason == nil || *out.Stores[0].CheckReason != "not_connected" {
		t.Fatalf("got %v", out.Stores[0].CheckReason)
	}
	_, err = db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, granted_scope, updated_at)
		VALUES ('conn-nc', 'user-1', ?, 'kroger', 'connected', 'cart.basic:write', ?)`, scope.HouseholdID(), when)
	if err != nil {
		t.Fatal(err)
	}
	out, err = catalog.List(ctx, scope, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Stores[0].CheckReason == nil || *out.Stores[0].CheckReason != "no_product_access" {
		t.Fatalf("no_product_access without compact scope: %v", out.Stores[0].CheckReason)
	}
	_, err = db.Exec(`UPDATE provider_connections SET granted_scope = 'cart.basic:write product.compact' WHERE id = 'conn-nc'`)
	if err != nil {
		t.Fatal(err)
	}
	out, err = catalog.List(ctx, scope, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Stores[0].CheckReason == nil || *out.Stores[0].CheckReason != "no_product_access" {
		t.Fatalf("product_access=0 with compact scope: %v", out.Stores[0].CheckReason)
	}
	_, err = db.Exec(`UPDATE provider_connections SET granted_scope = 'cart.basic:write' WHERE id = 'conn-nc'`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE provider_app_credentials SET product_access = 1 WHERE id = 'pac-2'`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE provider_connections SET granted_scope = 'cart.basic:write product.compact' WHERE id = 'conn-nc'`)
	if err != nil {
		t.Fatal(err)
	}
	out, err = catalog.List(ctx, scope, CheckingContext{})
	if err != nil || out.Stores[0].CheckReason != nil {
		t.Fatalf("checkReason want nil got %v err %v", out.Stores[0].CheckReason, err)
	}
	if !out.LookupsEnabled {
		t.Fatalf("lookupsEnabled want true got false")
	}

	// NULL product_access: not not_configured; follows connection then access.
	if _, err := db.Exec(`DELETE FROM provider_connections WHERE id = 'conn-nc'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE provider_app_credentials SET product_access = NULL WHERE id = 'pac-2'`); err != nil {
		t.Fatal(err)
	}
	out, err = catalog.List(ctx, scope, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Stores[0].CheckReason == nil || *out.Stores[0].CheckReason != "not_connected" {
		t.Fatalf("NULL access without connection: %v", out.Stores[0].CheckReason)
	}
	_, err = db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, granted_scope, updated_at)
		VALUES ('conn-reauth', 'user-1', ?, 'kroger', 'reauth_required', 'cart.basic:write', ?)`, scope.HouseholdID(), when)
	if err != nil {
		t.Fatal(err)
	}
	out, err = catalog.List(ctx, scope, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Stores[0].CheckReason == nil || *out.Stores[0].CheckReason != "not_connected" {
		t.Fatalf("NULL access with reauth: %v", out.Stores[0].CheckReason)
	}
	_, err = db.Exec(`UPDATE provider_connections SET state = 'connected' WHERE id = 'conn-reauth'`)
	if err != nil {
		t.Fatal(err)
	}
	out, err = catalog.List(ctx, scope, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Stores[0].CheckReason == nil || *out.Stores[0].CheckReason != "no_product_access" {
		t.Fatalf("NULL access when connected: %v", out.Stores[0].CheckReason)
	}
	_, err = db.Exec(`UPDATE provider_connections SET granted_scope = 'cart.basic:write product.compact' WHERE id = 'conn-reauth'`)
	if err != nil {
		t.Fatal(err)
	}
	out, err = catalog.List(ctx, scope, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Stores[0].CheckReason == nil || *out.Stores[0].CheckReason != "no_product_access" {
		t.Fatalf("NULL product_access with compact on connected row: %v", out.Stores[0].CheckReason)
	}
	if out.LookupsEnabled {
		t.Fatal("lookupsEnabled want false when product_access is NULL")
	}
	_ = st
}

func seedKrogerStoreWithCredentials(t *testing.T, catalog *Catalog, scope tenant.Scope, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	kroger, loc, chain := "kroger", "12345678", "KROGER"
	if _, err := catalog.Create(ctx, scope, CreateInput{Name: "K", LinkedProvider: &kroger, LocationID: &loc, LocationChain: &chain}); err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO provider_app_credentials (
			id, user_id, household_id, provider_id, client_id, client_secret, redirect_uri, product_access, product_access_at, updated_at
		) VALUES ('pac-scope', 'user-1', ?, 'kroger', 'cid', 'sec', 'https://example.test/cb', 1, ?, ?)`,
		scope.HouseholdID(), when, when); err != nil {
		t.Fatal(err)
	}
}

func assertNoProductAccessList(t *testing.T, out StoresResponse) {
	t.Helper()
	if out.LookupsEnabled {
		t.Fatal("lookupsEnabled true, want false")
	}
	if len(out.Stores) == 0 {
		t.Fatal("expected a store in list")
	}
	if out.Stores[0].CheckReason == nil || *out.Stores[0].CheckReason != "no_product_access" {
		t.Fatalf("checkReason = %v, want no_product_access", out.Stores[0].CheckReason)
	}
}

func TestCatalogProductCompactIgnoresNonConnectedRows(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
	}{
		{name: "disconnected", state: "disconnected"},
		{name: "reauth_required", state: "reauth_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, scope, db := newTestCatalog(t)
			ctx := context.Background()
			seedKrogerStoreWithCredentials(t, catalog, scope, db)
			when := time.Now().UTC().Format(time.RFC3339Nano)
			if _, err := db.Exec(`
				INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, granted_scope, updated_at)
				VALUES ('conn-mine', 'user-1', ?, 'kroger', 'connected', 'cart.basic:write', ?)`, scope.HouseholdID(), when); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`
				INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, granted_scope, updated_at)
				VALUES ('conn-other', 'user-2', ?, 'kroger', ?, 'cart.basic:write product.compact', ?)`,
				scope.HouseholdID(), tc.state, when); err != nil {
				t.Fatal(err)
			}
			out, err := catalog.List(ctx, scope, CheckingContext{})
			if err != nil {
				t.Fatal(err)
			}
			assertNoProductAccessList(t, out)
		})
	}
}

func TestCatalogProductCompactIgnoresForeignHousehold(t *testing.T) {
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	seedKrogerStoreWithCredentials(t, catalog, scope, db)
	foreignID := "0190bbbb-cccc-7000-8000-0000000000e5"
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Foreign Scope Q7', '', ?, 'cccccccccccccccccccccccccccccccc', 0)`, foreignID, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, granted_scope, updated_at)
		VALUES ('conn-foreign', 'user-9', ?, 'kroger', 'connected', 'cart.basic:write product.compact', ?)`, foreignID, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, granted_scope, updated_at)
		VALUES ('conn-mine', 'user-1', ?, 'kroger', 'connected', 'cart.basic:write', ?)`, scope.HouseholdID(), when); err != nil {
		t.Fatal(err)
	}
	out, err := catalog.List(ctx, scope, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	assertNoProductAccessList(t, out)
}

func TestCatalogLookupsEnabledDBError(t *testing.T) {
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	seedKrogerStoreWithCredentials(t, catalog, scope, db)
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, granted_scope, updated_at)
		VALUES ('conn-ok', 'user-1', ?, 'kroger', 'connected', 'cart.basic:write product.compact', ?)`, scope.HouseholdID(), when); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := catalog.List(ctx, scope, CheckingContext{})
	if err == nil {
		t.Fatal("expected list error when database is closed")
	}
}

func TestCatalogProductCompactAnyConnectedRow(t *testing.T) {
	t.Run("compactInsertedFirst", func(t *testing.T) {
		assertProductCompactAnyConnected(t, true)
	})
	t.Run("compactInsertedLast", func(t *testing.T) {
		assertProductCompactAnyConnected(t, false)
	})
}

func assertProductCompactAnyConnected(t *testing.T, compactFirst bool) {
	t.Helper()
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	kroger, loc, chain := "kroger", "12345678", "KROGER"
	if _, err := catalog.Create(ctx, scope, CreateInput{Name: "K", LinkedProvider: &kroger, LocationID: &loc, LocationChain: &chain}); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	newer := time.Now().UTC().Format(time.RFC3339Nano)
	compactRow := struct {
		id, scope, when string
	}{"conn-compact", "cart.basic:write product.compact", older}
	plainRow := struct {
		id, scope, when string
	}{"conn-plain", "cart.basic:write", newer}
	if !compactFirst {
		compactRow.when = newer
		plainRow.when = older
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO provider_app_credentials (id, user_id, household_id, provider_id, client_id, client_secret, redirect_uri, product_access, product_access_at, updated_at)
			VALUES ('pac-m', 'user-1', ?, 'kroger', 'cid', 'sec', 'https://example.test/cb', 1, ?, ?)`, []any{scope.HouseholdID(), older, older}},
		{`INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, granted_scope, updated_at)
			VALUES (?, 'user-1', ?, 'kroger', 'connected', ?, ?)`, []any{compactRow.id, scope.HouseholdID(), compactRow.scope, compactRow.when}},
		{`INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, granted_scope, updated_at)
			VALUES (?, 'user-2', ?, 'kroger', 'connected', ?, ?)`, []any{plainRow.id, scope.HouseholdID(), plainRow.scope, plainRow.when}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	out, err := catalog.List(ctx, scope, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Stores[0].CheckReason != nil {
		t.Fatalf("checkReason %q, want nil when any connected row has product.compact", *out.Stores[0].CheckReason)
	}
	if !out.LookupsEnabled {
		t.Fatal("lookupsEnabled want true")
	}

	if _, err := db.Exec(`UPDATE provider_connections SET granted_scope = 'cart.basic:write' WHERE id = ?`, compactRow.id); err != nil {
		t.Fatal(err)
	}
	out, err = catalog.List(ctx, scope, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Stores[0].CheckReason == nil || *out.Stores[0].CheckReason != "no_product_access" {
		t.Fatalf("neither row has compact: %v", out.Stores[0].CheckReason)
	}

	for _, scopeStr := range []string{"cart.basic:write product.compactX", "cart.basic:write xproduct.compact"} {
		if _, err := db.Exec(`UPDATE provider_connections SET granted_scope = ? WHERE id = ?`, scopeStr, compactRow.id); err != nil {
			t.Fatal(err)
		}
		out, err = catalog.List(ctx, scope, CheckingContext{})
		if err != nil {
			t.Fatal(err)
		}
		if out.Stores[0].CheckReason == nil || *out.Stores[0].CheckReason != "no_product_access" {
			t.Fatalf("scope %q must not grant compact: %v", scopeStr, out.Stores[0].CheckReason)
		}
	}
}

func TestCatalogPatchLocationClearsAvailability(t *testing.T) {
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	kroger := "kroger"
	loc := "12345678"
	chain := "KROGER"
	st, err := catalog.Create(ctx, scope, CreateInput{
		Name: "Loc change", LinkedProvider: &kroger, LocationID: &loc, LocationChain: &chain,
	})
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`
		INSERT INTO store_availability (
			household_id, store_id, product_id, location_id, status, checked_at, status_expires_at
		) VALUES (?, ?, 'prod-2', ?, 'carried', ?, ?)`,
		scope.HouseholdID(), st.ID, loc, when, when)
	if err != nil {
		t.Fatal(err)
	}
	newLoc := "87654321"
	_, err = catalog.Patch(ctx, scope, st.ID, PatchInput{
		SetLocation: true, LocationID: &newLoc, LocationChain: &chain,
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM store_availability WHERE household_id = ? AND store_id = ?`,
		scope.HouseholdID(), st.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("availability count %d err %v", n, err)
	}
}

func TestCatalogRemovalPreviewOpenLines(t *testing.T) {
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	first, err := catalog.Create(ctx, scope, CreateInput{Name: "First"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.Create(ctx, scope, CreateInput{Name: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.SetDefault(ctx, scope, &second.ID, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO products (id, name) VALUES ('prod-prev', 'Synthetic')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO items (id, user_id, product_id, household_id)
		VALUES ('item-prev', ?, 'prod-prev', ?)`, scope.HouseholdID(), scope.HouseholdID())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO shopping_list_items (id, household_id, item_id, user_id, quantity, source, created_at, run_store_id)
		VALUES ('line-prev', ?, 'item-prev', 'user-1', 1, 'manual', '2026-01-01T00:00:00Z', ?)`, scope.HouseholdID(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := catalog.RemovalPreview(ctx, scope, second.ID)
	if err != nil || !preview.IsDefault {
		t.Fatalf("preview default: %+v err %v", preview, err)
	}
	preview, err = catalog.RemovalPreview(ctx, scope, first.ID)
	if err != nil || preview.OpenLines != 1 || len(preview.OpenLinesNext) != 1 {
		t.Fatalf("open lines preview: %+v err %v", preview, err)
	}
	if preview.OpenLinesNext[0].StoreID == nil || *preview.OpenLinesNext[0].StoreID != second.ID {
		t.Fatalf("next store: %+v", preview.OpenLinesNext)
	}
}

func TestNameKeyUnicodeCaseFold(t *testing.T) {
	_, keyA, err := NameKey("Straße")
	if err != nil {
		t.Fatal(err)
	}
	_, keyB, err := NameKey("STRASSE")
	if err != nil {
		t.Fatal(err)
	}
	if keyA != keyB {
		t.Fatalf("unicode fold keys %q vs %q", keyA, keyB)
	}
}

func TestCatalogCreateStartsNoCheckJob(t *testing.T) {
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	st, err := catalog.Create(ctx, scope, CreateInput{Name: "No check job"})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM store_checks WHERE household_id = ? AND store_id = ?`,
		scope.HouseholdID(), st.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("store_checks count %d err %v", n, err)
	}
}

func TestCatalogCanConnectSameForOwnerAndMember(t *testing.T) {
	catalog, scope, _ := newTestCatalog(t)
	ctx := context.Background()
	chk := CheckingContext{CredentialsConfigured: true}
	out, err := catalog.List(ctx, scope, chk)
	if err != nil || !out.CanConnect {
		t.Fatalf("canConnect=%v err=%v", out.CanConnect, err)
	}
}

func TestCatalogCreateDuplicateName(t *testing.T) {
	catalog, scope, _ := newTestCatalog(t)
	ctx := context.Background()
	if _, err := catalog.Create(ctx, scope, CreateInput{Name: "Market"}); err != nil {
		t.Fatal(err)
	}
	_, err := catalog.Create(ctx, scope, CreateInput{Name: "  market "})
	if err == nil {
		t.Fatal("expected name taken")
	}
	var se *Error
	if !errors.As(err, &se) || se.Code != CodeNameTaken {
		t.Fatalf("got %v", err)
	}
}

func TestCatalogDeleteReordersRemaining(t *testing.T) {
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	first, err := catalog.Create(ctx, scope, CreateInput{Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.Create(ctx, scope, CreateInput{Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	third, err := catalog.Create(ctx, scope, CreateInput{Name: "C"})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Delete(ctx, scope, second.ID); err != nil {
		t.Fatal(err)
	}
	out, err := catalog.List(ctx, scope, CheckingContext{})
	if err != nil || len(out.Stores) != 2 {
		t.Fatalf("list len %d err %v", len(out.Stores), err)
	}
	if out.Stores[0].ID != first.ID || out.Stores[1].ID != third.ID {
		t.Fatalf("order: %+v", out.Stores)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stores WHERE id = ?`, second.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("row count %d err %v", n, err)
	}
}

func TestLookupsDisabledWhenKrogerOff(t *testing.T) {
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	kroger := "kroger"
	loc := "12345678"
	chain := "KROGER"
	_, err := catalog.Create(ctx, scope, CreateInput{
		Name: "K", LinkedProvider: &kroger, LocationID: &loc, LocationChain: &chain,
	})
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`
		INSERT INTO provider_app_credentials (
			id, user_id, household_id, provider_id, client_id, client_secret, redirect_uri, product_access, product_access_at, updated_at
		) VALUES ('pac-off', 'user-1', ?, 'kroger', 'cid', 'sec', 'https://example.test/cb', 1, ?, ?)`,
		scope.HouseholdID(), when, when)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, granted_scope, updated_at)
		VALUES ('conn-off', 'user-1', ?, 'kroger', 'connected', 'product.compact', ?)`, scope.HouseholdID(), when)
	if err != nil {
		t.Fatal(err)
	}
	out, err := catalog.List(ctx, scope, CheckingContext{KrogerDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.LookupsEnabled || out.CanConnect {
		t.Fatalf("lookups=%v canConnect=%v", out.LookupsEnabled, out.CanConnect)
	}
	if len(out.Stores) != 1 {
		t.Fatalf("stores: %+v", out.Stores)
	}
	if out.Stores[0].CheckReason != nil || out.Stores[0].Check != nil {
		t.Fatalf("linked store should omit checking fields when Kroger disabled: %+v", out.Stores[0])
	}
}

func TestCatalogListIncludesStoreCheck(t *testing.T) {
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	st, err := catalog.Create(ctx, scope, CreateInput{Name: "Checked"})
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`
		INSERT INTO store_checks (
			household_id, store_id, state, trigger, started_at, finished_at, lease_until,
			checked_count, found_count, total_count, failure
		) VALUES (?, ?, 'done', 'manual', ?, ?, ?, 3, 2, 3, NULL)`,
		scope.HouseholdID(), st.ID, when, when, when)
	if err != nil {
		t.Fatal(err)
	}
	out, err := catalog.List(ctx, scope, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Stores[0].Check == nil || out.Stores[0].Check.State != "done" {
		t.Fatalf("check: %+v", out.Stores[0].Check)
	}
}

func TestCatalogRemovalPreviewUsualAndExcluded(t *testing.T) {
	catalog, scope, db := newTestCatalog(t)
	ctx := context.Background()
	defaultStore, err := catalog.Create(ctx, scope, CreateInput{Name: "Default shop"})
	if err != nil {
		t.Fatal(err)
	}
	removeStore, err := catalog.Create(ctx, scope, CreateInput{Name: "Usual shop"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.Create(ctx, scope, CreateInput{Name: "Fallback"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.SetDefault(ctx, scope, &defaultStore.ID, CheckingContext{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO products (id, name) VALUES ('prod-usual', 'Item')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO product_stores (household_id, product_id, store_id, state, is_usual, set_by_user_id, set_at)
		VALUES (?, 'prod-usual', ?, 'carries', 1, 'user-1', '2026-01-01T00:00:00Z')`, scope.HouseholdID(), removeStore.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO product_stores (household_id, product_id, store_id, state, is_usual, set_by_user_id, set_at)
		VALUES (?, 'prod-usual', ?, 'excluded', 0, 'user-1', '2026-01-01T00:00:00Z')`, scope.HouseholdID(), defaultStore.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO items (id, user_id, product_id, household_id)
		VALUES ('item-usual', ?, 'prod-usual', ?)`, scope.HouseholdID(), scope.HouseholdID())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO shopping_list_items (id, household_id, item_id, user_id, quantity, source, created_at, run_store_id)
		VALUES ('line-usual', ?, 'item-usual', 'user-1', 1, 'manual', '2026-01-01T00:00:00Z', NULL)`, scope.HouseholdID())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := catalog.RemovalPreview(ctx, scope, removeStore.ID)
	if err != nil || preview.OpenLines != 1 {
		t.Fatalf("preview: %+v err %v", preview, err)
	}
	if len(preview.OpenLinesNext) != 1 || preview.OpenLinesNext[0].StoreID != nil {
		t.Fatalf("next after usual removed: %+v", preview.OpenLinesNext)
	}
}
