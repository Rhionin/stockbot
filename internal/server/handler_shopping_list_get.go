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
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ShoppingListGetHandler handles GET /api/shopping-list.
type ShoppingListGetHandler struct {
	ShoppingList shoppingListReader
	Pantry       pantryLister
	Ledger       *cart.Ledger
	Registry     *cart.Registry
	Adjustments  interface {
		GetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) (*shopping.Adjustment, error)
	}
	Credentials  *appcred.Vault
	DB           *sql.DB
	ReadDB       *sql.DB
	Supply       *supply.Service
	Coalescer    *shopping.ListCoalescer
	Shopping     *shopping.Store
}

func (h *ShoppingListGetHandler) Handle(req Request[struct{}, struct{}]) (changeView, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return changeView{}, InternalError(err)
	}
	statusMode := req.RawRequest != nil && strings.TrimSpace(req.RawRequest.URL.Query().Get("status")) == "1"
	since := ""
	if req.RawRequest != nil {
		since = strings.TrimSpace(req.RawRequest.URL.Query().Get("since"))
	}
	asm := listAssembler{
		Coalescer: h.Coalescer, Supply: h.Supply, SupplyRead: supply.Open(h.ReadDB),
		Shopping: h.Shopping, Pantry: h.Pantry, Ledger: h.Ledger, Registry: h.Registry,
		Adjustments: h.Adjustments, Credentials: h.Credentials, DB: h.DB, ReadDB: h.ReadDB,
	}
	if h.Shopping == nil {
		if s, ok := h.ShoppingList.(*shopping.Store); ok {
			asm.Shopping = s
		}
	}
	built, err := asm.assembleShoppingListW3(req, scope, statusMode)
	if err != nil {
		return changeView{}, err
	}
	if built.Items == nil {
		built.Items = []ShoppingListEntryResponse{}
	}
	if statusMode && since == "" {
		body := ShoppingListStatusResponse{
			Cursor: built.Cursor, ListStale: built.ListStale, Items: built.Items,
			OffList: built.OffList, Stores: built.Stores, AttentionGroups: built.AttentionGroups,
			StoreDisabled: built.StoreDisabled, LastTripEndedAt: built.LastTripEndedAt, LastTripEndedBy: built.LastTripEndedBy,
		}
		return changeView{Items: body, StatusEnvelope: true}, nil
	}
	view, err := viewChanges(req.Context, h.DB, scope.HouseholdID(), "list", since, "items", built.Items)
	if err != nil {
		return changeView{}, InternalError(err)
	}
	if statusMode && view.Changed && since != "" {
		body := ShoppingListStatusResponse{
			Cursor: built.Cursor, ListStale: built.ListStale, Items: built.Items,
			OffList: built.OffList, Stores: built.Stores, AttentionGroups: built.AttentionGroups,
			StoreDisabled: built.StoreDisabled, LastTripEndedAt: built.LastTripEndedAt, LastTripEndedBy: built.LastTripEndedBy,
		}
		view.Items = body
		view.StatusEnvelope = true
		view.Key = "envelope"
	}
	if !statusMode {
		view.ListStale = built.ListStale
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
			ID: row.ID, ItemID: row.ItemID, Quantity: provisionQty, Source: row.Source, Note: row.Note,
			Provider: lineProvider, ComputedQuantity: computed, Adjustment: adjustment,
			Group: shoppingGroupResponse(&row),
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