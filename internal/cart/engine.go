// Package cart provides provider-agnostic provisioning capability types and interfaces.
package cart

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/suggestion"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ReplenishmentMode identifies the shopping list calculation mode.
type ReplenishmentMode string

const (
	ReplenishMode ReplenishmentMode = "replenish"
	TargetMode    ReplenishmentMode = "target"
)

// Valid returns true if the mode is a valid value.
func (m ReplenishmentMode) Valid() bool {
	return m == ReplenishMode || m == TargetMode
}

// Engine implements the provisioning operation.
// It coordinates the flow from shopping list → identities → batches → provider → ledger.
type Engine struct {
	mu       sync.Mutex
	inFlight map[ProviderID]struct{} // tracks in-flight provisioning operations per provider

	registry       *Registry
	ledger         *Ledger
	tokenBroker    *connection.TokenBroker
	shoppingList   shopping.Store
	pantry         inventory.Pantry
	consumptionLog suggestion.ConsumptionLog
	catalog        product.Catalog
}

// NewEngine creates a new provisioning engine.
func NewEngine(registry *Registry, ledger *Ledger, tokenBroker *connection.TokenBroker) *Engine {
	return &Engine{
		registry:    registry,
		ledger:      ledger,
		tokenBroker: tokenBroker,
		inFlight:    make(map[ProviderID]struct{}),
	}
}

// SetShoppingList sets the shopping list store.
func (e *Engine) SetShoppingList(shoppingList shopping.Store) {
	e.shoppingList = shoppingList
}

// SetPantry sets the pantry.
func (e *Engine) SetPantry(pantry inventory.Pantry) {
	e.pantry = pantry
}

// SetConsumptionLog sets the consumption log.
func (e *Engine) SetConsumptionLog(consumptionLog suggestion.ConsumptionLog) {
	e.consumptionLog = consumptionLog
}

// SetCatalog sets the product catalog.
func (e *Engine) SetCatalog(catalog product.Catalog) {
	e.catalog = catalog
}

// Provision performs the complete provisioning operation for one provider.
// It claims an in-flight slot, computes quantities, resolves identities, batches requests,
// submits to the provider, records outcomes, and updates the ledger.
func (e *Engine) Provision(ctx context.Context, scope tenant.Scope, providerID ProviderID) (ProvisionReport, error) {
	if err := scope.Require(); err != nil {
		return ProvisionReport{}, err
	}
	e.mu.Lock()
	if _, exists := e.inFlight[providerID]; exists {
		e.mu.Unlock()
		return ProvisionReport{}, &ProvisionConflict{
			Reason: fmt.Sprintf("provisioning operation already in progress for provider %q", providerID),
		}
	}
	e.inFlight[providerID] = struct{}{}
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.inFlight, providerID)
		e.mu.Unlock()
	}()

	// Get the provider from registry
	provider, ok := e.registry.Get(providerID)
	if !ok {
		return ProvisionReport{}, fmt.Errorf("provider %q not registered", providerID)
	}

	caps := provider.Capabilities()

	// Check connection state
	conn, err := e.tokenBroker.Directory.Read(ctx, scope, string(providerID))
	if err != nil {
		return ProvisionReport{}, fmt.Errorf("failed to read connection: %w", err)
	}

	// For auth=none providers, the connection may not exist - that's OK
	shouldCheckConnection := caps.Auth != AuthNone
	if shouldCheckConnection && (conn == nil || conn.State != connection.StateConnected) {
		return ProvisionReport{}, &ProvisionConflict{
			Reason: fmt.Sprintf("provider %q is not connected", providerID),
		}
	}

	// Get computed entries from shopping list (with ledger-net quantities)
	entries, err := e.getComputedEntries(ctx, scope, providerID)
	if err != nil {
		return ProvisionReport{}, fmt.Errorf("failed to compute entries: %w", err)
	}

	// Resolve identities for each entry
	resolvedItems, unresolved, err := e.resolveIdentities(ctx, scope, provider, caps, providerID, entries)
	if err != nil {
		return ProvisionReport{}, fmt.Errorf("failed to resolve identities: %w", err)
	}

	// Combine resolved and unresolved into outcomes for entries with quantity >= 1
	outcomes := make([]EntryOutcome, 0, len(resolvedItems)+len(unresolved))

	// Add unresolved items as failed with a skip reason (no_upc / no_match).
	for _, item := range unresolved {
		reason := item.UnresolvedReason
		if reason == "" {
			reason = ReasonNoMatch
		}
		outcomes = append(outcomes, EntryOutcome{
			EntryID:  item.EntryID,
			ItemID:   item.ItemID,
			Name:     item.Name,
			Quantity: item.Quantity,
			Outcome:  OutcomeFailed,
			Reason:   reason,
		})
	}

	// If no resolved items, return early with just unresolved items
	if len(resolvedItems) == 0 {
		return ProvisionReport{
			Provider:  providerID,
			Confirmed: 0,
			Entries:   outcomes,
		}, nil
	}

	// Batch resolved items into ProvisionRequests
	batches := e.batchRequests(providerID, resolvedItems, caps)

	// Get credential based on auth type
	var cred Credential
	if caps.Auth == AuthNone {
		cred = NoCredential{}
	} else if caps.Auth == AuthOAuth2 {
		// For OAuth2, use the token broker to get an access token
		token, err := e.tokenBroker.AccessToken(ctx, scope, string(providerID), e.exchangeRefresh(ctx, provider))
		if err != nil {
			if OAuthFailureReason(err) == ReasonReconnectRequired {
				_ = e.markConnectionReauthRequired(ctx, scope, providerID)
			}
			return ProvisionReport{}, ClassifyOAuthAccessError(providerID, err)
		}
		cred = NewBearerCredential(token)
	}

	// Submit each batch to provider
	var confirmedCount int
	var reportHandoff *HandoffArtifact
	var reconnectRequired bool
	var haltRemaining bool
	var haltFailedReason OutcomeReason
	var haltFailedDetail string
	for _, req := range batches {
		if haltRemaining {
			outcomes = appendProvisionOutcomes(outcomes, req, OutcomeFailed, haltFailedReason, haltFailedDetail)
			continue
		}

		var result ProvisionResult
		err = nil
		if caps.Delivery == DeliveryServerPush {
			// Get the ServerPush interface
			if serverPush, ok := provider.(ServerPush); ok {
				result, err = serverPush.Add(ctx, cred, req)
			} else {
				err = fmt.Errorf("provider does not support server_push delivery")
			}
		} else if handoff, ok := provider.(HandoffBuilder); ok {
			artifact, buildErr := handoff.BuildHandoff(req)
			if buildErr != nil {
				err = buildErr
			} else {
				if reportHandoff == nil {
					reportHandoff = &artifact
				}
				result = ProvisionResult{Disposition: DispositionAccepted}
			}
		} else {
			err = fmt.Errorf("provider does not support %s delivery", caps.Delivery)
		}

		if err != nil {
			if provErr := wholeRequestProvisionError(providerID, confirmedCount, result, err); provErr != nil {
				return ProvisionReport{}, provErr
			}
			if confirmedCount > 0 {
				if batchFailureNeedsReconnect(result, err) {
					reconnectRequired = true
					if markErr := e.markConnectionReauthRequired(ctx, scope, providerID); markErr != nil {
						return ProvisionReport{}, markErr
					}
					detail := OAuthFailureDetail(ReasonReconnectRequired)
					outcomes = appendProvisionOutcomes(outcomes, req, OutcomeFailed, ReasonReconnectRequired, detail)
					haltRemaining = true
					haltFailedReason = ReasonReconnectRequired
					haltFailedDetail = detail
					continue
				}
				if batchFailureIsIndeterminate(result, err) {
					outcomes = appendProvisionOutcomes(outcomes, req, OutcomeUnknown, ReasonRequestIncomplete, requestIncompleteDetail)
					haltRemaining = true
					haltFailedReason = ReasonRequestIncomplete
					haltFailedDetail = lineNotSentDetail
					continue
				}
			}
			if result.Disposition == DispositionRejected {
				for _, line := range req.Lines {
					for _, item := range line.Accounts {
						outcomes = append(outcomes, EntryOutcome{
							EntryID:  item.EntryID,
							ItemID:   item.ItemID,
							Name:     item.Name,
							Quantity: item.Quantity,
							Outcome:  OutcomeFailed,
							Reason:   ReasonInvalidLine,
							Detail:   "This line could not be sent to the store.",
						})
					}
				}
				continue
			}
			outcomes = appendProvisionOutcomes(outcomes, req, OutcomeUnknown, ReasonRequestIncomplete, requestIncompleteDetail)
			continue
		}

		if result.Disposition == DispositionIndeterminate {
			outcomes = appendProvisionOutcomes(outcomes, req, OutcomeUnknown, ReasonRequestIncomplete, requestIncompleteDetail)
			if confirmedCount > 0 {
				haltRemaining = true
				haltFailedReason = ReasonRequestIncomplete
				haltFailedDetail = lineNotSentDetail
			}
			continue
		}

		if result.Disposition == DispositionRejected {
			detail := batchRejectedDetail(result.ProviderErrorCode)
			for _, line := range req.Lines {
				for _, item := range line.Accounts {
					outcomes = append(outcomes, EntryOutcome{
						EntryID:  item.EntryID,
						ItemID:   item.ItemID,
						Name:     item.Name,
						Quantity: item.Quantity,
						Outcome:  OutcomeFailed,
						Reason:   ReasonBatchRejected,
						Detail:   detail,
					})
				}
			}
			continue
		}

		// A handoff artifact carries no per-item result. Record unknown
		// and leave the ledger alone; the owner resolves it later.
		if caps.Delivery == DeliveryClientHandoff {
			for _, line := range req.Lines {
				for _, item := range line.Accounts {
					outcomes = append(outcomes, EntryOutcome{
						EntryID:  item.EntryID,
						ItemID:   item.ItemID,
						Name:     item.Name,
						Quantity: item.Quantity,
						Outcome:  OutcomeUnknown,
						Reason:   ReasonNoPerItemResult,
					})
				}
			}
			continue
		}

		// Accepted: record outcomes and update ledger
		var acceptedCount int
		switch caps.Confirmation {
		case ConfirmPerLine:
			// A line the provider did not confirm is failed and must not
			// advance the ledger. Only the confirmed lines are written.
			accepted := make([]ProvisionLine, 0, len(req.Lines))
			for _, line := range req.Lines {
				confirmed := result.PerLine[line.Identity]
				outcome := OutcomeFailed
				reason := ReasonUnavailable
				if confirmed {
					outcome = OutcomeConfirmed
					reason = ""
					accepted = append(accepted, line)
				}
				for _, item := range line.Accounts {
					outcomes = append(outcomes, EntryOutcome{
						EntryID:  item.EntryID,
						ItemID:   item.ItemID,
						Name:     item.Name,
						Quantity: item.Quantity,
						Outcome:  outcome,
						Reason:   reason,
					})
					if confirmed {
						acceptedCount++
					}
				}
			}
			req.Lines = accepted
		case ConfirmPerRequest:
			// per_request: every accounted entry is confirmed
			for _, line := range req.Lines {
				for _, item := range line.Accounts {
					outcomes = append(outcomes, EntryOutcome{
						EntryID:  item.EntryID,
						ItemID:   item.ItemID,
						Name:     item.Name,
						Quantity: item.Quantity,
						Outcome:  OutcomeConfirmed,
						Reason:   "", // not used for confirmed
					})
					acceptedCount++
				}
			}
		case ConfirmNone:
			// none: all accounted entries are unknown
			for _, line := range req.Lines {
				for _, item := range line.Accounts {
					outcomes = append(outcomes, EntryOutcome{
						EntryID:  item.EntryID,
						ItemID:   item.ItemID,
						Name:     item.Name,
						Quantity: item.Quantity,
						Outcome:  OutcomeUnknown,
						Reason:   ReasonNoPerItemResult,
					})
				}
			}
		}

		// Update ledger for accepted items
		if acceptedCount > 0 {
			if err := e.updateLedgerAndAdjustments(ctx, scope, providerID, req); err != nil {
				// If ledger update fails, we have a partial failure
				return ProvisionReport{
					Provider:  providerID,
					Confirmed: confirmedCount,
					Entries:   outcomes,
				}, fmt.Errorf("failed to update ledger: %w", err)
			}
			confirmedCount += acceptedCount
		}
	}

	return ProvisionReport{
		Provider:           providerID,
		Confirmed:          confirmedCount,
		Entries:            outcomes,
		Handoff:            reportHandoff,
		ReconnectRequired:  reconnectRequired,
	}, nil
}

func batchFailureNeedsReconnect(result ProvisionResult, err error) bool {
	if CartResponseNeedsReconnect(result.Status) {
		return true
	}
	return OAuthFailureReason(err) == ReasonReconnectRequired
}

func batchFailureIsIndeterminate(result ProvisionResult, err error) bool {
	if result.Disposition == DispositionIndeterminate {
		return true
	}
	if err == nil {
		return false
	}
	if batchFailureNeedsReconnect(result, err) {
		return false
	}
	return result.Status >= 500 || result.Status == 0
}

const (
	requestIncompleteDetail = "The store did not confirm this item."
	lineNotSentDetail       = "This line was not sent."
)

func appendProvisionOutcomes(outcomes []EntryOutcome, req ProvisionRequest, outcome Outcome, reason OutcomeReason, detail string) []EntryOutcome {
	for _, line := range req.Lines {
		for _, item := range line.Accounts {
			outcomes = append(outcomes, EntryOutcome{
				EntryID:  item.EntryID,
				ItemID:   item.ItemID,
				Name:     item.Name,
				Quantity: item.Quantity,
				Outcome:  outcome,
				Reason:   reason,
				Detail:   detail,
			})
		}
	}
	return outcomes
}

func wholeRequestProvisionError(providerID ProviderID, confirmedCount int, result ProvisionResult, err error) error {
	if confirmedCount > 0 || err == nil {
		return nil
	}
	if batchFailureNeedsReconnect(result, err) {
		return &ErrReconnectRequired{Provider: providerID, Err: err}
	}
	var tokenErr *OAuthTokenError
	if !errors.As(err, &tokenErr) {
		return nil
	}
	if OAuthFailureReason(err) == ReasonReconnectRequired {
		return &ErrReconnectRequired{Provider: providerID, Err: err}
	}
	return &ErrProviderUnavailable{Provider: providerID, Err: err}
}

func (e *Engine) markConnectionReauthRequired(ctx context.Context, scope tenant.Scope, providerID ProviderID) error {
	if e.tokenBroker == nil || e.tokenBroker.Directory == nil {
		return nil
	}
	conn, err := e.tokenBroker.Directory.Read(ctx, scope, string(providerID))
	if err != nil {
		return err
	}
	return e.tokenBroker.MarkReauthRequired(ctx, scope, string(providerID), conn)
}

// getComputedEntries reads the staged cart with ledger-net quantities.
// It does not recompute the supply plan, so a quantity the owner changed
// is the quantity sent. An adjustment, when one is stored for this provider,
// replaces that quantity for this operation only.
func (e *Engine) getComputedEntries(ctx context.Context, scope tenant.Scope, providerID ProviderID) ([]ResolvedItem, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	pantryItems, err := e.pantry.ListItems(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("failed to list pantry items: %w", err)
	}
	ledger, err := e.ledger.ListForProvider(ctx, scope, providerID)
	if err != nil {
		return nil, fmt.Errorf("failed to list ledger: %w", err)
	}
	active, err := e.shoppingList.ListUnpurchased(ctx, scope)
	if err != nil {
		return nil, err
	}

	itemNames := make(map[string]string, len(pantryItems))
	itemProductIDs := make(map[string]string, len(pantryItems))
	for _, item := range pantryItems {
		itemProductIDs[item.ID] = item.ProductID
		name := "Unknown"
		if item.Product != nil && item.Product.Name != "" {
			name = item.Product.Name
		}
		itemNames[item.ID] = name
	}

	var result []ResolvedItem
	for _, entry := range active {
		qty := entry.Quantity - ledger[entry.ItemID].Requested
		if qty < 0 {
			qty = 0
		}
		adjustment, err := e.shoppingList.GetAdjustment(ctx, scope, entry.ID, string(providerID))
		if err != nil {
			return nil, fmt.Errorf("failed to read adjustment: %w", err)
		}
		if adjustment != nil {
			qty = adjustment.Quantity
		}
		if qty < 1 {
			continue
		}
		itemID := entry.ItemID
		name := itemNames[itemID]
		if name == "" {
			name = "Unknown"
		}
		result = append(result, ResolvedItem{
			EntryID:   entry.ID,
			ItemID:    itemID,
			ProductID: itemProductIDs[itemID],
			Name:      name,
			Quantity:  qty,
		})
	}
	return result, nil
}

// exchangeRefresh asks the provider's OAuth flow for a new access token.
func (e *Engine) exchangeRefresh(ctx context.Context, provider Provider) func(refreshToken string) (string, string, time.Duration, error) {
	return func(refreshToken string) (string, string, time.Duration, error) {
		flow, ok := provider.(OAuthFlow)
		if !ok {
			return "", "", 0, fmt.Errorf("this provider cannot refresh an access token")
		}
		tokenSet, err := flow.RefreshAccessToken(ctx, refreshToken)
		if err != nil {
			return "", "", 0, err
		}
		return tokenSet.AccessToken, tokenSet.RefreshToken, tokenSet.ExpiresIn, nil
	}
}

// resolveIdentities resolves identities for each entry.
func (e *Engine) resolveIdentities(ctx context.Context, scope tenant.Scope, provider Provider, caps Capabilities, providerID ProviderID, entries []ResolvedItem) ([]ResolvedItem, []ResolvedItem, error) {
	var resolved, unresolved []ResolvedItem

	// Check if provider implements DerivedIdentity or LookedUpIdentity
	var derivedIdentity DerivedIdentity
	var lookedUpIdentity LookedUpIdentity
	var hasDerived bool
	var hasLookedUp bool

	if caps := provider.Capabilities(); caps.Identity == IdentityDerived {
		if di, ok := provider.(DerivedIdentity); ok {
			derivedIdentity = di
			hasDerived = true
		}
	} else if caps.Identity == IdentityLookedUp {
		if lu, ok := provider.(LookedUpIdentity); ok {
			lookedUpIdentity = lu
			hasLookedUp = true
		}
	}

	for _, entry := range entries {
		// Barcodes key on the product (products.id), not the pantry item.
		barcodes, err := e.catalog.ListBarcodesForProduct(ctx, scope, entry.ProductID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to list barcodes for %s: %w", entry.ProductID, err)
		}

		// Try each barcode until we find a valid identity
		var identity ProductIdentity
		found := false

		if hasDerived {
			for _, barcode := range barcodes {
				id, ok := derivedIdentity.DeriveIdentity(barcode)
				if ok {
					identity = id
					found = true
					break
				}
			}
		} else if hasLookedUp {
			// For looked_up, we need to call the provider's LookUpIdentity
			// Get credential based on auth type
			var cred Credential
			if caps.Auth == AuthNone {
				cred = NoCredential{}
			} else if caps.Auth == AuthOAuth2 {
				token, err := e.tokenBroker.AccessToken(ctx, scope, string(providerID), e.exchangeRefresh(ctx, provider))
				if err != nil {
					unresolved = append(unresolved, entry)
					continue
				}
				cred = NewBearerCredential(token)
			}

			for _, barcode := range barcodes {
				id, ok, err := lookedUpIdentity.LookUpIdentity(ctx, cred, barcode)
				if err != nil {
					continue
				}
				if ok {
					identity = id
					found = true
					break
				}
			}
		}

		if found {
			entry.Identity = identity
			resolved = append(resolved, entry)
		} else {
			if len(barcodes) == 0 {
				entry.UnresolvedReason = ReasonNoUPC
			} else {
				entry.UnresolvedReason = ReasonNoMatch
			}
			unresolved = append(unresolved, entry)
		}
	}

	return resolved, unresolved, nil
}

// batchRequests groups resolved items into batches based on identity.
func (e *Engine) batchRequests(providerID ProviderID, items []ResolvedItem, caps Capabilities) []ProvisionRequest {
	// Group by identity
	groups := make(map[ProductIdentity][]ResolvedItem)
	for _, item := range items {
		groups[item.Identity] = append(groups[item.Identity], item)
	}

	// Build lines
	var lines []ProvisionLine
	for identity, group := range groups {
		var totalQty int
		for _, item := range group {
			totalQty += item.Quantity
			if totalQty > 999 {
				totalQty = 999
			}
		}

		lines = append(lines, ProvisionLine{
			Identity: identity,
			Quantity: totalQty,
			Accounts: group,
		})
	}

	// Slice into batches of at most the provider's registered batch size.
	var batches []ProvisionRequest
	batchLimit := 50
	if e.registry != nil {
		if n := e.registry.BatchSize(providerID); n > 0 {
			batchLimit = n
		}
	}

	for len(lines) > 0 {
		batchSize := batchLimit
		if len(lines) < batchSize {
			batchSize = len(lines)
		}

		batchLines := make([]ProvisionLine, batchSize)
		copy(batchLines, lines[:batchSize])
		lines = lines[batchSize:]

		batches = append(batches, ProvisionRequest{
			Provider: providerID,
			Lines:    batchLines,
		})
	}

	return batches
}

// updateLedgerAndAdjustments advances the ledger and clears adjustments for
// every line of an accepted request, atomically in one transaction.
func (e *Engine) updateLedgerAndAdjustments(ctx context.Context, scope tenant.Scope, providerID ProviderID, req ProvisionRequest) error {
	if err := scope.Require(); err != nil {
		return err
	}
	// Create a transaction
	tx, err := e.ledger.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()

	for _, line := range req.Lines {
		// Advance each accounted item by its own quantity. The boundary the
		// operation computed against stays put; a missing row starts at now.
		for _, item := range line.Accounts {
			boundary, ok, err := e.ledger.boundaryTx(ctx, scope, tx, providerID, item.ItemID)
			if err != nil {
				return fmt.Errorf("failed to read ledger boundary: %w", err)
			}
			if !ok {
				boundary = now
			}
			if _, err := e.ledger.AdvanceTx(ctx, scope, tx, providerID, item.ItemID, item.Quantity, boundary); err != nil {
				return fmt.Errorf("failed to advance ledger: %w", err)
			}
			if err := e.shoppingList.ClearAdjustmentTx(ctx, scope, tx, item.EntryID, string(providerID)); err != nil {
				return fmt.Errorf("failed to clear adjustment for %s: %w", item.EntryID, err)
			}
		}
	}

	return tx.Commit()
}
