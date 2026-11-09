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

// ProviderGate collects registry and credential checks shared by GET and export.
type ProviderGate struct {
	Registered     ProviderRegistered
	Configured     func(providerID string) bool
	SoleConfigured func() (providerID string, ok bool)
}

// CanSendToCart reports whether a line resolved to storeID may use providerID
// for cart ledger and provisioning. When the line's store is linked, it must
// match providerID. Store-less lines send when exactly one store links to the
// provider, or when no store is linked and the household has exactly one
// configured, registered provider. Households with no store catalog keep
// legacy behavior (every registered line may send).
func CanSendToCart(c *LineStoreContext, storeID, providerID string, gate ProviderGate) bool {
	if providerID == "" {
		return false
	}
	if c == nil || !c.HasStores() {
		return true
	}
	if gate.Registered == nil || !gate.Registered(providerID) {
		return false
	}
	if gate.Configured != nil && !gate.Configured(providerID) {
		return false
	}
	if storeID != "" {
		for _, st := range c.stores {
			if st.id != storeID {
				continue
			}
			if st.linked == nil {
				return false
			}
			return *st.linked == providerID
		}
		return false
	}
	linked := c.registeredLinkedProviders(gate.Registered)
	if len(linked) > 1 {
		return false
	}
	if len(linked) == 1 {
		return linked[0] == providerID
	}
	if gate.SoleConfigured == nil {
		return false
	}
	sole, ok := gate.SoleConfigured()
	return ok && sole == providerID
}

func (c *LineStoreContext) registeredLinkedProviders(registered ProviderRegistered) []string {
	var out []string
	seen := map[string]bool{}
	for _, st := range c.stores {
		if st.linked == nil {
			continue
		}
		p := *st.linked
		if registered != nil && !registered(p) {
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
