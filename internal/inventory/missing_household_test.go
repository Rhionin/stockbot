package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		_, err := (*Pantry)(nil).AddInstance(context.Background(), tenant.Scope{}, ItemInstance{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("AddInstance: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).GetInstance(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("GetInstance: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).GetInventoryItem(context.Background(), tenant.Scope{}, "", time.Time{}, 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("GetInventoryItem: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).GetInventoryList(context.Background(), tenant.Scope{}, time.Time{}, 0, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("GetInventoryList: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).GetItem(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("GetItem: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).GetOrCreateItem(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("GetOrCreateItem: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).GetReplenishmentMode(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("GetReplenishmentMode: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).GetTargetQuantity(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("GetTargetQuantity: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).List(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("List: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).ListItemInstances(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListItemInstances: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).ListItems(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListItems: %v", err)
		}
	}
	{
		err := (*Pantry)(nil).RemoveInstance(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("RemoveInstance: %v", err)
		}
	}
	{
		err := UnshelveItem(context.Background(), nil, tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("UnshelveItem: %v", err)
		}
	}
	{
		err := (*Pantry)(nil).UpdateReplenishmentMode(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("UpdateReplenishmentMode: %v", err)
		}
	}
	{
		err := (*Pantry)(nil).UpdateTargetQuantity(context.Background(), tenant.Scope{}, "", 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("UpdateTargetQuantity: %v", err)
		}
	}
	{
		err := (*Pantry)(nil).attachBarcodes(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachBarcodes: %v", err)
		}
	}
	{
		err := (*Pantry)(nil).attachGroups(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachGroups: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).getItemByID(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("getItemByID: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).getItemByProduct(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("getItemByProduct: %v", err)
		}
	}
	{
		_, err := (*Pantry)(nil).groupSummaries(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("groupSummaries: %v", err)
		}
	}
}
