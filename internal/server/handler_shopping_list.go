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
