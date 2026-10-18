package server

import (
	"context"

	"github.com/Rhionin/stockbot/internal/scan"
)

type ScanLinkSuggestionsHandler struct {
	Queue interface {
		LinkSuggestions(ctx context.Context, id string) ([]scan.LinkSuggestion, error)
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
	suggestions, err := h.Queue.LinkSuggestions(req.Context, req.PathParams.ID)
	if err != nil {
		return linkSuggestionsResponse{}, commitClientError(err)
	}
	return linkSuggestionsResponse{Suggestions: suggestions}, nil
}
