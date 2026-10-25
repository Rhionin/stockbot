package scan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		err := (*Queue)(nil).BatchUpdateScanEntries(context.Background(), tenant.Scope{}, nil, nil, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("BatchUpdateScanEntries: %v", err)
		}
	}
	{
		_, err := (*Queue)(nil).CommitBackfill(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("CommitBackfill: %v", err)
		}
	}
	{
		err := (*Queue)(nil).CommitScanEntry(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("CommitScanEntry: %v", err)
		}
	}
	{
		err := (*Queue)(nil).CommitStockIn(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("CommitStockIn: %v", err)
		}
	}
	{
		_, err := (*Queue)(nil).CommitStockOut(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("CommitStockOut: %v", err)
		}
	}
	{
		_, err := (*Queue)(nil).CommitStockOutLink(context.Background(), tenant.Scope{}, nil, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("CommitStockOutLink: %v", err)
		}
	}
	{
		_, err := (*Queue)(nil).CountOnHand(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("CountOnHand: %v", err)
		}
	}
	{
		_, err := (*Queue)(nil).CreateScanEntry(context.Background(), tenant.Scope{}, ScanEntry{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("CreateScanEntry: %v", err)
		}
	}
	{
		_, err := (*Queue)(nil).GetScanEntry(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("GetScanEntry: %v", err)
		}
	}
	{
		_, err := (*Queue)(nil).LinkStockOutProduct(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("LinkStockOutProduct: %v", err)
		}
	}
	{
		_, err := (*Queue)(nil).LinkSuggestions(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("LinkSuggestions: %v", err)
		}
	}
	{
		_, err := (*Queue)(nil).ListScanEntries(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListScanEntries: %v", err)
		}
	}
	{
		err := (*Queue)(nil).ResolveFlaggedEntry(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ResolveFlaggedEntry: %v", err)
		}
	}
	{
		_, err := (*Queue)(nil).StockIn(context.Background(), tenant.Scope{}, "", time.Time{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("StockIn: %v", err)
		}
	}
	{
		_, err := (*Queue)(nil).StockOut(context.Background(), tenant.Scope{}, "", time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("StockOut: %v", err)
		}
	}
	{
		err := (*Queue)(nil).StockOutInstance(context.Background(), tenant.Scope{}, "", time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("StockOutInstance: %v", err)
		}
	}
	{
		err := (*Queue)(nil).UpdateScanEntry(context.Background(), tenant.Scope{}, "", nil, nil, nil, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("UpdateScanEntry: %v", err)
		}
	}
}
