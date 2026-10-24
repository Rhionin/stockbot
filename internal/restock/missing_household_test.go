package restock

import (
	"context"
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		err := (*Targets)(nil).ApplySuggestions(context.Background(), tenant.Scope{}, "", nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ApplySuggestions: %v", err)
		}
	}
	{
		err := (*Targets)(nil).ClearItem(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ClearItem: %v", err)
		}
	}
	{
		err := CopyLegacy(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("CopyLegacy: %v", err)
		}
	}
	{
		_, err := (*Targets)(nil).CreateCategory(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("CreateCategory: %v", err)
		}
	}
	{
		err := (*Targets)(nil).DeleteCategory(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("DeleteCategory: %v", err)
		}
	}
	{
		err := DeleteItem(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("DeleteItem: %v", err)
		}
	}
	{
		err := (*Targets)(nil).DismissSuggestions(context.Background(), tenant.Scope{}, "", nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("DismissSuggestions: %v", err)
		}
	}
	{
		_, err := (*Targets)(nil).ItemIDByProduct(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ItemIDByProduct: %v", err)
		}
	}
	{
		_, err := (*Targets)(nil).ItemTarget(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ItemTarget: %v", err)
		}
	}
	{
		_, err := Load(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Load: %v", err)
		}
	}
	{
		err := (*Targets)(nil).MergeCategory(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("MergeCategory: %v", err)
		}
	}
	{
		_, err := (*Targets)(nil).PreviewCategory(context.Background(), tenant.Scope{}, "", Target{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("PreviewCategory: %v", err)
		}
	}
	{
		_, err := (*Targets)(nil).PreviewGroup(context.Background(), tenant.Scope{}, "", Target{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("PreviewGroup: %v", err)
		}
	}
	{
		err := (*Targets)(nil).RenameCategory(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("RenameCategory: %v", err)
		}
	}
	{
		err := (*Targets)(nil).SetCategoryTarget(context.Background(), tenant.Scope{}, "", nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SetCategoryTarget: %v", err)
		}
	}
	{
		err := (*Targets)(nil).SetHousehold(context.Background(), tenant.Scope{}, Target{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SetHousehold: %v", err)
		}
	}
	{
		err := (*Targets)(nil).SetItem(context.Background(), tenant.Scope{}, "", Target{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SetItem: %v", err)
		}
	}
	{
		_, err := (*Targets)(nil).Snapshot(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Snapshot: %v", err)
		}
	}
	{
		_, err := (*Targets)(nil).Suggestions(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Suggestions: %v", err)
		}
	}
	{
		err := attachCategories(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachCategories: %v", err)
		}
	}
	{
		err := attachStoredTargets(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachStoredTargets: %v", err)
		}
	}
	{
		_, err := (*Targets)(nil).categoryByName(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("categoryByName: %v", err)
		}
	}
	{
		_, err := categoryName(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("categoryName: %v", err)
		}
	}
	{
		err := ensureCategory(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ensureCategory: %v", err)
		}
	}
	{
		err := ensureItem(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ensureItem: %v", err)
		}
	}
	{
		err := (*Targets)(nil).ensureStarters(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ensureStarters: %v", err)
		}
	}
	{
		_, err := loadBarcodes(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadBarcodes: %v", err)
		}
	}
	{
		_, err := loadCategoryRows(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadCategoryRows: %v", err)
		}
	}
	{
		_, err := loadDismissals(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadDismissals: %v", err)
		}
	}
	{
		_, err := loadItems(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadItems: %v", err)
		}
	}
	{
		_, err := loadRemoved(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadRemoved: %v", err)
		}
	}
	{
		err := (*Targets)(nil).membership(context.Background(), tenant.Scope{}, "", "", "", false)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("membership: %v", err)
		}
	}
	{
		_, err := suggest(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("suggest: %v", err)
		}
	}
}
