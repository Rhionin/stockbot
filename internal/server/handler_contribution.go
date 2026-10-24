package server

import (
	"context"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func contributionSettings(ctx context.Context, scope tenant.Scope, catalog *product.Catalog, contributor product.UpstreamContributor) (ContributionSettingsResponse, error) {
	enabled, err := catalog.ContributionEnabled(ctx, scope)
	if err != nil {
		return ContributionSettingsResponse{}, err
	}
	configured := contributor != nil && contributor.Configured()
	return ContributionSettingsResponse{Enabled: enabled, Configured: configured}, nil
}

// ContributionSettingsResponse tells the UI whether sharing is allowed and
// whether this server has an account. It never includes that account.
type ContributionSettingsResponse struct {
	Enabled    bool `json:"enabled"`
	Configured bool `json:"configured"`
}

type contributionSettingsBody struct {
	Enabled bool `json:"enabled"`
}

type ContributionSettingsGetHandler struct {
	Catalog     *product.Catalog
	Contributor product.UpstreamContributor
}

func (h *ContributionSettingsGetHandler) Handle(req Request[struct{}, struct{}]) (ContributionSettingsResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return ContributionSettingsResponse{}, err
	}
	return contributionSettings(req.Context, scope, h.Catalog, h.Contributor)
}

type ContributionSettingsPutHandler struct {
	Catalog     *product.Catalog
	Contributor product.UpstreamContributor
}

func (h *ContributionSettingsPutHandler) Handle(req Request[contributionSettingsBody, struct{}]) (ContributionSettingsResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return ContributionSettingsResponse{}, err
	}
	if err := h.Catalog.SetContributionEnabled(req.Context, scope, req.Body.Enabled); err != nil {
		return ContributionSettingsResponse{}, err
	}
	return contributionSettings(req.Context, scope, h.Catalog, h.Contributor)
}

type ContributionsListHandler struct {
	Catalog *product.Catalog
}

func (h *ContributionsListHandler) Handle(req Request[struct{}, struct{}]) ([]product.ContributionRecord, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	records, err := h.Catalog.ListContributions(req.Context, scope, "")
	if err != nil {
		return nil, err
	}
	if records == nil {
		records = []product.ContributionRecord{}
	}
	return records, nil
}

type productContributionsPathParams struct {
	ID string `json:"id"`
}

type ProductContributionsHandler struct {
	Catalog *product.Catalog
}

func (h *ProductContributionsHandler) Handle(req Request[struct{}, productContributionsPathParams]) ([]product.ContributionRecord, error) {
	if req.PathParams.ID == "" {
		return nil, BadRequest("product id is required")
	}
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	prod, err := h.Catalog.ProductFor(req.Context, scope, req.PathParams.ID)
	if err != nil {
		return nil, err
	}
	if prod == nil {
		return nil, NotFound("product not found")
	}
	records, err := h.Catalog.ListContributions(req.Context, scope, req.PathParams.ID)
	if err != nil {
		return nil, err
	}
	if records == nil {
		records = []product.ContributionRecord{}
	}
	return records, nil
}

type productDetailPathParams struct {
	ID string `json:"id"`
}

type productDetailResponse struct {
	product.Product
	Barcodes []string `json:"barcodes"`
}

type ProductGetHandler struct {
	Catalog *product.Catalog
}

func (h *ProductGetHandler) Handle(req Request[struct{}, productDetailPathParams]) (productDetailResponse, error) {
	if req.PathParams.ID == "" {
		return productDetailResponse{}, BadRequest("product id is required")
	}
	scope, err := requestScope(req.Context)
	if err != nil {
		return productDetailResponse{}, err
	}
	prod, err := h.Catalog.ProductFor(req.Context, scope, req.PathParams.ID)
	if err != nil {
		return productDetailResponse{}, err
	}
	if prod == nil {
		return productDetailResponse{}, NotFound("product not found")
	}
	barcodes, err := h.Catalog.BarcodesForProduct(req.Context, scope, prod.ID)
	if err != nil {
		return productDetailResponse{}, err
	}
	if barcodes == nil {
		barcodes = []string{}
	}
	return productDetailResponse{Product: *prod, Barcodes: barcodes}, nil
}
