package server

import (
	"context"
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
	UpdatedCount  int      `json:"updatedCount"`
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

	// Apply batch updates if any fields are provided
	if req.Body.Direction != nil || req.Body.UnitCount != nil || req.Body.ExpiresAt != nil {
		err := h.Queue.BatchUpdateScanEntries(
			req.Context,
			req.Body.ScanEntryIDs,
			req.Body.Direction,
			req.Body.UnitCount,
			req.Body.ExpiresAt,
		)
		if err != nil {
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

		for _, id := range req.Body.ScanEntryIDs {
			entry, err := h.Queue.GetScanEntry(req.Context, id)
			if err != nil {
				recordCommitFailure(&failedIDs, &failedReasons, &failedCodes, id, err)
				continue
			}
			if entry == nil {
				recordCommitFailure(&failedIDs, &failedReasons, &failedCodes, id, scan.ErrScanNotFound)
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
				recordCommitFailure(&failedIDs, &failedReasons, &failedCodes, id, scan.ErrNotPending)
				continue
			}
			if entry.Direction == nil {
				recordCommitFailure(&failedIDs, &failedReasons, &failedCodes, id, scan.ErrDirectionUnset)
				continue
			}
			if entry.ProductID == nil || *entry.ProductID == "" {
				recordCommitFailure(&failedIDs, &failedReasons, &failedCodes, id, scan.ErrProductUnresolved)
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
				recordCommitFailure(&failedIDs, &failedReasons, &failedCodes, id, commitErr)
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

func recordCommitFailure(ids, reasons, codes *[]string, id string, err error) {
	message, code := explainCommitFailure(err)
	*ids = append(*ids, id)
	*reasons = append(*reasons, message)
	*codes = append(*codes, code)
}
