package app_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/product"
)

// TestMigrationApplies verifies that RunMigrations applies the initial schema
// to an in-memory SQLite database and that every expected table is created.
func TestMigrationApplies(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	defer conn.Close()

	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Query sqlite_master for user-created tables (exclude the internal
	// schema_migrations tracking table).
	rows, err := conn.Query(`
		SELECT name FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name != 'schema_migrations'
		ORDER BY name`)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}

	want := []string{
		"app_settings",
		"audit_log",
		"barcode_misses",
		"barcodes",
		"brand_preferences",
		"categories",
		"category_suggestion_dismissals",
		"category_targets",
		"change_versions",
		"consumption_events",
		"device_tokens",
		"devices",
		"fulfillment_ledger",
		"group_suggestion_dismissals",
		"group_suggestion_members",
		"group_suggestions",
		"history_repair",
		"household_password",
		"household_product_overrides",
		"household_settings",
		"households",
		"invites",
		"item_categories",
		"item_deals",
		"item_instances",
		"item_targets",
		"items",
		"memberships",
		"operator_alerts",
		"pairing_codes",
		"product_contributions",
		"product_off_tags",
		"product_group_members",
		"product_groups",
		"products",
		"provider_app_credentials",
		"provider_connections",
		"push_subscriptions",
		"reset_links",
		"scan_alert_schedule",
		"scan_entries",
		"sessions",
		"setup_codes",
		"shopping_list_entry_adjustments",
		"shopping_list_items",
		"staged_cart_skips",
		"stock_in_events",
		"stock_moves",
		"supply_overrides",
		"users",
	}
	sort.Strings(want) // already sorted, but be explicit

	if len(got) != len(want) {
		t.Fatalf("expected %d tables, got %d: %v", len(want), len(got), got)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("table[%d]: want %q, got %q", i, name, got[i])
		}
	}
}

// TestMigrationIsIdempotent verifies that calling RunMigrations twice on the
// same database does not return an error or duplicate any tables.
func TestMigrationIsIdempotent(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	defer conn.Close()

	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("second RunMigrations (idempotency check): %v", err)
	}

	// One schema_migrations row per applied .sql file; the second RunMigrations
	// must not re-apply any file, so the count equals the number of migration
	// files (001 through 005, both 006 files, both 007 files, 008 through 020,
	// both 021 files, 022, 022a through 022f, 023, 024, 025, 026, and 027).
	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 37 {
		t.Errorf("schema_migrations should have 37 rows after two runs, got %d", count)
	}
}

// Feature: external-product-persistence, Property 4: Data Repair — Pre-existing Orphans Become Visible
//
// For any items row whose product_id has no products row at the time the repair
// runs, migration 002 SHALL insert a backing products row (placeholder name) so
// the item references an existing product, and re-running RunMigrations SHALL stay
// idempotent (no duplicate products rows, unchanged schema_migrations count).
//
// This exercises logic not reachable through the HTTP API: the backfill is a
// one-time startup repair against orphans that already exist in the DB. The
// migration runs once at construction against an empty DB, so to simulate the
// repair against a pre-existing orphan we seed the orphan, clear the 002 record,
// and re-run — mirroring the pattern in
// internal/server/handler_external_persistence_test.go's orphan case.
func TestMigration002BackfillsOrphanedProducts(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	defer conn.Close()

	// Create the schema (and record 002 as applied against the empty DB).
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations (create schema): %v", err)
	}

	const orphanProductID = "011110728227" // barcode with no products row

	// Seed an orphaned items row whose product_id has no matching products row.
	if _, err := conn.Exec(
		`INSERT INTO items (id, user_id, product_id) VALUES ('orphan-item', 'user-1', ?)`,
		orphanProductID,
	); err != nil {
		t.Fatalf("seed orphan item: %v", err)
	}

	// Clear the 002 record so RunMigrations re-applies the backfill against the
	// now-seeded orphan (simulating startup repair with a pre-existing orphan).
	if _, err := conn.Exec(
		`DELETE FROM schema_migrations WHERE filename = '002_backfill_orphaned_products.sql'`,
	); err != nil {
		t.Fatalf("reset migration 002 record: %v", err)
	}

	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations (repair against orphan): %v", err)
	}

	// The backfilled products row exists with the placeholder name.
	var name string
	if err := conn.QueryRow(`SELECT name FROM products WHERE id = ?`, orphanProductID).Scan(&name); err != nil {
		t.Fatalf("expected backfilled products row for %q: %v", orphanProductID, err)
	}
	if want := "Product " + orphanProductID; name != want {
		t.Errorf("backfilled product name: want %q, got %q", want, name)
	}

	// Capture state before re-running to assert idempotency.
	migrationsBefore := countMigrations(t, conn)
	productsBefore := countProducts(t, conn, orphanProductID)

	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations (idempotent re-run): %v", err)
	}

	if got := countMigrations(t, conn); got != migrationsBefore {
		t.Errorf("schema_migrations count changed on re-run: before %d, after %d", migrationsBefore, got)
	}
	if got := countProducts(t, conn, orphanProductID); got != productsBefore {
		t.Errorf("duplicate products row created on re-run: before %d, after %d", productsBefore, got)
	}
	if got := countProducts(t, conn, orphanProductID); got != 1 {
		t.Errorf("products rows for %q: want 1, got %d", orphanProductID, got)
	}
}

func countMigrations(t *testing.T, conn *sql.DB) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	return count
}

func countProducts(t *testing.T, conn *sql.DB, id string) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM products WHERE id = ?`, id).Scan(&count); err != nil {
		t.Fatalf("count products: %v", err)
	}
	return count
}

// Feature: product-cache-freshness, Property 1: Migration 004 backfills provenance without disturbing data
//
// For any products row that exists before migration 004 runs, THE Migration_Runner
// SHALL set source to 'external', refreshed_at to NULL, and name_overridden to false
// for that row, and SHALL leave that row's id, name, category, unit_of_measure,
// image_url, and created_at unchanged. Re-running RunMigrations SHALL be a no-op.
//
// Validates: Requirements 1.1, 1.2, 1.3, 1.4
func TestMigration004BackfillsProvenanceAndIsIdempotent(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	defer conn.Close()

	applyMigrationsThrough003(t, conn)

	// Seed products rows directly against the pre-004 schema (no source,
	// refreshed_at, or name_overridden columns yet).
	type seedRow struct {
		id, name, category, unitOfMeasure, imageURL string
		createdAt                                   time.Time
	}
	seeds := []seedRow{
		{
			id: "011110728227", name: "Whole Milk", category: "Dairy", unitOfMeasure: "gallon",
			imageURL: "https://images.openfoodfacts.org/milk.jpg", createdAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		},
		{
			id: "some-uuid", name: "Homemade Jam", category: "", unitOfMeasure: "",
			imageURL: "", createdAt: time.Date(2023, 6, 7, 8, 9, 10, 0, time.UTC),
		},
	}
	for _, s := range seeds {
		if _, err := conn.Exec(
			`INSERT INTO products (id, name, category, unit_of_measure, image_url, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			s.id, s.name, nullableSeed(s.category), nullableSeed(s.unitOfMeasure), nullableSeed(s.imageURL), s.createdAt,
		); err != nil {
			t.Fatalf("seed product %q: %v", s.id, err)
		}
	}

	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations (apply 004): %v", err)
	}

	for _, s := range seeds {
		var (
			gotName, gotCategory, gotUnit, gotImage, gotSource string
			gotCreatedAt                                       time.Time
			gotRefreshedAt                                     sql.NullTime
			gotNameOverridden                                  bool
		)
		if err := conn.QueryRow(
			`SELECT name, COALESCE(category, ''), COALESCE(unit_of_measure, ''), COALESCE(image_url, ''),
			        created_at, source, refreshed_at, name_overridden
			 FROM products WHERE id = ?`, s.id,
		).Scan(&gotName, &gotCategory, &gotUnit, &gotImage, &gotCreatedAt, &gotSource, &gotRefreshedAt, &gotNameOverridden); err != nil {
			t.Fatalf("read back product %q: %v", s.id, err)
		}

		if gotName != s.name {
			t.Errorf("product %q name: want %q, got %q", s.id, s.name, gotName)
		}
		if gotCategory != s.category {
			t.Errorf("product %q category: want %q, got %q", s.id, s.category, gotCategory)
		}
		if gotUnit != s.unitOfMeasure {
			t.Errorf("product %q unit_of_measure: want %q, got %q", s.id, s.unitOfMeasure, gotUnit)
		}
		if gotImage != s.imageURL {
			t.Errorf("product %q image_url: want %q, got %q", s.id, s.imageURL, gotImage)
		}
		if !gotCreatedAt.Equal(s.createdAt) {
			t.Errorf("product %q created_at: want %v, got %v", s.id, s.createdAt, gotCreatedAt)
		}
		if gotSource != "external" {
			t.Errorf("product %q source: want %q, got %q", s.id, "external", gotSource)
		}
		if gotRefreshedAt.Valid {
			t.Errorf("product %q refreshed_at: want NULL, got %v", s.id, gotRefreshedAt.Time)
		}
		if gotNameOverridden {
			t.Errorf("product %q name_overridden: want false, got true", s.id)
		}
	}

	// A second RunMigrations must be a no-op: schema_migrations count is stable.
	migrationsBefore := countMigrations(t, conn)
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations (idempotent re-run): %v", err)
	}
	if got := countMigrations(t, conn); got != migrationsBefore {
		t.Errorf("schema_migrations count changed on re-run: before %d, after %d", migrationsBefore, got)
	}
}

// applyMigrationsThrough003 builds a database migrated only through
// 003_add_product_image_url.sql, by applying migrations 001-003 directly from
// disk and recording each in schema_migrations. This simulates a pre-004
// database so TestMigration004BackfillsProvenanceAndIsIdempotent can seed rows
// against the old schema before RunMigrations applies 004.
func applyMigrationsThrough003(t *testing.T, conn *sql.DB) {
	t.Helper()

	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_migrations table: %v", err)
	}

	for _, name := range []string{
		"001_initial_schema.sql",
		"002_backfill_orphaned_products.sql",
		"003_add_product_image_url.sql",
	} {
		sqlBytes, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatalf("read migration %q: %v", name, err)
		}
		if _, err := conn.Exec(string(sqlBytes)); err != nil {
			t.Fatalf("apply migration %q: %v", name, err)
		}
		if _, err := conn.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name); err != nil {
			t.Fatalf("record migration %q: %v", name, err)
		}
	}
}

// Feature: open-products-facts-lookup, Property 1: Migration 005 preserves values and adds provenance
//
// WHEN migration 005 is applied to a database containing products rows with non-default
// source, refreshed_at, and name_overridden values, THE Migration_Runner SHALL retain
// those values unchanged and set external_source to NULL for every pre-existing row.
// THE barcode_misses table SHALL exist and be queryable after migration. A second
// RunMigrations call SHALL remain idempotent (schema_migrations count unchanged).
//
// Validates: Requirements 3.2, 3.3
func TestMigration005PreservesValuesAndAddsProvenanceAndIsIdempotent(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	defer conn.Close()

	applyMigrationsThrough004(t, conn)

	// Seed products rows with non-default source, refreshed_at, and name_overridden values.
	type seedRow struct {
		id             string
		name           string
		source         string
		refreshedAt    *time.Time
		nameOverridden bool
	}
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	seeds := []seedRow{
		{
			id:             "011110728227",
			name:           "Whole Milk",
			source:         "external",
			refreshedAt:    &now,
			nameOverridden: true,
		},
		{
			id:             "some-uuid",
			name:           "Homemade Jam",
			source:         "external",
			refreshedAt:    &now,
			nameOverridden: false,
		},
		{
			id:             "user-product",
			name:           "User Created",
			source:         "user",
			refreshedAt:    nil,
			nameOverridden: false,
		},
	}

	for _, s := range seeds {
		if _, err := conn.Exec(
			`INSERT INTO products (id, name, source, refreshed_at, name_overridden, created_at) 
			 VALUES (?, ?, ?, ?, ?, ?)`,
			s.id, s.name, s.source, s.refreshedAt, s.nameOverridden, time.Now(),
		); err != nil {
			t.Fatalf("seed product %q: %v", s.id, err)
		}
	}

	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations (apply 005): %v", err)
	}

	// Assert barcode_misses table exists and is queryable
	var tableCount int
	if err := conn.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master 
		WHERE type = 'table' AND name = 'barcode_misses'`).Scan(&tableCount); err != nil {
		t.Fatalf("check barcode_misses table: %v", err)
	}
	if tableCount != 1 {
		t.Errorf("barcode_misses table: want 1, got %d", tableCount)
	}

	// Verify we can query the barcode_misses table
	if _, err := conn.Exec(`SELECT COUNT(*) FROM barcode_misses`); err != nil {
		t.Fatalf("query barcode_misses table: %v", err)
	}

	// Assert pre-existing rows have unchanged source, refreshed_at, name_overridden, and NULL external_source
	for _, s := range seeds {
		var (
			gotSource         string
			gotRefreshedAt    sql.NullTime
			gotNameOverridden bool
			gotExternalSource sql.NullString
		)
		if err := conn.QueryRow(
			`SELECT source, refreshed_at, name_overridden, external_source
			 FROM products WHERE id = ?`, s.id,
		).Scan(&gotSource, &gotRefreshedAt, &gotNameOverridden, &gotExternalSource); err != nil {
			t.Fatalf("read back product %q: %v", s.id, err)
		}

		if gotSource != s.source {
			t.Errorf("product %q source: want %q, got %q", s.id, s.source, gotSource)
		}

		if s.refreshedAt == nil {
			if gotRefreshedAt.Valid {
				t.Errorf("product %q refreshed_at: want NULL, got %v", s.id, gotRefreshedAt.Time)
			}
		} else {
			if !gotRefreshedAt.Valid {
				t.Errorf("product %q refreshed_at: want %v, got NULL", s.id, s.refreshedAt)
			} else if !gotRefreshedAt.Time.Equal(*s.refreshedAt) {
				t.Errorf("product %q refreshed_at: want %v, got %v", s.id, s.refreshedAt, gotRefreshedAt.Time)
			}
		}

		if gotNameOverridden != s.nameOverridden {
			t.Errorf("product %q name_overridden: want %v, got %v", s.id, s.nameOverridden, gotNameOverridden)
		}

		// external_source must be NULL for all pre-existing rows
		if gotExternalSource.Valid {
			t.Errorf("product %q external_source: want NULL, got %q", s.id, gotExternalSource.String)
		}
	}

	// A second RunMigrations must be a no-op: schema_migrations count is stable.
	migrationsBefore := countMigrations(t, conn)
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations (idempotent re-run): %v", err)
	}
	if got := countMigrations(t, conn); got != migrationsBefore {
		t.Errorf("schema_migrations count changed on re-run: before %d, after %d", migrationsBefore, got)
	}
}

// applyMigrationsThrough004 builds a database migrated only through
// 004_add_product_freshness.sql, by applying migrations 001-004 directly from
// disk and recording each in schema_migrations. This simulates a pre-005
// database so TestMigration005PreservesValuesAndAddsProvenanceAndIsIdempotent
// can seed rows against the pre-005 schema before RunMigrations applies 005.
func applyMigrationsThrough004(t *testing.T, conn *sql.DB) {
	t.Helper()

	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_migrations table: %v", err)
	}

	for _, name := range []string{
		"001_initial_schema.sql",
		"002_backfill_orphaned_products.sql",
		"003_add_product_image_url.sql",
		"004_add_product_freshness.sql",
	} {
		sqlBytes, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatalf("read migration %q: %v", name, err)
		}
		if _, err := conn.Exec(string(sqlBytes)); err != nil {
			t.Fatalf("apply migration %q: %v", name, err)
		}
		if _, err := conn.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name); err != nil {
			t.Fatalf("record migration %q: %v", name, err)
		}
	}
}

// applyMigrationsThrough005 builds a database migrated only through
// 005_add_external_source_and_barcode_misses.sql, by applying migrations 001-005
// directly from disk and recording each in schema_migrations. This simulates a
// pre-006 database so tests can seed rows against the pre-006 schema before
// RunMigrations applies 006.
func applyMigrationsThrough005(t *testing.T, conn *sql.DB) {
	t.Helper()

	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_migrations table: %v", err)
	}

	for _, name := range []string{
		"001_initial_schema.sql",
		"002_backfill_orphaned_products.sql",
		"003_add_product_image_url.sql",
		"004_add_product_freshness.sql",
		"005_add_external_source_and_barcode_misses.sql",
	} {
		sqlBytes, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatalf("read migration %q: %v", name, err)
		}
		if _, err := conn.Exec(string(sqlBytes)); err != nil {
			t.Fatalf("apply migration %q: %v", name, err)
		}
		if _, err := conn.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name); err != nil {
			t.Fatalf("record migration %q: %v", name, err)
		}
	}
}

// nullableSeed converts an empty string to nil so seeded optional columns are
// stored as NULL rather than empty string, matching product.Catalog's convention.
func nullableSeed(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Feature: grocery-cart-integration, Property 1: Migration 006 drops cart_integrations and adds new tables
//
// Migration 006 replaces cart_integrations with provider_connections, fulfillment_ledger,
// and shopping_list_entry_adjustments tables. It also adds replenishment_mode to items.
//
// Validates: Requirements 2.2, 6.2, 8.2, 10.12
func TestMigration006ReplacesCartIntegrationsWithProviderLedger(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	defer conn.Close()

	applyMigrationsThrough005(t, conn)

	// Verify cart_integrations exists before migration
	var cartIntegrationsCount int
	if err := conn.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master 
		WHERE type = 'table' AND name = 'cart_integrations'`).Scan(&cartIntegrationsCount); err != nil {
		t.Fatalf("check cart_integrations table: %v", err)
	}
	if cartIntegrationsCount != 1 {
		t.Errorf("cart_integrations table before migration: want 1, got %d", cartIntegrationsCount)
	}

	// Verify provider_connections, fulfillment_ledger, shopping_list_entry_adjustments don't exist yet
	for _, tableName := range []string{"provider_connections", "fulfillment_ledger", "shopping_list_entry_adjustments"} {
		var count int
		if err := conn.QueryRow(`
			SELECT COUNT(*) FROM sqlite_master 
			WHERE type = 'table' AND name = ?`, tableName).Scan(&count); err != nil {
			t.Fatalf("check %s table: %v", tableName, err)
		}
		if count != 0 {
			t.Errorf("%s table should not exist before migration 006", tableName)
		}
	}

	// Verify items doesn't have replenishment_mode yet
	var hasReplenishmentMode bool
	if err := conn.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('items') WHERE name = 'replenishment_mode'`).Scan(&hasReplenishmentMode); err != nil {
		t.Fatalf("check replenishment_mode column: %v", err)
	}
	if hasReplenishmentMode {
		t.Errorf("replenishment_mode column should not exist before migration 006")
	}

	// Apply migration 006
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations (apply 006): %v", err)
	}

	// Verify cart_integrations no longer exists
	if err := conn.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master 
		WHERE type = 'table' AND name = 'cart_integrations'`).Scan(&cartIntegrationsCount); err != nil {
		t.Fatalf("check cart_integrations table: %v", err)
	}
	if cartIntegrationsCount != 0 {
		t.Errorf("cart_integrations table should not exist after migration 006")
	}

	// Verify new tables exist
	for _, tableName := range []string{"provider_connections", "fulfillment_ledger", "shopping_list_entry_adjustments"} {
		var count int
		if err := conn.QueryRow(`
			SELECT COUNT(*) FROM sqlite_master 
			WHERE type = 'table' AND name = ?`, tableName).Scan(&count); err != nil {
			t.Fatalf("check %s table: %v", tableName, err)
		}
		if count != 1 {
			t.Errorf("%s table should exist after migration 006", tableName)
		}
	}

	// Verify items has replenishment_mode
	if err := conn.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('items') WHERE name = 'replenishment_mode'`).Scan(&hasReplenishmentMode); err != nil {
		t.Fatalf("check replenishment_mode column: %v", err)
	}
	if !hasReplenishmentMode {
		t.Errorf("replenishment_mode column should exist after migration 006")
	}

	// Verify replenishment_mode has the correct default
	var defaultVal string
	if err := conn.QueryRow(`
		SELECT sql FROM sqlite_master 
		WHERE type = 'table' AND name = 'items'`).Scan(&defaultVal); err != nil {
		t.Fatalf("get items table SQL: %v", err)
	}
	if !strings.Contains(defaultVal, "replenishment_mode TEXT NOT NULL DEFAULT 'target'") {
		t.Errorf("replenishment_mode should have DEFAULT 'target': got %s", defaultVal)
	}

	// Verify idx_consumption_events_item_consumed index exists
	var indexCount int
	if err := conn.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master 
		WHERE type = 'index' AND name = 'idx_consumption_events_item_consumed'`).Scan(&indexCount); err != nil {
		t.Fatalf("check index: %v", err)
	}
	if indexCount != 1 {
		t.Errorf("idx_consumption_events_item_consumed index should exist")
	}

	// A second RunMigrations must be a no-op: schema_migrations count is stable.
	migrationsBefore := countMigrations(t, conn)
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations (idempotent re-run): %v", err)
	}
	if got := countMigrations(t, conn); got != migrationsBefore {
		t.Errorf("schema_migrations count changed on re-run: before %d, after %d", migrationsBefore, got)
	}
}

// Feature: grocery-cart-integration, Property 2: Migration 006 preserves existing items data
//
// WHEN migration 006 is applied to a database containing items rows, THE Migration_Runner
// SHALL leave all items columns unchanged and set replenishment_mode to 'target' for every row.
//
// Validates: Requirements 6.1, 6.2, 6.3, 6.4, 6.5
func TestMigration006PreservesItemsDataAndSetsDefaultMode(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	defer conn.Close()

	applyMigrationsThrough005(t, conn)

	// Seed items rows with various configurations
	type seedRow struct {
		id, userID, productID string
		targetQuantity        *int
		createdAt             time.Time
	}
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	seeds := []seedRow{
		{id: "item-1", userID: "user-1", productID: "011110728227", targetQuantity: intPtr(5), createdAt: now},
		{id: "item-2", userID: "user-1", productID: "some-uuid", targetQuantity: nil, createdAt: now.Add(time.Hour)},
		{id: "item-3", userID: "user-1", productID: "another-uuid", targetQuantity: intPtr(3), createdAt: now.Add(2 * time.Hour)},
	}
	for _, s := range seeds {
		if _, err := conn.Exec(
			`INSERT INTO items (id, user_id, product_id, target_quantity, created_at) VALUES (?, ?, ?, ?, ?)`,
			s.id, s.userID, s.productID, s.targetQuantity, s.createdAt,
		); err != nil {
			t.Fatalf("seed item %q: %v", s.id, err)
		}
	}

	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations (apply 006): %v", err)
	}

	// Verify all items have replenishment_mode = 'target' and original data preserved
	for _, s := range seeds {
		var (
			gotUserID, gotProductID string
			gotTargetQuantity       *int
			gotMode                 string
			gotCreatedAt            time.Time
		)
		if err := conn.QueryRow(
			`SELECT user_id, product_id, target_quantity, replenishment_mode, created_at
			 FROM items WHERE id = ?`, s.id,
		).Scan(&gotUserID, &gotProductID, &gotTargetQuantity, &gotMode, &gotCreatedAt); err != nil {
			t.Fatalf("read back item %q: %v", s.id, err)
		}

		if gotUserID != s.userID {
			t.Errorf("item %q user_id: want %q, got %q", s.id, s.userID, gotUserID)
		}
		if gotProductID != s.productID {
			t.Errorf("item %q product_id: want %q, got %q", s.id, s.productID, gotProductID)
		}
		if !targetQuantityEqual(s.targetQuantity, gotTargetQuantity) {
			t.Errorf("item %q target_quantity: want %v, got %v", s.id, s.targetQuantity, gotTargetQuantity)
		}
		if gotMode != "target" {
			t.Errorf("item %q replenishment_mode: want 'target', got %q", s.id, gotMode)
		}
		if !gotCreatedAt.Equal(s.createdAt) {
			t.Errorf("item %q created_at: want %v, got %v", s.id, s.createdAt, gotCreatedAt)
		}
	}

	// A second RunMigrations must be a no-op: schema_migrations count is stable.
	migrationsBefore := countMigrations(t, conn)
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations (idempotent re-run): %v", err)
	}
	if got := countMigrations(t, conn); got != migrationsBefore {
		t.Errorf("schema_migrations count changed on re-run: before %d, after %d", migrationsBefore, got)
	}
}

func TestMigration010NetSizeColumnsAndBackfill(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	for _, column := range []string{"net_base_value", "net_dimension", "net_size_origin", "pack_count"} {
		var count int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('products') WHERE name = ?`, column).Scan(&count); err != nil {
			t.Fatalf("pragma %s: %v", column, err)
		}
		if count != 1 {
			t.Errorf("products.%s missing", column)
		}
	}
	// Recording the file keeps a second RunMigrations from adding the columns again.
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations again: %v", err)
	}

	if _, err := conn.Exec(`INSERT INTO products (id, name, unit_of_measure) VALUES
		('sized', 'Great Value Cut Green Beans 14.5 oz', 'can'),
		('cleared', 'Kroger Cut Green Beans 14.5 oz', 'can'),
		('pack', 'Store Brand Seltzer 6 x 12 fl oz', 'pack'),
		('plain', 'Oats', 'each')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := conn.Exec(`UPDATE products SET net_size_origin = 'manual' WHERE id = 'cleared'`); err != nil {
		t.Fatalf("mark cleared: %v", err)
	}

	filled, err := product.BackfillNetSizes(context.Background(), conn)
	if err != nil {
		t.Fatalf("BackfillNetSizes: %v", err)
	}
	if filled != 2 {
		t.Fatalf("filled rows: want 2, got %d", filled)
	}

	var origin string
	var base float64
	var dimension string
	if err := conn.QueryRow(`SELECT net_size_origin, net_base_value, net_dimension FROM products WHERE id = 'sized'`).Scan(&origin, &base, &dimension); err != nil {
		t.Fatalf("read sized: %v", err)
	}
	if origin != product.OriginBackfill || dimension != product.DimensionMass {
		t.Fatalf("sized origin/dimension = %s/%s", origin, dimension)
	}
	amount, unit, ok := product.DisplayNetSize(base, dimension)
	if !ok || unit != "oz" || amount != 14.5 {
		t.Fatalf("sized display = %v %s ok=%v", amount, unit, ok)
	}

	var clearedOrigin string
	var clearedBase sql.NullFloat64
	if err := conn.QueryRow(`SELECT net_size_origin, net_base_value FROM products WHERE id = 'cleared'`).Scan(&clearedOrigin, &clearedBase); err != nil {
		t.Fatalf("read cleared: %v", err)
	}
	if clearedOrigin != product.OriginManual || clearedBase.Valid {
		t.Fatalf("cleared row was overwritten: origin %s base valid %v", clearedOrigin, clearedBase.Valid)
	}

	var pack int
	var packDim string
	if err := conn.QueryRow(`SELECT pack_count, net_dimension FROM products WHERE id = 'pack'`).Scan(&pack, &packDim); err != nil {
		t.Fatalf("read pack: %v", err)
	}
	if pack != 6 || packDim != product.DimensionVolume {
		t.Fatalf("pack = %d dimension %s", pack, packDim)
	}

	if _, err := conn.Exec(`UPDATE products SET name = 'Renamed 29 oz' WHERE id = 'sized'`); err != nil {
		t.Fatalf("rename: %v", err)
	}
	again, err := product.BackfillNetSizes(context.Background(), conn)
	if err != nil {
		t.Fatalf("second BackfillNetSizes: %v", err)
	}
	if again != 0 {
		t.Fatalf("second backfill wrote %d rows", again)
	}
	var still float64
	if err := conn.QueryRow(`SELECT net_base_value FROM products WHERE id = 'sized'`).Scan(&still); err != nil {
		t.Fatalf("reread sized: %v", err)
	}
	if still != base {
		t.Fatalf("second backfill changed the size from %v to %v", base, still)
	}
}

func TestMigration011ProductGroups(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO product_groups (id, user_id, name, name_key, window_months) VALUES ('g', 'group-owner', 'Beans', 'beans', 3)`); err != nil {
		t.Fatalf("insert window: %v", err)
	}
	if _, err := conn.Exec(`UPDATE product_groups SET quantity_base_value = 10, quantity_dimension = 'mass' WHERE id = 'g'`); err == nil {
		t.Fatal("window and quantity were both stored")
	}
	if _, err := conn.Exec(`INSERT INTO group_suggestion_dismissals (user_id, product_id_a, product_id_b) VALUES ('user-1', 'b', 'a')`); err == nil {
		t.Fatal("dismissal stored the higher id first")
	}
}

func intPtr(i int) *int {
	return &i
}

func targetQuantityEqual(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
