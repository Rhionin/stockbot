package supply_test

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/supply"
)

func TestWipeClearsW1Tables(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	household := scope.HouseholdID()
	rows := []struct {
		stmt string
		args []any
	}{
		{`INSERT INTO stock_in_clusters (household_id, started_at, closed_at, product_count, k_used, trip_seq) VALUES (?, 's', 'c', 5, 5, 0)`, []any{household}},
		{`INSERT INTO list_line_changes (household_id, line_key, effect, cause, at, trip_seq) VALUES (?, 'item:x', 'added', 'list_updated', '2026-01-01T00:00:00Z', 0)`, []any{household}},
		{`INSERT INTO cart_line_results (household_id, provider_id, item_id, status, attempted_at) VALUES (?, 'kroger', 'item-x', 'sent', '2026-01-01T00:00:00Z')`, []any{household}},
		{`INSERT INTO attention_marks (household_id, line_key, flagged_trip) VALUES (?, 'item:x', 0)`, []any{household}},
	}
	for _, row := range rows {
		if _, err := conn.Exec(row.stmt, row.args...); err != nil {
			t.Fatal(err)
		}
	}
	svc := supply.Open(conn)
	if err := svc.Wipe(context.Background(), scope, supply.WipePhrase); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"stock_in_clusters", "list_line_changes", "cart_line_results", "attention_marks"} {
		var n int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE household_id = ?`, household).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s still has %d rows", table, n)
		}
	}
}
