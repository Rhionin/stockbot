package cart

import "github.com/Rhionin/stockbot/internal/tenant"

// HouseholdAdapterResolver returns the grocery provider adapter for a household.
// Today every household uses the same server-wide adapter; per-household
// resolution can replace this implementation later.
type HouseholdAdapterResolver interface {
	Adapter(scope tenant.Scope, providerID ProviderID) (Provider, bool)
}

// ServerRegistryResolver serves the shared registry adapter for every household.
type ServerRegistryResolver struct {
	Registry *Registry
}

// Adapter implements HouseholdAdapterResolver.
func (r ServerRegistryResolver) Adapter(_ tenant.Scope, providerID ProviderID) (Provider, bool) {
	if r.Registry == nil {
		return nil, false
	}
	return r.Registry.Get(providerID)
}
