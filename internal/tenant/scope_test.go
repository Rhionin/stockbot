package tenant

import (
	"context"
	"errors"
	"testing"
)

func TestNewRejectsUnverifiedIDs(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"household",
		"01901111-1111-7000-8000-0000000000A1",
		"01901111-1111-7000-8000-0000000000a1 ",
		"not-a-uuid",
		"019011111111700080000000000000a1",
	}
	for _, id := range cases {
		scope, err := New(id)
		if err == nil || scope.Valid() {
			t.Fatalf("New(%q) = %+v, %v", id, scope, err)
		}
	}
}

func TestNewAndContextRoundTrip(t *testing.T) {
	t.Parallel()
	const id = "01901111-1111-7000-8000-0000000000a1"
	scope, err := New(id)
	if err != nil {
		t.Fatal(err)
	}
	if scope.HouseholdID() != id || !scope.Valid() || scope.Require() != nil {
		t.Fatalf("scope %+v", scope)
	}
	stored, ok := FromContext(WithScope(context.Background(), scope))
	if !ok || stored.HouseholdID() != id {
		t.Fatalf("context scope %+v ok %v", stored, ok)
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("empty context yielded a scope")
	}
	if _, ok := FromContext(nil); ok {
		t.Fatal("nil context yielded a scope")
	}
	if _, ok := FromContext(WithScope(context.Background(), Scope{})); ok {
		t.Fatal("zero scope was readable")
	}
}

func TestRequireZero(t *testing.T) {
	t.Parallel()
	if !errors.Is(Scope{}.Require(), ErrMissingHousehold) {
		t.Fatal(Scope{}.Require())
	}
}
