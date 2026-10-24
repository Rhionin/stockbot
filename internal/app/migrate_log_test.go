package app

import (
	"bytes"
	"log"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// withMigrationProgress turns startup migration lines back on for tests that
// assert on the wording. The flag is process-wide, so these tests stay
// sequential with the rest of the package.
func withMigrationProgress(t *testing.T) {
	t.Helper()
	prev := showMigrationProgress
	showMigrationProgress = true
	t.Cleanup(func() { showMigrationProgress = prev })
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := log.Writer()
	previousFlags := log.Flags()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previous)
		log.SetFlags(previousFlags)
	})
	return &buf
}

// TestRunMigrations_StaysQuietDuringTests verifies that a normal test database
// setup does not print migration progress. CI failure logs were dominated by
// those lines.
func TestRunMigrations_StaysQuietDuringTests(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	buf := captureLog(t)
	if err := RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if err := RunMigrations(conn); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}
	if got := buf.String(); got != "" {
		t.Fatalf("log output: want empty, got %q", got)
	}
}

// TestRunMigrations_LogsAppliedMigrationsOnFreshDB verifies that RunMigrations
// logs one "applied migration: <filename>" line per migration file it applies
// against a fresh database, and does not log the "schema up to date" summary
// line (since at least one file was applied).
func TestRunMigrations_LogsAppliedMigrationsOnFreshDB(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	withMigrationProgress(t)
	buf := captureLog(t)

	if err := RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	output := buf.String()
	for _, name := range []string{
		"001_initial_schema.sql",
		"002_backfill_orphaned_products.sql",
		"003_add_product_image_url.sql",
		"022a_household_tables.sql",
		"022f_drop_triggers.sql",
	} {
		want := "applied migration: " + name
		if !strings.Contains(output, want) {
			t.Errorf("log output missing %q; got: %s", want, output)
		}
	}
	if strings.Contains(output, "schema up to date") {
		t.Errorf("log output should not contain the summary line when migrations were applied; got: %s", output)
	}
}

// TestRunMigrations_LogsSummaryWhenAlreadyMigrated verifies that RunMigrations
// logs exactly the "schema up to date, no migrations applied" summary line
// (and no "applied migration:" lines) when called against a database that has
// already been fully migrated.
func TestRunMigrations_LogsSummaryWhenAlreadyMigrated(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	if err := RunMigrations(conn); err != nil {
		t.Fatalf("first RunMigrations (set up schema): %v", err)
	}

	withMigrationProgress(t)
	buf := captureLog(t)

	if err := RunMigrations(conn); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}

	want := "schema up to date, no migrations applied\n"
	if got := buf.String(); got != want {
		t.Errorf("log output: want %q, got %q", want, got)
	}
}
