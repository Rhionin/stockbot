package push

import (
	"context"
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		err := (*Subscriptions)(nil).Delete(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Delete: %v", err)
		}
	}
	{
		err := (*Subscriptions)(nil).DeleteBatchAlert(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("DeleteBatchAlert: %v", err)
		}
	}
	{
		_, err := (*Subscriptions)(nil).DeleteHost(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("DeleteHost: %v", err)
		}
	}
	{
		_, err := (*Subscriptions)(nil).List(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("List: %v", err)
		}
	}
	{
		_, err := (*Subscriptions)(nil).ListBatchAlerts(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListBatchAlerts: %v", err)
		}
	}
	{
		err := (*Subscriptions)(nil).Save(context.Background(), tenant.Scope{}, Subscription{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Save: %v", err)
		}
	}
	{
		err := (*Subscriptions)(nil).SaveBatchAlert(context.Background(), tenant.Scope{}, scheduledBatch{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SaveBatchAlert: %v", err)
		}
	}
}
