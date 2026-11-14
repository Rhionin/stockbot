package supply

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/tenant"
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

func TestPlanViewRequiresScope(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = Open(db).PlanView(context.Background(), tenant.Scope{}, time.Now(), nil)
	if err == nil {
		t.Fatal("expected scope error")
	}
}

func TestPlanViewAmountAndMonthsTargets(t *testing.T) {
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
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	for i, spec := range []struct {
		prod, item, kind string
		val              int
	}{
		{"prod-amt", "item-amt", "amount", 5},
		{"prod-mo", "item-mo", "months", 2},
	} {
		if _, err := db.Exec(`INSERT INTO products (id, name, source) VALUES (?, 'P', 'external')`, spec.prod); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, ?, ?, ?)`,
			spec.item, hid, spec.prod, hid); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO item_instances (id, item_id, household_id, stock_in_at) VALUES (?, ?, ?, '2026-01-01T00:00:00Z')`,
			fmt.Sprintf("inst-%d", i), spec.item, hid); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO item_targets (household_id, item_id, kind, value) VALUES (?, ?, ?, ?)`,
			hid, spec.item, spec.kind, spec.val); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, 'using_started_at', '2026-01-01T00:00:00Z')`, hid); err != nil {
		t.Fatal(err)
	}
	svc := Open(db)
	views, err := svc.PlanView(context.Background(), scope, now, map[string]int{"item-amt": 1})
	if err != nil {
		t.Fatal(err)
	}
	if v := views["item:item-amt"]; v.Target.Kind != "amount" {
		t.Fatalf("amount target %#v", v.Target)
	}
	if v := views["item:item-mo"]; v.Target.Kind != "months" {
		t.Fatalf("months target %#v", v.Target)
	}
	if views["item:item-amt"].ConsumedUnits != 1 {
		t.Fatalf("requested ledger not applied")
	}
}