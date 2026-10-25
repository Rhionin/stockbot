package server

import (
	"context"
	stdjson "encoding/json"
	"log"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
)

type CreateHandler struct {
	Catalog     *product.Catalog
	Contributor product.UpstreamContributor
	Groups      *group.Groups
	Pantry      *inventory.Pantry
	Events      interface {
		PublishInventoryEvent(householdID string, item inventory.InventoryItem)
	}
}

type productWriteBody struct {
	Name          string   `json:"name"`
	Brand         string   `json:"brand"`
	Size          string   `json:"size"`
	Category      string   `json:"category"`
	UnitOfMeasure string   `json:"unitOfMeasure"`
	Contribute    bool     `json:"contribute"`
	ContributeTo  string   `json:"contributeTo"`
	Barcode       string   `json:"barcode"`
	CreateItem    bool     `json:"createItem"`
	NetAmount     *float64 `json:"netAmount"`
	NetUnit       string   `json:"netUnit"`
	PackCount     *int     `json:"packCount"`
	NetSizeSet    bool     `json:"-"`
	PackCountSet  bool     `json:"-"`
	SizeSet       bool     `json:"-"`
}

func (b *productWriteBody) UnmarshalJSON(data []byte) error {
	var raw map[string]stdjson.RawMessage
	if err := stdjson.Unmarshal(data, &raw); err != nil {
		return err
	}
	var plain struct {
		Name          string   `json:"name"`
		Brand         string   `json:"brand"`
		Size          string   `json:"size"`
		Category      string   `json:"category"`
		UnitOfMeasure string   `json:"unitOfMeasure"`
		Contribute    bool     `json:"contribute"`
		ContributeTo  string   `json:"contributeTo"`
		Barcode       string   `json:"barcode"`
		CreateItem    bool     `json:"createItem"`
		NetAmount     *float64 `json:"netAmount"`
		NetUnit       string   `json:"netUnit"`
		PackCount     *int     `json:"packCount"`
	}
	if err := stdjson.Unmarshal(data, &plain); err != nil {
		return err
	}
	*b = productWriteBody{
		Name:          plain.Name,
		Brand:         plain.Brand,
		Size:          plain.Size,
		Category:      plain.Category,
		UnitOfMeasure: plain.UnitOfMeasure,
		Contribute:    plain.Contribute,
		ContributeTo:  plain.ContributeTo,
		Barcode:       plain.Barcode,
		CreateItem:    plain.CreateItem,
		NetAmount:     plain.NetAmount,
		NetUnit:       plain.NetUnit,
		PackCount:     plain.PackCount,
	}
	if _, ok := raw["brand"]; ok {
		// brand is optional; presence is tracked only when needed later
	}
	if _, ok := raw["size"]; ok {
		b.SizeSet = true
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
	ItemID       string                  `json:"itemId,omitempty"`
	Contribution *product.ShareOutcome   `json:"contribution,omitempty"`
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
	if err := applySizeText(&prod, req.Body); err != nil {
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

	if strings.TrimSpace(req.Body.Brand) != "" {
		if err := h.Catalog.SaveBrand(req.Context, scope, stored.ID, req.Body.Brand); err != nil {
			return Created{}, inputOrInternal(err)
		}
	}
	outcome, err := recordWriteContribution(req.Context, scope, h.Catalog, h.Contributor, req.Body, stored)
	if err != nil {
		return Created{}, err
	}
	considerGroupProduct(req.Context, scope, h.Groups, stored.ID)
	resp := productWriteResponse{Product: stored, Contribution: outcome}
	if shouldEnsureHouseholdItem(req.Body) {
		itemID, err := ensureHouseholdItem(req.Context, scope, h.Pantry, h.Events, stored.ID)
		if err != nil {
			return Created{}, err
		}
		resp.ItemID = itemID
	}
	return Created{Value: resp}, nil
}

func shouldEnsureHouseholdItem(body productWriteBody) bool {
	return strings.TrimSpace(body.Barcode) == "" || body.CreateItem
}

func ensureHouseholdItem(ctx context.Context, scope tenant.Scope, pantry *inventory.Pantry, events interface {
	PublishInventoryEvent(householdID string, item inventory.InventoryItem)
}, productID string) (string, error) {
	if pantry == nil {
		return "", InternalError(nil)
	}
	item, err := pantry.GetOrCreateItem(ctx, scope, productID)
	if err != nil {
		return "", InternalError(err)
	}
	if events != nil {
		inv, err := pantry.GetInventoryItem(ctx, scope, item.ID, time.Now(), inventory.DefaultWarningDays)
		if err == nil && inv != nil {
			events.PublishInventoryEvent(scope.HouseholdID(), *inv)
		}
	}
	return item.ID, nil
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
