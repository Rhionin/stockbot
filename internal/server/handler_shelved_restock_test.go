package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Rhionin/stockbot/internal/restock"
	"github.com/Rhionin/stockbot/internal/scan"
)

// A wipe hides the item from restock until it is stocked in again. The saved
// item target is still stored and shows up on that later stock-in.
func TestWipeHidesRestockItemsUntilStockIn(t *testing.T) {
	handler, env := setupTestWithDB(t)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-milk", "Milk", "item-milk")
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-milk", `{"kind":"amount","value":4}`, http.StatusOK)
	dairy := decodeID(t, exchangeJSON(t, handler, http.MethodPost, "/api/restock/categories", `{"name":"Dairy"}`, http.StatusOK))
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/categories/"+dairy+"/target", `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/restock/items/item-milk/categories", `{"categoryId":"`+dairy+`"}`, http.StatusOK)

	before := mustRestock(t, handler)
	if restockByProduct(before, "prod-milk") == nil || before.ItemCount != 1 {
		t.Fatalf("setup restock = itemCount %d items %d", before.ItemCount, len(before.Items))
	}
	if got := categoryNamed(before, "Dairy"); got == nil || got.Items != 1 || got.OwnTargets != 1 || got.Target == nil || got.Target.Value != 2 {
		t.Fatalf("setup Dairy = %+v", got)
	}

	exchangeJSON(t, handler, http.MethodPost, "/api/inventory/wipe", `{"confirmation":"WIPE INVENTORY"}`, http.StatusOK)

	wiped := mustRestock(t, handler)
	if restockByProduct(wiped, "prod-milk") != nil || wiped.ItemCount != 0 || len(wiped.Items) != 0 {
		t.Fatalf("wiped restock still lists items: itemCount %d %+v", wiped.ItemCount, wiped.Items)
	}
	if got := categoryNamed(wiped, "Dairy"); got == nil || got.Items != 0 || got.OwnTargets != 0 {
		t.Fatalf("wiped Dairy count = %+v", got)
	}
	if got := categoryTarget(wiped, "Dairy"); got == nil || got.Kind != restock.KindAmount || got.Value != 2 {
		t.Fatalf("wiped Dairy target = %+v", got)
	}

	insertPendingScan(env, "scan-milk-again", "prod-milk", scan.StockIn)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-milk-again/commit", ``, http.StatusOK)

	back := mustRestock(t, handler)
	view := restockByProduct(back, "prod-milk")
	if view == nil || view.Target.Kind != restock.KindAmount || view.Target.Value != 4 || view.Level != restock.LevelItem {
		t.Fatalf("stocking Milk in again did not bring back its saved target of 4: %+v", view)
	}
	if back.ItemCount != 1 {
		t.Fatalf("itemCount after restock = %d", back.ItemCount)
	}
	if got := categoryNamed(back, "Dairy"); got == nil || got.Items != 1 || got.OwnTargets != 1 {
		t.Fatalf("Dairy count after restock = %+v", got)
	}
}

// Category counts and the mobile sync snapshot follow the same hiding rule
// as inventory and restock: a wiped product is gone until it is stocked in.
func TestWipedProductLeavesCategoryCountsAndTheSyncSnapshot(t *testing.T) {
	const token = "sync-token-value-ok"
	handler, env := setupTestWithContributor(t, nil, WithSyncToken(token))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-milk", "Milk", "item-milk")
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-milk", `{"kind":"amount","value":4}`, http.StatusOK)
	dairy := decodeID(t, exchangeJSON(t, handler, http.MethodPost, "/api/restock/categories", `{"name":"Dairy"}`, http.StatusOK))
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/categories/"+dairy+"/target", `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/restock/items/item-milk/categories", `{"categoryId":"`+dairy+`"}`, http.StatusOK)

	before := mustRestock(t, handler)
	if got := categoryNamed(before, "Dairy"); got == nil || got.Items != 1 {
		t.Fatalf("setup Dairy count = %+v", got)
	}
	if !syncSnapshotShowsProduct(syncSnapshot(t, handler, token), "prod-milk") {
		t.Fatal("setup: the sync snapshot did not include Milk")
	}

	exchangeJSON(t, handler, http.MethodPost, "/api/inventory/wipe", `{"confirmation":"WIPE INVENTORY"}`, http.StatusOK)

	wipedRestock := mustRestock(t, handler)
	if got := categoryNamed(wipedRestock, "Dairy"); got == nil || got.Items != 0 {
		t.Fatalf("Dairy still counts Milk after a wipe: %+v", got)
	}
	wipedSnap := syncSnapshot(t, handler, token)
	if syncSnapshotShowsProduct(wipedSnap, "prod-milk") {
		t.Fatalf("sync snapshot still has Milk after a wipe: %+v", wipedSnap)
	}

	insertPendingScan(env, "scan-milk-sync", "prod-milk", scan.StockIn)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-milk-sync/commit", ``, http.StatusOK)

	back := mustRestock(t, handler)
	if got := categoryNamed(back, "Dairy"); got == nil || got.Items != 1 {
		t.Fatalf("Dairy did not count Milk again after restock: %+v", got)
	}
	shown := syncSnapshot(t, handler, token)
	if !syncSnapshotShowsProduct(shown, "prod-milk") {
		t.Fatalf("sync snapshot did not show Milk after restock: %+v", shown)
	}
	product := syncProductByID(shown, "prod-milk")
	if product == nil || product.TargetQuantity == nil || *product.TargetQuantity != 4 {
		t.Fatalf("restocked Milk snapshot target = %+v", product)
	}
}

type syncSnapshotBody struct {
	Products []struct {
		ID             string `json:"id"`
		TargetQuantity *int   `json:"targetQuantity"`
	} `json:"products"`
	Instances []struct {
		ProductID string `json:"productId"`
	} `json:"instances"`
	Moves []struct {
		ProductID string `json:"productId"`
	} `json:"moves"`
	ShoppingList []struct {
		ProductID string `json:"productId"`
	} `json:"shoppingList"`
	Removals []struct {
		ProductID string `json:"productId"`
	} `json:"removals"`
}

func syncSnapshot(t *testing.T, handler http.Handler, token string) syncSnapshotBody {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/sync/snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync snapshot status %d: %s", rec.Code, rec.Body.String())
	}
	var snap syncSnapshotBody
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("sync snapshot: %v body %s", err, rec.Body.String())
	}
	return snap
}

func syncProductByID(snap syncSnapshotBody, productID string) *struct {
	ID             string
	TargetQuantity *int
} {
	for i := range snap.Products {
		if snap.Products[i].ID == productID {
			return &struct {
				ID             string
				TargetQuantity *int
			}{ID: snap.Products[i].ID, TargetQuantity: snap.Products[i].TargetQuantity}
		}
	}
	return nil
}

// A wiped product is absent when the snapshot has none of its stock, history,
// shopping lines, or item target. The catalog name can remain. Restocking
// brings the target and a unit back.
func syncSnapshotShowsProduct(snap syncSnapshotBody, productID string) bool {
	if product := syncProductByID(snap, productID); product != nil && product.TargetQuantity != nil {
		return true
	}
	for _, row := range snap.Instances {
		if row.ProductID == productID {
			return true
		}
	}
	for _, row := range snap.Moves {
		if row.ProductID == productID {
			return true
		}
	}
	for _, row := range snap.ShoppingList {
		if row.ProductID == productID {
			return true
		}
	}
	for _, row := range snap.Removals {
		if row.ProductID == productID {
			return true
		}
	}
	return false
}

func categoryNamed(snap restock.Snapshot, name string) *restock.CategorySummary {
	for i := range snap.Categories {
		if snap.Categories[i].Name == name {
			return &snap.Categories[i]
		}
	}
	return nil
}
