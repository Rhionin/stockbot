package product

import (
	"context"
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		_, err := (*Catalog)(nil).BarcodesForProduct(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("BarcodesForProduct: %v", err)
		}
	}
	{
		_, err := (*Catalog)(nil).ContributionEnabled(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ContributionEnabled: %v", err)
		}
	}
	{
		_, err := (*Catalog)(nil).CreateTypedProduct(context.Background(), tenant.Scope{}, Product{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("CreateTypedProduct: %v", err)
		}
	}
	{
		err := (*Catalog)(nil).InsertContribution(context.Background(), tenant.Scope{}, ContributionRecord{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("InsertContribution: %v", err)
		}
	}
	{
		_, err := (*Catalog)(nil).ListBarcodesForProduct(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListBarcodesForProduct: %v", err)
		}
	}
	{
		_, err := (*Catalog)(nil).ListContributions(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListContributions: %v", err)
		}
	}
	{
		_, err := (*Catalog)(nil).ListProducts(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListProducts: %v", err)
		}
	}
	{
		_, err := (*Catalog)(nil).LookupBarcode(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("LookupBarcode: %v", err)
		}
	}
	{
		_, err := (*Catalog)(nil).ProductFor(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ProductFor: %v", err)
		}
	}
	{
		err := (*Catalog)(nil).SaveDisplayName(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SaveDisplayName: %v", err)
		}
	}
	{
		err := (*Catalog)(nil).SetContributionEnabled(context.Background(), tenant.Scope{}, false)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SetContributionEnabled: %v", err)
		}
	}
	{
		err := (*Catalog)(nil).UpsertHouseholdBarcode(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("UpsertHouseholdBarcode: %v", err)
		}
	}
}
