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

type zeroForTripPending struct {
	item ShoppingListEntryResponse
	adj  *shopping.Adjustment
}

// zeroForTripWork is the single active person-set 0 pass (§8 exemption + placement).
type zeroForTripWork struct {
	ExemptKeys map[string]struct{}
	Pending    []zeroForTripPending
}

func lineKeyForEntry(item ShoppingListEntryResponse) string {
	key := item.LineKey
	if key == "" {
		key = shopping.ItemLineKey(item.ItemID)
	}
	return key
}

func classifyActiveZeroForTrip(
	ctx context.Context,
	items []ShoppingListEntryResponse,
	providerID string,
	adjustments interface {
		GetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) (*shopping.Adjustment, error)
	},
	scope tenant.Scope,
) (zeroForTripWork, error) {
	out := zeroForTripWork{ExemptKeys: map[string]struct{}{}}
	if providerID == "" || adjustments == nil {
		return out, nil
	}
	for _, item := range items {
		if item.Adjustment == nil || *item.Adjustment != 0 || !item.CanSendToCart {
			continue
		}
		adj, err := adjustments.GetAdjustment(ctx, scope, item.ID, providerID)
		if err != nil {
			return zeroForTripWork{}, err
		}
		if adj == nil || adj.Quantity != 0 {
			continue
		}
		key := lineKeyForEntry(item)
		out.ExemptKeys[key] = struct{}{}
		out.Pending = append(out.Pending, zeroForTripPending{item: item, adj: adj})
	}
	return out, nil
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
	work *zeroForTripWork,
) ([]ShoppingListEntryResponse, []OffListRowResponse, error) {
	if applyZeroForTripTestErr != nil {
		return nil, nil, applyZeroForTripTestErr
	}
	if providerID == "" || adjustments == nil {
		return items, off, nil
	}
	var pending []zeroForTripPending
	if work != nil {
		pending = work.Pending
	} else {
		classified, err := classifyActiveZeroForTrip(ctx, items, providerID, adjustments, scope)
		if err != nil {
			return nil, nil, err
		}
		pending = classified.Pending
	}
	kept := make([]ShoppingListEntryResponse, 0, len(items))
	pendingIDs := map[string]struct{}{}
	for _, z := range pending {
		pendingIDs[z.item.ID] = struct{}{}
	}
	for _, item := range items {
		if _, wait := pendingIDs[item.ID]; !wait {
			kept = append(kept, item)
		}
	}
	if len(pending) == 0 {
		return kept, off, nil
	}
	actors := map[string]string{}
	for _, z := range pending {
		if z.adj.ActorUserID != nil && *z.adj.ActorUserID != "" {
			actors[*z.adj.ActorUserID] = ""
		}
	}
	resolveActorDisplayNames(ctx, db, actors)
	offByID := map[string]int{}
	for i, row := range off {
		offByID[row.ID] = i
	}
	for _, z := range pending {
		item := z.item
		key := lineKeyForEntry(item)
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
