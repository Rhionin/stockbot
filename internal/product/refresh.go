package product

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// defaultBackgroundTimeout bounds a background revalidation when
// BackgroundTimeout is unset.
const defaultBackgroundTimeout = 30 * time.Second

// RefreshOutcome classifies a completed revalidation attempt.
type RefreshOutcome string

const (
	OutcomeUpdated          RefreshOutcome = "updated"
	OutcomeUnchanged        RefreshOutcome = "unchanged"
	OutcomeNotFoundUpstream RefreshOutcome = "not_found_upstream"
)

// ErrRefreshTargetMissing indicates a refresh was requested for a product ID
// that has no products row. The handler maps this to HTTP 404.
var ErrRefreshTargetMissing = errors.New("refresh target not found")

// Refresher revalidates externally-sourced product rows against Product Opener
// databases. It is the only component that writes freshness-driven changes to the
// products table.
//
// For source = 'external' rows the product ID is the barcode:
// persistExternalProduct sets Product.ID = barcode for every externally-
// resolved product, and migration 002's placeholder rows have
// id = product_id = barcode as well. So every external row's ID is its
// barcode by construction, and Refresh can call Upstream.LookupIn with the
// product ID directly, with no second query against barcodes.
type Refresher struct {
	Catalog interface {
		GetProductByID(ctx context.Context, id string) (*Product, error)
		SaveRefresh(ctx context.Context, p Product, refreshedAt time.Time) error
		MarkRefreshed(ctx context.Context, id string, refreshedAt time.Time) error
	}
	Upstream interface {
		LookupIn(ctx context.Context, source ExternalSource, barcode string) (*ProductSummary, error)
	}

	// TTL is the age at which an external row becomes stale.
	TTL time.Duration

	// Now supplies the current time. Defaults to time.Now when nil.
	Now func() time.Time

	// ExternalLookupEnabled reports whether upstream requests are permitted.
	// False disables all revalidation, including refreshed_at stamping.
	ExternalLookupEnabled bool

	// BackgroundTimeout bounds a background revalidation. Defaults to 30s
	// when zero.
	BackgroundTimeout time.Duration

	// Tags stores Open Food Facts category tags seen during a refresh. Nil skips that write.
	Tags interface {
		SaveOffTags(ctx context.Context, productID string, categories, foodGroups []string) error
	}

	mu       sync.Mutex
	inFlight map[string]struct{}
	wg       sync.WaitGroup
}

// now returns the current time, defaulting to time.Now so existing
// construction sites that leave Now nil behave unchanged.
func (r *Refresher) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// backgroundTimeout bounds a background revalidation goroutine, defaulting
// to defaultBackgroundTimeout so existing construction sites that leave
// BackgroundTimeout unset behave unchanged.
func (r *Refresher) backgroundTimeout() time.Duration {
	if r.BackgroundTimeout != 0 {
		return r.BackgroundTimeout
	}
	return defaultBackgroundTimeout
}

// isStale reports whether p is due for revalidation: a row that has never
// been checked (RefreshedAt is nil) is always stale, and otherwise a row is
// stale once TTL has elapsed since its last check.
func (r *Refresher) isStale(p *Product) bool {
	if p.RefreshedAt == nil {
		return true
	}
	return r.now().Sub(*p.RefreshedAt) > r.TTL
}

// mergeRefresh returns cached with upstream values applied. A field is taken
// from upstream only when upstream actually reported a value for it; an empty
// upstream field means "no opinion", not "clear this". This guard matters in
// practice: OpenFoodFactsClient.LookupBarcode hardcodes UnitOfMeasure: "" (Open
// Food Facts is never asked for a unit and never supplies one), so an
// unconditional overwrite would blank the unit on every product the first time
// it is refreshed. The same guard keeps a rotated-but-missing thumbnail from
// wiping a working one. source, created_at, id, and name_overridden are never
// touched. upstream is taken by value because the fake OFF client returns a
// pointer into its own store, and mergeRefresh must not mutate it.
func mergeRefresh(cached Product, upstream ProductSummary) Product {
	merged := cached
	if upstream.Name != "" && !cached.NameOverridden {
		merged.Name = upstream.Name
	}
	if upstream.Category != "" {
		merged.Category = upstream.Category
	}
	if upstream.UnitOfMeasure != "" {
		merged.UnitOfMeasure = upstream.UnitOfMeasure
	}
	if upstream.ImageURL != "" {
		merged.ImageURL = upstream.ImageURL
	}
	// A size the person typed stays. Open Food Facts replaces an unknown size
	// and a backfill, because the upstream measure is the one they did not type.
	if merged.NetSizeOrigin != OriginManual && upstream.NetBaseValue != nil && upstream.NetDimension != "" {
		base := *upstream.NetBaseValue
		merged.NetBaseValue = &base
		merged.NetDimension = upstream.NetDimension
		merged.NetSizeOrigin = OriginOff
		merged.fillDisplay()
	}
	if merged.NetSizeOrigin != OriginManual && merged.PackCount == nil && upstream.PackCount != nil && *upstream.PackCount >= 1 {
		pack := *upstream.PackCount
		merged.PackCount = &pack
	}
	return merged
}

// classify compares the four merged field values against their pre-refresh
// values and reports whether the refresh changed anything a client cares
// about. refreshed_at moving never by itself counts as an update.
func classify(before, after Product) RefreshOutcome {
	if before.Name != after.Name ||
		before.Category != after.Category ||
		before.UnitOfMeasure != after.UnitOfMeasure ||
		before.ImageURL != after.ImageURL ||
		!sameOptionalFloat(before.NetBaseValue, after.NetBaseValue) ||
		before.NetDimension != after.NetDimension ||
		before.NetSizeOrigin != after.NetSizeOrigin ||
		!sameOptionalInt(before.PackCount, after.PackCount) {
		return OutcomeUpdated
	}
	return OutcomeUnchanged
}

func sameOptionalFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameOptionalInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// Refresh revalidates a single product row against its upstream database. It is
// the single core path both ScheduleRefresh and the synchronous refresh
// endpoint call, and it ignores the TTL entirely: staleness is only ever
// consulted by ScheduleRefresh, so a caller of Refresh always gets a real
// upstream attempt.
//
// ExternalLookupEnabled is consulted directly rather than inferred from an
// error value: disabledProductLookup returns ErrProductNotFound for every
// barcode, and treating that as a genuine upstream not-found would stamp
// refreshed_at and silently suppress real revalidation for a full TTL while
// external lookup is toggled off.
func (r *Refresher) Refresh(ctx context.Context, productID string) (RefreshOutcome, error) {
	if !r.ExternalLookupEnabled {
		return OutcomeUnchanged, nil
	}

	row, err := r.Catalog.GetProductByID(ctx, productID)
	if err != nil {
		return OutcomeUnchanged, err
	}
	if row == nil {
		return OutcomeUnchanged, ErrRefreshTargetMissing
	}

	if row.Source != SourceExternal {
		return OutcomeUnchanged, nil
	}

	now := r.now()

	// A Legacy_External_Row has no provenance, so it is revalidated against
	// Open Food Facts — the only database it could have come from, since it
	// was cached before any other was queried.
	target := row.ExternalSource
	if target == "" {
		target = ExternalSourceOpenFoodFacts
	}

	// Exactly one database, named by the row. The external-row invariant
	// "product ID is the barcode" still holds, so row.ID is passed directly
	// with no barcodes query.
	upstream, err := r.Upstream.LookupIn(ctx, target, row.ID)
	if err != nil {
		writeCtx, cancel := r.catalogWriteContext(ctx)
		defer cancel()
		if markErr := r.Catalog.MarkRefreshed(writeCtx, row.ID, now); markErr != nil {
			return OutcomeUnchanged, markErr
		}
		if errors.Is(err, ErrProductNotFound) {
			return OutcomeNotFoundUpstream, nil
		}
		return OutcomeUnchanged, err
	}

	merged := mergeRefresh(*row, *upstream)
	// One unconditional write covers two requirements: for a row that already
	// had provenance this is a no-op rewrite of the same value, and for a
	// Legacy_External_Row it is the stamp that records where the data came from.
	merged.ExternalSource = target

	writeCtx, cancel := r.catalogWriteContext(ctx)
	defer cancel()
	if err := r.Catalog.SaveRefresh(writeCtx, merged, now); err != nil {
		return OutcomeUnchanged, err
	}
	if err := saveOffTags(writeCtx, r.Tags, upstream); err != nil {
		return OutcomeUnchanged, err
	}
	return classify(*row, merged), nil
}

// catalogWriteContext bounds a catalog write after LookupIn returns.
// The lookup context is often already canceled by then, and database/sql
// drops a write on a done context. A missed refreshed_at stamp makes the
// next read retry immediately.
func (r *Refresher) catalogWriteContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), r.backgroundTimeout())
}

// ScheduleRefresh revalidates productID in the background if, and only if,
// it is a stale external row not already being revalidated. The gate (is
// external lookup enabled? does the row exist? is it external? is it stale?
// is a revalidation already in flight?) runs synchronously on ctx, because
// it is a single primary-key read against local SQLite. A fresh row
// schedules nothing: no goroutine, no WaitGroup entry. This keeps the clock
// and the TTL out of LookupService.
//
// ScheduleRefresh never returns an error; a background revalidation failure
// is logged, not surfaced, because the caller's response already went out
// unaffected by revalidation.
func (r *Refresher) ScheduleRefresh(ctx context.Context, productID string) {
	if !r.ExternalLookupEnabled {
		return
	}

	row, err := r.Catalog.GetProductByID(ctx, productID)
	if err != nil || row == nil {
		return
	}
	if row.Source != SourceExternal {
		return
	}
	if !r.isStale(row) {
		return
	}

	r.mu.Lock()
	if r.inFlight == nil {
		r.inFlight = make(map[string]struct{})
	}
	if _, ok := r.inFlight[productID]; ok {
		r.mu.Unlock()
		return
	}
	r.inFlight[productID] = struct{}{}
	r.wg.Add(1)
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.inFlight, productID)
			r.mu.Unlock()
			r.wg.Done()
		}()

		// The caller cancels ctx when it finishes the response. Detach so that
		// cancellation does not abort the revalidation.
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.backgroundTimeout())
		defer cancel()

		if _, err := r.Refresh(bgCtx, productID); err != nil {
			log.Printf("refresh %s failed: %v", productID, err)
		}
	}()
}

// Wait blocks until every goroutine started by ScheduleRefresh has returned.
// It is a legitimate production lifecycle API (drain-before-exit) as well as
// the mechanism tests use to await background revalidation deterministically:
// wg.Add(1) happens synchronously in ScheduleRefresh before it returns, so by
// the time a caller observes a response the counter is already incremented
// and Wait cannot return early.
func (r *Refresher) Wait() {
	r.wg.Wait()
}
