package cart

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
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
	if _, err := db.Exec(`
		INSERT INTO products (id, name) VALUES ('prod', 'Milk');
		INSERT INTO items (id, user_id, product_id) VALUES ('item', 'user-1', 'prod')`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ledger := NewLedger(db)
	ctx := context.Background()
	if err := ledger.ResetForItemTx(ctx, tx, "kroger", "item", later); err != nil {
		t.Fatal(err)
	}
	n, err := ledger.AdvanceTx(ctx, tx, "kroger", "item", 1, later.UTC())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("advance affected %d rows", n)
	}
}
