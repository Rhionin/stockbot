package server

import (
	"context"
	"net/http"
	"net/url"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func requireHouseholdOwner(ctx context.Context, authSvc *auth.Service, householdID string) (tenant.Scope, auth.Principal, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.Kind != "account" {
		return tenant.Scope{}, auth.Principal{}, Unauthorized()
	}
	scope, deniedRole, notMember, err := auth.HouseholdOwnerScope(ctx, authSvc, householdID)
	if err != nil {
		return tenant.Scope{}, auth.Principal{}, InternalError(err)
	}
	if notMember {
		return tenant.Scope{}, auth.Principal{}, NotFound(auth.SentenceNotFound)
	}
	if deniedRole {
		return tenant.Scope{}, auth.Principal{}, &HTTPError{
			Code: http.StatusForbidden, Message: auth.SentenceForbidden, ErrorCode: "forbidden_role",
		}
	}
	if authSvc != nil {
		role, _, memberOK, err := auth.ResolveMembership(ctx, authSvc, p.UserID, householdID)
		if err != nil {
			return tenant.Scope{}, auth.Principal{}, InternalError(err)
		}
		if memberOK {
			p.Role = role
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
		// Nil Auth is test-harness only; production always wires WithStage1 (cmd/server/main.go:349-350).
		return scope, auth.Principal{}, nil
	}
	return requireHouseholdOwner(ctx, h.Auth, scope.HouseholdID())
}

func (h *ProvidersHandler) providerCallbackReturnPath(ctx context.Context, scope tenant.Scope, providerID string) string {
	returnPath := "/shopping"
	if h.ConnectionDir != nil {
		if pending, _ := h.ConnectionDir.Read(ctx, scope, providerID); pending != nil && pending.AuthReturn == "household" {
			returnPath = "/household"
		}
	}
	return returnPath
}

func providerCallbackOutcomeLocation(returnPath, providerID, outcome string) string {
	return returnPath + "?" + providerID + "=" + url.QueryEscape(outcome)
}

func (h *ProvidersHandler) callbackForbiddenRole(ctx context.Context, scope tenant.Scope, providerID string) (SeeOther, bool, error) {
	if h.Auth == nil {
		return SeeOther{}, false, nil
	}
	_, deniedRole, notMember, err := auth.HouseholdOwnerScope(ctx, h.Auth, scope.HouseholdID())
	if err != nil {
		return SeeOther{}, false, InternalError(err)
	}
	if notMember {
		return SeeOther{}, false, NotFound(auth.SentenceNotFound)
	}
	if !deniedRole {
		return SeeOther{}, false, nil
	}
	returnPath := h.providerCallbackReturnPath(ctx, scope, providerID)
	if h.ConnectionDir != nil {
		if err := h.ConnectionDir.DiscardAuthState(ctx, scope, providerID); err != nil {
			return SeeOther{}, false, InternalError(err)
		}
	}
	return SeeOther{Location: providerCallbackOutcomeLocation(returnPath, providerID, "not_allowed")}, true, nil
}