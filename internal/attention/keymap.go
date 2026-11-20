package attention

import (
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
)

// StagedRow is one unpurchased staged line row used for send key mapping step 1.
type StagedRow struct {
	ItemID  string
	GroupID string
	Qty     int
}

// LineKeyForItemSend maps one item's latest send result to the line key the person sees.
func LineKeyForItemSend(
	itemID string,
	stagedByItem map[string]StagedRow,
	planViews map[string]supply.LinePlanView,
	itemProductGroup map[string]string,
) string {
	if row, ok := stagedByItem[itemID]; ok && row.Qty >= 1 {
		if row.GroupID != "" {
			return shopping.GroupLineKey(row.GroupID)
		}
		return shopping.ItemLineKey(itemID)
	}
	itemKey := shopping.ItemLineKey(itemID)
	if _, ok := planViews[itemKey]; ok {
		return itemKey
	}
	if gid := itemProductGroup[itemID]; gid != "" {
		gKey := shopping.GroupLineKey(gid)
		if _, ok := planViews[gKey]; ok {
			return gKey
		}
	}
	return itemKey
}