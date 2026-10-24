package inventory

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInventoryHandlersUseTheClockAndWarningWindow(t *testing.T) {
	now := time.Date(2026, 4, 2, 8, 0, 0, 0, time.UTC)
	expires := now.Add(48 * time.Hour)
	list := &recordingList{items: []InventoryItem{{Item: Item{ID: "item-1"}}}}
	handler := &ListHandler{Pantry: list, UserID: "user-1", Now: func() time.Time { return now }, WarningDays: 3}
	got, err := handler.Handle(struct{ Context context.Context }{Context: context.Background()})
	if err != nil || len(got) != 1 || got[0].Item.ID != "item-1" {
		t.Fatalf("list %+v err %v", got, err)
	}
	if list.user != "user-1" || list.days != 3 || !list.now.Equal(now) {
		t.Fatalf("list called user=%s days=%d now=%s", list.user, list.days, list.now)
	}

	defaults := &ListHandler{Pantry: list, UserID: "user-1"}
	if _, err := defaults.Handle(struct{ Context context.Context }{Context: context.Background()}); err != nil {
		t.Fatal(err)
	}
	if list.days != 7 {
		t.Fatalf("default warning days %d", list.days)
	}

	instances := &recordingInstances{items: []ItemInstance{{ID: "inst-1", ExpiresAt: &expires}}}
	listed, err := (&InstancesListHandler{
		Pantry: instances, Now: func() time.Time { return now }, WarningDays: 3,
	}).Handle(struct {
		Context    context.Context
		PathParams instancesListPathParams
	}{Context: context.Background(), PathParams: instancesListPathParams{ItemID: "item-1"}})
	if err != nil || len(listed) != 1 || listed[0].ID != "inst-1" {
		t.Fatalf("instances %+v err %v", listed, err)
	}
	if instances.itemID != "item-1" {
		t.Fatalf("item %s", instances.itemID)
	}
	if _, err := (&InstancesListHandler{Pantry: &recordingInstances{err: errors.New("missing")}}).Handle(struct {
		Context    context.Context
		PathParams instancesListPathParams
	}{Context: context.Background()}); err == nil {
		t.Fatal("instance list error was ignored")
	}

	when := now
	created, err := (&InstanceCreateHandler{Pantry: recordingCreate{}}).Handle(struct {
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

	if _, err := (&InstanceDeleteHandler{Pantry: recordingDelete{}}).Handle(struct {
		Context    context.Context
		PathParams instanceDeletePathParams
	}{Context: context.Background(), PathParams: instanceDeletePathParams{InstanceID: "inst-1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&InstanceDeleteHandler{Pantry: recordingDelete{err: errors.New("gone")}}).Handle(struct {
		Context    context.Context
		PathParams instanceDeletePathParams
	}{Context: context.Background()}); err == nil {
		t.Fatal("delete error was ignored")
	}
}

type recordingList struct {
	items []InventoryItem
	user  string
	days  int
	now   time.Time
}

func (p *recordingList) GetInventoryList(_ context.Context, userID string, now time.Time, warningDays int) ([]InventoryItem, error) {
	p.user = userID
	p.days = warningDays
	p.now = now
	return p.items, nil
}

type recordingInstances struct {
	items  []ItemInstance
	itemID string
	err    error
}

func (p *recordingInstances) ListItemInstances(_ context.Context, itemID string) ([]ItemInstance, error) {
	if p.err != nil {
		return nil, p.err
	}
	p.itemID = itemID
	return p.items, nil
}

type recordingCreate struct{}

func (recordingCreate) AddInstance(_ context.Context, instance ItemInstance) (*ItemInstance, error) {
	return &instance, nil
}

type recordingDelete struct{ err error }

func (p recordingDelete) RemoveInstance(context.Context, string, string) error { return p.err }
