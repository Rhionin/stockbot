package syncsnap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		_, err := Build(context.Background(), tenant.Scope{}, nil, time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Build: %v", err)
		}
	}
	{
		err := applyResolvedRestock(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("applyResolvedRestock: %v", err)
		}
	}
	{
		err := attachBarcodes(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachBarcodes: %v", err)
		}
	}
	{
		err := attachItemTargets(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachItemTargets: %v", err)
		}
	}
	{
		err := attachItems(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachItems: %v", err)
		}
	}
	{
		err := attachMembers(context.Background(), tenant.Scope{}, nil, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachMembers: %v", err)
		}
	}
	{
		_, err := loadGroups(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadGroups: %v", err)
		}
	}
	{
		_, err := loadInstances(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadInstances: %v", err)
		}
	}
	{
		_, err := loadMoves(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadMoves: %v", err)
		}
	}
	{
		_, err := loadRemovals(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadRemovals: %v", err)
		}
	}
	{
		_, err := loadShopping(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadShopping: %v", err)
		}
	}
}
