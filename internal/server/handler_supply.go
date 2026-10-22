package server

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/restock"
	"github.com/Rhionin/stockbot/internal/supply"
)

type supplySettingsResponse struct {
	Months     int    `json:"months"`
	Opening    bool   `json:"opening"`
	WipePhrase string `json:"wipePhrase"`
}

type supplyMonthsRequest struct {
	Months int `json:"months"`
}

type supplyOverrideRequest struct {
	WindowMonths *int  `json:"windowMonths"`
	Quantity     *int  `json:"quantity"`
	Clear        *bool `json:"clear"`
}

type supplyOverrideResponse struct {
	WindowMonths *int `json:"windowMonths,omitempty"`
	Quantity     *int `json:"quantity,omitempty"`
}

type onboardingCompleteResponse struct {
	StartedAt string `json:"startedAt"`
}

type productIDParams struct {
	ID string `json:"id"`
}

// SupplySettingsGetHandler handles GET /api/settings/supply.
type SupplySettingsGetHandler struct {
	Supply *supply.Service
}

func (h *SupplySettingsGetHandler) Handle(req Request[struct{}, struct{}]) (*supplySettingsResponse, error) {
	settings, err := h.Supply.Settings(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}
	return settingsResponse(settings), nil
}

// SupplySettingsPutHandler handles PUT /api/settings/supply.
type SupplySettingsPutHandler struct {
	Supply *supply.Service
}

func (h *SupplySettingsPutHandler) Handle(req Request[supplyMonthsRequest, struct{}]) (*supplySettingsResponse, error) {
	months, err := supply.ParseMonths(req.Body.Months)
	if err != nil {
		return nil, BadRequest(err.Error())
	}
	if err := h.Supply.SetMonths(req.Context, months); err != nil {
		return nil, InternalError(err)
	}
	settings, err := h.Supply.Settings(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}
	return settingsResponse(settings), nil
}

// SupplyOverrideGetHandler handles GET /api/products/{id}/supply-override.
type SupplyOverrideGetHandler struct {
	Targets *restock.Targets
	Groups  *group.Groups
}

func (h *SupplyOverrideGetHandler) Handle(req Request[struct{}, productIDParams]) (*supplyOverrideResponse, error) {
	if req.PathParams.ID == "" {
		return nil, BadRequest("missing product id")
	}
	if err := refuseGroupedOverride(req.Context, h.Groups, req.PathParams.ID); err != nil {
		return nil, err
	}
	target, err := h.productTarget(req.Context, req.PathParams.ID)
	if err != nil {
		return nil, err
	}
	return targetAsOverride(target), nil
}

// SupplyOverridePutHandler handles PUT /api/products/{id}/supply-override.
type SupplyOverridePutHandler struct {
	Targets *restock.Targets
	Groups  *group.Groups
}

func (h *SupplyOverridePutHandler) Handle(req Request[supplyOverrideRequest, productIDParams]) (*supplyOverrideResponse, error) {
	if req.PathParams.ID == "" {
		return nil, BadRequest("missing product id")
	}
	if err := refuseGroupedOverride(req.Context, h.Groups, req.PathParams.ID); err != nil {
		return nil, err
	}
	override, err := parseSupplyOverride(req.Body)
	if err != nil {
		return nil, err
	}
	itemID, err := h.Targets.ItemIDByProduct(req.Context, req.PathParams.ID)
	if err != nil {
		return nil, InternalError(err)
	}
	if itemID == "" {
		return nil, NotFound("That product isn't in the household.")
	}
	switch {
	case override == supply.Override{}:
		err = h.Targets.ClearItem(req.Context, itemID)
	default:
		if months, ok := override.Window(); ok {
			err = h.Targets.SetItem(req.Context, itemID, restock.Target{Kind: restock.KindMonths, Value: float64(months)})
		} else if qty, ok := override.Quantity(); ok {
			err = h.Targets.SetItem(req.Context, itemID, restock.Target{Kind: restock.KindAmount, Value: float64(qty)})
		}
	}
	if err != nil {
		return nil, restockError(err)
	}
	target, err := h.Targets.ItemTarget(req.Context, itemID)
	if err != nil {
		return nil, restockError(err)
	}
	return targetAsOverride(target), nil
}

func (h *SupplyOverrideGetHandler) productTarget(ctx context.Context, productID string) (*restock.Target, error) {
	itemID, err := h.Targets.ItemIDByProduct(ctx, productID)
	if err != nil {
		return nil, InternalError(err)
	}
	if itemID == "" {
		return nil, nil
	}
	target, err := h.Targets.ItemTarget(ctx, itemID)
	if err != nil {
		return nil, restockError(err)
	}
	return target, nil
}

// OnboardingCompleteHandler handles POST /api/onboarding/complete.
type OnboardingCompleteHandler struct {
	Supply *supply.Service
}

func (h *OnboardingCompleteHandler) Handle(req Request[struct{}, struct{}]) (*onboardingCompleteResponse, error) {
	phase, err := h.Supply.Complete(req.Context, time.Now())
	if err != nil {
		return nil, InternalError(err)
	}
	started, ok := phase.StartedAt()
	if !ok {
		return nil, InternalError(errors.New("could not save the supply start date"))
	}
	return &onboardingCompleteResponse{StartedAt: started.UTC().Format(time.RFC3339)}, nil
}

func refuseGroupedOverride(ctx context.Context, groups *group.Groups, productID string) error {
	if groups == nil {
		return nil
	}
	groupID, err := groups.MemberGroupID(ctx, productID)
	if err != nil {
		return InternalError(err)
	}
	if groupID == "" {
		return nil
	}
	return ConflictDetails(
		"This product is in a group. Change what the group keeps on hand.",
		"in_group",
		map[string]string{"groupId": groupID},
	)
}

func settingsResponse(settings supply.Settings) *supplySettingsResponse {
	return &supplySettingsResponse{
		Months:     int(settings.Months),
		Opening:    settings.Opening,
		WipePhrase: settings.WipePhrase,
	}
}

func targetAsOverride(target *restock.Target) *supplyOverrideResponse {
	resp := &supplyOverrideResponse{}
	if target == nil || target.Value <= 0 {
		return resp
	}
	n := int(math.Round(target.Value))
	switch target.Kind {
	case restock.KindMonths:
		resp.WindowMonths = &n
	case restock.KindAmount:
		resp.Quantity = &n
	}
	return resp
}

func parseSupplyOverride(body supplyOverrideRequest) (supply.Override, error) {
	n := 0
	if body.WindowMonths != nil {
		n++
	}
	if body.Quantity != nil {
		n++
	}
	if body.Clear != nil {
		n++
	}
	if n != 1 {
		return supply.Override{}, BadRequest("Send a supply window, a quantity, or clear.")
	}
	switch {
	case body.Clear != nil:
		if !*body.Clear {
			return supply.Override{}, BadRequest("Send a supply window, a quantity, or clear.")
		}
		return supply.Override{}, nil
	case body.WindowMonths != nil:
		months, err := supply.ParseMonths(*body.WindowMonths)
		if err != nil {
			return supply.Override{}, BadRequest(err.Error())
		}
		override, err := supply.Window(months)
		if err != nil {
			return supply.Override{}, BadRequest(err.Error())
		}
		return override, nil
	default:
		units, err := supply.ParseUnits(*body.Quantity)
		if err != nil {
			return supply.Override{}, BadRequest(err.Error())
		}
		override, err := supply.Quantity(units)
		if err != nil {
			return supply.Override{}, BadRequest(err.Error())
		}
		return override, nil
	}
}
