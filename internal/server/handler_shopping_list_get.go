package server

import (
	"context"
	"sort"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ShoppingListGetHandler handles GET /api/shopping-list.
// It returns the staged cart. Filling the cart is what snapshots the supply plan.
type ShoppingListGetHandler struct {
	ShoppingList shoppingListReader
	Pantry       pantryLister
	Ledger       *cart.Ledger
	Registry     *cart.Registry
	// GetAdjustment is separate so tests can satisfy shoppingListReader
	// without pulling adjustment methods onto every list reader.
	Adjustments interface {
		GetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) (*shopping.Adjustment, error)
	}
}

func (h *ShoppingListGetHandler) Handle(req Request[struct{}, struct{}]) ([]ShoppingListEntryResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}

	provision, err := loadShoppingSnapshot(req.Context, scope, h.Pantry, h.ShoppingList)
	if err != nil {
		return nil, InternalError(err)
	}
	return presentShoppingList(req, scope, provision, h.Ledger, h.Registry, h.Adjustments)
}

func presentShoppingList(req Request[struct{}, struct{}], scope tenant.Scope, provision shoppingProvision, ledgerStore *cart.Ledger, registry *cart.Registry, adjustments interface {
	GetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) (*shopping.Adjustment, error)
}) ([]ShoppingListEntryResponse, error) {
	providerID := targetProviderID(req, registry)
	itemByID := make(map[string]inventory.Item, len(provision.Items))
	for _, item := range provision.Items {
		itemByID[item.ID] = item
	}

	rows := append([]shopping.ShoppingListItem(nil), provision.Rows...)
	sort.Slice(rows, func(i, j int) bool {
		left := itemByID[rows[i].ItemID].ProductID
		right := itemByID[rows[j].ItemID].ProductID
		if left != right {
			return left < right
		}
		return rows[i].ItemID < rows[j].ItemID
	})

	var err error
	ledger := map[string]cart.LedgerEntry{}
	if ledgerStore != nil && providerID != "" {
		ledger, err = ledgerStore.ListForProvider(req.Context, scope, cart.ProviderID(providerID))
		if err != nil {
			return nil, InternalError(err)
		}
	}

	resp := make([]ShoppingListEntryResponse, 0, len(rows))
	for _, row := range rows {
		computed := row.Quantity
		if requested := ledger[row.ItemID].Requested; requested > 0 {
			computed -= requested
			if computed < 0 {
				computed = 0
			}
		}
		provisionQty := computed
		var adjustment *int
		if providerID != "" && row.ID != "" && adjustments != nil {
			adj, adjErr := adjustments.GetAdjustment(req.Context, scope, row.ID, providerID)
			if adjErr != nil {
				return nil, InternalError(adjErr)
			}
			if adj != nil {
				adjustment = &adj.Quantity
				provisionQty = adj.Quantity
			}
		}
		entry := ShoppingListEntryResponse{
			ID:               row.ID,
			ItemID:           row.ItemID,
			Quantity:         provisionQty,
			Source:           row.Source,
			Note:             row.Note,
			Provider:         providerID,
			ComputedQuantity: computed,
			Adjustment:       adjustment,
			Group:            shoppingGroupResponse(&row),
		}
		resp = append(resp, entry)
	}
	return resp, nil
}

func targetProviderID(req Request[struct{}, struct{}], registry *cart.Registry) string {
	if req.RawRequest != nil {
		if provider := req.RawRequest.URL.Query().Get("provider"); provider != "" {
			return provider
		}
	}
	if registry != nil {
		if id, ok := registry.SoleConfigured(); ok {
			return string(id)
		}
	}
	return ""
}
