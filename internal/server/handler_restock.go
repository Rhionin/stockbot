package server

import (
	"context"

	"github.com/Rhionin/stockbot/internal/restock"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// RestockHandler serves household restock targets and categories.
type RestockHandler struct {
	Targets *restock.Targets
	// Refresh recomputes the staged shopping list after a target change.
	// Nil skips that step, which unit tests use.
	Refresh func(ctx context.Context) error
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
	Kind  *string  `json:"kind"`
	Value *float64 `json:"value"`
	Clear *bool    `json:"clear"`
}

func explicitTarget(body restockTargetBody) (restock.Target, error) {
	if body.Kind == nil || (*body.Kind != string(restock.KindMonths) && *body.Kind != string(restock.KindAmount)) {
		return restock.Target{}, BadRequest("Choose months of supply or a fixed amount.")
	}
	if body.Value == nil {
		return restock.Target{}, BadRequest("Enter a target. 0 means don't restock.")
	}
	return restock.Target{Kind: restock.Kind(*body.Kind), Value: *body.Value}, nil
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
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	snap, err := h.Targets.Snapshot(req.Context, scope)
	if err != nil {
		return nil, InternalError(err)
	}
	return &snap, nil
}

func (h *RestockHandler) PutHousehold(req Request[restockTargetBody, struct{}]) (*restock.Snapshot, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	target, err := explicitTarget(req.Body)
	if err != nil {
		return nil, err
	}
	if err := h.Targets.SetHousehold(req.Context, scope, target); err != nil {
		return nil, restockError(err)
	}
	if err := h.syncList(req.Context); err != nil {
		return nil, err
	}
	return h.Snapshot(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) PutItem(req Request[restockTargetBody, restockItemParams]) (*restock.ItemView, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	if req.PathParams.ItemID == "" {
		return nil, BadRequest("missing item id")
	}
	target, err := explicitTarget(req.Body)
	if err != nil {
		return nil, err
	}
	if err := h.Targets.SetItem(req.Context, scope, req.PathParams.ItemID, target); err != nil {
		return nil, restockError(err)
	}
	if err := h.syncList(req.Context); err != nil {
		return nil, err
	}
	return h.oneItem(req.Context, scope, req.PathParams.ItemID)
}

func (h *RestockHandler) DeleteItem(req Request[struct{}, restockItemParams]) (*restock.ItemView, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	if req.PathParams.ItemID == "" {
		return nil, BadRequest("missing item id")
	}
	if err := h.Targets.ClearItem(req.Context, scope, req.PathParams.ItemID); err != nil {
		return nil, restockError(err)
	}
	if err := h.syncList(req.Context); err != nil {
		return nil, err
	}
	return h.oneItem(req.Context, scope, req.PathParams.ItemID)
}

func (h *RestockHandler) DontRestock(req Request[struct{}, restockItemParams]) (*restockDontResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	itemID := req.PathParams.ItemID
	if itemID == "" {
		return nil, BadRequest("missing item id")
	}
	beforeSnap, err := h.Targets.Snapshot(req.Context, scope)
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
	previous, err := h.Targets.ItemTarget(req.Context, scope, itemID)
	if err != nil {
		return nil, InternalError(err)
	}
	if err := h.Targets.SetItem(req.Context, scope, itemID, restock.Target{Kind: restock.KindMonths, Value: 0}); err != nil {
		return nil, restockError(err)
	}
	if err := h.syncList(req.Context); err != nil {
		return nil, err
	}
	return &restockDontResponse{
		Previous: previous,
		Title:    name + " won't be restocked",
		Detail:   restock.DontRestockDetail(before),
	}, nil
}

func (h *RestockHandler) Categories(req Request[struct{}, struct{}]) (*[]restock.CategorySummary, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	snap, err := h.Targets.Snapshot(req.Context, scope)
	if err != nil {
		return nil, InternalError(err)
	}
	return &snap.Categories, nil
}

func (h *RestockHandler) CreateCategory(req Request[restockNameBody, struct{}]) (*restock.CategorySummary, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	created, err := h.Targets.CreateCategory(req.Context, scope, req.Body.Name)
	if err != nil {
		return nil, restockError(err)
	}
	return &created, nil
}

func (h *RestockHandler) PutCategoryTarget(req Request[restockTargetBody, restockCategoryParams]) (*restock.Snapshot, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	if req.PathParams.ID == "" {
		return nil, BadRequest("missing category id")
	}
	var target *restock.Target
	if req.Body.Clear == nil || !*req.Body.Clear {
		value, err := explicitTarget(req.Body)
		if err != nil {
			return nil, err
		}
		target = &value
	}
	if err := h.Targets.SetCategoryTarget(req.Context, scope, req.PathParams.ID, target); err != nil {
		return nil, restockError(err)
	}
	if err := h.syncList(req.Context); err != nil {
		return nil, err
	}
	return h.Snapshot(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) PreviewCategory(req Request[restockTargetBody, restockCategoryParams]) (*restock.Preview, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	target, err := explicitTarget(req.Body)
	if err != nil {
		return nil, err
	}
	preview, err := h.Targets.PreviewCategory(req.Context, scope, req.PathParams.ID, target)
	if err != nil {
		return nil, restockError(err)
	}
	return &preview, nil
}

func (h *RestockHandler) RenameCategory(req Request[restockNameBody, restockCategoryParams]) (*restock.Snapshot, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	if err := h.Targets.RenameCategory(req.Context, scope, req.PathParams.ID, req.Body.Name); err != nil {
		return nil, restockError(err)
	}
	return h.Snapshot(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) MergeCategory(req Request[restockMergeBody, restockCategoryParams]) (*restock.Snapshot, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	if err := h.Targets.MergeCategory(req.Context, scope, req.PathParams.ID, req.Body.IntoID); err != nil {
		return nil, restockError(err)
	}
	return h.Snapshot(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) DeleteCategory(req Request[struct{}, restockCategoryParams]) (*restock.Snapshot, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	if err := h.Targets.DeleteCategory(req.Context, scope, req.PathParams.ID); err != nil {
		return nil, restockError(err)
	}
	return h.Snapshot(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) AddItemCategory(req Request[restockCategoryBody, restockItemParams]) (*restock.ItemView, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	if err := h.Targets.AddItemCategory(req.Context, scope, req.PathParams.ItemID, req.Body.CategoryID); err != nil {
		return nil, restockError(err)
	}
	return h.oneItem(req.Context, scope, req.PathParams.ItemID)
}

func (h *RestockHandler) RemoveItemCategory(req Request[struct{}, restockItemCategoryParams]) (*restock.ItemView, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	if err := h.Targets.RemoveItemCategory(req.Context, scope, req.PathParams.ItemID, req.PathParams.CategoryID); err != nil {
		return nil, restockError(err)
	}
	return h.oneItem(req.Context, scope, req.PathParams.ItemID)
}

func (h *RestockHandler) PreviewGroup(req Request[restockTargetBody, restockGroupParams]) (*restock.Preview, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	target, err := explicitTarget(req.Body)
	if err != nil {
		return nil, err
	}
	preview, err := h.Targets.PreviewGroup(req.Context, scope, req.PathParams.ID, target)
	if err != nil {
		return nil, restockError(err)
	}
	return &preview, nil
}

func (h *RestockHandler) Suggestions(req Request[struct{}, struct{}]) (*[]restock.SuggestionCard, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	cards, err := h.Targets.Suggestions(req.Context, scope)
	if err != nil {
		return nil, InternalError(err)
	}
	if cards == nil {
		cards = []restock.SuggestionCard{}
	}
	return &cards, nil
}

func (h *RestockHandler) ApplySuggestions(req Request[restockSuggestBody, struct{}]) (*restock.Snapshot, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	if err := h.Targets.ApplySuggestions(req.Context, scope, req.Body.Category, req.Body.ItemIDs); err != nil {
		return nil, restockError(err)
	}
	return h.Snapshot(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) DismissSuggestions(req Request[restockSuggestBody, struct{}]) (*[]restock.SuggestionCard, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	if err := h.Targets.DismissSuggestions(req.Context, scope, req.Body.Category, req.Body.ItemIDs); err != nil {
		return nil, restockError(err)
	}
	return h.Suggestions(Request[struct{}, struct{}]{Context: req.Context})
}

func (h *RestockHandler) oneItem(ctx context.Context, scope tenant.Scope, itemID string) (*restock.ItemView, error) {
	snap, err := h.Targets.Snapshot(ctx, scope)
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

func (h *RestockHandler) syncList(ctx context.Context) error {
	if h == nil || h.Refresh == nil {
		return nil
	}
	if err := h.Refresh(ctx); err != nil {
		return InternalError(err)
	}
	return nil
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
