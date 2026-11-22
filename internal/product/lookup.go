package product

import (
	"context"
	"errors"
	"fmt"
	"time"

	barcodec "github.com/Rhionin/stockbot/internal/barcode"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// defaultMissTTL is the default age at which a confirmed-miss record expires.
const defaultMissTTL = 7 * 24 * time.Hour

// ErrNotLookupable means the barcode is QR-shaped and must not enter tiered lookup.
// Callers that skipped classification should treat it like a QR payload, not a miss.
var ErrNotLookupable = errors.New("barcode is not lookupable")

// LookupResult represents the outcome of a three-tier product lookup.
type LookupResult struct {
	// Product is the single matching product, or nil if not found.
	Product *ProductSummary `json:"product"`

	// Source indicates where the result came from: "user_override", "global", "external", or empty when not found.
	Source string `json:"source"`
}

// IsFound returns true if the lookup found a product.
func (r LookupResult) IsFound() bool {
	return r.Product != nil
}

// LookupService orchestrates three-tier product lookup: user overrides first,
// global DB second, External Lookup API third.
type LookupService struct {
	Catalog interface {
		LookupByBarcode(ctx context.Context, scope tenant.Scope, barcode string) (*ProductSummary, error)
		GetProductByID(ctx context.Context, id string) (*Product, error)
		CreateProduct(ctx context.Context, product Product) error
		SaveNetSize(ctx context.Context, product Product) error
		UpsertBarcodeMapping(ctx context.Context, barcode, productID, source, userID string) error
		GetBarcodeMiss(ctx context.Context, barcode string) (*time.Time, error)
		RecordBarcodeMiss(ctx context.Context, barcode string, checkedAt time.Time) error
		DeleteBarcodeMiss(ctx context.Context, barcode string) error
	}
	Upstream interface {
		Lookup(ctx context.Context, barcode string) FanOutResult
	}

	// Refresher schedules background revalidation of stale cached rows.
	// Nil disables revalidation, so every existing LookupService literal
	// keeps compiling and behaving as it does today.
	Refresher interface {
		ScheduleRefresh(ctx context.Context, productID string)
	}

	// Now supplies the current time. Defaults to time.Now when nil.
	Now func() time.Time

	// MissTTL is the age at which a recorded confirmed miss expires. Defaults to
	// defaultMissTTL when zero, so existing struct literals keep compiling.
	MissTTL time.Duration

	// Tags stores Open Food Facts category tags for a product. Nil skips that write.
	Tags interface {
		SaveOffTags(ctx context.Context, productID string, categories, foodGroups []string) error
	}
}

// now returns the current time, defaulting to time.Now().UTC so existing
// construction sites that leave Now nil behave unchanged. UTC drops the
// monotonic clock reading before checked_at and refreshed_at are stored.
func (s *LookupService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// missTTL returns the time-to-live for a confirmed barcode miss, defaulting to
// defaultMissTTL when MissTTL is zero.
func (s *LookupService) missTTL() time.Duration {
	if s.MissTTL > 0 {
		return s.MissTTL
	}
	return defaultMissTTL
}

// Lookup resolves a barcode for the household. QR-shaped input returns empty
// without I/O. Local-only codes (4–20 letter+digit tokens) consult only
// LookupByBarcode and never upstream or barcode_misses. Product barcodes (1–14
// digits) run the full tiered lookup:
//
//  1. User overrides for this specific user
//  2. Global database entries
//  3. Confirmed misses (barcodes every Product Opener database reported unknown)
//  4. Open Products Facts external API (all four Product Opener databases)
//
// Returns:
//   - LookupResult with Product set if a match is found
//   - LookupResult with IsFound() == false if no product is found anywhere
//   - error if a non-recoverable error occurs
//
// Note: The UNIQUE (barcode, source, user_id) constraint in the schema prevents
// multiple products mapping to the same barcode within a source. Barcode conflicts
// are resolved via the flagged entry workflow where users can create overrides.
//
// Requirements: 1.4, 1.6, 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.8, 6.1, 6.2, 6.6, 10.5, 10.6
func (s *LookupService) Lookup(ctx context.Context, scope tenant.Scope, barcode string) (LookupResult, error) {
	if barcode == "" {
		return LookupResult{}, fmt.Errorf("barcode cannot be empty")
	}
	if err := scope.Require(); err != nil {
		return LookupResult{}, err
	}
	trimmed := barcodec.TrimASCII(barcode)
	kind := barcodec.Classify(barcode)
	if kind == barcodec.LookupQR {
		return LookupResult{}, ErrNotLookupable
	}

	// Tier 1 & 2: this household's mapping, then the global catalog.
	product, err := s.Catalog.LookupByBarcode(ctx, scope, trimmed)
	if err != nil {
		return LookupResult{}, fmt.Errorf("database lookup failed: %w", err)
	}

	// If we found exactly one product in the database, return it.
	if product != nil {
		result := LookupResult{
			Product: product,
			Source:  "global", // Conservative default; the catalog prioritizes overrides internally
		}
		if kind == barcodec.LookupProduct && s.Refresher != nil {
			s.Refresher.ScheduleRefresh(ctx, product.ID)
		}
		return result, nil
	}

	if kind == barcodec.LookupLocalOnly {
		// Local miss: no external hosts, no barcode_misses (scan path flags the entry).
		return LookupResult{}, nil
	}

	// Confirmed_Miss gate: check if this barcode was recorded as unknown within the Miss TTL.
	// This sits AFTER Tier 1/2 deliberately: a user who resolved a flagged entry by creating
	// a product has a barcodes mapping, so Tier 2 answers and the stale miss row is never
	// consulted (Requirement 5.8). Reordering these two steps would break that requirement.
	miss, err := s.Catalog.GetBarcodeMiss(ctx, trimmed)
	if err != nil {
		return LookupResult{}, fmt.Errorf("could not check known-unknown barcodes: %w", err)
	}
	if miss != nil && s.now().Sub(*miss) <= s.missTTL() {
		// Same empty result and empty Source a fresh all-miss returns, so the
		// flagged-entry workflow cannot tell the difference (Requirement 5.6).
		return LookupResult{}, nil
	}

	// Tier 3: Fall back to external API (all Product Opener databases).
	result := s.Upstream.Lookup(ctx, trimmed)
	switch result.Outcome {
	case FanOutHit:
		// External API returned a product. Persist it so the returned ID references a
		// real products row and a subsequent scan resolves at Tier 2.
		if err := s.persistExternalProduct(ctx, result.Product, result.Source); err != nil {
			return LookupResult{}, err
		}

		// Set the external source on the ProductSummary so it round-trips through
		// responses (Requirement 9.1).
		result.Product.ExternalSource = result.Source

		return LookupResult{
			Product: result.Product,
			Source:  "external",
		}, nil

	case FanOutConfirmedMiss:
		// All Product Opener databases reported the barcode unknown and no database errored.
		// Record this confirmed miss so repeated scans stop fanning out requests.
		if err := s.Catalog.RecordBarcodeMiss(ctx, trimmed, s.now()); err != nil {
			return LookupResult{}, err
		}
		return LookupResult{}, nil

	default: // FanOutUnresolved
		// At least one Product Opener database errored and no database supplied a hit.
		// Write nothing. The barcode stays retryable (Requirements 6.1–6.3)
		// and the caller learns nothing about the upstream failure (6.6).
		return LookupResult{}, nil
	}
}

// persistExternalProduct idempotently stores an externally-resolved product and
// its barcode mapping. For external results product.ID equals the barcode, so
// the products row and the barcodes mapping share that value. Repeated calls for
// the same barcode create no duplicate rows.
func (s *LookupService) persistExternalProduct(ctx context.Context, product *ProductSummary, source ExternalSource) error {
	existing, err := s.Catalog.GetProductByID(ctx, product.ID)
	if err != nil {
		return fmt.Errorf("could not check for existing product: %w", err)
	}
	if existing == nil {
		now := s.now()
		created := Product{
			ID:             product.ID,
			Name:           product.Name,
			Category:       product.Category,
			UnitOfMeasure:  product.UnitOfMeasure,
			ImageURL:       product.ImageURL,
			Source:         SourceExternal,
			ExternalSource: source,
			RefreshedAt:    &now,
		}
		applyUpstreamSize(&created, product)
		err = s.Catalog.CreateProduct(ctx, created)
		if err != nil {
			return fmt.Errorf("could not save product: %w", err)
		}
	} else if existing.NetSizeOrigin == "" {
		applyUpstreamSize(existing, product)
		if existing.NetSizeOrigin == OriginOff {
			if err := s.Catalog.SaveNetSize(ctx, *existing); err != nil {
				return fmt.Errorf("could not save product size: %w", err)
			}
		}
	}

	// External products are stored as global catalog entries; the barcodes.source
	// CHECK permits only "global" and "user_override".
	if err := s.Catalog.UpsertBarcodeMapping(ctx, product.ID, product.ID, "global", ""); err != nil {
		return fmt.Errorf("could not save barcode mapping: %w", err)
	}

	// A barcode that now resolves will never reach the miss gate again
	// (Tier 2 answers first), so this row is dead data. Deleting it keeps the
	// table from accumulating entries for barcodes that are no longer misses.
	if err := s.Catalog.DeleteBarcodeMiss(ctx, product.ID); err != nil {
		return fmt.Errorf("could not delete barcode miss: %w", err)
	}
	if err := saveOffTags(ctx, s.Tags, product); err != nil {
		return err
	}

	return nil
}

func saveOffTags(ctx context.Context, tags interface {
	SaveOffTags(ctx context.Context, productID string, categories, foodGroups []string) error
}, product *ProductSummary) error {
	if tags == nil || product == nil {
		return nil
	}
	if len(product.CategoriesTags) == 0 && len(product.FoodGroupsTags) == 0 {
		return nil
	}
	if err := tags.SaveOffTags(ctx, product.ID, product.CategoriesTags, product.FoodGroupsTags); err != nil {
		return fmt.Errorf("could not save product tags: %w", err)
	}
	return nil
}

// applyUpstreamSize copies a parsed Open Food Facts size onto a product and
// marks the origin off. An empty upstream size leaves the product alone.
func applyUpstreamSize(p *Product, summary *ProductSummary) {
	if p == nil || summary == nil || summary.NetBaseValue == nil || summary.NetDimension == "" {
		return
	}
	base := *summary.NetBaseValue
	p.NetBaseValue = &base
	p.NetDimension = summary.NetDimension
	p.NetSizeOrigin = OriginOff
	if p.PackCount == nil && summary.PackCount != nil && *summary.PackCount >= 1 {
		pack := *summary.PackCount
		p.PackCount = &pack
	}
	p.fillDisplay()
}
