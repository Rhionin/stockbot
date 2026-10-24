package inventory

import (
	"context"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// ListHandler handles GET /api/inventory requests.
type ListHandler struct {
	Pantry interface {
		GetInventoryList(ctx context.Context, scope tenant.Scope, now time.Time, warningDays int) ([]InventoryItem, error)
	}
	Scope       tenant.Scope
	Now         func() time.Time
	WarningDays int
}

func (h *ListHandler) Handle(req struct {
	Context context.Context
}) ([]InventoryItem, error) {
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}

	warningDays := 7
	if h.WarningDays != 0 {
		warningDays = h.WarningDays
	}

	return h.Pantry.GetInventoryList(req.Context, h.Scope, now, warningDays)
}

// InstancesListHandler handles GET /api/inventory/{itemId}/instances requests.
type InstancesListHandler struct {
	Pantry interface {
		ListItemInstances(ctx context.Context, scope tenant.Scope, itemID string) ([]ItemInstance, error)
	}
	Scope       tenant.Scope
	Now         func() time.Time
	WarningDays int
}

type instancesListPathParams struct {
	ItemID string `json:"itemId"`
}

// ItemInstanceWithStatus extends ItemInstance with computed expiry status.
type ItemInstanceWithStatus struct {
	ItemInstance
	ExpiryStatus ExpiryStatus `json:"expiryStatus"`
}

func (h *InstancesListHandler) Handle(req struct {
	Context    context.Context
	PathParams instancesListPathParams
}) ([]ItemInstanceWithStatus, error) {
	instances, err := h.Pantry.ListItemInstances(req.Context, h.Scope, req.PathParams.ItemID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}

	warningDays := 7
	if h.WarningDays != 0 {
		warningDays = h.WarningDays
	}

	result := make([]ItemInstanceWithStatus, len(instances))
	for i, inst := range instances {
		result[i] = ItemInstanceWithStatus{
			ItemInstance: inst,
			ExpiryStatus: ComputeExpiryStatus(inst.ExpiresAt, now, warningDays),
		}
	}

	return result, nil
}

// InstanceCreateHandler handles POST /api/inventory/{itemId}/instances requests.
type InstanceCreateHandler struct {
	Pantry interface {
		AddInstance(ctx context.Context, scope tenant.Scope, instance ItemInstance) (*ItemInstance, error)
	}
	Scope tenant.Scope
}

type instanceCreatePathParams struct {
	ItemID string `json:"itemId"`
}

type instanceCreateRequest struct {
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

func (h *InstanceCreateHandler) Handle(req struct {
	Context    context.Context
	PathParams instanceCreatePathParams
	Body       instanceCreateRequest
}) (*ItemInstance, error) {
	instance := ItemInstance{
		ItemID:    req.PathParams.ItemID,
		StockInAt: time.Now(),
		ExpiresAt: req.Body.ExpiresAt,
	}

	return h.Pantry.AddInstance(req.Context, h.Scope, instance)
}

// InstanceDeleteHandler handles DELETE /api/inventory/instances/{instanceId} requests.
type InstanceDeleteHandler struct {
	Pantry interface {
		RemoveInstance(ctx context.Context, scope tenant.Scope, instanceID string, reason string) error
	}
	Scope tenant.Scope
}

type instanceDeletePathParams struct {
	InstanceID string `json:"instanceId"`
}

func (h *InstanceDeleteHandler) Handle(req struct {
	Context    context.Context
	PathParams instanceDeletePathParams
}) (struct{}, error) {
	err := h.Pantry.RemoveInstance(req.Context, h.Scope, req.PathParams.InstanceID, "manual")
	if err != nil {
		return struct{}{}, err
	}
	return struct{}{}, nil
}
