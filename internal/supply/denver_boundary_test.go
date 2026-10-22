package supply

import (
	"context"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Rhionin/stockbot/internal/app"
)

// A use on a Denver evening is the next morning in UTC. The shopping plan
// still has to replace that unit. Opening is stored as a UTC instant, and the
// scan path records the use as a time in the process zone.
func TestPlanReplacesUseAcrossDenverUTCDayBoundary(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	used := time.Date(2026, 10, 17, 20, 30, 0, 0, denver)
	started := used.Add(-30 * time.Minute)
	if started.UTC().Day() == used.In(denver).Day() {
		t.Fatalf("fixture is not a day boundary: started %s used %s", started.UTC(), used)
	}

	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetMaxOpenConns(1)
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO products (id, name, unit_of_measure) VALUES
			('prod-rice', 'Rice', 'bag'),
			('prod-oats', 'Oats', 'bag')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO items (id, user_id, product_id) VALUES
			('item-rice', 'user-1', 'prod-rice'),
			('item-oats', 'user-1', 'prod-oats')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(
		`INSERT INTO app_settings (key, value) VALUES ('onboarding_started_at', ?)`,
		started.UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatal(err)
	}
	// Opening scans are not usage. This one is an hour before the snapshot,
	// still on the Denver evening that UTC already calls the next day.
	if _, err := conn.Exec(
		`INSERT INTO consumption_events (id, item_id, consumed_at) VALUES ('ce-oats', 'item-oats', ?)`,
		started.Add(-time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(
		`INSERT INTO consumption_events (id, item_id, consumed_at) VALUES ('ce-rice', 'item-rice', ?)`,
		used,
	); err != nil {
		t.Fatal(err)
	}

	lines, err := Open(conn).Plan(context.Background(), used.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Product != "prod-rice" || lines[0].Buy != 1 || lines[0].Note != "replacing 1 you used" || lines[0].Source != SourceReplace {
		var raw string
		_ = conn.QueryRow(`SELECT consumed_at FROM consumption_events WHERE id = 'ce-rice'`).Scan(&raw)
		t.Fatalf("lines = %#v\nstored consumed_at = %q\nstarted = %s\nused = %s", lines, raw, started.UTC().Format(time.RFC3339), used.Format(time.RFC3339))
	}
}
