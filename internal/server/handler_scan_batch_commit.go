package server

import (
	"context"
	"errors"
	"time"

	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/Rhionin/stockbot/internal/scan"
)

type ScanBatchCommitHandler struct {
	Queue interface {
		GetScanEntry(ctx context.Context, id string) (*scan.ScanEntry, error)
		BatchUpdateScanEntries(ctx context.Context, ids []string, direction *scan.ScanDirection, unitCount *int, expiresAt *time.Time) error
		CommitStockIn(ctx context.Context, scanEntry *scan.ScanEntry) error
		CommitStockOut(ctx context.Context, scanEntry *scan.ScanEntry, instanceID *string) error
	}
}

type batchCommitRequest struct {
	ScanEntryIDs []string            `json:"scanEntryIds"`
	Direction    *scan.ScanDirection `json:"direction,omitempty"`
	UnitCount    *int                `json:"unitCount,omitempty"`
	ExpiresAt    *time.Time          `json:"expiresAt,omitempty"`
	Commit       bool                `json:"commit"`
}

type batchCommitResponse struct {
	UpdatedCount  int      `json:"updatedCount"`
	CommittedIDs  []string `json:"committedIds,omitempty"`
	FailedIDs     []string `json:"failedIds,omitempty"`
	FailedReasons []string `json:"failedReasons,omitempty"`
}

func (h *ScanBatchCommitHandler) Handle(req Request[batchCommitRequest, struct{}]) (batchCommitResponse, error) {
	if len(req.Body.ScanEntryIDs) == 0 {
		return batchCommitResponse{}, BadRequest("scanEntryIds is required")
	}

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
			return batchCommitResponse{}, InternalError(err)
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

		for _, id := range req.Body.ScanEntryIDs {
			entry, err := h.Queue.GetScanEntry(req.Context, id)
			if err != nil || entry == nil {
				failedIDs = append(failedIDs, id)
				failedReasons = append(failedReasons, "scan entry not found")
				continue
			}

			if entry.Status != scan.Pending {
				failedIDs = append(failedIDs, id)
				failedReasons = append(failedReasons, "not pending")
				continue
			}

			if entry.Direction == nil {
				failedIDs = append(failedIDs, id)
				failedReasons = append(failedReasons, "direction not set")
				continue
			}

			if entry.ProductID == nil || *entry.ProductID == "" {
				failedIDs = append(failedIDs, id)
				failedReasons = append(failedReasons, "product not resolved")
				continue
			}

			var commitErr error
			if *entry.Direction == scan.StockIn {
				commitErr = h.Queue.CommitStockIn(req.Context, entry)
			} else {
				commitErr = h.Queue.CommitStockOut(req.Context, entry, nil)
			}

			if commitErr != nil {
				failedIDs = append(failedIDs, id)
				if errors.Is(commitErr, scan.ErrProductNotResolved) {
					text, _ := clientmsg.Reject(clientmsg.ProductUnresolvedText, clientmsg.ProductUnresolvedCode, commitErr)
					failedReasons = append(failedReasons, text)
				} else {
					failedReasons = append(failedReasons, commitErr.Error())
				}
			} else {
				committedIDs = append(committedIDs, id)
			}
		}

		response.CommittedIDs = committedIDs
		response.FailedIDs = failedIDs
		response.FailedReasons = failedReasons
	}

	return response, nil
}
