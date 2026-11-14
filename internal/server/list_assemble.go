package server

import (
	"context"
	"database/sql"
	"log/slog"
	"sort"
	"time"

	"github.com/Rhionin/stockbot/internal/attention"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/product"
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
	if statusMode && a.Shopping != nil {
		purchased, err := a.Shopping.ListPurchasedAuto(req.Context, scope)
		if err != nil {
			return out, InternalError(err)
		}
		provision.Rows = append(provision.Rows, purchased...)
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
		zeroForTrip := activeZeroForTripKeys(out.Items)
		sendMap := sendFails
		perLine, groups := attention.Evaluate(attention.EvaluateInput{
			TripSeq: tripSeq, Marks: marks, SendFailures: sendMap,
			Skipped: skips, Opening: opening, PlannedKeys: planned, StagedKeys: staged,
			ZeroForTrip: zeroForTrip,
		})
		productNames, err := batchOffListProductNames(req.Context, a.DB, scope, planViews)
		if err != nil {
			return out, InternalError(err)
		}
		groupNames, err := batchOffListGroupNames(req.Context, a.DB, scope, planViews)
		if err != nil {
			return out, InternalError(err)
		}
		off, wireGroups, _ := buildOffListAndGroups(planViews, changes, provision, perLine, groups, skips, opening, lineCtx, ledger, providerID, productNames, groupNames)
		out.Items, off, err = applyZeroForTrip(req.Context, a.DB, out.Items, off, skips, planViews, productNames, groupNames, providerID, a.Adjustments, scope)
		if err != nil {
			return out, InternalError(err)
		}
		out.OffList, out.AttentionGroups = off, wireGroups
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
	invByItem := map[string]inventory.Item{}
	for _, it := range provision.Items {
		invByItem[it.ID] = it
	}
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
		if touched[base[i].ID] {
			base[i].Touched = true
		}
		if pv, ok := planViews[key]; ok {
			base[i].PlanBasis = wirePlanBasis(pv)
			base[i].Basis = masterLegacyBasis(base[i], pv, sent)
		}
		if ch, ok := changes[key]; ok {
			base[i].LastChange = wireLineChange(ch)
		}
		base[i].LineKey = key
		if statusMode {
			base[i].LineDisplayName = listEntryLineDisplayName(base[i], invByItem[base[i].ItemID])
		}
	}
	return base, nil
}

func listEntryLineDisplayName(entry ShoppingListEntryResponse, inv inventory.Item) string {
	if entry.Group != nil && entry.Group.Name != "" {
		return entry.Group.Name
	}
	if inv.Product != nil && inv.Product.Name != "" {
		return inv.Product.Name
	}
	if entry.Group != nil {
		for _, m := range entry.Group.Members {
			if m.ItemID == entry.ItemID && m.Name != "" {
				return m.Name
			}
		}
	}
	return ""
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

// masterLegacyBasis matches master presentShoppingList: basis is omitted until master populates it.
func masterLegacyBasis(entry ShoppingListEntryResponse, _ supply.LinePlanView, _ int) *ShoppingListBasis {
	_ = entry
	return nil
}

func masterLegacyBasisForOffList(_ supply.LinePlanView, _ int) *ShoppingListBasis {
	return nil
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


func buildOffListAndGroups(planViews map[string]supply.LinePlanView, changes map[string]shopping.LineChange, provision shoppingProvision, perLine map[string]*attention.NeedsAttention, groups []attention.AttentionGroup, skipped map[string]struct{}, opening bool, lineCtx *store.LineStoreContext, ledger map[string]cart.LedgerEntry, providerID string, productNames, groupNames map[string]string) ([]OffListRowResponse, []AttentionGroupResponse, map[string]struct{}) {
	staged := map[string]struct{}{}
	for _, row := range provision.Rows {
		key := shopping.ItemLineKey(row.ItemID)
		if row.GroupID != "" {
			key = shopping.GroupLineKey(row.GroupID)
		}
		staged[key] = struct{}{}
	}
	var off []OffListRowResponse
	droppedUnnamed := map[string]struct{}{}
	addOff := func(key string) {
		if _, on := staged[key]; on {
			return
		}
		pv := planViews[key]
		ch := changes[key]
		na := perLine[key]
		reason := offListReason(key, pv, ch, na, skipped, opening, nil)
		if reason == "" {
			return
		}
		if _, already := findOffList(off, key); already {
			return
		}
		name := offListDisplayName(pv, productNames, groupNames)
		if name == "" || name == key {
			slog.Warn("off_list_row_dropped", "line_key", key, "off_list_reason", reason)
			droppedUnnamed[key] = struct{}{}
			delete(perLine, key)
			return
		}
		off = append(off, offListRow(key, name, pv, ch, na, reason, lineCtx, ledger, providerID))
	}
	for key := range skipped {
		addOff(key)
	}
	if opening {
		for key, pv := range planViews {
			if pv.Buy <= 0 {
				continue
			}
			addOff(key)
		}
	}
	for key, ch := range changes {
		if ch.Effect != shopping.EffectTakenOff {
			continue
		}
		addOff(key)
	}
	for key, na := range perLine {
		if na == nil {
			continue
		}
		addOff(key)
	}
	sort.Slice(off, func(i, j int) bool { return off[i].ID < off[j].ID })
	return off, wireAttentionGroups(groups, perLine, droppedUnnamed), droppedUnnamed
}

func wireAttentionGroups(groups []attention.AttentionGroup, perLine map[string]*attention.NeedsAttention, droppedUnnamed map[string]struct{}) []AttentionGroupResponse {
	wireGroups := make([]AttentionGroupResponse, 0, len(groups))
	for _, g := range groups {
		keys := g.RowKeys
		stuck := g.Stuck
		if len(droppedUnnamed) > 0 {
			filtered := keys[:0]
			stuck = 0
			for _, k := range keys {
				if _, drop := droppedUnnamed[k]; drop {
					continue
				}
				filtered = append(filtered, k)
				if na := perLine[k]; na != nil && na.Stuck {
					stuck++
				}
			}
			keys = filtered
		}
		if len(keys) == 0 {
			continue
		}
		wireGroups = append(wireGroups, AttentionGroupResponse{
			Reason: g.Reason, Detail: g.Detail, Count: len(keys), Stuck: stuck, RowKeys: keys,
		})
	}
	return wireGroups
}

func offListReason(key string, pv supply.LinePlanView, ch shopping.LineChange, na *attention.NeedsAttention, skipped map[string]struct{}, opening bool, zeroForTrip map[string]struct{}) string {
	if _, ok := skipped[key]; ok {
		return "removed"
	}
	if _, ok := zeroForTrip[key]; ok {
		return "zero_for_trip"
	}
	if ch.Effect == shopping.EffectTakenOff {
		return "taken_off"
	}
	if opening && pv.Buy > 0 {
		return "opening"
	}
	if na != nil {
		return "not_on_list"
	}
	return ""
}

func countW3ShortNow(items []ShoppingListEntryResponse) int {
	seen := map[string]struct{}{}
	n := 0
	for _, item := range items {
		if item.Quantity <= 0 || item.NeedsAttention != nil {
			continue
		}
		key := item.LineKey
		if key == "" {
			key = shopping.ItemLineKey(item.ItemID)
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		n++
	}
	return n
}

func countW3NotOnList(off []OffListRowResponse) int {
	n := 0
	for _, row := range off {
		if row.NeedsAttention != nil {
			continue
		}
		switch row.OffListReason {
		case "removed", "opening", "taken_off", "zero_for_trip":
			n++
		}
	}
	return n
}

// batchOffListProductNamesHook is a test seam to fault product name lookup without breaking household reads.
var batchOffListProductNamesHook func(context.Context, *sql.DB, tenant.Scope, map[string]supply.LinePlanView) (map[string]string, error)

func batchOffListProductNames(ctx context.Context, db *sql.DB, scope tenant.Scope, planViews map[string]supply.LinePlanView) (map[string]string, error) {
	if batchOffListProductNamesHook != nil {
		return batchOffListProductNamesHook(ctx, db, scope, planViews)
	}
	ids := make([]string, 0)
	seen := map[string]struct{}{}
	for _, pv := range planViews {
		if pv.ProductID == "" {
			continue
		}
		if _, ok := seen[pv.ProductID]; ok {
			continue
		}
		seen[pv.ProductID] = struct{}{}
		ids = append(ids, pv.ProductID)
	}
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	query := `SELECT p.id, ` + product.DisplayNameSQL("p") + ` FROM products p WHERE p.id IN (` + sqlPlaceholders(len(ids)) + `)`
	args := make([]any, 0, 1+len(ids))
	args = append(args, scope.HouseholdID())
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// batchOffListGroupNamesHook is a test seam to fault group name lookup without breaking household reads.
var batchOffListGroupNamesHook func(context.Context, *sql.DB, tenant.Scope, map[string]supply.LinePlanView) (map[string]string, error)

func batchOffListGroupNames(ctx context.Context, db *sql.DB, scope tenant.Scope, planViews map[string]supply.LinePlanView) (map[string]string, error) {
	if batchOffListGroupNamesHook != nil {
		return batchOffListGroupNamesHook(ctx, db, scope, planViews)
	}
	ids := make([]string, 0)
	seen := map[string]struct{}{}
	for _, pv := range planViews {
		if pv.GroupID == "" {
			continue
		}
		if _, ok := seen[pv.GroupID]; ok {
			continue
		}
		seen[pv.GroupID] = struct{}{}
		ids = append(ids, pv.GroupID)
	}
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	query := `SELECT id, name FROM product_groups WHERE household_id = ? AND id IN (` + sqlPlaceholders(len(ids)) + `)`
	args := []any{scope.HouseholdID()}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

func sqlPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	s := "?"
	for i := 1; i < n; i++ {
		s += ",?"
	}
	return s
}

func offListDisplayName(pv supply.LinePlanView, productNames, groupNames map[string]string) string {
	if pv.GroupID != "" {
		if name := groupNames[pv.GroupID]; name != "" {
			return name
		}
	}
	if pv.ProductID != "" {
		if name := productNames[pv.ProductID]; name != "" {
			return name
		}
	}
	return ""
}

func findOffList(rows []OffListRowResponse, id string) (OffListRowResponse, bool) {
	for _, r := range rows {
		if r.ID == id {
			return r, true
		}
	}
	return OffListRowResponse{}, false
}

func offListRow(key, name string, pv supply.LinePlanView, ch shopping.LineChange, na *attention.NeedsAttention, offListReason string, lineCtx *store.LineStoreContext, ledger map[string]cart.LedgerEntry, providerID string) OffListRowResponse {
	sent := 0
	if pv.ItemID != "" && providerID != "" {
		sent = ledger[pv.ItemID].Requested
	}
	row := OffListRowResponse{
		ID: key, Name: name, OffListReason: offListReason, Sent: sent,
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
	row.Basis = masterLegacyBasisForOffList(pv, sent)
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