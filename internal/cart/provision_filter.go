package cart

import "context"

type provisionFilterKey struct{}

// ProvisionFilter limits which shopping lines Provision may send.
type ProvisionFilter struct {
	CanSendToCart func(itemID, productID string, runStoreID *string) bool
	ScopedItemIDs map[string]struct{} // nil exports every sendable line
}

// WithProvisionFilter attaches line filtering to ctx for Engine.Provision.
func WithProvisionFilter(ctx context.Context, filter *ProvisionFilter) context.Context {
	if filter == nil {
		return ctx
	}
	return context.WithValue(ctx, provisionFilterKey{}, filter)
}

// ProvisionFilterFromContext returns filtering attached by WithProvisionFilter.
func ProvisionFilterFromContext(ctx context.Context) (*ProvisionFilter, bool) {
	filter, ok := ctx.Value(provisionFilterKey{}).(*ProvisionFilter)
	if !ok || filter == nil {
		return nil, false
	}
	return filter, true
}
