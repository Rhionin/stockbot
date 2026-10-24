package supply

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		_, err := (*Service)(nil).Complete(context.Background(), tenant.Scope{}, time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Complete: %v", err)
		}
	}
	{
		_, err := (*Service)(nil).GroupUsage(context.Background(), tenant.Scope{}, "", time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("GroupUsage: %v", err)
		}
	}
	{
		_, err := (*Service)(nil).OpeningTx(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("OpeningTx: %v", err)
		}
	}
	{
		_, err := (*Service)(nil).Plan(context.Background(), tenant.Scope{}, time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Plan: %v", err)
		}
	}
	{
		err := (*Service)(nil).SetMonths(context.Background(), tenant.Scope{}, 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SetMonths: %v", err)
		}
	}
	{
		_, err := (*Service)(nil).Settings(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Settings: %v", err)
		}
	}
	{
		err := (*Service)(nil).Wipe(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Wipe: %v", err)
		}
	}
	{
		err := attachEvents(context.Background(), tenant.Scope{}, nil, nil, time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachEvents: %v", err)
		}
	}
	{
		err := attachGroupSignals(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachGroupSignals: %v", err)
		}
	}
	{
		err := attachManuals(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachManuals: %v", err)
		}
	}
	{
		err := attachResolved(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachResolved: %v", err)
		}
	}
	{
		_, err := loadFacts(context.Background(), tenant.Scope{}, nil, Phase{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadFacts: %v", err)
		}
	}
	{
		_, err := readOverride(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("readOverride: %v", err)
		}
	}
	{
		err := wipeHousehold(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("wipeHousehold: %v", err)
		}
	}
	{
		err := writeOverride(context.Background(), tenant.Scope{}, nil, "", Override{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("writeOverride: %v", err)
		}
	}
}
