package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type ScanBatchCommitHandler struct {
	Queue interface {
		GetScanEntry(ctx context.Context, scope tenant.Scope, id string) (*scan.ScanEntry, error)
		BatchUpdateScanEntries(ctx context.Context, scope tenant.Scope, ids []string, direction *scan.ScanDirection, unitCount *int, expiresAt *time.Time) error
		CommitStockIn(ctx context.Context, scope tenant.Scope, scanEntry *scan.ScanEntry) error
		CommitStockOut(ctx context.Context, scope tenant.Scope, scanEntry *scan.ScanEntry, instanceID *string) (int, error)
	}
}

type batchCommitRequest struct {
	ScanEntryIDs []string            `json:"scanEntryIds"`
	Direction    *scan.ScanDirection `json:"direction,omitempty"`
	UnitCount    *int                `json:"unitCount,omitempty"`
	ExpiresAt    *time.Time          `json:"expiresAt,omitempty"`
	Commit       bool                `json:"commit"`
	// Backfill is accepted and ignored. One batch flag cannot choose a
	// backfill for some cards and a normal commit for the others.
	Backfill bool `json:"backfill,omitempty"`
}

// stockOutCount is one committed stock-out. removedCount is how many units
// left the shelf. requestedCount is that card's unit count.
type stockOutCount struct {
	RemovedCount   int `json:"removedCount"`
	RequestedCount int `json:"requestedCount"`
}

type batchCommitResponse struct {
	UpdatedCount int `json:"updatedCount"`
	// CommittedIDs lists scans this request committed, or confirmed as already
	// committed. An id this request did not read is left out.
	CommittedIDs  []string `json:"committedIds,omitempty"`
	FailedIDs     []string `json:"failedIds,omitempty"`
	FailedReasons []string `json:"failedReasons,omitempty"`
	// FailedCodes is parallel to FailedIDs and FailedReasons. Older clients
	// that only read the sentences keep working.
	FailedCodes []string `json:"failedCodes,omitempty"`
	// StockOutCounts is keyed by scan id, one entry per committed stock-out
	// (a full removal included). A stock-out that was already committed is
	// included again with the counts stored at that first commit. Stock-ins
	// and failed cards are left out. Failures stay parallel arrays; this is
	// a map because it is only the stock-out subset of committedIds.
	StockOutCounts map[string]stockOutCount `json:"stockOutCounts,omitempty"`
}

// batchStopsOnStorageFailure is the product decision for a busy or full
// database in one batch commit. The item that failed and every item not yet
// tried share that code and sentence and are not written. Those later ids
// are not read, even when one was already committed by something else.
// Items this request already committed stay committed. Other per-item
// failures do not stop the batch.
const batchStopsOnStorageFailure = true

func batchStopsEarly(code string) bool {
	return batchStopsOnStorageFailure && (code == busyErrorCode || code == noSpaceErrorCode)
}

func (h *ScanBatchCommitHandler) Handle(req Request[batchCommitRequest, struct{}]) (batchCommitResponse, error) {
	if len(req.Body.ScanEntryIDs) == 0 {
		return batchCommitResponse{}, BadRequest("scanEntryIds is required")
	}
	req.Body.ScanEntryIDs = uniqueInOrder(req.Body.ScanEntryIDs)

	// Validate direction if provided
	if req.Body.Direction != nil {
		dir := *req.Body.Direction
		if dir != scan.StockIn && dir != scan.StockOut {
			return batchCommitResponse{}, BadRequest("invalid direction value")
		}
	}

	scope, err := requestScope(req.Context)
	if err != nil {
		return batchCommitResponse{}, err
	}

	// An update-only request applies one transaction to every id. A missing id
	// is left out of that write and then reported as scan_not_found. A lock
	// or a full disk on that transaction stops the batch: every id, including
	// a missing one, gets busy or no_space, and nothing is read afterward.
	// A confirm applies those fields to each pending card as that card is
	// reached, so an unknown id stays a per-item result. A flagged card is
	// not edited. A lock rolls that card's update back, so the card stays as
	// it was. On a full disk the field edit can persist and the stock commit
	// then fails. Stock is still applied exactly once.
	applyFields := req.Body.Direction != nil || req.Body.UnitCount != nil || req.Body.ExpiresAt != nil
	response := batchCommitResponse{
		UpdatedCount: len(req.Body.ScanEntryIDs),
	}
	if applyFields && !req.Body.Commit {
		err := h.Queue.BatchUpdateScanEntries(
			req.Context,
			scope,
			req.Body.ScanEntryIDs,
			req.Body.Direction,
			req.Body.UnitCount,
			req.Body.ExpiresAt,
		)
		if err != nil {
			if failure, ok := batchStorageFailure(req, err); ok {
				return failure, nil
			}
			return batchCommitResponse{}, commitClientError(err)
		}
		failures := &batchItemFailures{req: req}
		for _, id := range req.Body.ScanEntryIDs {
			if failures.skip(id) {
				continue
			}
			entry, err := h.Queue.GetScanEntry(req.Context, scope, id)
			if err != nil {
				failures.fail(id, err)
				continue
			}
			if entry == nil {
				failures.fail(id, scan.ErrScanNotFound)
			}
		}
		response.FailedIDs = failures.ids
		response.FailedReasons = failures.reasons
		response.FailedCodes = failures.codes
		return response, nil
	}

	// If commit flag is set, commit each entry
	if req.Body.Commit {
		committedIDs := []string{}
		var stockOutCounts map[string]stockOutCount
		failures := &batchItemFailures{req: req}

		for _, id := range req.Body.ScanEntryIDs {
			if failures.skip(id) {
				continue
			}
			entry, err := h.Queue.GetScanEntry(req.Context, scope, id)
			if err != nil {
				failures.fail(id, err)
				continue
			}
			if entry == nil {
				failures.fail(id, scan.ErrScanNotFound)
				continue
			}
			if entry.Status == scan.Committed {
				committedIDs = append(committedIDs, id)
				if entry.Direction != nil && *entry.Direction == scan.StockOut &&
					entry.RemovedCount != nil && entry.RequestedCount != nil {
					if stockOutCounts == nil {
						stockOutCounts = map[string]stockOutCount{}
					}
					stockOutCounts[id] = stockOutCount{
						RemovedCount:   *entry.RemovedCount,
						RequestedCount: *entry.RequestedCount,
					}
				}
				continue
			}
			if entry.Status != scan.Pending {
				failures.fail(id, scan.ErrNotPending)
				continue
			}
			if applyFields {
				err := h.Queue.BatchUpdateScanEntries(
					req.Context,
					scope,
					[]string{id},
					req.Body.Direction,
					req.Body.UnitCount,
					req.Body.ExpiresAt,
				)
				if err != nil {
					failures.fail(id, err)
					continue
				}
				entry, err = h.Queue.GetScanEntry(req.Context, scope, id)
				if err != nil {
					failures.fail(id, err)
					continue
				}
				if entry == nil {
					failures.fail(id, scan.ErrScanNotFound)
					continue
				}
			}
			if entry.Direction == nil {
				failures.fail(id, scan.ErrDirectionUnset)
				continue
			}
			if entry.ProductID == nil || *entry.ProductID == "" {
				failures.fail(id, scan.ErrProductUnresolved)
				continue
			}
			passCommitCheck()

			var removed int
			var commitErr error
			if *entry.Direction == scan.StockIn {
				commitErr = h.Queue.CommitStockIn(req.Context, scope, entry)
			} else {
				removed, commitErr = h.Queue.CommitStockOut(req.Context, scope, entry, nil)
			}

			if commitErr != nil {
				failures.fail(id, commitErr)
				continue
			}
			committedIDs = append(committedIDs, id)
			if *entry.Direction != scan.StockOut {
				continue
			}
			if stockOutCounts == nil {
				stockOutCounts = map[string]stockOutCount{}
			}
			stockOutCounts[id] = stockOutCount{
				RemovedCount:   removed,
				RequestedCount: entry.UnitCount,
			}
		}

		response.CommittedIDs = committedIDs
		response.FailedIDs = failures.ids
		response.FailedReasons = failures.reasons
		response.FailedCodes = failures.codes
		response.StockOutCounts = stockOutCounts
	}

	return response, nil
}

// batchItemFailures records one result per id and stops after the first
// busy or no_space result. Later ids share that code and are not read.
type batchItemFailures struct {
	req           Request[batchCommitRequest, struct{}]
	ids           []string
	reasons       []string
	codes         []string
	haltMessage   string
	haltCode      string
	loggedNoSpace bool
	loggedBusy    bool
	loggedCancel  bool
}

func (f *batchItemFailures) skip(id string) bool {
	if f.haltCode == "" {
		return false
	}
	recordCommitResult(&f.ids, &f.reasons, &f.codes, id, f.haltMessage, f.haltCode)
	return true
}

func (f *batchItemFailures) fail(id string, err error) {
	if errors.Is(err, context.Canceled) && !f.loggedCancel {
		slog.Info("request canceled")
		f.loggedCancel = true
	}
	message, code := explainCommitFailure(err)
	if code == noSpaceErrorCode && !f.loggedNoSpace {
		logNoSpace(f.req.RawRequest, err)
		f.loggedNoSpace = true
	}
	if code == busyErrorCode && !f.loggedBusy {
		logBusy(f.req.RawRequest, err)
		f.loggedBusy = true
	}
	recordCommitResult(&f.ids, &f.reasons, &f.codes, id, message, code)
	if batchStopsEarly(code) {
		f.haltMessage, f.haltCode = message, code
	}
}

// batchStorageFailure is the HTTP 200 for a lock or a full disk that happens
// before any item is committed. Every id gets that code. Nothing is read
// afterward to see whether an id was already committed.
func batchStorageFailure(req Request[batchCommitRequest, struct{}], err error) (batchCommitResponse, bool) {
	message, code, ok := storageClientResult(err)
	if !ok {
		return batchCommitResponse{}, false
	}
	if code == busyErrorCode {
		logBusy(req.RawRequest, err)
	} else {
		logNoSpace(req.RawRequest, err)
	}
	response := batchCommitResponse{UpdatedCount: len(req.Body.ScanEntryIDs)}
	for _, id := range req.Body.ScanEntryIDs {
		recordCommitResult(&response.FailedIDs, &response.FailedReasons, &response.FailedCodes, id, message, code)
	}
	return response, true
}

func uniqueInOrder(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return unique
}

func recordCommitResult(ids, reasons, codes *[]string, id, message, code string) {
	*ids = append(*ids, id)
	*reasons = append(*reasons, message)
	*codes = append(*codes, code)
}
