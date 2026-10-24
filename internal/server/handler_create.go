package server

import (
	"context"
	stdjson "encoding/json"
	"log"
	"strings"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
)

type CreateHandler struct {
	Catalog     *product.Catalog
	Contributor product.UpstreamContributor
	Groups      *group.Groups
}

type productWriteBody struct {
	Name          string   `json:"name"`
	Category      string   `json:"category"`
	UnitOfMeasure string   `json:"unitOfMeasure"`
	Contribute    bool     `json:"contribute"`
	ContributeTo  string   `json:"contributeTo"`
	Barcode       string   `json:"barcode"`
	NetAmount     *float64 `json:"netAmount"`
	NetUnit       string   `json:"netUnit"`
	PackCount     *int     `json:"packCount"`
	NetSizeSet    bool     `json:"-"`
	PackCountSet  bool     `json:"-"`
}

func (b *productWriteBody) UnmarshalJSON(data []byte) error {
	var raw map[string]stdjson.RawMessage
	if err := stdjson.Unmarshal(data, &raw); err != nil {
		return err
	}
	var plain struct {
		Name          string   `json:"name"`
		Category      string   `json:"category"`
		UnitOfMeasure string   `json:"unitOfMeasure"`
		Contribute    bool     `json:"contribute"`
		ContributeTo  string   `json:"contributeTo"`
		Barcode       string   `json:"barcode"`
		NetAmount     *float64 `json:"netAmount"`
		NetUnit       string   `json:"netUnit"`
		PackCount     *int     `json:"packCount"`
	}
	if err := stdjson.Unmarshal(data, &plain); err != nil {
		return err
	}
	*b = productWriteBody{
		Name:          plain.Name,
		Category:      plain.Category,
		UnitOfMeasure: plain.UnitOfMeasure,
		Contribute:    plain.Contribute,
		ContributeTo:  plain.ContributeTo,
		Barcode:       plain.Barcode,
		NetAmount:     plain.NetAmount,
		NetUnit:       plain.NetUnit,
		PackCount:     plain.PackCount,
	}
	if _, ok := raw["netAmount"]; ok {
		b.NetSizeSet = true
	}
	if _, ok := raw["netUnit"]; ok {
		b.NetSizeSet = true
	}
	if _, ok := raw["packCount"]; ok {
		b.PackCountSet = true
	}
	return nil
}

type productWriteResponse struct {
	product.Product
	Contribution *product.ShareOutcome `json:"contribution,omitempty"`
}

func (h *CreateHandler) Handle(req Request[productWriteBody, struct{}]) (Created, error) {
	if req.Body.Name == "" {
		return Created{}, BadRequest("name is required")
	}
	if err := validateWriteContribution(req.Body); err != nil {
		return Created{}, err
	}

	prod := product.Product{
		ID:            uuid.NewString(),
		Name:          req.Body.Name,
		Category:      req.Body.Category,
		UnitOfMeasure: req.Body.UnitOfMeasure,
	}
	if err := applyWrittenSize(&prod, req.Body, true); err != nil {
		return Created{}, err
	}
	scope, err := requestScope(req.Context)
	if err != nil {
		return Created{}, err
	}
	stored, err := h.Catalog.CreateTypedProduct(req.Context, scope, prod)
	if err != nil {
		return Created{}, inputOrInternal(err)
	}

	outcome, err := recordWriteContribution(req.Context, scope, h.Catalog, h.Contributor, req.Body, stored)
	if err != nil {
		return Created{}, err
	}
	considerGroupProduct(req.Context, scope, h.Groups, stored.ID)
	return Created{Value: productWriteResponse{Product: stored, Contribution: outcome}}, nil
}

func considerGroupProduct(ctx context.Context, scope tenant.Scope, groups *group.Groups, productID string) {
	if groups == nil || strings.TrimSpace(productID) == "" {
		return
	}
	if err := groups.ConsiderProduct(ctx, scope, productID); err != nil {
		log.Printf("could not update group suggestions: %v", err)
	}
}

func validateWriteContribution(body productWriteBody) error {
	if err := product.ValidateContribution(body.Contribute, body.Barcode, product.ExternalSource(body.ContributeTo)); err != nil {
		return BadRequest(err.Error())
	}
	return nil
}

func recordWriteContribution(ctx context.Context, scope tenant.Scope, catalog *product.Catalog, contributor product.UpstreamContributor, body productWriteBody, prod product.Product) (*product.ShareOutcome, error) {
	return product.RecordShare(ctx, scope, catalog, contributor, product.ShareRequest{
		Contribute:            body.Contribute,
		ProductID:             prod.ID,
		Name:                  prod.Name,
		Category:              prod.Category,
		UnitOfMeasure:         prod.UnitOfMeasure,
		Quantity:              prod.UpstreamQuantity(),
		Barcode:               body.Barcode,
		Database:              product.ExternalSource(body.ContributeTo),
		ProductSource:         prod.Source,
		ProductExternalSource: prod.ExternalSource,
	})
}
