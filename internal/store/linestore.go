package store

import (
	"context"
	"database/sql"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// LineStoreContext holds household store data used to resolve which store
// a shopping line belongs to and whether that store can send to an online cart.
type LineStoreContext struct {
	stores    []storeRow
	usual     map[string]string
	excluded  map[string]map[string]bool
	defaultID *string
}

// LoadLineStoreContext reads store routing data for one household.
func LoadLineStoreContext(ctx context.Context, db *sql.DB, scope tenant.Scope) (*LineStoreContext, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	c := &Catalog{db: db}
	hasStoresTable, err := c.hasMultiStoreSchema(ctx)
	if err != nil {
		return nil, err
	}
	if !hasStoresTable {
		return &LineStoreContext{}, nil
	}
	stores, err := c.loadStoreRows(ctx, scope)
	if err != nil {
		return nil, err
	}
	usual, err := c.loadUsualStores(ctx, scope)
	if err != nil {
		return nil, err
	}
	excluded, err := c.loadExcluded(ctx, scope)
	if err != nil {
		return nil, err
	}
	defaultID, err := c.readDefaultStoreID(ctx, scope)
	if err != nil {
		return nil, err
	}
	return &LineStoreContext{
		stores:    stores,
		usual:     usual,
		excluded:  excluded,
		defaultID: defaultID,
	}, nil
}

// HasStores is false for households with no configured stores.
func (c *LineStoreContext) HasStores() bool {
	return c != nil && len(c.stores) > 0
}

// LineStore resolves the store id for a line from run override and product defaults.
func (c *LineStoreContext) LineStore(runStoreID *string, productID string) string {
	if c == nil || !c.HasStores() {
		return ""
	}
	line := openLine{runStoreID: runStoreID, productID: productID}
	return effectiveStore(line, c.stores, c.usual, c.excluded, c.defaultID)
}

// ProviderRegistered reports whether providerID is registered on this server.
type ProviderRegistered func(providerID string) bool

// CanSendToCart reports whether a line resolved to storeID may use providerID
// for cart ledger and provisioning. The line's store must be linked to providerID,
// or the line is store-less and the household has exactly one linked provider.
// Households with no store catalog keep the legacy behavior (every line may send).
func CanSendToCart(c *LineStoreContext, storeID, providerID string) bool {
	if providerID == "" {
		return false
	}
	if c == nil || !c.HasStores() {
		return true
	}
	if storeID == "" {
		linked := c.linkedProviders()
		if len(linked) != 1 {
			return false
		}
		return linked[0] == providerID
	}
	for _, st := range c.stores {
		if st.id != storeID {
			continue
		}
		return st.linked != nil && *st.linked == providerID
	}
	return false
}

// CanSendToCartForExport is CanSendToCart with a registered-provider guard for export.
func CanSendToCartForExport(c *LineStoreContext, storeID, providerID string, registered ProviderRegistered) bool {
	if providerID == "" || registered == nil || !registered(providerID) {
		return false
	}
	if c == nil || !c.HasStores() {
		return true
	}
	if storeID == "" {
		linked := c.registeredLinkedProviders(registered)
		if len(linked) != 1 {
			return false
		}
		return linked[0] == providerID
	}
	for _, st := range c.stores {
		if st.id != storeID {
			continue
		}
		return st.linked != nil && registered(*st.linked) && *st.linked == providerID
	}
	return false
}

func (c *LineStoreContext) linkedProviders() []string {
	var out []string
	seen := map[string]bool{}
	for _, st := range c.stores {
		if st.linked == nil {
			continue
		}
		p := *st.linked
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func (c *LineStoreContext) registeredLinkedProviders(registered ProviderRegistered) []string {
	var out []string
	seen := map[string]bool{}
	for _, st := range c.stores {
		if st.linked == nil {
			continue
		}
		p := *st.linked
		if !registered(p) || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
