package shopping

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	_ "modernc.org/sqlite"
)

func TestStockInFinishesLine(t *testing.T) {
	const (
		equalUTC    = "2026-10-17 18:55:19.032067103 +0000 UTC"
		withSuffix  = "2026-10-17 18:55:19.032067103 +0000 UTC m=+6592.05"
		denverNight = "2026-10-17 00:00:00 -0600 MDT"
		sixUTC      = "2026-10-17 06:00:00 +0000 UTC"
		beforeNight = "2026-10-16 23:59:59 -0600 MDT"
	)
	tests := []struct {
		name    string
		stockIn string
		added   string
		want    bool
	}{
		{name: "equal instant", stockIn: equalUTC, added: equalUTC, want: true},
		{name: "denver midnight is 06:00Z", stockIn: denverNight, added: sixUTC, want: true},
		{name: "06:00Z is denver midnight", stockIn: sixUTC, added: denverNight, want: true},
		{name: "before denver midnight", stockIn: beforeNight, added: sixUTC, want: false},
		{name: "suffix on the stock-in", stockIn: withSuffix, added: equalUTC, want: true},
		{name: "suffix on the added time", stockIn: equalUTC, added: withSuffix, want: true},
		{name: "suffix still before the added time", stockIn: withSuffix, added: "2026-10-17 18:55:20 +0000 UTC", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stockIn, err := parseStoredTime(tt.stockIn)
			if err != nil {
				t.Fatal(err)
			}
			added, err := parseStoredTime(tt.added)
			if err != nil {
				t.Fatal(err)
			}
			if got := stockInFinishesLine([]time.Time{stockIn}, added); got != tt.want {
				t.Fatalf("finished = %v, want %v (%s vs %s)", got, tt.want, stockIn, added)
			}
		})
	}
}

func TestStoredStockInFinishesTheLine(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO products (id, name) VALUES ('prod-fin', 'Finished')`); err != nil {
		t.Fatal(err)
	}
	scope := scopeOf(t, conn)
	household := scope.HouseholdID()
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-fin', ?, 'prod-fin', ?)`, household, household); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cases := []struct {
		name    string
		stockIn string
		added   string
		want    bool
	}{
		{name: "equal", stockIn: "2026-10-17 18:55:19.032067103 +0000 UTC", added: "2026-10-17 18:55:19.032067103 +0000 UTC", want: true},
		{name: "denver midnight", stockIn: "2026-10-17 00:00:00 -0600 MDT", added: "2026-10-17 06:00:00 +0000 UTC", want: true},
		{name: "suffix on stock-in", stockIn: "2026-10-17 18:55:19.032067103 +0000 UTC m=+6592.05", added: "2026-10-17 18:55:19.032067103 +0000 UTC", want: true},
		{name: "suffix on added", stockIn: "2026-10-17 18:55:19 +0000 UTC", added: "2026-10-17 18:55:19 +0000 UTC m=+6592.05", want: true},
		{name: "earlier stock-in", stockIn: "2026-10-17 18:55:18 +0000 UTC m=+1.0", added: "2026-10-17 18:55:19 +0000 UTC", want: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := conn.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`DELETE FROM stock_in_events WHERE household_id = ?`, household); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO stock_in_events (household_id, id, product_id, at) VALUES (?, 1, 'prod-fin', ?)`, household, tt.stockIn); err != nil {
				t.Fatal(err)
			}
			added, err := parseStoredTime(tt.added)
			if err != nil {
				t.Fatal(err)
			}
			times, err := stockInTimes(ctx, scope, tx, "item-fin")
			if err != nil {
				t.Fatal(err)
			}
			if got := stockInFinishesLine(times, added); got != tt.want {
				t.Fatalf("stored finished = %v, want %v (read %v)", got, tt.want, times)
			}
		})
	}
}
