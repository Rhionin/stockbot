package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/history"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// HistoryHandler serves pace and the recent trail, and the corrections on a move.
type HistoryHandler struct {
	Journal *history.Journal
	Pantry  *inventory.Pantry
	Events  *events.Broadcaster
}

type historyItemParams struct {
	ItemID string `json:"itemId"`
}

type historyGroupParams struct {
	GroupID string `json:"groupId"`
}

type historyMoveParams struct {
	ID string `json:"id"`
}

type historyQuantityBody struct {
	Quantity int `json:"quantity"`
}

// Product is the trail for one item id. A wiped item is hidden from
// inventory and restock lists, and this direct read still returns 200.
func (h *HistoryHandler) Product(req Request[struct{}, historyItemParams]) (history.View, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return history.View{}, err
	}
	view, err := h.Journal.Product(req.Context, scope, req.PathParams.ItemID, time.Now())
	if err != nil {
		return history.View{}, historyHTTP(err)
	}
	return view, nil
}

func (h *HistoryHandler) Group(req Request[struct{}, historyGroupParams]) (history.View, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return history.View{}, err
	}
	view, err := h.Journal.Group(req.Context, scope, req.PathParams.GroupID, time.Now())
	if err != nil {
		return history.View{}, historyHTTP(err)
	}
	return view, nil
}

func (h *HistoryHandler) Household(req Request[struct{}, struct{}]) (history.Ledger, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return history.Ledger{}, err
	}
	query := req.RawRequest.URL.Query()
	ledger, err := h.Journal.Household(req.Context, scope, history.LedgerFilter{
		Direction: query.Get("direction"),
		Query:     query.Get("q"),
	})
	if err != nil {
		return history.Ledger{}, historyHTTP(err)
	}
	return ledger, nil
}

func (h *HistoryHandler) SetQuantity(req Request[historyQuantityBody, historyMoveParams]) (history.View, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return history.View{}, err
	}
	view, err := h.Journal.SetQuantity(req.Context, scope, req.PathParams.ID, req.Body.Quantity, time.Now())
	if err != nil {
		return history.View{}, historyHTTP(err)
	}
	h.publish(req.Context, scope, view.ID)
	return view, nil
}

func (h *HistoryHandler) Undo(req Request[struct{}, historyMoveParams]) (history.View, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return history.View{}, err
	}
	view, err := h.Journal.Undo(req.Context, scope, req.PathParams.ID, time.Now())
	if err != nil {
		return history.View{}, historyHTTP(err)
	}
	h.publish(req.Context, scope, view.ID)
	return view, nil
}

type undoPreviewResponse struct {
	Effects []undoEffectJSON `json:"effects"`
}

type undoEffectJSON struct {
	ItemID          string `json:"itemId"`
	ProductID       string `json:"productId"`
	Name            string `json:"name"`
	StockInMoveID   string `json:"stockInMoveId,omitempty"`
	StockOutMoveID  string `json:"stockOutMoveId,omitempty"`
	OnHand          int    `json:"onHand"`
	ReshelvesOnUndo bool   `json:"reshelvesOnUndo"`
}

// PreviewUndo reports what DELETE /api/moves/{id} would change. It changes nothing.
func (h *HistoryHandler) PreviewUndo(req Request[struct{}, historyMoveParams]) (undoPreviewResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return undoPreviewResponse{}, err
	}
	preview, err := h.Journal.PreviewUndo(req.Context, scope, req.PathParams.ID)
	if err != nil {
		return undoPreviewResponse{}, historyHTTP(err)
	}
	effects := make([]undoEffectJSON, 0, len(preview.Effects))
	for _, effect := range preview.Effects {
		effects = append(effects, undoEffectJSON{
			ItemID: effect.ItemID, ProductID: effect.ProductID, Name: effect.Name,
			StockInMoveID: effect.StockInMoveID, StockOutMoveID: effect.StockOutMoveID,
			OnHand: effect.OnHand, ReshelvesOnUndo: effect.ReshelvesOnUndo,
		})
	}
	return undoPreviewResponse{Effects: effects}, nil
}

func (h *HistoryHandler) publish(ctx context.Context, scope tenant.Scope, itemID string) {
	if h.Events == nil || h.Pantry == nil || itemID == "" {
		return
	}
	item, err := h.Pantry.GetInventoryItem(ctx, scope, itemID, time.Now(), inventory.DefaultWarningDays)
	if err != nil || item == nil {
		return
	}
	h.Events.PublishInventoryEvent(scope.HouseholdID(), *item)
}

func historyHTTP(err error) error {
	if errors.Is(err, history.ErrBackfillLocked) {
		return &HTTPError{
			Code:      http.StatusBadRequest,
			Message:   inventory.BackfillLockedMessage,
			ErrorCode: inventory.CodeBackfillLocked,
			Err:       err,
		}
	}
	var missing *history.NotFoundError
	if errors.As(err, &missing) {
		return NotFound(missing.Error())
	}
	var input *history.InputError
	if errors.As(err, &input) {
		return BadRequest(input.Error())
	}
	var conflict *history.ConflictError
	if errors.As(err, &conflict) {
		return Conflict(conflict.Error())
	}
	var full *history.PreviewFullError
	if errors.As(err, &full) {
		return &HTTPError{
			Code:    http.StatusServiceUnavailable,
			Message: clientmsg.PreviewNoSpace,
			Err:     err,
		}
	}
	return InternalError(err)
}
