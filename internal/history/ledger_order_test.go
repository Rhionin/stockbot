package history

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
)

func TestBackfillPairListsOutBeforeInWhenTimestampsMatch(t *testing.T) {
	ctx := context.Background()
	db := openHistoryDB(t)
	scope, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	seedCorrectionProduct(t, db, "item-order", "prod-order", "Ledger Order")
	at := time.Date(2026, 7, 4, 18, 0, 0, 0, time.UTC)
	recordBackfillPair(t, db, "item-order", "prod-order", "scan-order", "inst-order", at, false)
	if _, err := db.Exec(`UPDATE stock_moves SET id = 'pair-in' WHERE scan_entry_id = 'scan-order' AND source = 'backfill'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE stock_moves SET id = 'pair-out' WHERE scan_entry_id = 'scan-order' AND direction = 'out'`); err != nil {
		t.Fatal(err)
	}

	journal := NewJournal(db)
	view, err := journal.Product(ctx, scope, "item-order", at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Moves) != 2 || view.Moves[0].ID != "pair-out" || view.Moves[0].Direction != "out" ||
		view.Moves[1].ID != "pair-in" || view.Moves[1].Direction != "in" || view.Moves[1].Source != "backfill" {
		t.Fatalf("product history = %+v", view.Moves)
	}

	ledger, err := journal.Household(ctx, scope, LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Moves) < 2 || ledger.Moves[0].ID != "pair-out" || ledger.Moves[1].ID != "pair-in" {
		t.Fatalf("household history = %+v", ledger.Moves)
	}
}
