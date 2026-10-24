package app

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// beforeHouseholdMigration runs after the numbered migrations and before
// the household steps, outside their transaction. Tests seed a synthetic
// pantry here. The household steps do not copy the database file.
var beforeHouseholdMigration = func(*sql.DB) error { return nil }

// isHouseholdMigration reports the stage-1 household steps. 022_ sorts
// before 022a, so the unshelve backfill stays a normal migration.
func isHouseholdMigration(name string) bool {
	base := strings.TrimSuffix(name, ".sql")
	if len(base) < 4 || !strings.HasPrefix(base, "022") {
		return false
	}
	letter := base[3]
	if letter < 'a' || letter > 'f' {
		return false
	}
	return len(base) == 4 || base[4] == '_'
}

func householdMigrationFiles() []string {
	return []string{
		"022a_household_tables.sql",
		"022b_household_columns.sql",
		"022c_household_backfill.sql",
		"022d_household_literals.sql",
		"022e_household_device.sql",
		"022f_drop_triggers.sql",
	}
}

// applyHouseholdMigrations runs 022a through 022f in one transaction.
// A failure rolls the schema and the schema_migrations rows back together.
// There is no copy of the pantry file beside the database; the copy taken
// before deploy is the backup for this change.
func applyHouseholdMigrations(db *sql.DB) (int, error) {
	files := householdMigrationFiles()
	pending := 0
	for _, name := range files {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, name).Scan(&count); err != nil {
			return 0, fmt.Errorf("check migration %q: %w", name, err)
		}
		if count == 0 {
			pending++
		}
	}
	if pending == 0 {
		return 0, nil
	}
	if pending != len(files) {
		return 0, fmt.Errorf("the household update was only partly recorded, so it stopped. Restore the pantry and start again")
	}
	if err := beforeHouseholdMigration(db); err != nil {
		return 0, err
	}

	household, err := uuid.NewV7()
	if err != nil {
		return 0, fmt.Errorf("could not prepare the household update: %w", err)
	}
	device, err := uuid.NewV7()
	if err != nil {
		return 0, fmt.Errorf("could not prepare the household update: %w", err)
	}
	channel, err := randomHex(16)
	if err != nil {
		return 0, fmt.Errorf("could not prepare the household update: %w", err)
	}
	issued := time.Now().UTC().Format(time.RFC3339)
	replacer, err := householdReplacer(household.String(), device.String(), issued, channel)
	if err != nil {
		return 0, err
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if err := guardSingleHousehold(tx); err != nil {
		return 0, err
	}
	before, err := tableCounts(tx)
	if err != nil {
		return 0, err
	}

	for _, name := range files {
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return 0, fmt.Errorf("read embedded migration %q: %w", name, err)
		}
		sqlText := replacer(prepareMigrationSQL(name, string(sqlBytes)))
		if strings.Contains(sqlText, "{{") {
			return 0, fmt.Errorf("apply migration %q: a household migration still has a blank", name)
		}
		if _, err := tx.Exec(sqlText); err != nil {
			return 0, fmt.Errorf("apply migration %q: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name); err != nil {
			return 0, fmt.Errorf("apply migration %q: %w", name, err)
		}
		log.Printf("applied migration: %s", name)
	}
	if err := checkHouseholdCounts(tx, before); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(files), nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func householdReplacer(household, device, issued, channel string) (func(string) string, error) {
	if !validUUID(household) || !validUUID(device) || !validIssued(issued) || !validChannel(channel) {
		return nil, fmt.Errorf("could not prepare the household update")
	}
	return func(sqlText string) string {
		sqlText = strings.ReplaceAll(sqlText, "{{household}}", household)
		sqlText = strings.ReplaceAll(sqlText, "{{device}}", device)
		sqlText = strings.ReplaceAll(sqlText, "{{issued}}", issued)
		sqlText = strings.ReplaceAll(sqlText, "{{channel}}", channel)
		return sqlText
	}, nil
}

func validUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value
}

func validIssued(value string) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	return err == nil && parsed.UTC().Format(time.RFC3339) == value
}

func validChannel(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// legacyHouseholdLiterals are the only tenant values a stage-1 pantry may
// already store. Anything else stops the update before a row changes.
var legacyUserIDTables = []string{
	"items",
	"barcodes",
	"brand_preferences",
	"group_suggestion_dismissals",
	"group_suggestions",
	"item_deals",
	"product_groups",
	"provider_app_credentials",
	"provider_connections",
	"scan_entries",
	"shopping_list_items",
	"staged_cart_skips",
	"push_subscriptions",
}

var legacyHouseholdIDTables = []string{
	"categories",
	"category_targets",
	"item_targets",
	"item_categories",
	"category_suggestion_dismissals",
}

func guardSingleHousehold(tx *sql.Tx) error {
	var parts []string
	for _, table := range legacyUserIDTables {
		n, err := countOutside(tx, table, "user_id")
		if err != nil {
			return err
		}
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%s.user_id: %d rows outside the first household", table, n))
		}
	}
	for _, table := range legacyHouseholdIDTables {
		n, err := countOutside(tx, table, "household_id")
		if err != nil {
			return err
		}
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%s.household_id: %d rows outside the first household", table, n))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return fmt.Errorf("this pantry has rows outside the first household, so the update stopped and nothing was changed: %s", strings.Join(parts, "; "))
}

func countOutside(tx *sql.Tx, table, column string) (int, error) {
	var n int
	query := `SELECT COUNT(*) FROM ` + table + ` WHERE ` + column + ` NOT IN ('user-1', '')`
	if err := tx.QueryRow(query).Scan(&n); err != nil {
		return 0, fmt.Errorf("could not check this pantry before the household update: %w", err)
	}
	return n, nil
}

func tableCounts(tx *sql.Tx) (map[string]int, error) {
	rows, err := tx.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, fmt.Errorf("could not count pantry rows: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("could not count pantry rows: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not count pantry rows: %w", err)
	}
	counts := make(map[string]int, len(names))
	for _, name := range names {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM ` + quoteIdent(name)).Scan(&n); err != nil {
			return nil, fmt.Errorf("could not count pantry rows: %w", err)
		}
		counts[name] = n
	}
	return counts, nil
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func checkHouseholdCounts(tx *sql.Tx, before map[string]int) error {
	after, err := tableCounts(tx)
	if err != nil {
		return err
	}
	var problems []string
	for name, n := range before {
		if name == "schema_migrations" {
			continue
		}
		got := after[name]
		if name == "app_settings" {
			if got < n || got > n+1 {
				problems = append(problems, fmt.Sprintf("app_settings had %d rows and now has %d", n, got))
			}
			continue
		}
		if got != n {
			problems = append(problems, fmt.Sprintf("%s had %d rows and now has %d", name, n, got))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the household update changed row counts, so it stopped and nothing was changed: %s", strings.Join(problems, "; "))
}
