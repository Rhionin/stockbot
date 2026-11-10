package server

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/store"
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
	Credentials *appcred.Vault
	DB        *sql.DB
}

func (h *ShoppingListGetHandler) Handle(req Request[struct{}, struct{}]) (changeView, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return changeView{}, InternalError(err)
	}

	provision, err := loadShoppingSnapshot(req.Context, scope, h.Pantry, h.ShoppingList)
	if err != nil {
		return changeView{}, InternalError(err)
	}
	lineCtx, err := loadLineStoreContext(req.Context, h.DB, scope)
	if err != nil {
		return changeView{}, InternalError(err)
	}
	items, err := presentShoppingList(req, scope, provision, h.Ledger, h.Registry, h.Adjustments, lineCtx, h.Credentials)
	if err != nil {
		return changeView{}, err
	}
	if items == nil {
		items = []ShoppingListEntryResponse{}
	}
	since := ""
	if req.RawRequest != nil {
		since = strings.TrimSpace(req.RawRequest.URL.Query().Get("since"))
	}
	view, err := viewChanges(req.Context, h.DB, scope.HouseholdID(), "list", since, "items", items)
	if err != nil {
		return changeView{}, InternalError(err)
	}
	return view, nil
}

func presentShoppingList(req Request[struct{}, struct{}], scope tenant.Scope, provision shoppingProvision, ledgerStore *cart.Ledger, registry *cart.Registry, adjustments interface {
	GetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) (*shopping.Adjustment, error)
}, lineCtx *store.LineStoreContext, credentials *appcred.Vault) ([]ShoppingListEntryResponse, error) {
	providerID := targetProviderID(req, scope, registry, credentials)
	itemByID := make(map[string]inventory.Item, len(provision.Items))
	products := productIDByItemID(provision.Items)
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

	gate := providerGate(req.Context, scope, registry, credentials)
	resp := make([]ShoppingListEntryResponse, 0, len(rows))
	for _, row := range rows {
		cartLine := lineCanSendToCart(lineCtx, row, products[row.ItemID], providerID, nil, gate)
		computed := row.Quantity
		lineProvider := ""
		if cartLine && providerID != "" {
			lineProvider = providerID
			if requested := ledger[row.ItemID].Requested; requested > 0 {
				computed -= requested
				if computed < 0 {
					computed = 0
				}
			}
		}
		provisionQty := computed
		var adjustment *int
		if cartLine && providerID != "" && row.ID != "" && adjustments != nil {
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
			Provider:         lineProvider,
			ComputedQuantity: computed,
			Adjustment:       adjustment,
			Group:            shoppingGroupResponse(&row),
		}
		if !cartLine {
			entry.ComputedQuantity = row.Quantity
			entry.Quantity = row.Quantity
		}
		resp = append(resp, entry)
	}
	return resp, nil
}

func targetProviderID(req Request[struct{}, struct{}], scope tenant.Scope, registry *cart.Registry, credentials *appcred.Vault) string {
	if req.RawRequest != nil {
		if provider := req.RawRequest.URL.Query().Get("provider"); provider != "" {
			return provider
		}
	}
	if id, ok := soleHouseholdConfiguredProvider(req.Context, scope, registry, credentials); ok {
		return id
	}
	return ""
}
