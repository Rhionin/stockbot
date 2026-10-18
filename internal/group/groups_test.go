package group

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/shopping"
)

func newTestGroups(t *testing.T) (*Groups, *product.Catalog, *sql.DB) {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewGroups(conn), product.NewCatalog(conn), conn
}

func mustProduct(t *testing.T, catalog *product.Catalog, id, name string) {
	t.Helper()
	if err := catalog.CreateProduct(context.Background(), product.Product{ID: id, Name: name}); err != nil {
		t.Fatalf("product %s: %v", id, err)
	}
}

func TestGroupQuantityRoundTripAndDuplicateName(t *testing.T) {
	groups, catalog, _ := newTestGroups(t)
	ctx := context.Background()
	mustProduct(t, catalog, "p1", "Beans")
	qty := 24.0
	view, err := groups.Create(ctx, "Cut green beans", []string{"p1"}, &TargetInput{Quantity: &qty})
	if err != nil {
		t.Fatal(err)
	}
	if view.Dimension != product.DimensionMass || view.Quantity == nil || *view.Quantity != 24 {
		t.Fatalf("%+v", view)
	}
	if _, err := groups.Create(ctx, " cut green beans ", nil, nil); err == nil {
		t.Fatal("expected a duplicate name")
	}
}

func TestRemoveLastMemberDeletesGroupAndClearsPin(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	mustProduct(t, catalog, "p1", "A")
	mustProduct(t, catalog, "p2", "B")
	view, err := groups.Create(ctx, "Beans", []string{"p1", "p2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pin := "p1"
	if _, err := groups.SetRule(ctx, view.ID, string(KindFavorite), &pin, true); err != nil {
		t.Fatal(err)
	}
	kept, deleted, err := groups.RemoveMember(ctx, view.ID, "p1")
	if err != nil || deleted {
		t.Fatalf("deleted %v err %v", deleted, err)
	}
	if kept.PinnedProductID != "" || kept.RuleConfirmed {
		t.Fatalf("pin stayed %+v", kept)
	}
	if _, deleted, err = groups.RemoveMember(ctx, view.ID, "p2"); err != nil || !deleted {
		t.Fatalf("deleted %v err %v", deleted, err)
	}
	if _, err := groups.Get(ctx, view.ID); err == nil {
		t.Fatal("group remained")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM product_groups`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("groups left %d err %v", n, err)
	}
}

func TestMoveMemberAndRejectSecondGroup(t *testing.T) {
	groups, catalog, _ := newTestGroups(t)
	ctx := context.Background()
	mustProduct(t, catalog, "p1", "A")
	first, err := groups.Create(ctx, "First", []string{"p1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := groups.Create(ctx, "Second", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := groups.AddMembers(ctx, second.ID, []string{"p1"}, "", nil); err == nil {
		t.Fatal("expected a conflict")
	}
	moved, err := groups.AddMembers(ctx, second.ID, []string{"p1"}, first.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved.Members) != 1 || moved.Members[0].ProductID != "p1" {
		t.Fatalf("%+v", moved.Members)
	}
	if _, err := groups.Get(ctx, first.ID); err == nil {
		t.Fatal("empty source group remained")
	}
}

func TestAcceptDismissesOnlyExcludedPairs(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		mustProduct(t, catalog, id, id)
	}
	if _, err := db.Exec(`INSERT INTO group_suggestions (id, user_id, kind, title, status) VALUES ('sug', 'user-1', 'looks_alike', 'Beans', 'open')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, err := db.Exec(`INSERT INTO group_suggestion_members (suggestion_id, product_id, included) VALUES ('sug', ?, 1)`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := groups.Accept(ctx, "sug", []string{"a", "b"}, nil, ""); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT product_id_a, product_id_b FROM group_suggestion_dismissals ORDER BY product_id_a, product_id_b`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var pairs []string
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		pairs = append(pairs, a+" "+b)
	}
	if len(pairs) != 2 || pairs[0] != "a c" || pairs[1] != "b c" {
		t.Fatalf("pairs %v", pairs)
	}
}

func TestFavorVarietyAndNoRestockRoundTrip(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	mustProduct(t, catalog, "plain", "Plain")
	mustProduct(t, catalog, "fancy", "Fancy")
	view, err := groups.Create(ctx, "Muffin mix", []string{"plain", "fancy"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := groups.SetRule(ctx, view.ID, string(KindFavorVariety), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Rule != string(KindFavorVariety) || !saved.RuleConfirmed {
		t.Fatalf("%+v", saved)
	}
	marked, err := groups.SetMemberRestock(ctx, view.ID, "fancy", true)
	if err != nil {
		t.Fatal(err)
	}
	var fancy Member
	for _, member := range marked.Members {
		if member.ProductID == "fancy" {
			fancy = member
		}
		if member.ProductID == "plain" && member.NoRestock {
			t.Fatal("plain was marked")
		}
	}
	if !fancy.NoRestock {
		t.Fatal("fancy stayed in rotation")
	}
	if _, err := db.Exec(`INSERT INTO product_group_members (product_id, group_id, no_restock) VALUES ('nope', 'missing', 2)`); err == nil {
		t.Fatal("expected the flag check to reject 2")
	}
	back, err := groups.SetMemberRestock(ctx, view.ID, "fancy", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range back.Members {
		if member.NoRestock {
			t.Fatalf("still marked %+v", member)
		}
	}
}

func TestParseDBTimeDropsMonotonicSuffix(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	mustProduct(t, catalog, "p1", "Oats")
	if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-1', 'user-1', 'p1')`); err != nil {
		t.Fatal(err)
	}
	const raw = "2026-10-17 18:55:19.032067103 +0000 UTC m=+12.3"
	if _, err := db.Exec(`INSERT INTO consumption_events (id, item_id, consumed_at) VALUES ('c1', 'item-1', ?)`, raw); err != nil {
		t.Fatal(err)
	}
	view, err := groups.Create(ctx, "Oats", []string{"p1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := groups.Get(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 17, 18, 55, 19, 32067103, time.UTC)
	if len(got.Members) != 1 || got.Members[0].LastConsumedAt.IsZero() || !got.Members[0].LastConsumedAt.Equal(want) {
		t.Fatalf("last used = %+v, want %s", got.Members, want)
	}
}

func TestLoadLastConsumed(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	mustProduct(t, catalog, "p1", "Hunt's")
	if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-1', 'user-1', 'p1')`); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 3, 2, 15, 4, 5, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO consumption_events (id, item_id, consumed_at) VALUES ('c1', 'item-1', ?)`, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO stock_in_events (product_id, at) VALUES ('p1', ?)`, when.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	view, err := groups.Create(ctx, "Beans", []string{"p1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := groups.Get(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 1 || !got.Members[0].LastConsumedAt.Equal(when) || got.Members[0].LastStockedAt.IsZero() {
		t.Fatalf("%+v", got.Members)
	}
	var ge *Error
	if _, err := groups.Get(ctx, "missing"); !errors.As(err, &ge) || ge.Code != codeMissing {
		t.Fatalf("err %v", err)
	}
}

func TestTargetWindowVolumeAndDeals(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	mustProduct(t, catalog, "p1", "Milk")
	qty := 12.0
	view, err := groups.Create(ctx, "Milk", []string{"p1"}, &TargetInput{Quantity: &qty, Dimension: product.DimensionVolume})
	if err != nil {
		t.Fatal(err)
	}
	if view.Dimension != product.DimensionVolume || view.Quantity == nil || *view.Quantity != 12 {
		t.Fatalf("%+v", view)
	}
	months := 4
	cleared, err := groups.SetTarget(ctx, view.ID, &TargetInput{Window: &months})
	if err != nil || cleared.WindowMonths == nil || *cleared.WindowMonths != 4 || cleared.Quantity != nil {
		t.Fatalf("%+v err %v", cleared, err)
	}
	again, err := groups.SetTarget(ctx, view.ID, &TargetInput{Clear: true})
	if err != nil || again.WindowMonths != nil || again.hasQuantity {
		t.Fatalf("%+v err %v", again, err)
	}
	if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-1', 'user-1', 'p1')`); err != nil {
		t.Fatal(err)
	}
	price := 59
	if err := shopping.NewStore(db).SaveDeal(ctx, "user-1", shopping.Deal{ItemID: "item-1", PriceCents: &price, Label: "sale"}); err != nil {
		t.Fatal(err)
	}
	deals, err := groups.DealsFor(ctx, []string{"p1"})
	if err != nil || len(deals) != 1 || deals[0].PriceCents == nil || *deals[0].PriceCents != 59 || deals[0].NotedAt.IsZero() {
		t.Fatalf("%+v err %v", deals, err)
	}
	if err := groups.Skip(ctx, "missing"); err == nil {
		t.Fatal("expected a missing suggestion")
	}
	if _, err := db.Exec(`INSERT INTO group_suggestions (id, user_id, kind, title) VALUES ('sug', 'user-1', 'looks_alike', 'Milk')`); err != nil {
		t.Fatal(err)
	}
	if err := groups.Skip(ctx, "sug"); err != nil {
		t.Fatal(err)
	}
	renamed, err := groups.Rename(ctx, view.ID, "Dairy")
	if err != nil || renamed.Name != "Dairy" {
		t.Fatalf("%+v err %v", renamed, err)
	}
	if err := groups.Delete(ctx, view.ID); err != nil {
		t.Fatal(err)
	}
}
