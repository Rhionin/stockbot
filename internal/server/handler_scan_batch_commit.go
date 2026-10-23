package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

type ScanBatchCommitHandler struct {
	Queue interface {
		GetScanEntry(ctx context.Context, id string) (*scan.ScanEntry, error)
		BatchUpdateScanEntries(ctx context.Context, ids []string, direction *scan.ScanDirection, unitCount *int, expiresAt *time.Time) error
		CommitStockIn(ctx context.Context, scanEntry *scan.ScanEntry) error
		CommitStockOut(ctx context.Context, scanEntry *scan.ScanEntry, instanceID *string) (int, error)
	}
}

type batchCommitRequest struct {
	ScanEntryIDs []string            `json:"scanEntryIds"`
	Direction    *scan.ScanDirection `json:"direction,omitempty"`
	UnitCount    *int                `json:"unitCount,omitempty"`
	ExpiresAt    *time.Time          `json:"expiresAt,omitempty"`
	Commit       bool                `json:"commit"`
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

	// Apply batch updates if any fields are provided. A lock or a full disk
	// here is the same per-item result as a commit that cannot start.
	// A lock rolls this update back, so the cards stay as they were.
	// On a full disk the field edit can persist and the stock commit then
	// fails. Stock is still applied exactly once.
	if req.Body.Direction != nil || req.Body.UnitCount != nil || req.Body.ExpiresAt != nil {
		err := h.Queue.BatchUpdateScanEntries(
			req.Context,
			req.Body.ScanEntryIDs,
			req.Body.Direction,
			req.Body.UnitCount,
			req.Body.ExpiresAt,
		)
		if err != nil {
			if response, ok := batchStorageFailure(req, err); ok {
				return response, nil
			}
			return batchCommitResponse{}, commitClientError(err)
		}
	}

	response := batchCommitResponse{
		UpdatedCount: len(req.Body.ScanEntryIDs),
	}

	// If commit flag is set, commit each entry
	if req.Body.Commit {
		committedIDs := []string{}
		failedIDs := []string{}
		failedReasons := []string{}
		failedCodes := []string{}
		var stockOutCounts map[string]stockOutCount
		var haltMessage, haltCode string
		loggedNoSpace := false
		loggedBusy := false
		loggedCancel := false
		failItem := func(id string, err error) {
			if errors.Is(err, context.Canceled) && !loggedCancel {
				slog.Info("request canceled")
				loggedCancel = true
			}
			message, code := explainCommitFailure(err)
			if code == noSpaceErrorCode && !loggedNoSpace {
				logNoSpace(req.RawRequest, err)
				loggedNoSpace = true
			}
			if code == busyErrorCode && !loggedBusy {
				logBusy(req.RawRequest, err)
				loggedBusy = true
			}
			recordCommitResult(&failedIDs, &failedReasons, &failedCodes, id, message, code)
			if batchStopsEarly(code) {
				haltMessage, haltCode = message, code
			}
		}

		for _, id := range req.Body.ScanEntryIDs {
			if haltCode != "" {
				recordCommitResult(&failedIDs, &failedReasons, &failedCodes, id, haltMessage, haltCode)
				continue
			}
			entry, err := h.Queue.GetScanEntry(req.Context, id)
			if err != nil {
				failItem(id, err)
				continue
			}
			if entry == nil {
				failItem(id, scan.ErrScanNotFound)
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
				failItem(id, scan.ErrNotPending)
				continue
			}
			if entry.Direction == nil {
				failItem(id, scan.ErrDirectionUnset)
				continue
			}
			if entry.ProductID == nil || *entry.ProductID == "" {
				failItem(id, scan.ErrProductUnresolved)
				continue
			}

			var removed int
			var commitErr error
			if *entry.Direction == scan.StockIn {
				commitErr = h.Queue.CommitStockIn(req.Context, entry)
			} else {
				removed, commitErr = h.Queue.CommitStockOut(req.Context, entry, nil)
			}

			if commitErr != nil {
				failItem(id, commitErr)
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
		response.FailedIDs = failedIDs
		response.FailedReasons = failedReasons
		response.FailedCodes = failedCodes
		response.StockOutCounts = stockOutCounts
	}

	return response, nil
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
