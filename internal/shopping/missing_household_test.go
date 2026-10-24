package shopping

import (
	"context"
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		_, err := (*Store)(nil).AddManualItem(context.Background(), tenant.Scope{}, "", 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("AddManualItem: %v", err)
		}
	}
	{
		err := (*Store)(nil).ClearAdjustmentTx(context.Background(), tenant.Scope{}, nil, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ClearAdjustmentTx: %v", err)
		}
	}
	{
		err := (*Store)(nil).DeleteDeal(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("DeleteDeal: %v", err)
		}
	}
	{
		err := (*Store)(nil).EndTrip(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("EndTrip: %v", err)
		}
	}
	{
		_, err := (*Store)(nil).GetAdjustment(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("GetAdjustment: %v", err)
		}
	}
	{
		_, err := (*Store)(nil).ItemGroups(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ItemGroups: %v", err)
		}
	}
	{
		_, err := (*Store)(nil).ListDeals(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListDeals: %v", err)
		}
	}
	{
		_, err := (*Store)(nil).ListManualItems(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListManualItems: %v", err)
		}
	}
	{
		_, err := (*Store)(nil).ListUnpurchased(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListUnpurchased: %v", err)
		}
	}
	{
		_, err := RetailerDealConfig{}.LiveDeals(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("LiveDeals: %v", err)
		}
	}
	{
		err := (*Store)(nil).MarkPurchased(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("MarkPurchased: %v", err)
		}
	}
	{
		err := (*Store)(nil).RemoveAdjustment(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("RemoveAdjustment: %v", err)
		}
	}
	{
		err := (*Store)(nil).RemoveItem(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("RemoveItem: %v", err)
		}
	}
	{
		err := (*Store)(nil).SaveDeal(context.Background(), tenant.Scope{}, Deal{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SaveDeal: %v", err)
		}
	}
	{
		err := (*Store)(nil).SavePlannedLines(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SavePlannedLines: %v", err)
		}
	}
	{
		err := (*Store)(nil).SetAdjustment(context.Background(), tenant.Scope{}, "", "", 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SetAdjustment: %v", err)
		}
	}
	{
		err := (*Store)(nil).SwapAutoLine(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SwapAutoLine: %v", err)
		}
	}
	{
		err := deleteShoppingRow(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("deleteShoppingRow: %v", err)
		}
	}
	{
		err := deleteUnplannedAuto(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("deleteUnplannedAuto: %v", err)
		}
	}
	{
		_, err := (*Store)(nil).fillGroupMembers(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("fillGroupMembers: %v", err)
		}
	}
	{
		_, err := (*Store)(nil).getByID(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("getByID: %v", err)
		}
	}
	{
		_, _, err := listSkippedItems(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("listSkippedItems: %v", err)
		}
	}
	{
		_, _, err := listTouchedAutoItems(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("listTouchedAutoItems: %v", err)
		}
	}
	{
		_, err := stockInTimes(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("stockInTimes: %v", err)
		}
	}
	{
		err := upsertAutoLine(context.Background(), tenant.Scope{}, nil, "", "", 0, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("upsertAutoLine: %v", err)
		}
	}
}
