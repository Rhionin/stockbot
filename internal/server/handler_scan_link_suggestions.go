package server

import (
	"context"

	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type ScanLinkSuggestionsHandler struct {
	Queue interface {
		LinkSuggestions(ctx context.Context, scope tenant.Scope, id string) ([]scan.LinkSuggestion, error)
	}
}

type linkSuggestionsPath struct {
	ID string `json:"id"`
}

type linkSuggestionsResponse struct {
	Suggestions []scan.LinkSuggestion `json:"suggestions"`
}

func (h *ScanLinkSuggestionsHandler) Handle(req Request[struct{}, linkSuggestionsPath]) (linkSuggestionsResponse, error) {
	if req.PathParams.ID == "" {
		return linkSuggestionsResponse{}, BadRequest("scan entry id is required")
	}
	scope, err := requestScope(req.Context)
	if err != nil {
		return linkSuggestionsResponse{}, err
	}
	suggestions, err := h.Queue.LinkSuggestions(req.Context, scope, req.PathParams.ID)
	if err != nil {
		return linkSuggestionsResponse{}, commitClientError(err)
	}
	return linkSuggestionsResponse{Suggestions: suggestions}, nil
}
