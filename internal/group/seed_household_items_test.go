package group

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

const (
	seedOrphanCatalogID = "catalog-zephyr-569b"
	seedHouseholdCopyID = "typed-zephyr-569b"
	zephyrDisplayName   = "Zephyr Blend Cleaner Refill"
	lookA               = "nym-lookalike-alpha"
	lookB               = "nym-lookalike-bravo"
	shelvedLookA        = "nym-shelved-alpha"
	shelvedLookB        = "nym-shelved-bravo"
	crossHomeProduct    = "nym-cross-remote"
)

func TestRefreshSuggestionsSkipsOrphanCatalogWithMatchingHouseholdName(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	if err := catalog.CreateProduct(ctx, product.Product{
		ID: seedOrphanCatalogID, Name: zephyrDisplayName, Source: product.SourceExternal,
	}); err != nil {
		t.Fatal(err)
	}
	typed, err := catalog.CreateTypedProduct(ctx, scope, product.Product{
		ID: seedHouseholdCopyID, Name: zephyrDisplayName,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustHouseholdItem(t, db, scope, "item-"+seedHouseholdCopyID, typed.ID)

	if err := groups.ConsiderProduct(ctx, scope, typed.ID); err != nil {
		t.Fatal(err)
	}
	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, card := range cards {
		if card.Kind != "looks_alike" {
			continue
		}
		for _, member := range card.Members {
			if member.ProductID == seedOrphanCatalogID {
				t.Fatalf("orphan catalog in card %+v", card)
			}
		}
	}
}

func TestRefreshSuggestionsGroupsTwoHouseholdItemsWithLookalikeNames(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProductWithItem(t, catalog, db, scope, lookA, "Nym Great Cut Green Beans")
	mustProductWithItem(t, catalog, db, scope, lookB, "Nym Kroger Cut Green Beans")

	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	card := cardByTitle(t, cards, "Cut green beans")
	if card.Kind != "looks_alike" || len(card.Members) != 2 {
		t.Fatalf("%+v", card)
	}
	ids := map[string]struct{}{}
	for _, member := range card.Members {
		ids[member.ProductID] = struct{}{}
	}
	if _, ok := ids[lookA]; !ok {
		t.Fatalf("missing %s in %+v", lookA, card.Members)
	}
	if _, ok := ids[lookB]; !ok {
		t.Fatalf("missing %s in %+v", lookB, card.Members)
	}
}

func TestRefreshSuggestionsTreatsShelvedItemProductAsCandidate(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProductWithItem(t, catalog, db, scope, shelvedLookA, "Nym Great Cut Green Beans")
	mustProductWithItem(t, catalog, db, scope, shelvedLookB, "Nym Kroger Cut Green Beans")
	if _, err := db.Exec(`UPDATE items SET shelved = 1 WHERE product_id = ?`, shelvedLookA); err != nil {
		t.Fatal(err)
	}

	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	card := cardByTitle(t, cards, "Cut green beans")
	seenShelved := false
	for _, member := range card.Members {
		if member.ProductID == shelvedLookA {
			seenShelved = true
		}
	}
	if !seenShelved {
		t.Fatalf("shelved candidate missing from %+v", card.Members)
	}
}

func TestRefreshSuggestionsExcludesOtherHouseholdItems(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProductWithItem(t, catalog, db, scope, lookA, "Nym Great Cut Green Beans")

	const otherHousehold = "0190bbbb-cccc-7000-8000-0000000000f7"
	if _, err := db.Exec(`
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Nym Other', ?, ?, 0)`,
		otherHousehold, time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"d0ffee00d0ffee00d0ffee00d0ffee01"); err != nil {
		t.Fatal(err)
	}
	other, err := tenant.New(otherHousehold)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.CreateProduct(ctx, product.Product{ID: crossHomeProduct, Name: "Nym Great Cut Green Beans"}); err != nil {
		t.Fatal(err)
	}
	mustHouseholdItem(t, db, other, "item-remote", crossHomeProduct)

	mustProductWithItem(t, catalog, db, scope, lookB, "Nym Kroger Cut Green Beans")

	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	var foundHomePair bool
	for _, card := range cards {
		hasLookA, hasLookB := false, false
		for _, member := range card.Members {
			if member.ProductID == crossHomeProduct {
				t.Fatalf("other household product leaked into %+v", card)
			}
			if member.ProductID == lookA {
				hasLookA = true
			}
			if member.ProductID == lookB {
				hasLookB = true
			}
		}
		if hasLookA && hasLookB {
			foundHomePair = true
		}
	}
	if !foundHomePair {
		t.Fatalf("expected a lookalike card for %s and %s, got %+v", lookA, lookB, cards)
	}
}

func TestRefreshSuggestionsHonorsDismissedLookalikePair(t *testing.T) {
	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProductWithItem(t, catalog, db, scope, "dismiss-a", "Nym Great Cut Green Beans")
	mustProductWithItem(t, catalog, db, scope, "dismiss-b", "Nym Kroger Cut Green Beans")

	cards, err := groups.ListSuggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("%+v", cards)
	}
	if err := groups.Dismiss(ctx, scope, cards[0].ID); err != nil {
		t.Fatal(err)
	}
	again, err := groups.Rescan(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("dismissed pair returned: %+v", again)
	}
}

func TestLoadSeedProductsUsesHouseholdItemsOnly(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scope := scopeOf(t, db)
	catalog := product.NewCatalog(db)
	if err := catalog.CreateProduct(ctx, product.Product{ID: seedOrphanCatalogID, Name: "Nym Orphan Only"}); err != nil {
		t.Fatal(err)
	}
	mustProductWithItem(t, catalog, db, scope, lookA, "Nym In Pantry")

	seeded, err := loadSeedProducts(ctx, scope, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(seeded) != 1 || seeded[0].id != lookA {
		t.Fatalf("seeded %+v", seeded)
	}
}
