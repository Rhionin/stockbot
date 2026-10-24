package group

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
)

func TestRefreshSuggestionsClustersLookAlikesOnce(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "gv", "Great Value Cut Green Beans")
	mustProduct(t, catalog, "kr", "Kroger Cut Green Beans")
	mustProduct(t, catalog, "dm", "Del Monte Cut Green Beans")
	mustProduct(t, catalog, "fr", "French Style Green Beans")
	mustProduct(t, catalog, "cr", "Great Value Crunchy Peanut Butter")
	mustProduct(t, catalog, "sm", "Kroger Creamy Peanut Butter")
	mustProduct(t, catalog, "gvpb", "Great Value Peanut Butter")

	first, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("cards: %+v", first)
	}
	beans := cardByTitle(t, first, "Cut green beans")
	if beans.Kind != "looks_alike" || len(beans.Members) != 3 {
		t.Fatalf("%+v", beans)
	}
	for _, member := range beans.Members {
		if member.ProductID == "fr" || !member.Included || member.Name == "" {
			t.Fatalf("%+v", beans.Members)
		}
	}
	butter := cardByTitle(t, first, "Peanut butter")
	var crunchyIncluded bool
	var sawCrunchy bool
	for _, member := range butter.Members {
		if member.ProductID == "cr" {
			sawCrunchy = true
			crunchyIncluded = member.Included
			if member.Caution != "Crunchy. Probably not the same." {
				t.Fatalf("caution %q", member.Caution)
			}
		}
	}
	if !sawCrunchy || crunchyIncluded {
		t.Fatalf("%+v", butter.Members)
	}

	second, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 2 || !sameIDs(first, second) {
		t.Fatalf("duplicated: %+v", second)
	}

	if err := groups.Dismiss(ctx, scope, beans.ID); err != nil {
		t.Fatal(err)
	}
	after, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].Title == "Cut green beans" {
		t.Fatalf("dismissed card returned: %+v", after)
	}
}

func TestRefreshSuggestionsJoinsAnExistingGroup(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "gv", "Great Value Cut Green Beans")
	mustProduct(t, catalog, "kr", "Kroger Cut Green Beans")
	view, err := groups.Create(ctx, scope, "Cut green beans", []string{"gv", "kr"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustProduct(t, catalog, "dm", "Del Monte Cut Green Beans")

	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].ExistingGroupID != view.ID {
		t.Fatalf("%+v", cards)
	}
	if len(cards[0].Members) != 3 {
		t.Fatalf("%+v", cards[0].Members)
	}

	if _, err := groups.AddMembers(ctx, scope, view.ID, []string{"dm"}, "", nil); err != nil {
		t.Fatal(err)
	}
	left, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].ID != cards[0].ID {
		t.Fatalf("open card changed after the product joined: %+v", left)
	}
}

func TestRefreshSuggestionsFindsNationalBrandsAddedLater(t *testing.T) {
	groups, catalog, conn := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, conn)
	mustProduct(t, catalog, "gv", "Great Value Cut Green Beans")
	if err := groups.Seed(ctx, scope); err != nil {
		t.Fatal(err)
	}
	mustProduct(t, catalog, "hunts", "Hunt's Tomato Ketchup")
	mustProduct(t, catalog, "heinz", "Heinz Tomato Ketchup")

	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	ketchup := cardByTitle(t, cards, "Tomato ketchup")
	if len(ketchup.Members) != 2 {
		t.Fatalf("%+v", ketchup.Members)
	}

	again, err := groups.Rescan(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("rescan copied cards: %+v", again)
	}

	if err := groups.Dismiss(ctx, scope, ketchup.ID); err != nil {
		t.Fatal(err)
	}
	after, err := groups.Rescan(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("dismissed pair returned: %+v", after)
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM group_suggestions WHERE status = 'open' AND title = 'Tomato ketchup'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("open ketchup cards = %d", n)
	}
}

func TestRefreshSuggestionsPinsALaterSameNeed(t *testing.T) {
	groups, catalog, conn := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, conn)
	if err := catalog.CreateProduct(ctx, product.Product{ID: "whole", Name: "Whole Milk", UnitOfMeasure: "carton"}); err != nil {
		t.Fatal(err)
	}
	if err := groups.Seed(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := catalog.CreateProduct(ctx, product.Product{ID: "gv", Name: "Great Value Whole Milk", UnitOfMeasure: "carton"}); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, household_id, user_id, product_id) VALUES ('item-whole', ?, ?, 'whole')`, scope.HouseholdID(), scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	key, ok := needKey("Whole Milk", "carton")
	if !ok {
		t.Fatal("need key")
	}
	if _, err := conn.Exec(`INSERT INTO brand_preferences (household_id, user_id, need_key, item_id, ignore_price) VALUES (?, ?, ?, 'item-whole', 1)`, scope.HouseholdID(), scope.HouseholdID(), key); err != nil {
		t.Fatal(err)
	}

	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Kind != "from_old_plan" || cards[0].ProposedRule != "favorite" || cards[0].PinnedProductID != "whole" {
		t.Fatalf("%+v", cards)
	}
}

func TestRescanRespectsAPartialDismissal(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "hunts", "Hunt's Tomato Ketchup")
	mustProduct(t, catalog, "heinz", "Heinz Tomato Ketchup")
	first, err := groups.Rescan(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("%+v", first)
	}
	if err := groups.Dismiss(ctx, scope, first[0].ID); err != nil {
		t.Fatal(err)
	}
	mustProduct(t, catalog, "plain", "Ketchup")
	cards, err := groups.Rescan(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("%+v", cards)
	}
	ids := map[string]struct{}{}
	for _, member := range cards[0].Members {
		ids[member.ProductID] = struct{}{}
	}
	if _, both := ids["hunts"]; both {
		if _, other := ids["heinz"]; other {
			t.Fatalf("dismissed pair came back: %+v", cards[0].Members)
		}
	}
	if _, ok := ids["plain"]; !ok {
		t.Fatalf("plain ketchup missing: %+v", cards[0].Members)
	}
}

func TestConsiderProductJoinsAnExistingGroup(t *testing.T) {
	groups, catalog, conn := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, conn)
	mustProduct(t, catalog, "kr", "Kroger Cut Green Beans")
	view, err := groups.Create(ctx, scope, "Cut green beans", []string{"kr"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustProduct(t, catalog, "hunts", "Hunt's Cut Green Beans")
	if err := groups.ConsiderProduct(ctx, scope, "hunts"); err != nil {
		t.Fatal(err)
	}
	var existing string
	if err := conn.QueryRow(`SELECT COALESCE(existing_group_id, '') FROM group_suggestions WHERE status = 'open'`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing != view.ID {
		t.Fatalf("existing group = %q, want %q", existing, view.ID)
	}
	if err := groups.ConsiderProduct(ctx, scope, "missing"); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptJoinsAnExistingGroupAndRenamesANewOne(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "gv", "Great Value Cut Green Beans")
	mustProduct(t, catalog, "kr", "Kroger Cut Green Beans")
	created, err := groups.Create(ctx, scope, "Cut green beans", []string{"gv"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustProduct(t, catalog, "dm", "Del Monte Cut Green Beans")
	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	card := cardByTitle(t, cards, "Cut green beans")
	if card.ExistingGroupID != created.ID {
		t.Fatalf("existing group = %q", card.ExistingGroupID)
	}
	if _, err := groups.Accept(ctx, scope, card.ID, nil, &TargetInput{Window: intPtr(2), Quantity: floatPtr(8)}, ""); err == nil {
		t.Fatal("accepted a target that sets two fields")
	}
	joined, err := groups.Accept(ctx, scope, card.ID, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if joined.ID != created.ID {
		t.Fatalf("joined group = %s, want %s", joined.ID, created.ID)
	}
	if _, err := groups.Accept(ctx, scope, card.ID, nil, nil, ""); err == nil {
		t.Fatal("accepted a suggestion that was already settled")
	}

	mustProduct(t, catalog, "oats", "Rolled Oats")
	mustProduct(t, catalog, "quick", "Quick Oats")
	if _, err := db.Exec(`
		INSERT INTO group_suggestions (id, user_id, household_id, kind, title, status)
		VALUES ('sug-oats', 'user-1', ?, 'looks_alike', 'Oats', 'open')`, scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO group_suggestion_members (suggestion_id, product_id, included)
		VALUES ('sug-oats', 'oats', 1), ('sug-oats', 'quick', 0)`); err != nil {
		t.Fatal(err)
	}
	renamed, err := groups.Accept(ctx, scope, "sug-oats", []string{"oats"}, &TargetInput{Clear: true}, "Breakfast oats")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "Breakfast oats" {
		t.Fatalf("name = %q", renamed.Name)
	}
}

func floatPtr(n float64) *float64 { return &n }

func sameIDs(a, b []Suggestion) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]struct{}{}
	for _, card := range a {
		seen[card.ID] = struct{}{}
	}
	for _, card := range b {
		if _, ok := seen[card.ID]; !ok {
			return false
		}
	}
	return true
}

func cardByTitle(t *testing.T, cards []Suggestion, title string) Suggestion {
	t.Helper()
	for _, card := range cards {
		if card.Title == title {
			return card
		}
	}
	t.Fatalf("missing %s in %+v", title, cards)
	return Suggestion{}
}
