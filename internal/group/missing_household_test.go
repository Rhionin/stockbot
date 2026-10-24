package group

import (
	"context"
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestMissingHouseholdIsRefused(t *testing.T) {
	{
		_, err := (*Groups)(nil).Accept(context.Background(), tenant.Scope{}, "", nil, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Accept: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).AddMembers(context.Background(), tenant.Scope{}, "", nil, "", nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("AddMembers: %v", err)
		}
	}
	{
		err := (*Groups)(nil).ConsiderProduct(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ConsiderProduct: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).Create(context.Background(), tenant.Scope{}, "", nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Create: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).DealsFor(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("DealsFor: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).DefaultRule(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("DefaultRule: %v", err)
		}
	}
	{
		err := (*Groups)(nil).Delete(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Delete: %v", err)
		}
	}
	{
		err := (*Groups)(nil).Dismiss(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Dismiss: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).Get(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Get: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).Hints(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Hints: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).List(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("List: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).ListSuggestions(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ListSuggestions: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).MemberGroupID(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("MemberGroupID: %v", err)
		}
	}
	{
		err := (*Groups)(nil).NoteFromScan(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("NoteFromScan: %v", err)
		}
	}
	{
		err := (*Groups)(nil).RefreshSuggestions(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("RefreshSuggestions: %v", err)
		}
	}
	{
		_, _, err := (*Groups)(nil).RemoveMember(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("RemoveMember: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).Rename(context.Background(), tenant.Scope{}, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Rename: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).Rescan(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Rescan: %v", err)
		}
	}
	{
		err := (*Groups)(nil).Seed(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Seed: %v", err)
		}
	}
	{
		err := (*Groups)(nil).SetDefaultRule(context.Background(), tenant.Scope{}, "same_as_ran_out")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SetDefaultRule: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).SetMemberRestock(context.Background(), tenant.Scope{}, "", "", false)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SetMemberRestock: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).SetRule(context.Background(), tenant.Scope{}, "", "", nil, false)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SetRule: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).SetTarget(context.Background(), tenant.Scope{}, "", nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("SetTarget: %v", err)
		}
	}
	{
		err := (*Groups)(nil).Skip(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("Skip: %v", err)
		}
	}
	{
		err := addMembersInTx(context.Background(), tenant.Scope{}, nil, "", nil, "", nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("addMembersInTx: %v", err)
		}
	}
	{
		err := applyTarget(context.Background(), tenant.Scope{}, nil, "", nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("applyTarget: %v", err)
		}
	}
	{
		err := attachBarcodes(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("attachBarcodes: %v", err)
		}
	}
	{
		_, err := classifyJoining(context.Background(), tenant.Scope{}, nil, "", nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("classifyJoining: %v", err)
		}
	}
	{
		_, err := createInTx(context.Background(), tenant.Scope{}, nil, "", nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("createInTx: %v", err)
		}
	}
	{
		err := deleteOverrides(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("deleteOverrides: %v", err)
		}
	}
	{
		err := dismissPairs(context.Background(), tenant.Scope{}, nil, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("dismissPairs: %v", err)
		}
	}
	{
		err := ensureGroup(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ensureGroup: %v", err)
		}
	}
	{
		_, err := ensureHouseholdItem(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ensureHouseholdItem: %v", err)
		}
	}
	{
		err := ensureJoinable(context.Background(), tenant.Scope{}, nil, nil, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ensureJoinable: %v", err)
		}
	}
	{
		err := ensureNameFree(context.Background(), tenant.Scope{}, nil, "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("ensureNameFree: %v", err)
		}
	}
	{
		_, err := hintsFor(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("hintsFor: %v", err)
		}
	}
	{
		_, err := insertGroup(context.Background(), tenant.Scope{}, nil, "", "", "", nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("insertGroup: %v", err)
		}
	}
	{
		_, err := insertSeeded(context.Background(), tenant.Scope{}, nil, seedCluster{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("insertSeeded: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).listOpenSuggestions(context.Background(), tenant.Scope{})
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("listOpenSuggestions: %v", err)
		}
	}
	{
		_, err := (*Groups)(nil).load(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("load: %v", err)
		}
	}
	{
		_, err := loadDismissed(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadDismissed: %v", err)
		}
	}
	{
		_, _, err := loadHintGroups(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadHintGroups: %v", err)
		}
	}
	{
		_, err := loadMembers(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadMembers: %v", err)
		}
	}
	{
		_, _, err := loadMembership(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadMembership: %v", err)
		}
	}
	{
		_, err := loadNotedFromScan(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadNotedFromScan: %v", err)
		}
	}
	{
		_, err := loadOpenSets(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadOpenSets: %v", err)
		}
	}
	{
		_, err := loadSeedPrefs(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadSeedPrefs: %v", err)
		}
	}
	{
		_, err := loadSuggestion(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("loadSuggestion: %v", err)
		}
	}
	{
		_, err := memberIDsOf(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("memberIDsOf: %v", err)
		}
	}
	{
		err := moveMember(context.Background(), tenant.Scope{}, nil, "", "", "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("moveMember: %v", err)
		}
	}
	{
		_, err := overrideNotices(context.Background(), tenant.Scope{}, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("overrideNotices: %v", err)
		}
	}
	{
		err := (*Groups)(nil).preflightName(context.Background(), tenant.Scope{}, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("preflightName: %v", err)
		}
	}
	{
		err := refreshSuggestions(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("refreshSuggestions: %v", err)
		}
	}
	{
		err := requireTargetOrNone(context.Background(), tenant.Scope{}, nil, nil, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("requireTargetOrNone: %v", err)
		}
	}
	{
		_, err := seedSuggestions(context.Background(), tenant.Scope{}, nil)
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("seedSuggestions: %v", err)
		}
	}
	{
		_, err := suggestionMembers(context.Background(), tenant.Scope{}, nil, "")
		if !errors.Is(err, tenant.ErrMissingHousehold) {
			t.Fatalf("suggestionMembers: %v", err)
		}
	}
}
