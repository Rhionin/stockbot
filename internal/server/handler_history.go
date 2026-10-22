package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/history"
	"github.com/Rhionin/stockbot/internal/inventory"
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
	view, err := h.Journal.Product(req.Context, history.DefaultHousehold(), req.PathParams.ItemID, time.Now())
	if err != nil {
		return history.View{}, historyHTTP(err)
	}
	return view, nil
}

func (h *HistoryHandler) Group(req Request[struct{}, historyGroupParams]) (history.View, error) {
	view, err := h.Journal.Group(req.Context, req.PathParams.GroupID, time.Now())
	if err != nil {
		return history.View{}, historyHTTP(err)
	}
	return view, nil
}

func (h *HistoryHandler) Household(req Request[struct{}, struct{}]) (history.Ledger, error) {
	query := req.RawRequest.URL.Query()
	ledger, err := h.Journal.Household(req.Context, history.LedgerFilter{
		Direction: query.Get("direction"),
		Query:     query.Get("q"),
	})
	if err != nil {
		return history.Ledger{}, historyHTTP(err)
	}
	return ledger, nil
}

func (h *HistoryHandler) SetQuantity(req Request[historyQuantityBody, historyMoveParams]) (history.View, error) {
	household, err := moveHousehold(req.RawRequest)
	if err != nil {
		return history.View{}, err
	}
	view, err := h.Journal.SetQuantity(req.Context, household, req.PathParams.ID, req.Body.Quantity, time.Now())
	if err != nil {
		return history.View{}, historyHTTP(err)
	}
	h.publish(req.Context, view.ID)
	return view, nil
}

func (h *HistoryHandler) Undo(req Request[struct{}, historyMoveParams]) (history.View, error) {
	household, err := moveHousehold(req.RawRequest)
	if err != nil {
		return history.View{}, err
	}
	view, err := h.Journal.Undo(req.Context, household, req.PathParams.ID, time.Now())
	if err != nil {
		return history.View{}, historyHTTP(err)
	}
	h.publish(req.Context, view.ID)
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
	household, err := moveHousehold(req.RawRequest)
	if err != nil {
		return undoPreviewResponse{}, err
	}
	preview, err := h.Journal.PreviewUndo(req.Context, household, req.PathParams.ID)
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

// moveHousehold is the household a move correction uses. Until signed-in
// households exist, the only household is the default. A client userId that
// names another household is the same 404 as a missing move, and the handler
// returns it before any read or write.
func moveHousehold(r *http.Request) (string, error) {
	household := history.DefaultHousehold()
	if r != nil {
		if id := strings.TrimSpace(r.URL.Query().Get("userId")); id != "" && id != household {
			return "", NotFound("That move is no longer in the history.")
		}
	}
	return household, nil
}

func (h *HistoryHandler) publish(ctx context.Context, itemID string) {
	if h.Events == nil || h.Pantry == nil || itemID == "" {
		return
	}
	item, err := h.Pantry.GetInventoryItem(ctx, itemID, time.Now(), inventory.DefaultWarningDays)
	if err != nil || item == nil {
		return
	}
	h.Events.PublishInventoryEvent(*item)
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
