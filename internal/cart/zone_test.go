package cart

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

func TestLedgerAdvanceMatchesTheSameInstantAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	later := time.Date(2026, 11, 1, 0, 30, 0, 0, loc)
	if later.Location() == time.UTC || !later.IsDST() {
		t.Fatal("fixture is not a Denver DST instant")
	}
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scope, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO products (id, name) VALUES ('prod', 'Milk');
		INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item', ?, ?, 'prod')`, scope.HouseholdID(), scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ledger := NewLedger(db)
	if err := ledger.ResetForItemTx(ctx, scope, tx, "kroger", "item", later); err != nil {
		t.Fatal(err)
	}
	n, err := ledger.AdvanceTx(ctx, scope, tx, "kroger", "item", 1, later.UTC())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("advance affected %d rows", n)
	}
}
