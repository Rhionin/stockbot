package server

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/tenant"
)

const sendResultsMaxItemIDs = 200

// ShoppingListSendResultsHandler handles GET /api/shopping-list/send-results.
type ShoppingListSendResultsHandler struct {
	SendResults *cart.ResultRecorder
	Registry    *cart.Registry
	Credentials *appcred.Vault
}

type shoppingListSendResultEntry struct {
	ItemID      string            `json:"itemId"`
	EntryID     *string           `json:"entryId"`
	ProviderID  string            `json:"providerId"`
	Status      cart.PublicStatus `json:"status"`
	Reason      cart.PublicReason `json:"reason,omitempty"`
	Quantity    int               `json:"quantity"`
	AttemptedAt string            `json:"attemptedAt"`
}

type shoppingListSendResultsResponse struct {
	RetentionDays int                           `json:"retentionDays"`
	Results       []shoppingListSendResultEntry `json:"results"`
}

func (h *ShoppingListSendResultsHandler) Handle(req Request[struct{}, struct{}]) (*shoppingListSendResultsResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}
	now := time.Now().UTC()
	cutoff := now.Add(-cart.SendResultRetention)

	query := req.RawRequest.URL.Query()
	provider, err := h.resolveProvider(req.Context, scope, strings.TrimSpace(query.Get("provider")))
	if err != nil {
		return nil, err
	}

	itemFilter, err := parseSendResultsItemIDs(query)
	if err != nil {
		return nil, err
	}

	resp := &shoppingListSendResultsResponse{
		RetentionDays: int(cart.SendResultRetention / (24 * time.Hour)),
		Results:       []shoppingListSendResultEntry{},
	}
	if h.SendResults == nil || provider == "" {
		return resp, nil
	}

	rows, err := h.SendResults.LatestResults(req.Context, scope, cart.ProviderID(provider), cutoff)
	if err != nil {
		return nil, InternalError(err)
	}
	resp.Results = buildSendResultEntries(rows, itemFilter)
	return resp, nil
}

func (h *ShoppingListSendResultsHandler) resolveProvider(ctx context.Context, scope tenant.Scope, named string) (string, error) {
	providerID := named
	if providerID == "" {
		if id, ok := soleHouseholdConfiguredProvider(ctx, scope, h.Registry, h.Credentials); ok {
			return id, nil
		}
		if h.Registry != nil {
			configured := 0
			for _, id := range h.Registry.List() {
				ok, err := h.householdConfigured(ctx, scope, string(id))
				if err != nil {
					return "", InternalError(err)
				}
				if ok {
					configured++
				}
			}
			if configured > 1 {
				return "", BadRequest(`provider "" is not registered`)
			}
		}
		return "", nil
	}
	if h.Registry == nil {
		return "", BadRequest(fmt.Sprintf("provider %q is not registered", providerID))
	}
	if _, ok := h.Registry.Get(cart.ProviderID(providerID)); !ok {
		return "", BadRequest(fmt.Sprintf("provider %q is not registered", providerID))
	}
	return providerID, nil
}

func (h *ShoppingListSendResultsHandler) householdConfigured(ctx context.Context, scope tenant.Scope, providerID string) (bool, error) {
	return householdProviderConfigured(ctx, scope, providerID, h.Credentials)
}

func parseSendResultsItemIDs(query url.Values) (map[string]struct{}, error) {
	raw := query["itemId"]
	if len(raw) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(raw))
	for _, part := range raw {
		for _, itemID := range strings.Split(part, ",") {
			itemID = strings.TrimSpace(itemID)
			if itemID == "" {
				continue
			}
			seen[itemID] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil, nil
	}
	if len(seen) > sendResultsMaxItemIDs {
		return nil, &HTTPError{Code: 422, Message: "Choose at most 200 items."}
	}
	return seen, nil
}

func buildSendResultEntries(rows map[string]cart.StoredSendResult, itemFilter map[string]struct{}) []shoppingListSendResultEntry {
	keys := make([]string, 0, len(rows))
	for itemID := range rows {
		if itemFilter != nil {
			if _, ok := itemFilter[itemID]; !ok {
				continue
			}
		}
		keys = append(keys, itemID)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := rows[keys[i]].AttemptedAt
		right := rows[keys[j]].AttemptedAt
		if left != right {
			return left > right
		}
		return keys[i] < keys[j]
	})

	out := make([]shoppingListSendResultEntry, 0, len(keys))
	for _, itemID := range keys {
		row := rows[itemID]
		entry := shoppingListSendResultEntry{
			ItemID:      itemID,
			ProviderID:  row.ProviderID,
			Status:      row.Status,
			Quantity:    row.Quantity,
			AttemptedAt: row.AttemptedAt,
		}
		if row.Reason != "" {
			entry.Reason = row.Reason
		}
		if row.EntryID != "" {
			entryID := row.EntryID
			entry.EntryID = &entryID
		}
		out = append(out, entry)
	}
	return out
}
