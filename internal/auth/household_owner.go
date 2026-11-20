package auth

import (
	"context"

	"github.com/Rhionin/stockbot/internal/tenant"
)

type membershipHookKey struct{}

// WithMembershipHook replaces Membership resolution for tests in this process.
func WithMembershipHook(ctx context.Context, hook func(context.Context, string, string) (role, deleted string, ok bool, err error)) context.Context {
	return context.WithValue(ctx, membershipHookKey{}, hook)
}

// ResolveMembership returns household membership, honoring test hooks on ctx when set.
func ResolveMembership(ctx context.Context, svc *Service, userID, householdID string) (role, deleted string, ok bool, err error) {
	if hook, ok := ctx.Value(membershipHookKey{}).(func(context.Context, string, string) (string, string, bool, error)); ok && hook != nil {
		return hook(ctx, userID, householdID)
	}
	if svc == nil {
		return "owner", "", true, nil
	}
	return svc.Membership(ctx, userID, householdID)
}

// HouseholdOwner reports whether the signed-in account is an owner of householdID.
// When svc is nil, any signed-in account is treated as an owner (legacy harness only).
func HouseholdOwner(ctx context.Context, svc *Service, householdID string) (bool, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok || p.Kind != "account" {
		return false, nil
	}
	role, _, memberOK, err := ResolveMembership(ctx, svc, p.UserID, householdID)
	if err != nil {
		return false, err
	}
	if !memberOK {
		return false, nil
	}
	return role == "owner", nil
}

// HouseholdOwnerScope resolves membership and returns scope when the caller is an owner.
// deniedRole is true when the account is a non-owner member. notMember is true for foreign households.
func HouseholdOwnerScope(ctx context.Context, svc *Service, householdID string) (scope tenant.Scope, deniedRole, notMember bool, err error) {
	p, ok := PrincipalFrom(ctx)
	if !ok || p.Kind != "account" {
		return tenant.Scope{}, false, false, nil
	}
	role, _, memberOK, err := ResolveMembership(ctx, svc, p.UserID, householdID)
	if err != nil {
		return tenant.Scope{}, false, false, err
	}
	if !memberOK {
		return tenant.Scope{}, false, true, nil
	}
	scope, err = tenant.New(householdID)
	if err != nil {
		return tenant.Scope{}, false, true, nil
	}
	if role != "owner" {
		return tenant.Scope{}, true, false, nil
	}
	return scope, false, false, nil
}