package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		_, err := loadShoppingSnapshot(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadShoppingSnapshot: %v", err)
		}
	}
	{
		_, err := stageShoppingPlan(context.Background(), tenant.Scope{}, nil, nil, nil, time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("stageShoppingPlan: %v", err)
		}
	}
}
