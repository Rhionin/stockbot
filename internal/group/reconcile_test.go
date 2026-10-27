package group

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func mustOpenSuggestion(t *testing.T, db *sql.DB, scope tenant.Scope, id, title string, productIDs []string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO group_suggestions (id, household_id, user_id, kind, title, status)
		VALUES (?, ?, ?, 'looks_alike', ?, 'open')`,
		id, scope.HouseholdID(), scope.HouseholdID(), title); err != nil {
		t.Fatal(err)
	}
	for _, pid := range productIDs {
		if _, err := db.Exec(`
			INSERT INTO group_suggestion_members (household_id, suggestion_id, product_id, included)
			VALUES (?, ?, ?, 1)`, scope.HouseholdID(), id, pid); err != nil {
			t.Fatal(err)
		}
	}
}

func suggestionStatus(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM group_suggestions WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestCreateClosesStaleOpenSuggestionWhenAllProductsGrouped(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "aqua-a", "Aqua Fresh Cool Mint Gel")
	mustProduct(t, catalog, "aqua-b", "Aqua Fresh Extreme Clean")
	mustOpenSuggestion(t, db, scope, "sug-aqua", "Aqua fresh toothpaste", []string{"aqua-a", "aqua-b"})

	if _, err := groups.Create(ctx, scope, "Aqua fresh toothpaste", []string{"aqua-a", "aqua-b"}, nil); err != nil {
		t.Fatal(err)
	}
	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Fatalf("expected no open suggestions, got %+v", cards)
	}
	if status := suggestionStatus(t, db, "sug-aqua"); status != "accepted" {
		t.Fatalf("status = %q", status)
	}
}

func TestListReconcilesPreexistingStaleSuggestionRow(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "aqua-a", "Aqua Fresh Cool Mint Gel")
	mustProduct(t, catalog, "aqua-b", "Aqua Fresh Extreme Clean")
	if _, err := groups.Create(ctx, scope, "Aqua fresh toothpaste", []string{"aqua-a", "aqua-b"}, nil); err != nil {
		t.Fatal(err)
	}
	mustOpenSuggestion(t, db, scope, "stale-sug", "Aqua fresh toothpaste", []string{"aqua-a", "aqua-b"})

	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Fatalf("expected reconciliation to close stale row, got %+v", cards)
	}
	if status := suggestionStatus(t, db, "stale-sug"); status != "accepted" {
		t.Fatalf("status = %q", status)
	}
}

func TestPartialOverlapLinksSuggestionToExistingGroup(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "aqua-a", "Aqua Fresh Cool Mint Gel")
	mustProduct(t, catalog, "aqua-b", "Aqua Fresh Extreme Clean")
	mustProduct(t, catalog, "aqua-c", "Aqua Fresh Whitening")
	view, err := groups.Create(ctx, scope, "Aqua fresh toothpaste", []string{"aqua-a", "aqua-b"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustOpenSuggestion(t, db, scope, "sug-partial", "Aqua fresh toothpaste", []string{"aqua-a", "aqua-b", "aqua-c"})

	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("cards: %+v", cards)
	}
	if cards[0].ExistingGroupID != view.ID {
		t.Fatalf("existing group = %q, want %q", cards[0].ExistingGroupID, view.ID)
	}
}

func TestSingleUngroupedProductDismissesSuggestionWithoutNameMatch(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "tp-a", "Colgate Total")
	mustProduct(t, catalog, "tp-b", "Sensodyne Repair")
	mustProduct(t, catalog, "travel-d", "Travel toothbrush")
	mustOpenSuggestion(t, db, scope, "sug-tooth", "Toothpaste", []string{"tp-a", "tp-b"})
	kit, err := groups.Create(ctx, scope, "Bathroom Travel Kit", []string{"tp-a", "travel-d"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = kit

	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Fatalf("expected dismissed suggestion, got %+v", cards)
	}
	if status := suggestionStatus(t, db, "sug-tooth"); status != "dismissed" {
		t.Fatalf("status = %q", status)
	}
}

func TestAcceptMergesIntoExistingGroupOnNameCollision(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "aqua-a", "Aqua Fresh Cool Mint Gel")
	mustProduct(t, catalog, "aqua-b", "Aqua Fresh Extreme Clean")
	mustProduct(t, catalog, "aqua-c", "Aqua Fresh Whitening")
	view, err := groups.Create(ctx, scope, "Aqua fresh toothpaste", []string{"aqua-a", "aqua-b"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustOpenSuggestion(t, db, scope, "sug-accept", "Aqua fresh toothpaste", []string{"aqua-a", "aqua-b", "aqua-c"})

	joined, err := groups.Accept(ctx, scope, "sug-accept", []string{"aqua-c"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if joined.ID != view.ID {
		t.Fatalf("joined %s, want %s", joined.ID, view.ID)
	}
	if len(joined.Members) != 3 {
		t.Fatalf("members %+v", joined.Members)
	}
	if status := suggestionStatus(t, db, "sug-accept"); status != "accepted" {
		t.Fatalf("status = %q", status)
	}

	// Idempotent when a chosen product is already in the target group.
	mustOpenSuggestion(t, db, scope, "sug-idem", "Aqua fresh toothpaste", []string{"aqua-a", "aqua-c"})
	again, err := groups.Accept(ctx, scope, "sug-idem", []string{"aqua-a", "aqua-c"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != view.ID || len(again.Members) != 3 {
		t.Fatalf("idempotent accept: %+v", again)
	}
}

func TestReconcileDoesNotCrossHouseholds(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "aqua-a", "Aqua Fresh Cool Mint Gel")
	mustProduct(t, catalog, "aqua-b", "Aqua Fresh Extreme Clean")
	mustOpenSuggestion(t, db, scope, "sug-home", "Aqua fresh toothpaste", []string{"aqua-a", "aqua-b"})

	const otherID = "0190bbbb-cccc-7000-8000-0000000000e6"
	if _, err := db.Exec(`
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Other', ?, ?, 0)`,
		otherID, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC).Format(time.RFC3339), "d0ffee00d0ffee00d0ffee00d0ffee00"); err != nil {
		t.Fatal(err)
	}
	other, err := tenant.New(otherID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := groups.Create(ctx, other, "Aqua fresh toothpaste", []string{"aqua-a", "aqua-b"}, nil); err != nil {
		t.Fatal(err)
	}

	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("home suggestion should stay open: %+v", cards)
	}
	if status := suggestionStatus(t, db, "sug-home"); status != "open" {
		t.Fatalf("status = %q", status)
	}
}

func TestAddMembersClosesStaleSuggestion(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "aqua-a", "Aqua Fresh Cool Mint Gel")
	mustProduct(t, catalog, "aqua-b", "Aqua Fresh Extreme Clean")
	view, err := groups.Create(ctx, scope, "Aqua fresh toothpaste", []string{"aqua-a"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustOpenSuggestion(t, db, scope, "sug-add", "Aqua fresh toothpaste", []string{"aqua-a", "aqua-b"})

	if _, err := groups.AddMembers(ctx, scope, view.ID, []string{"aqua-b"}, "", nil); err != nil {
		t.Fatal(err)
	}
	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Fatalf("expected closed suggestion, got %+v", cards)
	}
}
