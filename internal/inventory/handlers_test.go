package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

const handlerHouseholdID = "0190bbbb-cccc-7000-8000-0000000000e5"

func handlerScope(t *testing.T) tenant.Scope {
	t.Helper()
	scope, err := tenant.New(handlerHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestInventoryHandlersUseTheClockAndWarningWindow(t *testing.T) {
	now := time.Date(2026, 4, 2, 8, 0, 0, 0, time.UTC)
	expires := now.Add(48 * time.Hour)
	scope := handlerScope(t)
	list := &recordingList{items: []InventoryItem{{Item: Item{ID: "item-1"}}}}
	handler := &ListHandler{Pantry: list, Scope: scope, Now: func() time.Time { return now }, WarningDays: 3}
	got, err := handler.Handle(struct{ Context context.Context }{Context: context.Background()})
	if err != nil || len(got) != 1 || got[0].Item.ID != "item-1" {
		t.Fatalf("list %+v err %v", got, err)
	}
	if list.household != handlerHouseholdID || list.days != 3 || !list.now.Equal(now) {
		t.Fatalf("list called household=%s days=%d now=%s", list.household, list.days, list.now)
	}

	defaults := &ListHandler{Pantry: list, Scope: scope}
	if _, err := defaults.Handle(struct{ Context context.Context }{Context: context.Background()}); err != nil {
		t.Fatal(err)
	}
	if list.days != 7 {
		t.Fatalf("default warning days %d", list.days)
	}

	instances := &recordingInstances{items: []ItemInstance{{ID: "inst-1", ExpiresAt: &expires}}}
	listed, err := (&InstancesListHandler{
		Pantry: instances, Scope: scope, Now: func() time.Time { return now }, WarningDays: 3,
	}).Handle(struct {
		Context    context.Context
		PathParams instancesListPathParams
	}{Context: context.Background(), PathParams: instancesListPathParams{ItemID: "item-1"}})
	if err != nil || len(listed) != 1 || listed[0].ID != "inst-1" {
		t.Fatalf("instances %+v err %v", listed, err)
	}
	if instances.itemID != "item-1" || instances.household != handlerHouseholdID {
		t.Fatalf("item %s household %s", instances.itemID, instances.household)
	}
	if _, err := (&InstancesListHandler{Pantry: &recordingInstances{err: errors.New("missing")}, Scope: scope}).Handle(struct {
		Context    context.Context
		PathParams instancesListPathParams
	}{Context: context.Background()}); err == nil {
		t.Fatal("instance list error was ignored")
	}

	when := now
	created, err := (&InstanceCreateHandler{Pantry: recordingCreate{}, Scope: scope}).Handle(struct {
		Context    context.Context
		PathParams instanceCreatePathParams
		Body       instanceCreateRequest
	}{
		Context:    context.Background(),
		PathParams: instanceCreatePathParams{ItemID: "item-1"},
		Body:       instanceCreateRequest{ExpiresAt: &when},
	})
	if err != nil || created == nil || created.ItemID != "item-1" || created.ExpiresAt == nil {
		t.Fatalf("created %+v err %v", created, err)
	}

	if _, err := (&InstanceDeleteHandler{Pantry: recordingDelete{}, Scope: scope}).Handle(struct {
		Context    context.Context
		PathParams instanceDeletePathParams
	}{Context: context.Background(), PathParams: instanceDeletePathParams{InstanceID: "inst-1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&InstanceDeleteHandler{Pantry: recordingDelete{err: errors.New("gone")}, Scope: scope}).Handle(struct {
		Context    context.Context
		PathParams instanceDeletePathParams
	}{Context: context.Background()}); err == nil {
		t.Fatal("delete error was ignored")
	}
}

type recordingList struct {
	items     []InventoryItem
	household string
	days      int
	now       time.Time
}

func (p *recordingList) GetInventoryList(_ context.Context, scope tenant.Scope, now time.Time, warningDays int) ([]InventoryItem, error) {
	p.household = scope.HouseholdID()
	p.days = warningDays
	p.now = now
	return p.items, nil
}

type recordingInstances struct {
	items     []ItemInstance
	itemID    string
	household string
	err       error
}

func (p *recordingInstances) ListItemInstances(_ context.Context, scope tenant.Scope, itemID string) ([]ItemInstance, error) {
	if p.err != nil {
		return nil, p.err
	}
	p.household = scope.HouseholdID()
	p.itemID = itemID
	return p.items, nil
}

type recordingCreate struct{}

func (recordingCreate) AddInstance(_ context.Context, _ tenant.Scope, instance ItemInstance) (*ItemInstance, error) {
	return &instance, nil
}

type recordingDelete struct{ err error }

func (p recordingDelete) RemoveInstance(context.Context, tenant.Scope, string, string) error {
	return p.err
}
