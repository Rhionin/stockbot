package server

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/Rhionin/stockbot/internal/attention"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/store"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

const listDrainTimeout = 250 * time.Millisecond

type listAssembler struct {
	Coalescer   *shopping.ListCoalescer
	Supply      *supply.Service
	SupplyRead  *supply.Service
	Shopping    *shopping.Store
	Pantry      pantryLister
	Ledger      *cart.Ledger
	Registry    *cart.Registry
	Adjustments interface {
		GetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) (*shopping.Adjustment, error)
	}
	Credentials *appcred.Vault
	DB          *sql.DB
	ReadDB      *sql.DB
}

type assembledList struct {
	Items           []ShoppingListEntryResponse
	OffList         []OffListRowResponse
	Stores          []ShoppingListStoreInfo
	AttentionGroups []AttentionGroupResponse
	StoreDisabled   *StoreDisabledResponse
	LastTripEndedAt *string
	LastTripEndedBy *string
	Cursor          string
	ListStale       bool
}

func (a *listAssembler) assembleShoppingListW3(req Request[struct{}, struct{}], scope tenant.Scope, statusMode bool) (assembledList, error) {
	out := assembledList{}
	household := scope.HouseholdID()
	if a.Coalescer != nil {
		out.ListStale = !a.Coalescer.Drain(req.Context, household, listDrainTimeout)
	}
	readSvc := a.SupplyRead
	if readSvc == nil {
		readSvc = a.Supply
	}
	if readSvc == nil && a.ReadDB != nil {
		readSvc = supply.Open(a.ReadDB)
	}
	now := time.Now().UTC()
	provision, err := loadShoppingSnapshot(req.Context, scope, a.Pantry, a.Shopping)
	if err != nil {
		return out, err
	}
	lineCtx, err := loadLineStoreContext(req.Context, a.DB, scope)
	if err != nil {
		return out, err
	}
	providerID := targetProviderID(req, scope, a.Registry, a.Credentials)
	ledger := map[string]cart.LedgerEntry{}
	if a.Ledger != nil && providerID != "" {
		ledger, err = a.Ledger.ListForProvider(req.Context, scope, cart.ProviderID(providerID))
		if err != nil {
			return out, InternalError(err)
		}
	}
	requested := map[string]int{}
	for itemID, row := range ledger {
		requested[itemID] = row.Requested
	}
	planViews := map[string]supply.LinePlanView{}
	if readSvc != nil {
		if pv, err := readSvc.PlanView(req.Context, scope, now, requested); err == nil {
			planViews = pv
		}
	}
	changes, err := a.Shopping.ListLineChanges(req.Context, scope)
	if err != nil {
		return out, InternalError(err)
	}
	touched, err := batchTouched(req.Context, a.DB, scope)
	if err != nil {
		return out, err
	}
	items, err := presentShoppingListW3(req, scope, provision, a.Ledger, a.Registry, a.Adjustments, lineCtx, a.Credentials, planViews, changes, touched, ledger, statusMode)
	if err != nil {
		return out, err
	}
	out.Items = items
	if statusMode {
		tripSeq, _ := householdTripMeta(req.Context, a.DB, scope)
		marks, _ := attention.LoadMarks(req.Context, a.DB, scope)
		sendFails, _ := attention.LoadSendFailures(req.Context, a.DB, scope)
		skips, _ := batchActiveSkips(req.Context, a.DB, scope)
		planned := map[string]struct{}{}
		staged := map[string]struct{}{}
		for k := range planViews {
			planned[k] = struct{}{}
		}
		for _, row := range provision.Rows {
			key := shopping.ItemLineKey(row.ItemID)
			if row.GroupID != "" {
				key = shopping.GroupLineKey(row.GroupID)
			}
			staged[key] = struct{}{}
		}
		opening := false
		if readSvc != nil {
			settings, err := readSvc.Settings(req.Context, scope)
			if err == nil {
				opening = settings.Opening
			}
		}
		sendMap := sendFails
		perLine, groups := attention.Evaluate(attention.EvaluateInput{
			TripSeq: tripSeq, Marks: marks, SendFailures: sendMap,
			Skipped: skips, Opening: opening, PlannedKeys: planned, StagedKeys: staged,
		})
		out.OffList, out.AttentionGroups = buildOffListAndGroups(req, scope, planViews, changes, provision, perLine, groups, lineCtx, ledger, providerID, a.Registry, a.Credentials)
		for i := range out.Items {
			key := out.Items[i].LineKey
			if key == "" {
				key = shopping.ItemLineKey(out.Items[i].ItemID)
			}
			out.Items[i].NeedsAttention = wireNeedsAttention(perLine[key])
		}
		out.Stores = listStoreInfos(lineCtx)
	}
	cur, err := changesCursor(req.Context, a.DB, household)
	if err != nil {
		return out, err
	}
	out.Cursor = cur
	return out, nil
}

func presentShoppingListW3(req Request[struct{}, struct{}], scope tenant.Scope, provision shoppingProvision, ledgerStore *cart.Ledger, registry *cart.Registry, adjustments interface {
	GetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) (*shopping.Adjustment, error)
}, lineCtx *store.LineStoreContext, credentials *appcred.Vault, planViews map[string]supply.LinePlanView, changes map[string]shopping.LineChange, touched map[string]bool, ledger map[string]cart.LedgerEntry, statusMode bool) ([]ShoppingListEntryResponse, error) {
	base, err := presentShoppingList(req, scope, provision, ledgerStore, registry, adjustments, lineCtx, credentials)
	if err != nil {
		return nil, err
	}
	providerID := targetProviderID(req, scope, registry, credentials)
	gate := providerGate(req.Context, scope, registry, credentials)
	products := productIDByItemID(provision.Items)
	rowByItem := map[string]shopping.ShoppingListItem{}
	for _, row := range provision.Rows {
		rowByItem[row.ItemID] = row
	}
	for i := range base {
		row := rowByItem[base[i].ItemID]
		key := shopping.ItemLineKey(row.ItemID)
		if row.GroupID != "" {
			key = shopping.GroupLineKey(row.GroupID)
		}
		sent := 0
		if providerID != "" {
			sent = ledger[row.ItemID].Requested
		}
		base[i].Sent = sent
		base[i].StoreID = effectiveStoreID(lineCtx, row, products[row.ItemID])
		base[i].CanSendToCart = lineCanSendToCart(lineCtx, row, products[row.ItemID], providerID, nil, gate)
		if touched[row.ID] {
			base[i].Touched = true
		}
		if pv, ok := planViews[key]; ok {
			base[i].PlanBasis = wirePlanBasis(pv)
			base[i].Basis = wireLegacyBasis(pv, sent)
		}
		if ch, ok := changes[key]; ok {
			base[i].LastChange = wireLineChange(ch)
		}
		if statusMode {
			base[i].LineKey = key
		} else {
			base[i].LineKey = key
		}
	}
	return base, nil
}

func wirePlanBasis(pv supply.LinePlanView) *ShoppingListPlanBasis {
	return &ShoppingListPlanBasis{
		OnHand: pv.OnHand, OnHandAmount: pv.OnHandAmount, Unit: pv.Unit,
		Target: ShoppingPlanTarget{
			Kind: pv.Target.Kind, Value: pv.Target.Value, Par: pv.Target.Par,
			Goal: pv.Target.Goal, Unit: pv.Target.Unit,
		},
		RatePerMonth: pv.RatePerMonth, Replacing: pv.Replacing, Short: pv.Short,
	}
}

func wireLegacyBasis(pv supply.LinePlanView, requested int) *ShoppingListBasis {
	return &ShoppingListBasis{
		Mode:           pv.Mode,
		TargetQuantity: pv.TargetQuantity,
		InstanceCount:  pv.InstanceCount,
		ConsumedUnits:  pv.ConsumedUnits,
		Requested:      requested,
	}
}

func wireLineChange(ch shopping.LineChange) *ListLineChangeResponse {
	resp := &ListLineChangeResponse{
		Effect: ch.Effect, Cause: ch.Cause,
		FromQty: ch.FromQty, ToQty: ch.ToQty, N: ch.N,
		ActorUserID: ch.ActorUserID, ActorDisplayName: ch.ActorDisplayName,
		At: ch.At.UTC().Format(time.RFC3339Nano), TripSeq: ch.TripSeq,
	}
	if ch.Reason != "" {
		resp.Reason = string(ch.Reason)
	}
	return resp
}

func wireNeedsAttention(na *attention.NeedsAttention) *NeedsAttentionResponse {
	if na == nil {
		return nil
	}
	return &NeedsAttentionResponse{
		Reason: na.Reason, Detail: na.Detail, Others: na.Others, Stuck: na.Stuck,
		Fix: NeedsAttentionFixResponse{Who: []NeedsAttentionWho{{Role: "member"}}},
	}
}

func effectiveStoreID(lineCtx *store.LineStoreContext, row shopping.ShoppingListItem, productID string) *string {
	if lineCtx == nil {
		return nil
	}
	id := lineCtx.LineStore(row.RunStoreID, productID)
	if id == "" {
		return nil
	}
	return &id
}

func batchTouched(ctx context.Context, db *sql.DB, scope tenant.Scope) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, touched FROM shopping_list_items WHERE household_id = ? AND purchased_at IS NULL`,
		scope.HouseholdID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		var touched int
		if err := rows.Scan(&id, &touched); err != nil {
			return nil, err
		}
		out[id] = touched != 0
	}
	return out, rows.Err()
}

func changesCursor(ctx context.Context, db *sql.DB, household string) (string, error) {
	v, err := changes.Version(ctx, db, household, changes.ScopeList)
	if err != nil {
		return "", err
	}
	return changes.Cursor(household, changes.ScopeList, v), nil
}

func householdTripMeta(ctx context.Context, db *sql.DB, scope tenant.Scope) (int, error) {
	var seq int
	err := db.QueryRowContext(ctx, `SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&seq)
	return seq, err
}

func batchActiveSkips(ctx context.Context, db *sql.DB, scope tenant.Scope) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(group_id,''), item_id FROM staged_cart_skips s
		JOIN households h ON h.id = s.household_id
		WHERE s.household_id = ? AND h.trip_seq < s.expires_trip`, scope.HouseholdID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var groupID, itemID string
		if err := rows.Scan(&groupID, &itemID); err != nil {
			return nil, err
		}
		key := shopping.ItemLineKey(itemID)
		if groupID != "" {
			key = shopping.GroupLineKey(groupID)
		}
		out[key] = struct{}{}
	}
	return out, rows.Err()
}


func buildOffListAndGroups(req Request[struct{}, struct{}], scope tenant.Scope, planViews map[string]supply.LinePlanView, changes map[string]shopping.LineChange, provision shoppingProvision, perLine map[string]*attention.NeedsAttention, groups []attention.AttentionGroup, lineCtx *store.LineStoreContext, ledger map[string]cart.LedgerEntry, providerID string, registry *cart.Registry, credentials *appcred.Vault) ([]OffListRowResponse, []AttentionGroupResponse) {
	staged := map[string]struct{}{}
	for _, row := range provision.Rows {
		key := shopping.ItemLineKey(row.ItemID)
		if row.GroupID != "" {
			key = shopping.GroupLineKey(row.GroupID)
		}
		staged[key] = struct{}{}
	}
	var off []OffListRowResponse
	for key, pv := range planViews {
		if _, on := staged[key]; on {
			continue
		}
		if pv.Buy > 0 {
			off = append(off, offListRow(key, pv, changes[key], perLine[key], lineCtx, ledger, providerID))
		}
	}
	for key, na := range perLine {
		if _, on := staged[key]; on {
			continue
		}
		if _, already := findOffList(off, key); already {
			continue
		}
		pv := planViews[key]
		off = append(off, offListRow(key, pv, changes[key], na, lineCtx, ledger, providerID))
	}
	sort.Slice(off, func(i, j int) bool { return off[i].ID < off[j].ID })
	wireGroups := make([]AttentionGroupResponse, len(groups))
	for i, g := range groups {
		wireGroups[i] = AttentionGroupResponse{
			Reason: g.Reason, Detail: g.Detail, Count: g.Count, Stuck: g.Stuck, RowKeys: g.RowKeys,
		}
	}
	return off, wireGroups
}

func findOffList(rows []OffListRowResponse, id string) (OffListRowResponse, bool) {
	for _, r := range rows {
		if r.ID == id {
			return r, true
		}
	}
	return OffListRowResponse{}, false
}

func offListRow(key string, pv supply.LinePlanView, ch shopping.LineChange, na *attention.NeedsAttention, lineCtx *store.LineStoreContext, ledger map[string]cart.LedgerEntry, providerID string) OffListRowResponse {
	sent := 0
	if pv.ItemID != "" && providerID != "" {
		sent = ledger[pv.ItemID].Requested
	}
	reason := "taken_off"
	if pv.Buy > 0 {
		reason = "not_on_list"
	}
	row := OffListRowResponse{
		ID: key, Name: key, OffListReason: reason, Sent: sent,
		CanSendToCart: false, NeedsAttention: wireNeedsAttention(na),
		StoreID:       effectiveStoreID(lineCtx, shopping.ShoppingListItem{ItemID: pv.ItemID}, pv.ProductID),
	}
	if pv.ItemID != "" {
		row.ItemID = pv.ItemID
	}
	if pv.GroupID != "" {
		row.GroupID = pv.GroupID
	}
	if pv.ProductID != "" {
		row.ProductID = pv.ProductID
	}
	if pv.Buy > 0 {
		row.Quantity = pv.Buy
	}
	row.PlanBasis = wirePlanBasis(pv)
	row.Basis = wireLegacyBasis(pv, sent)
	if ch.LineKey != "" {
		row.LastChange = wireLineChange(ch)
	}
	return row
}

func listStoreInfos(lineCtx *store.LineStoreContext) []ShoppingListStoreInfo {
	if lineCtx == nil || !lineCtx.HasStores() {
		return []ShoppingListStoreInfo{}
	}
	return []ShoppingListStoreInfo{}
}