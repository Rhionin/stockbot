package server

import (
	"context"
	"errors"
	"time"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// GroupHandler serves product groups, suggestions, and the account rule.
type GroupHandler struct {
	Groups  *group.Groups
	Supply  *supply.Service
	Refresh func(ctx context.Context) error
}

type groupIDParams struct {
	ID string `json:"id"`
}

type groupMemberParams struct {
	ID        string `json:"id"`
	ProductID string `json:"productId"`
}

type createGroupBody struct {
	Name       string      `json:"name"`
	ProductIDs []string    `json:"productIds"`
	Target     *targetBody `json:"target"`
}

type patchGroupBody struct {
	Name string `json:"name"`
}

type addMembersBody struct {
	ProductIDs  []string    `json:"productIds"`
	FromGroupID string      `json:"fromGroupId"`
	Target      *targetBody `json:"target"`
}

type ruleBody struct {
	Rule            string  `json:"rule"`
	PinnedProductID *string `json:"pinnedProductId"`
	Confirm         bool    `json:"confirm"`
}

type targetBody struct {
	WindowMonths *int     `json:"windowMonths"`
	Quantity     *float64 `json:"quantity"`
	Dimension    string   `json:"dimension"`
	Clear        *bool    `json:"clear"`
}

type defaultRuleBody struct {
	Rule string `json:"rule"`
}

type defaultRuleResponse struct {
	Rule string `json:"rule"`
}

type acceptSuggestionBody struct {
	ProductIDs *[]string   `json:"productIds"`
	Name       string      `json:"name"`
	Target     *targetBody `json:"target"`
}

type removeMemberResponse struct {
	Deleted bool         `json:"deleted"`
	Group   *group.Group `json:"group,omitempty"`
}

type previewBody struct {
	GroupID         string          `json:"groupId"`
	Rule            string          `json:"rule"`
	PinnedProductID string          `json:"pinnedProductId"`
	Members         []previewMember `json:"members"`
	Deals           []previewDeal   `json:"deals"`
	Target          *targetBody     `json:"target"`
	Rate            *rateBody       `json:"rate"`
}

type previewMember struct {
	ProductID      string    `json:"productId"`
	Name           string    `json:"name"`
	ItemID         string    `json:"itemId"`
	NetBase        *float64  `json:"netBaseValue"`
	Dimension      string    `json:"netDimension"`
	OnHand         int       `json:"onHand"`
	LastConsumedAt time.Time `json:"lastConsumedAt"`
	LastStockedAt  time.Time `json:"lastStockedAt"`
	NoRestock      bool      `json:"noRestock"`
}

type previewDeal struct {
	ItemID            string    `json:"itemId"`
	ProductID         string    `json:"productId"`
	PriceCents        *int      `json:"priceCents"`
	RegularPriceCents *int      `json:"regularPriceCents"`
	Label             string    `json:"label"`
	NotedAt           time.Time `json:"notedAt"`
}

type rateBody struct {
	PerDay    float64 `json:"perDay"`
	OK        bool    `json:"ok"`
	ItemCount bool    `json:"itemCount"`
}

type previewResponse struct {
	ProductID       string `json:"productId"`
	Because         string `json:"because"`
	ComparedPerItem bool   `json:"comparedPerItem"`
	Buy             int    `json:"buy"`
	Explain         string `json:"explain"`
}

func (h *GroupHandler) List(req Request[struct{}, struct{}]) ([]group.Group, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	rows, err := h.Groups.List(req.Context, scope)
	if err != nil {
		return nil, groupErr(err)
	}
	if rows == nil {
		rows = []group.Group{}
	}
	account, err := h.accountMonths(req.Context, scope)
	if err != nil {
		return nil, InternalError(err)
	}
	for i := range rows {
		rows[i].RunningLow = rows[i].IsLow(account)
	}
	return rows, nil
}

func (h *GroupHandler) Create(req Request[createGroupBody, struct{}]) (Created, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return Created{}, err
	}
	target, err := targetFromBody(req.Body.Target)
	if err != nil {
		return Created{}, groupErr(err)
	}
	view, err := h.Groups.Create(req.Context, scope, req.Body.Name, req.Body.ProductIDs, target)
	if err != nil {
		return Created{}, groupErr(err)
	}
	return Created{Value: view}, nil
}

// groupView is one group plus the computed amount rate. Usage is omitted
// when the household has not used enough of the group to measure it.
type groupView struct {
	group.Group
	Usage *supply.Usage `json:"usage,omitempty"`
}

func (h *GroupHandler) Get(req Request[struct{}, groupIDParams]) (*groupView, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	view, err := h.Groups.Get(req.Context, scope, req.PathParams.ID)
	if err != nil {
		return nil, groupErr(err)
	}
	return h.withUsage(req.Context, scope, view)
}

func (h *GroupHandler) Patch(req Request[patchGroupBody, groupIDParams]) (*groupView, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	view, err := h.Groups.Rename(req.Context, scope, req.PathParams.ID, req.Body.Name)
	if err != nil {
		return nil, groupErr(err)
	}
	return h.withUsage(req.Context, scope, view)
}

func (h *GroupHandler) Delete(req Request[struct{}, groupIDParams]) (NoContent, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return NoContent{}, err
	}
	if err := h.Groups.Delete(req.Context, scope, req.PathParams.ID); err != nil {
		return NoContent{}, groupErr(err)
	}
	return NoContent{}, nil
}

func (h *GroupHandler) AddMembers(req Request[addMembersBody, groupIDParams]) (*group.Group, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	target, err := targetFromBody(req.Body.Target)
	if err != nil {
		return nil, groupErr(err)
	}
	view, err := h.Groups.AddMembers(req.Context, scope, req.PathParams.ID, req.Body.ProductIDs, req.Body.FromGroupID, target)
	if err != nil {
		return nil, groupErr(err)
	}
	return &view, nil
}

func (h *GroupHandler) RemoveMember(req Request[struct{}, groupMemberParams]) (*removeMemberResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	view, deleted, err := h.Groups.RemoveMember(req.Context, scope, req.PathParams.ID, req.PathParams.ProductID)
	if err != nil {
		return nil, groupErr(err)
	}
	if deleted {
		return &removeMemberResponse{Deleted: true}, nil
	}
	return &removeMemberResponse{Group: &view}, nil
}

type restockBody struct {
	NoRestock bool `json:"noRestock"`
}

func (h *GroupHandler) PutMemberRestock(req Request[restockBody, groupMemberParams]) (*group.Group, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	view, err := h.Groups.SetMemberRestock(req.Context, scope, req.PathParams.ID, req.PathParams.ProductID, req.Body.NoRestock)
	if err != nil {
		return nil, groupErr(err)
	}
	if err := h.syncList(req.Context); err != nil {
		return nil, err
	}
	return &view, nil
}

func (h *GroupHandler) PutRule(req Request[ruleBody, groupIDParams]) (*group.Group, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	view, err := h.Groups.SetRule(req.Context, scope, req.PathParams.ID, req.Body.Rule, req.Body.PinnedProductID, req.Body.Confirm)
	if err != nil {
		return nil, groupErr(err)
	}
	return &view, nil
}

func (h *GroupHandler) PutTarget(req Request[targetBody, groupIDParams]) (*groupView, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	target, err := targetFromBody(&req.Body)
	if err != nil {
		return nil, groupErr(err)
	}
	view, err := h.Groups.SetTarget(req.Context, scope, req.PathParams.ID, target)
	if err != nil {
		return nil, groupErr(err)
	}
	if err := h.syncList(req.Context); err != nil {
		return nil, err
	}
	return h.withUsage(req.Context, scope, view)
}

func (h *GroupHandler) syncList(ctx context.Context) error {
	if h == nil || h.Refresh == nil {
		return nil
	}
	if err := h.Refresh(ctx); err != nil {
		return InternalError(err)
	}
	return nil
}

func (h *GroupHandler) withUsage(ctx context.Context, scope tenant.Scope, view group.Group) (*groupView, error) {
	usage, err := h.groupUsage(ctx, scope, view.ID)
	if err != nil {
		return nil, InternalError(err)
	}
	return &groupView{Group: view, Usage: usage}, nil
}

func (h *GroupHandler) groupUsage(ctx context.Context, scope tenant.Scope, id string) (*supply.Usage, error) {
	if h.Supply == nil {
		return nil, nil
	}
	return h.Supply.GroupUsage(ctx, scope, id, time.Now())
}

type fromScanBody struct {
	ProductID string `json:"productId"`
}

func (h *GroupHandler) NoteFromScan(req Request[fromScanBody, struct{}]) (NoContent, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return NoContent{}, err
	}
	if err := h.Groups.NoteFromScan(req.Context, scope, req.Body.ProductID); err != nil {
		return NoContent{}, groupErr(err)
	}
	return NoContent{}, nil
}

func (h *GroupHandler) Rescan(req Request[struct{}, struct{}]) ([]group.Suggestion, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	rows, err := h.Groups.Rescan(req.Context, scope)
	if err != nil {
		return nil, groupErr(err)
	}
	if rows == nil {
		rows = []group.Suggestion{}
	}
	return rows, nil
}

func (h *GroupHandler) ListSuggestions(req Request[struct{}, struct{}]) ([]group.Suggestion, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	rows, err := h.Groups.ListSuggestions(req.Context, scope)
	if err != nil {
		return nil, groupErr(err)
	}
	if rows == nil {
		rows = []group.Suggestion{}
	}
	return rows, nil
}

func (h *GroupHandler) AcceptSuggestion(req Request[acceptSuggestionBody, groupIDParams]) (*group.Group, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	target, err := targetFromBody(req.Body.Target)
	if err != nil {
		return nil, groupErr(err)
	}
	var productIDs []string
	if req.Body.ProductIDs != nil {
		productIDs = *req.Body.ProductIDs
	}
	view, err := h.Groups.Accept(req.Context, scope, req.PathParams.ID, productIDs, target, req.Body.Name)
	if err != nil {
		return nil, groupErr(err)
	}
	return &view, nil
}

func (h *GroupHandler) DismissSuggestion(req Request[struct{}, groupIDParams]) (NoContent, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return NoContent{}, err
	}
	if err := h.Groups.Dismiss(req.Context, scope, req.PathParams.ID); err != nil {
		return NoContent{}, groupErr(err)
	}
	return NoContent{}, nil
}

func (h *GroupHandler) SkipSuggestion(req Request[struct{}, groupIDParams]) (NoContent, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return NoContent{}, err
	}
	if err := h.Groups.Skip(req.Context, scope, req.PathParams.ID); err != nil {
		return NoContent{}, groupErr(err)
	}
	return NoContent{}, nil
}

func (h *GroupHandler) GetDefaultRule(req Request[struct{}, struct{}]) (*defaultRuleResponse, error) {
	rule, err := h.Groups.DefaultRule(req.Context)
	if err != nil {
		return nil, groupErr(err)
	}
	return &defaultRuleResponse{Rule: string(rule)}, nil
}

func (h *GroupHandler) PutDefaultRule(req Request[defaultRuleBody, struct{}]) (*defaultRuleResponse, error) {
	if err := h.Groups.SetDefaultRule(req.Context, req.Body.Rule); err != nil {
		return nil, groupErr(err)
	}
	return &defaultRuleResponse{Rule: req.Body.Rule}, nil
}

func (h *GroupHandler) Preview(req Request[previewBody, struct{}]) (*previewResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	body := req.Body
	var members []group.Member
	var deals []shopping.Deal
	rule := body.Rule
	pin := body.PinnedProductID
	var stored *group.Group

	if len(body.Members) > 0 {
		for _, m := range body.Members {
			members = append(members, group.Member{
				ProductID:      m.ProductID,
				Name:           m.Name,
				ItemID:         m.ItemID,
				NetBase:        m.NetBase,
				Dimension:      m.Dimension,
				OnHand:         m.OnHand,
				LastConsumedAt: m.LastConsumedAt,
				LastStockedAt:  m.LastStockedAt,
				NoRestock:      m.NoRestock,
			})
		}
	} else if body.GroupID != "" {
		view, err := h.Groups.Get(req.Context, scope, body.GroupID)
		if err != nil {
			return nil, groupErr(err)
		}
		stored = &view
		members = view.Members
		if rule == "" {
			rule = view.Rule
		}
		if pin == "" {
			pin = view.PinnedProductID
		}
	}
	if !group.Valid(rule) {
		return nil, BadRequest("Pick one of the rules.")
	}
	if len(body.Deals) > 0 {
		for _, d := range body.Deals {
			itemID := d.ItemID
			if itemID == "" {
				itemID = d.ProductID
			}
			deals = append(deals, shopping.Deal{
				ItemID:            itemID,
				PriceCents:        d.PriceCents,
				RegularPriceCents: d.RegularPriceCents,
				Label:             d.Label,
				NotedAt:           d.NotedAt,
			})
		}
	} else if stored != nil {
		loaded, err := h.Groups.DealsFor(req.Context, scope, memberProductIDs(members))
		if err != nil {
			return nil, groupErr(err)
		}
		deals = loaded
	}
	picked, err := group.Pick(group.Kind(rule), group.Input{
		Members:         members,
		PinnedProductID: pin,
		Deals:           deals,
	})
	if err != nil {
		return nil, InternalError(err)
	}
	target, rate, err := h.previewTarget(req, scope, body, stored)
	if err != nil {
		return nil, err
	}
	buy, explain := group.BuyCount(target, members, picked.ProductID, rate)
	return &previewResponse{
		ProductID:       picked.ProductID,
		Because:         picked.Because,
		ComparedPerItem: picked.ComparedPerItem,
		Buy:             buy,
		Explain:         explain,
	}, nil
}

func (h *GroupHandler) previewTarget(req Request[previewBody, struct{}], scope tenant.Scope, body previewBody, stored *group.Group) (group.BuyTarget, group.UsageRate, error) {
	var rate group.UsageRate
	if body.Rate != nil {
		rate = group.UsageRate{PerDay: body.Rate.PerDay, OK: body.Rate.OK, ItemCount: body.Rate.ItemCount}
	}
	account, monthsErr := h.accountMonths(req.Context, scope)
	if monthsErr != nil {
		return group.BuyTarget{}, rate, InternalError(monthsErr)
	}
	if body.Target != nil {
		in, err := targetFromBody(body.Target)
		if err != nil {
			return group.BuyTarget{}, rate, groupErr(err)
		}
		if in == nil || in.Clear {
			return group.BuyTarget{WindowMonths: account}, rate, nil
		}
		if in.Window != nil {
			return group.BuyTarget{WindowMonths: *in.Window}, rate, nil
		}
		dim := in.Dimension
		if dim == "" {
			dim = product.DimensionMass
		}
		base, got, err := product.BaseFromAmount(*in.Quantity, unitForDimension(dim))
		if err != nil {
			return group.BuyTarget{}, rate, BadRequest("The quantity has to be more than zero and at most 999 ounces.")
		}
		return group.BuyTarget{HasQuantity: true, Base: base, Dimension: got}, rate, nil
	}
	if stored != nil {
		return stored.BuyTarget(account), rate, nil
	}
	return group.BuyTarget{WindowMonths: account}, rate, nil
}

func unitForDimension(dimension string) string {
	if dimension == product.DimensionVolume {
		return "fl oz"
	}
	return "oz"
}

func (h *GroupHandler) accountMonths(ctx context.Context, scope tenant.Scope) (int, error) {
	if h.Supply == nil {
		return int(supply.DefaultMonths), nil
	}
	settings, err := h.Supply.Settings(ctx, scope)
	if err != nil {
		return 0, err
	}
	return int(settings.Months), nil
}

func memberProductIDs(members []group.Member) []string {
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.ProductID
	}
	return ids
}

func targetFromBody(body *targetBody) (*group.TargetInput, error) {
	if body == nil {
		return nil, nil
	}
	clear := body.Clear != nil && *body.Clear
	in := &group.TargetInput{
		Clear:     clear,
		Window:    body.WindowMonths,
		Quantity:  body.Quantity,
		Dimension: body.Dimension,
	}
	if err := group.ValidateTarget(in); err != nil {
		return nil, err
	}
	return in, nil
}

func groupErr(err error) error {
	if err == nil {
		return nil
	}
	var ge *group.Error
	if !errors.As(err, &ge) {
		return InternalError(err)
	}
	switch ge.Code {
	case "invalid":
		return BadRequest(ge.Error())
	case "missing":
		return NotFound(ge.Error())
	case "conflict":
		return Conflict(ge.Error())
	case "target":
		return ConflictDetails(ge.Error(), "target_decision_required", ge.Members)
	default:
		return InternalError(err)
	}
}
