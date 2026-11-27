package supply

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
)

func seedSupplyDefault(t *testing.T, db *sql.DB, hid string, kind, value string) {
	t.Helper()
	if kind != "" {
		if _, err := db.Exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, 'supply_target_kind', ?)`, hid, kind); err != nil {
			t.Fatal(err)
		}
	}
	if value != "" {
		if _, err := db.Exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, 'supply_target_value', ?)`, hid, value); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadMonthsQ7(t *testing.T) {
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
	ctx := context.Background()

	cases := []struct {
		name      string
		kind      string
		value     string
		wantKind  DefaultKind
		wantMonths Months
	}{
		{name: "no setting", wantKind: DefaultKindMonths, wantMonths: DefaultMonths},
		{name: "months 5", kind: "months", value: "5", wantKind: DefaultKindMonths, wantMonths: 5},
		{name: "months 0", kind: "months", value: "0", wantKind: DefaultKindDontRestock, wantMonths: 0},
		{name: "amount 2", kind: "amount", value: "2", wantKind: DefaultKindAmount, wantMonths: DefaultMonths},
		{name: "amount 0", kind: "amount", value: "0", wantKind: DefaultKindDontRestock, wantMonths: 0},
		{name: "amount kind no value", kind: "amount", wantKind: DefaultKindAmount, wantMonths: DefaultMonths},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(`DELETE FROM household_settings WHERE household_id = ?`, hid); err != nil {
				t.Fatal(err)
			}
			seedSupplyDefault(t, db, hid, tc.kind, tc.value)
			gotKind, err := ReadDefaultKind(ctx, scope, db)
			if err != nil {
				t.Fatal(err)
			}
			if gotKind != tc.wantKind {
				t.Fatalf("defaultKind = %q, want %q", gotKind, tc.wantKind)
			}
			gotMonths, err := readMonths(ctx, scope, db)
			if err != nil {
				t.Fatal(err)
			}
			if gotMonths != tc.wantMonths {
				t.Fatalf("months = %d, want %d", gotMonths, tc.wantMonths)
			}
		})
	}
}

// TestZZ771Q7 aliases the Q7 read-months contract for mutant scripts.
func TestZZ771Q7ReadMonths(t *testing.T) { TestReadMonthsQ7(t) }

func TestReadMonthsQ7GoallessGroupNoLineUnderAmountZero(t *testing.T) {
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
	seedSupplyDefault(t, db, hid, "amount", "0")
	if _, err := db.Exec(`INSERT INTO products (id, name, source) VALUES ('prod-g', 'G', 'external')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-g', ?, 'prod-g', ?)`, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO product_groups (id, user_id, household_id, name, name_key, rule) VALUES ('grp-q7', ?, ?, 'Group', 'group', 'same_as_ran_out')`, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO product_group_members (group_id, product_id, household_id) VALUES ('grp-q7', 'prod-g', ?)`, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, 'onboarding_started_at', '2020-01-01T00:00:00Z')`, hid); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	months, err := readMonths(ctx, scope, db)
	if err != nil {
		t.Fatal(err)
	}
	if months != 0 {
		t.Fatalf("account months = %d, want 0", months)
	}
	svc := Open(db)
	lines, err := svc.Plan(ctx, scope, grpNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("lines %#v", lines)
	}
}