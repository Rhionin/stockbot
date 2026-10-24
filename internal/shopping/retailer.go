package shopping

import (
	"context"
	"strings"

	"github.com/Rhionin/stockbot/internal/tenant"
)

const (
	// RetailerDealsUnavailable means no store API key is configured.
	RetailerDealsUnavailable = "unavailable"
	// RetailerDealsStubbed means a key is set but no store adapter is connected,
	// so live prices are not fetched.
	RetailerDealsStubbed = "stubbed"
)

// RetailerDealConfig is the gate for live store prices.
// APIKey comes from PANTRY_RETAILER_API_KEY. BaseURL is reserved for the
// adapter (PANTRY_RETAILER_API_URL) and is unused until a retailer is chosen.
type RetailerDealConfig struct {
	APIKey  string
	BaseURL string
}

// Status tells the shopper whether live prices can affect the cart.
// Recorded sales apply in both states.
func (c RetailerDealConfig) Status() (string, string) {
	if strings.TrimSpace(c.APIKey) == "" {
		return RetailerDealsUnavailable, "Live store prices aren't connected. Sales you note yourself still show up here."
	}
	return RetailerDealsStubbed, "A retailer key is set, but live store prices aren't available yet. Sales you note yourself still show up here."
}

// LiveDeals would ask a store for current prices. No retailer adapter is
// connected — a key alone must not invent prices or block cart building.
func (c RetailerDealConfig) LiveDeals(_ context.Context, scope tenant.Scope, _ []string) ([]Deal, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	return nil, nil
}
