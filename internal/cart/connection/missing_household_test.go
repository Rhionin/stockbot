package connection

import (
	"context"
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		_, err := (*TokenBroker)(nil).AccessToken(context.Background(), tenant.Scope{}, "", nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("AccessToken: %v", err)
		}
	}
	{
		_, err := (*Directory)(nil).ConsumeAuthState(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ConsumeAuthState: %v", err)
		}
	}
	{
		err := (*Directory)(nil).Disconnect(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Disconnect: %v", err)
		}
	}
	{
		err := (*Directory)(nil).GenerateAuthState(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("GenerateAuthState: %v", err)
		}
	}
	{
		_, err := (*Directory)(nil).Read(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Read: %v", err)
		}
	}
	{
		err := (*Directory)(nil).Write(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Write: %v", err)
		}
	}
}
