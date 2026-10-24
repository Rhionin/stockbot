package server

import (
	"context"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type InventoryInstancesListHandler struct {
	Pantry interface {
		ListItemInstances(ctx context.Context, scope tenant.Scope, itemID string) ([]inventory.ItemInstance, error)
	}
}

type inventoryInstancesListPathParams struct {
	ItemID string `json:"itemId"`
}

// ItemInstanceWithStatus extends ItemInstance with computed expiry status.
type ItemInstanceWithStatus struct {
	inventory.ItemInstance
	ExpiryStatus inventory.ExpiryStatus `json:"expiryStatus"`
}

// Handle lists instances for one item id. A wiped item stays addressable
// here and returns 200 with an empty list. Inventory, restock, and the
// planner hide that item; this direct read does not.
func (h *InventoryInstancesListHandler) Handle(req Request[struct{}, inventoryInstancesListPathParams]) ([]ItemInstanceWithStatus, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	instances, err := h.Pantry.ListItemInstances(req.Context, scope, req.PathParams.ItemID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	warningDays := 7

	result := make([]ItemInstanceWithStatus, len(instances))
	for i, inst := range instances {
		result[i] = ItemInstanceWithStatus{
			ItemInstance: inst,
			ExpiryStatus: inventory.ComputeExpiryStatus(inst.ExpiresAt, now, warningDays),
		}
	}

	return result, nil
}
