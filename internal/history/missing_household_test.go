package history

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		err := AttachStockIn(context.Background(), tenant.Scope{}, nil, InNote{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("AttachStockIn: %v", err)
		}
	}
	{
		err := AttachStockOut(context.Background(), tenant.Scope{}, nil, OutNote{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("AttachStockOut: %v", err)
		}
	}
	{
		_, err := (*Journal)(nil).Group(context.Background(), tenant.Scope{}, "", time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Group: %v", err)
		}
	}
	{
		_, err := (*Journal)(nil).Household(context.Background(), tenant.Scope{}, LedgerFilter{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Household: %v", err)
		}
	}
	{
		_, err := (*Journal)(nil).PreviewUndo(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("PreviewUndo: %v", err)
		}
	}
	{
		_, err := (*Journal)(nil).Product(context.Background(), tenant.Scope{}, "", time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Product: %v", err)
		}
	}
	{
		_, err := (*Journal)(nil).SetQuantity(context.Background(), tenant.Scope{}, "", 0, time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SetQuantity: %v", err)
		}
	}
	{
		_, err := (*Journal)(nil).Undo(context.Background(), tenant.Scope{}, "", time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Undo: %v", err)
		}
	}
	{
		err := addStockInUnits(context.Background(), tenant.Scope{}, nil, moveRow{}, 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("addStockInUnits: %v", err)
		}
	}
	{
		err := addStockOutUnits(context.Background(), tenant.Scope{}, nil, moveRow{}, 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("addStockOutUnits: %v", err)
		}
	}
	{
		_, err := applyUndo(context.Background(), tenant.Scope{}, nil, "", false)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("applyUndo: %v", err)
		}
	}
	{
		_, _, err := backfillPartner(context.Background(), tenant.Scope{}, nil, moveRow{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("backfillPartner: %v", err)
		}
	}
	{
		_, err := countOnHand(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("countOnHand: %v", err)
		}
	}
	{
		_, err := countQuery(context.Background(), tenant.Scope{}, nil, "", nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("countQuery: %v", err)
		}
	}
	{
		err := deleteConsumptions(context.Background(), tenant.Scope{}, nil, "", 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("deleteConsumptions: %v", err)
		}
	}
	{
		err := ensure(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ensure: %v", err)
		}
	}
	{
		err := ensureRemoved(context.Background(), tenant.Scope{}, nil, scanIndex{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ensureRemoved: %v", err)
		}
	}
	{
		err := ensureStockIns(context.Background(), tenant.Scope{}, nil, scanIndex{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ensureStockIns: %v", err)
		}
	}
	{
		err := ensureStockOuts(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ensureStockOuts: %v", err)
		}
	}
	{
		_, err := groupView(context.Background(), tenant.Scope{}, nil, "", time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("groupView: %v", err)
		}
	}
	{
		_, err := idsOf(context.Background(), tenant.Scope{}, nil, "", nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("idsOf: %v", err)
		}
	}
	{
		err := linkRemoved(context.Background(), tenant.Scope{}, nil, "", "", time.Time{}, 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("linkRemoved: %v", err)
		}
	}
	{
		_, err := linkedScanIDs(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("linkedScanIDs: %v", err)
		}
	}
	{
		_, err := listHousehold(context.Background(), tenant.Scope{}, nil, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("listHousehold: %v", err)
		}
	}
	{
		_, err := listMoves(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("listMoves: %v", err)
		}
	}
	{
		_, err := listUses(context.Background(), tenant.Scope{}, nil, nil, time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("listUses: %v", err)
		}
	}
	{
		_, err := loadCommittedScans(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadCommittedScans: %v", err)
		}
	}
	{
		_, err := loadMove(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadMove: %v", err)
		}
	}
	{
		_, err := productView(context.Background(), tenant.Scope{}, nil, "", time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("productView: %v", err)
		}
	}
	{
		err := relabelManualMoves(context.Background(), tenant.Scope{}, nil, scanIndex{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("relabelManualMoves: %v", err)
		}
	}
	{
		err := removeStockInUnits(context.Background(), tenant.Scope{}, nil, moveRow{}, 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("removeStockInUnits: %v", err)
		}
	}
	{
		err := removeStockOutUnits(context.Background(), tenant.Scope{}, nil, moveRow{}, 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("removeStockOutUnits: %v", err)
		}
	}
	{
		err := repair(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("repair: %v", err)
		}
	}
	{
		_, err := reshelveItem(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("reshelveItem: %v", err)
		}
	}
	{
		err := resizeStockIn(context.Background(), tenant.Scope{}, nil, moveRow{}, 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("resizeStockIn: %v", err)
		}
	}
	{
		err := resizeStockOut(context.Background(), tenant.Scope{}, nil, moveRow{}, 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("resizeStockOut: %v", err)
		}
	}
	{
		_, err := restoreUnits(context.Background(), tenant.Scope{}, nil, moveRow{}, 0)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("restoreUnits: %v", err)
		}
	}
	{
		_, err := stockInTracked(context.Background(), tenant.Scope{}, nil, "", time.Time{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("stockInTracked: %v", err)
		}
	}
	{
		err := undoStockIn(context.Background(), tenant.Scope{}, nil, moveRow{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("undoStockIn: %v", err)
		}
	}
	{
		err := undoStockOut(context.Background(), tenant.Scope{}, nil, moveRow{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("undoStockOut: %v", err)
		}
	}
}
