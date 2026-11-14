package supply

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

func TestPlanViewReturnsLineKey(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope := scopeOf(t, db)
	hid := scope.HouseholdID()
	if _, err := db.Exec(`INSERT INTO products (id, name, source) VALUES ('prod-pv', 'PV', 'external')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-pv', ?, 'prod-pv', ?)`, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO item_instances (id, item_id, household_id, stock_in_at) VALUES ('inst-pv', 'item-pv', ?, '2026-01-01T00:00:00Z')`, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, 'using_started_at', '2026-01-01T00:00:00Z')`, hid); err != nil {
		t.Fatal(err)
	}
	svc := Open(db)
	views, err := svc.PlanView(context.Background(), scope, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), nil)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := views["item:item-pv"]
	if !ok {
		t.Fatalf("views %#v", views)
	}
	if v.Target.Kind == "" {
		t.Fatalf("target %#v", v.Target)
	}
}