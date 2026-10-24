package shopping

import (
	"testing"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestRetailerDealStatus(t *testing.T) {
	status, detail := (RetailerDealConfig{}).Status()
	if status != RetailerDealsUnavailable || detail == "" {
		t.Fatalf("unconfigured status = %q %q", status, detail)
	}
	status, detail = (RetailerDealConfig{APIKey: "secret", BaseURL: "https://example.invalid"}).Status()
	if status != RetailerDealsStubbed || detail == "" {
		t.Fatalf("keyed status = %q %q", status, detail)
	}
	scope, err := tenant.New("0190aaaa-bbbb-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	deals, err := (RetailerDealConfig{APIKey: "secret"}).LiveDeals(t.Context(), scope, []string{"item"})
	if err != nil || len(deals) != 0 {
		t.Fatalf("live deals = %+v, %v; a key must not invent prices", deals, err)
	}
}
