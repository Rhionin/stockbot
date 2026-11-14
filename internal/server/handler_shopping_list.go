package server

import (
	"time"

	"github.com/Rhionin/stockbot/internal/shopping"
)

// ShoppingListGroupMemberResponse is one product the shopper can buy for a group line.
type ShoppingListGroupMemberResponse struct {
	ItemID          string   `json:"itemId"`
	ProductID       string   `json:"productId"`
	Name            string   `json:"name"`
	ImageURL        string   `json:"imageUrl,omitempty"`
	Brand           string   `json:"brand,omitempty"`
	NetAmount       *float64 `json:"netAmount,omitempty"`
	NetUnit         string   `json:"netUnit,omitempty"`
	PackCount       *int     `json:"packCount,omitempty"`
	OnHand          int      `json:"onHand"`
	LastStockedAt   *string  `json:"lastStockedAt,omitempty"`
	Barcodes        []string `json:"barcodes,omitempty"`
}

// ShoppingListGroupResponse is the product group a line was planned for.
type ShoppingListGroupResponse struct {
	ID            string                            `json:"id"`
	Name          string                            `json:"name"`
	Rule          string                            `json:"rule"`
	RuleConfirmed bool                              `json:"ruleConfirmed"`
	Members       []ShoppingListGroupMemberResponse `json:"members"`
}

// ShoppingListEntryResponse is the API representation of a shopping list entry.
type ShoppingListEntryResponse struct {
	ID                string                     `json:"id"`
	ItemID            string                     `json:"itemId"`
	Quantity          int                        `json:"quantity"`
	Source            string                     `json:"source"` // "auto" or "manual"
	Note              string                     `json:"note"`
	PurchasedAt       *string                    `json:"purchasedAt"` // ISO 8601 or null
	ReplenishmentMode string                     `json:"replenishmentMode,omitempty"`
	Provider          string                     `json:"provider,omitempty"`
	ComputedQuantity  int                        `json:"computedQuantity,omitempty"`
	Adjustment        *int                       `json:"adjustment,omitempty"`
	Basis             *ShoppingListBasis         `json:"basis,omitempty"`
	Group             *ShoppingListGroupResponse `json:"group,omitempty"`
	PlanBasis         *ShoppingListPlanBasis     `json:"planBasis,omitempty"`
	Sent              int                        `json:"sent"`
	StoreID           *string                    `json:"storeId"`
	CanSendToCart     bool                       `json:"canSendToCart"`
	Touched           bool                       `json:"touched,omitempty"`
	LastChange        *ListLineChangeResponse    `json:"lastChange,omitempty"`
	LineKey           string                     `json:"lineKey,omitempty"`
	NeedsAttention    *NeedsAttentionResponse    `json:"needsAttention,omitempty"`
}

// ShoppingListPlanBasis is read-time planner numbers for one line.
type ShoppingListPlanBasis struct {
	OnHand         int              `json:"onHand"`
	OnHandAmount   *float64         `json:"onHandAmount,omitempty"`
	Unit           string           `json:"unit,omitempty"`
	Target         ShoppingPlanTarget `json:"target"`
	RatePerMonth   *float64         `json:"ratePerMonth,omitempty"`
	Replacing      *int             `json:"replacing,omitempty"`
	Short          int              `json:"short"`
}

type ShoppingPlanTarget struct {
	Kind  string `json:"kind"`
	Value int    `json:"value,omitempty"`
	Par   int    `json:"par,omitempty"`
	Goal  int    `json:"goal,omitempty"`
	Unit  string `json:"unit,omitempty"`
}

// ListLineChangeResponse is the wire shape for lastChange.
type ListLineChangeResponse struct {
	Effect           shopping.Effect `json:"effect"`
	Reason           string          `json:"reason,omitempty"`
	Cause            shopping.Cause  `json:"cause"`
	FromQty          *int                    `json:"fromQty,omitempty"`
	ToQty            *int                    `json:"toQty,omitempty"`
	N                *int                    `json:"n,omitempty"`
	ActorUserID      *string                 `json:"actorUserId"`
	ActorDisplayName *string                 `json:"actorDisplayName"`
	At               string                  `json:"at"`
	TripSeq          int                     `json:"tripSeq"`
}

// NeedsAttentionResponse mirrors attention.NeedsAttention on the wire.
type NeedsAttentionResponse struct {
	Reason string                  `json:"reason"`
	Detail string                  `json:"detail,omitempty"`
	Others []string                `json:"others"`
	Fix    NeedsAttentionFixResponse `json:"fix"`
	Stuck  bool                    `json:"stuck"`
}

type NeedsAttentionFixResponse struct {
	Who []NeedsAttentionWho `json:"who"`
}

type NeedsAttentionWho struct {
	Role string `json:"role,omitempty"`
}

// ShoppingListStatusResponse is GET ?status=1.
type ShoppingListStatusResponse struct {
	Cursor            string                         `json:"cursor"`
	ListStale         bool                           `json:"listStale"`
	Items             []ShoppingListEntryResponse    `json:"items"`
	OffList           []OffListRowResponse           `json:"offList"`
	Stores            []ShoppingListStoreInfo        `json:"stores"`
	AttentionGroups   []AttentionGroupResponse       `json:"attentionGroups"`
	StoreDisabled     *StoreDisabledResponse         `json:"storeDisabled"`
	LastTripEndedAt   *string                        `json:"lastTripEndedAt"`
	LastTripEndedBy   *string                        `json:"lastTripEndedBy"`
}

type OffListRowResponse struct {
	ID             string                  `json:"id"`
	ItemID         string                  `json:"itemId,omitempty"`
	GroupID        string                  `json:"groupId,omitempty"`
	ProductID      string                  `json:"productId,omitempty"`
	Name           string                  `json:"name"`
	OffListReason  string                  `json:"offListReason"`
	Quantity       int                     `json:"quantity,omitempty"`
	Basis          *ShoppingListBasis      `json:"basis,omitempty"`
	PlanBasis      *ShoppingListPlanBasis  `json:"planBasis,omitempty"`
	Sent           int                     `json:"sent"`
	StoreID        *string                 `json:"storeId"`
	CanSendToCart  bool                    `json:"canSendToCart"`
	LastChange     *ListLineChangeResponse `json:"lastChange,omitempty"`
	NeedsAttention *NeedsAttentionResponse `json:"needsAttention"`
}

type ShoppingListStoreInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	CanSendToCart   bool   `json:"canSendToCart"`
}

type AttentionGroupResponse struct {
	Reason  string   `json:"reason"`
	Detail  string   `json:"detail,omitempty"`
	Count   int      `json:"count"`
	Stuck   int      `json:"stuck"`
	RowKeys []string `json:"rowKeys"`
}

type StoreDisabledResponse struct {
	ProviderID string `json:"providerId"`
}

// ShoppingListSummaryW3Response is GET /api/shopping-list/summary.
type ShoppingListSummaryW3Response struct {
	ShortNow          int                      `json:"shortNow"`
	NeedsAttention    int                      `json:"needsAttention"`
	NotOnList         int                      `json:"notOnList"`
	Cursor            string                   `json:"cursor"`
	ListStale         bool                     `json:"listStale"`
	AttentionGroups   []AttentionGroupResponse `json:"attentionGroups,omitempty"`
	LastTripEndedAt   *string                  `json:"lastTripEndedAt,omitempty"`
	LastTripEndedBy   *string                  `json:"lastTripEndedBy,omitempty"`
	StoreDisabled     *StoreDisabledResponse   `json:"storeDisabled,omitempty"`
}

// ShoppingListBasis is the arithmetic behind one entry's computed quantity.
type ShoppingListBasis struct {
	Mode           string `json:"mode"`
	TargetQuantity *int   `json:"targetQuantity,omitempty"`
	InstanceCount  int    `json:"instanceCount"`
	ConsumedUnits  int    `json:"consumedUnits"`
	Requested      int    `json:"requested"`
}

// shoppingListItemPathParams holds path parameters for item-specific endpoints.
type shoppingListItemPathParams struct {
	ID string `json:"id"`
}

// shoppingListItemToResponse converts a ShoppingListItem to an API response.
func shoppingListItemToResponse(item *shopping.ShoppingListItem) ShoppingListEntryResponse {
	r := ShoppingListEntryResponse{
		ID:       item.ID,
		ItemID:   item.ItemID,
		Quantity: item.Quantity,
		Source:   item.Source,
	}
	if item.PurchasedAt != nil {
		ts := item.PurchasedAt.Format(time.RFC3339)
		r.PurchasedAt = &ts
	}
	r.Group = shoppingGroupResponse(item)
	return r
}

func shoppingGroupResponse(item *shopping.ShoppingListItem) *ShoppingListGroupResponse {
	if item == nil || item.GroupID == "" {
		return nil
	}
	members := make([]ShoppingListGroupMemberResponse, len(item.GroupMembers))
	for i, member := range item.GroupMembers {
		resp := ShoppingListGroupMemberResponse{
			ItemID:    member.ItemID,
			ProductID: member.ProductID,
			Name:      member.Name,
			ImageURL:  member.ImageURL,
			Brand:     member.Brand,
			NetAmount: member.NetAmount,
			NetUnit:   member.NetUnit,
			PackCount: member.PackCount,
			OnHand:    member.OnHand,
			Barcodes:  member.Barcodes,
		}
		if member.LastStockedAt != nil {
			ts := member.LastStockedAt.Format(time.RFC3339)
			resp.LastStockedAt = &ts
		}
		members[i] = resp
	}
	return &ShoppingListGroupResponse{
		ID:            item.GroupID,
		Name:          item.GroupName,
		Rule:          item.GroupRule,
		RuleConfirmed: item.RuleConfirmed,
		Members:       members,
	}
}
