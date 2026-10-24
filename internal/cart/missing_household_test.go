package cart

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		err := (*Ledger)(nil).Advance(context.Background(), tenant.Scope{}, "", "", 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Advance: %v", err)
		}
	}
	{
		_, err := (*Ledger)(nil).AdvanceTx(context.Background(), tenant.Scope{}, nil, "", "", 0, time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("AdvanceTx: %v", err)
		}
	}
	{
		_, err := (*Ledger)(nil).ListForProvider(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListForProvider: %v", err)
		}
	}
	{
		_, err := (*Engine)(nil).Provision(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Provision: %v", err)
		}
	}
	{
		err := (*Ledger)(nil).ResetForItemTx(context.Background(), tenant.Scope{}, nil, "", "", time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ResetForItemTx: %v", err)
		}
	}
	{
		err := (*Ledger)(nil).ResetForProvider(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ResetForProvider: %v", err)
		}
	}
	{
		_, _, err := (*Ledger)(nil).boundaryTx(context.Background(), tenant.Scope{}, nil, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("boundaryTx: %v", err)
		}
	}
	{
		_, err := (*Engine)(nil).getComputedEntries(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("getComputedEntries: %v", err)
		}
	}
	{
		err := (*Engine)(nil).updateLedgerAndAdjustments(context.Background(), tenant.Scope{}, "", ProvisionRequest{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("updateLedgerAndAdjustments: %v", err)
		}
	}
}
