package server

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// applyZeroForTripTestErr is a test-only fault injector (G2 lock).
var applyZeroForTripTestErr error

func zeroForTripExemptKeys(off []OffListRowResponse) map[string]struct{} {
	out := map[string]struct{}{}
	for _, row := range off {
		if row.OffListReason == "zero_for_trip" {
			out[row.ID] = struct{}{}
		}
	}
	return out
}

func activeZeroForTripKeys(items []ShoppingListEntryResponse) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range items {
		if item.Adjustment == nil || *item.Adjustment != 0 || !item.CanSendToCart {
			continue
		}
		key := item.LineKey
		if key == "" {
			key = shopping.ItemLineKey(item.ItemID)
		}
		out[key] = struct{}{}
	}
	return out
}

func applyZeroForTrip(
	ctx context.Context,
	db *sql.DB,
	items []ShoppingListEntryResponse,
	off []OffListRowResponse,
	skipped map[string]struct{},
	planViews map[string]supply.LinePlanView,
	productNames, groupNames map[string]string,
	providerID string,
	adjustments interface {
		GetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) (*shopping.Adjustment, error)
	},
	scope tenant.Scope,
) ([]ShoppingListEntryResponse, []OffListRowResponse, error) {
	if applyZeroForTripTestErr != nil {
		return nil, nil, applyZeroForTripTestErr
	}
	if providerID == "" || adjustments == nil {
		return items, off, nil
	}
	actors := map[string]string{}
	type zeroRow struct {
		item ShoppingListEntryResponse
		adj  *shopping.Adjustment
	}
	pending := []zeroRow{}
	kept := make([]ShoppingListEntryResponse, 0, len(items))
	for _, item := range items {
		if item.Adjustment == nil || *item.Adjustment != 0 || !item.CanSendToCart {
			kept = append(kept, item)
			continue
		}
		adj, err := adjustments.GetAdjustment(ctx, scope, item.ID, providerID)
		if err != nil {
			return nil, nil, err
		}
		if adj == nil || adj.Quantity != 0 {
			kept = append(kept, item)
			continue
		}
		pending = append(pending, zeroRow{item: item, adj: adj})
		if adj.ActorUserID != nil && *adj.ActorUserID != "" {
			actors[*adj.ActorUserID] = ""
		}
	}
	if len(pending) == 0 {
		return kept, off, nil
	}
	resolveActorDisplayNames(ctx, db, actors)
	offByID := map[string]int{}
	for i, row := range off {
		offByID[row.ID] = i
	}
	for _, z := range pending {
		item := z.item
		key := item.LineKey
		if key == "" {
			key = shopping.ItemLineKey(item.ItemID)
		}
		if _, rem := skipped[key]; rem {
			kept = append(kept, item)
			continue
		}
		zft := wireZeroForTrip(item.ID, providerID, z.adj, actors)
		if idx, ok := offByID[key]; ok {
			if off[idx].OffListReason == "removed" {
				continue
			}
			off[idx].OffListReason = "zero_for_trip"
			off[idx].ZeroForTrip = zft
			off[idx].NeedsAttention = nil
			continue
		}
		pv := planViews[key]
		if pv.ItemID == "" {
			pv.ItemID = item.ItemID
		}
		if item.Group != nil && pv.GroupID == "" {
			pv.GroupID = item.Group.ID
		}
		name := zeroForTripOffListName(item, pv, productNames, groupNames, key)
		if name == "" {
			if err := scope.Require(); err == nil {
				slog.Warn("zero_for_trip_off_list_dropped", "household_id", scope.HouseholdID(), "line_key", key)
			} else {
				slog.Warn("zero_for_trip_off_list_dropped", "line_key", key)
			}
			kept = append(kept, item)
			continue
		}
		row := OffListRowResponse{
			ID: key, Name: name, OffListReason: "zero_for_trip",
			ItemID: item.ItemID, Sent: item.Sent, StoreID: item.StoreID, CanSendToCart: false,
			PlanBasis: item.PlanBasis, Basis: item.Basis, LastChange: item.LastChange,
			ZeroForTrip: zft,
		}
		if item.Group != nil {
			row.GroupID = item.Group.ID
		}
		if pv.ProductID != "" {
			row.ProductID = pv.ProductID
		}
		off = append(off, row)
	}
	return kept, off, nil
}

func zeroForTripOffListName(item ShoppingListEntryResponse, pv supply.LinePlanView, productNames, groupNames map[string]string, key string) string {
	name := offListDisplayName(pv, productNames, groupNames)
	if name == "" && item.Group != nil {
		name = item.Group.Name
	}
	if name == "" {
		name = shoppingListEntryLineName(item)
	}
	if name == "" || name == key {
		return ""
	}
	return name
}

func shoppingListEntryLineName(item ShoppingListEntryResponse) string {
	if item.LineDisplayName != "" {
		return item.LineDisplayName
	}
	if item.Group == nil {
		return ""
	}
	if item.Group.Name != "" {
		return item.Group.Name
	}
	for _, m := range item.Group.Members {
		if m.ItemID == item.ItemID && m.Name != "" {
			return m.Name
		}
	}
	for _, m := range item.Group.Members {
		if m.Name != "" {
			return m.Name
		}
	}
	return ""
}

func wireZeroForTrip(entryID, providerID string, adj *shopping.Adjustment, actors map[string]string) *OffListZeroForTripResponse {
	if adj == nil {
		return nil
	}
	z := &OffListZeroForTripResponse{EntryID: entryID, ProviderID: providerID, ActorUserID: adj.ActorUserID}
	if adj.ActorUserID != nil {
		if name := actors[*adj.ActorUserID]; name != "" {
			z.ActorDisplayName = &name
		}
	}
	if adj.SetAt != nil {
		raw := adj.SetAt.UTC().Format(time.RFC3339Nano)
		z.SetAt = &raw
	}
	return z
}

func resolveActorDisplayNames(ctx context.Context, db *sql.DB, actors map[string]string) {
	if len(actors) == 0 || db == nil {
		return
	}
	args := make([]any, 0, len(actors))
	placeholders := make([]string, 0, len(actors))
	for id := range actors {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	q := `SELECT id, COALESCE(name, '') FROM users WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return
		}
		actors[id] = name
	}
}