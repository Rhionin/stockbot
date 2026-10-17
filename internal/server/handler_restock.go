package server

import (
	"context"

	"github.com/Rhionin/stockbot/internal/restock"
)

// RestockHandler serves household restock targets and categories.
type RestockHandler struct {
	Targets *restock.Targets
}

type restockItemParams struct {
	ItemID string `json:"itemId"`
}

type restockCategoryParams struct {
	ID string `json:"id"`
}

type restockItemCategoryParams struct {
	ItemID     string `json:"itemId"`
	CategoryID string `json:"categoryId"`
}

type restockGroupParams struct {
	ID string `json:"id"`
}

type restockTargetBody struct {
	Kind  string  `json:"kind"`
	Value float64 `json:"value"`
	Clear *bool   `json:"clear"`
}

type restockNameBody struct {
	Name string `json:"name"`
}

type restockMergeBody struct {
	IntoID string `json:"intoId"`
}

type restockCategoryBody struct {
	CategoryID string `json:"categoryId"`
}

type restockSuggestBody struct {
	Category string   `json:"category"`
	ItemIDs  []string `json:"itemIds"`
}

type restockDontResponse struct {
	Previous *restock.Target `json:"previous"`
	Title    string          `json:"title"`
	Detail   string          `json:"detail"`
}

func (h *RestockHandler) Snapshot(req Request[struct{}, struct{}]) (*restock.Snapshot, error) {
	snap, err := h.Targets.Snapshot(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}
	return &snap, nil
}

func (h *RestockHandler) PutHousehold(req Request[restockTargetBody, struct{}]) (*restock.Snapshot, error) {
	if err := h.Targets.SetHousehold(req.Context, restock.Target{Kind: restock.Kind(req.Body.Kind), Value: req.Body.Value}); err != nil {
		return nil, restockError(err)
	}
	return h.Snapshot(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) PutItem(req Request[restockTargetBody, restockItemParams]) (*restock.ItemView, error) {
	if req.PathParams.ItemID == "" {
		return nil, BadRequest("missing item id")
	}
	if err := h.Targets.SetItem(req.Context, req.PathParams.ItemID, restock.Target{Kind: restock.Kind(req.Body.Kind), Value: req.Body.Value}); err != nil {
		return nil, restockError(err)
	}
	return h.oneItem(req.Context, req.PathParams.ItemID)
}

func (h *RestockHandler) DeleteItem(req Request[struct{}, restockItemParams]) (*restock.ItemView, error) {
	if req.PathParams.ItemID == "" {
		return nil, BadRequest("missing item id")
	}
	if err := h.Targets.ClearItem(req.Context, req.PathParams.ItemID); err != nil {
		return nil, restockError(err)
	}
	return h.oneItem(req.Context, req.PathParams.ItemID)
}

func (h *RestockHandler) DontRestock(req Request[struct{}, restockItemParams]) (*restockDontResponse, error) {
	itemID := req.PathParams.ItemID
	if itemID == "" {
		return nil, BadRequest("missing item id")
	}
	beforeSnap, err := h.Targets.Snapshot(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}
	var before restock.Resolution
	var name string
	for _, item := range beforeSnap.Items {
		if item.ItemID == itemID {
			name = item.Name
			before = restock.Resolution{Target: item.Target, Level: item.Level, Name: item.LevelName}
		}
	}
	if name == "" {
		return nil, NotFound("That item isn't in the household.")
	}
	previous, err := h.Targets.ItemTarget(req.Context, itemID)
	if err != nil {
		return nil, InternalError(err)
	}
	if err := h.Targets.SetItem(req.Context, itemID, restock.Target{Kind: restock.KindMonths, Value: 0}); err != nil {
		return nil, restockError(err)
	}
	return &restockDontResponse{
		Previous: previous,
		Title:    name + " won't be restocked",
		Detail:   restock.DontRestockDetail(before),
	}, nil
}

func (h *RestockHandler) Categories(req Request[struct{}, struct{}]) (*[]restock.CategorySummary, error) {
	snap, err := h.Targets.Snapshot(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}
	return &snap.Categories, nil
}

func (h *RestockHandler) CreateCategory(req Request[restockNameBody, struct{}]) (*restock.CategorySummary, error) {
	created, err := h.Targets.CreateCategory(req.Context, req.Body.Name)
	if err != nil {
		return nil, restockError(err)
	}
	return &created, nil
}

func (h *RestockHandler) PutCategoryTarget(req Request[restockTargetBody, restockCategoryParams]) (*restock.Snapshot, error) {
	if req.PathParams.ID == "" {
		return nil, BadRequest("missing category id")
	}
	var target *restock.Target
	if req.Body.Clear == nil || !*req.Body.Clear {
		value := restock.Target{Kind: restock.Kind(req.Body.Kind), Value: req.Body.Value}
		target = &value
	}
	if err := h.Targets.SetCategoryTarget(req.Context, req.PathParams.ID, target); err != nil {
		return nil, restockError(err)
	}
	return h.Snapshot(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) PreviewCategory(req Request[restockTargetBody, restockCategoryParams]) (*restock.Preview, error) {
	preview, err := h.Targets.PreviewCategory(req.Context, req.PathParams.ID, restock.Target{Kind: restock.Kind(req.Body.Kind), Value: req.Body.Value})
	if err != nil {
		return nil, restockError(err)
	}
	return &preview, nil
}

func (h *RestockHandler) RenameCategory(req Request[restockNameBody, restockCategoryParams]) (*restock.Snapshot, error) {
	if err := h.Targets.RenameCategory(req.Context, req.PathParams.ID, req.Body.Name); err != nil {
		return nil, restockError(err)
	}
	return h.Snapshot(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) MergeCategory(req Request[restockMergeBody, restockCategoryParams]) (*restock.Snapshot, error) {
	if err := h.Targets.MergeCategory(req.Context, req.PathParams.ID, req.Body.IntoID); err != nil {
		return nil, restockError(err)
	}
	return h.Snapshot(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) DeleteCategory(req Request[struct{}, restockCategoryParams]) (*restock.Snapshot, error) {
	if err := h.Targets.DeleteCategory(req.Context, req.PathParams.ID); err != nil {
		return nil, restockError(err)
	}
	return h.Snapshot(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) AddItemCategory(req Request[restockCategoryBody, restockItemParams]) (*restock.ItemView, error) {
	if err := h.Targets.AddItemCategory(req.Context, req.PathParams.ItemID, req.Body.CategoryID); err != nil {
		return nil, restockError(err)
	}
	return h.oneItem(req.Context, req.PathParams.ItemID)
}

func (h *RestockHandler) RemoveItemCategory(req Request[struct{}, restockItemCategoryParams]) (*restock.ItemView, error) {
	if err := h.Targets.RemoveItemCategory(req.Context, req.PathParams.ItemID, req.PathParams.CategoryID); err != nil {
		return nil, restockError(err)
	}
	return h.oneItem(req.Context, req.PathParams.ItemID)
}

func (h *RestockHandler) PreviewGroup(req Request[restockTargetBody, restockGroupParams]) (*restock.Preview, error) {
	preview, err := h.Targets.PreviewGroup(req.Context, req.PathParams.ID, restock.Target{Kind: restock.Kind(req.Body.Kind), Value: req.Body.Value})
	if err != nil {
		return nil, restockError(err)
	}
	return &preview, nil
}

func (h *RestockHandler) Suggestions(req Request[struct{}, struct{}]) (*[]restock.SuggestionCard, error) {
	cards, err := h.Targets.Suggestions(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}
	if cards == nil {
		cards = []restock.SuggestionCard{}
	}
	return &cards, nil
}

func (h *RestockHandler) ApplySuggestions(req Request[restockSuggestBody, struct{}]) (*restock.Snapshot, error) {
	if err := h.Targets.ApplySuggestions(req.Context, req.Body.Category, req.Body.ItemIDs); err != nil {
		return nil, restockError(err)
	}
	return h.Snapshot(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) DismissSuggestions(req Request[restockSuggestBody, struct{}]) (*[]restock.SuggestionCard, error) {
	if err := h.Targets.DismissSuggestions(req.Context, req.Body.Category, req.Body.ItemIDs); err != nil {
		return nil, restockError(err)
	}
	return h.Suggestions(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) oneItem(ctx context.Context, itemID string) (*restock.ItemView, error) {
	snap, err := h.Targets.Snapshot(ctx)
	if err != nil {
		return nil, InternalError(err)
	}
	for i := range snap.Items {
		if snap.Items[i].ItemID == itemID {
			return &snap.Items[i], nil
		}
	}
	return nil, NotFound("That item isn't in the household.")
}

func restockError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	switch {
	case message == "That item isn't in the household." || message == "That category isn't in the household." || message == "That group isn't in the household.":
		return NotFound(message)
	default:
		if len(message) > 0 && message[0] >= 'A' && message[0] <= 'Z' {
			return BadRequest(message)
		}
		return InternalError(err)
	}
}
