package server

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Rhionin/stockbot/internal/product"
)

const codeQueryTooShort = "query_too_short"

type productSearchQuery struct {
	Q     string `json:"q"`
	Limit int    `json:"limit"`
}

type ProductSearchHandler struct {
	Catalog *product.Catalog
}

func (h *ProductSearchHandler) Handle(req Request[struct{}, productSearchQuery]) ([]product.SearchHit, error) {
	query := req.RawRequest.URL.Query()
	q := strings.TrimSpace(query.Get("q"))
	if utf8.RuneCountInString(q) < 2 {
		return nil, &HTTPError{
			Code:      400,
			Message:   "Type at least 2 letters.",
			ErrorCode: codeQueryTooShort,
		}
	}
	limit := 0
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit == 0 {
		limit = 20
	}
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	hits, err := h.Catalog.SearchProducts(req.Context, scope, q, limit)
	if err != nil {
		return nil, InternalError(err)
	}
	return hits, nil
}
