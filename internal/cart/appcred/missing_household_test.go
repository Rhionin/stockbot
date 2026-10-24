package appcred

import (
	"context"
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		err := (*Vault)(nil).Clear(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Clear: %v", err)
		}
	}
	{
		_, _, err := (*Vault)(nil).Load(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Load: %v", err)
		}
	}
	{
		err := (*Vault)(nil).Save(context.Background(), tenant.Scope{}, Saved{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Save: %v", err)
		}
	}
}
