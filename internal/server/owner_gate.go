package server

import (
	"context"
	"net/http"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// requireHouseholdOwner returns scope when the signed-in account is an owner of householdID.
func requireHouseholdOwner(ctx context.Context, authSvc *auth.Service, householdID string) (tenant.Scope, auth.Principal, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.Kind != "account" {
		return tenant.Scope{}, auth.Principal{}, Unauthorized()
	}
	if authSvc != nil {
		role, _, memberOK, err := authSvc.Membership(ctx, p.UserID, householdID)
		if err != nil {
			return tenant.Scope{}, auth.Principal{}, InternalError(err)
		}
		if !memberOK {
			return tenant.Scope{}, auth.Principal{}, NotFound(auth.SentenceNotFound)
		}
		p.Role = role
	}
	scope, err := tenant.New(householdID)
	if err != nil {
		return tenant.Scope{}, auth.Principal{}, NotFound(auth.SentenceNotFound)
	}
	if p.Role != "owner" {
		return tenant.Scope{}, auth.Principal{}, &HTTPError{
			Code: http.StatusForbidden, Message: auth.SentenceForbidden, ErrorCode: "forbidden_role",
		}
	}
	return scope, p, nil
}

func (h *ProvidersHandler) requireOwner(ctx context.Context) (tenant.Scope, auth.Principal, error) {
	scope, err := requestScope(ctx)
	if err != nil {
		return tenant.Scope{}, auth.Principal{}, InternalError(err)
	}
	if h.Auth == nil {
		return scope, auth.Principal{}, nil
	}
	return requireHouseholdOwner(ctx, h.Auth, scope.HouseholdID())
}