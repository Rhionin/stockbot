package suggestion

import (
	"context"
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		_, err := (*ConsumptionLog)(nil).InsertConsumptionEvent(context.Background(), tenant.Scope{}, ConsumptionEvent{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("InsertConsumptionEvent: %v", err)
		}
	}
	{
		_, err := (*ConsumptionLog)(nil).ListConsumedAtByItems(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListConsumedAtByItems: %v", err)
		}
	}
	{
		_, err := (*ConsumptionLog)(nil).ListConsumptionEvents(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListConsumptionEvents: %v", err)
		}
	}
	{
		_, err := (*ConsumptionLog)(nil).getByID(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("getByID: %v", err)
		}
	}
}
