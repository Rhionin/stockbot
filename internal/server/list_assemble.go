package server

import (
	"context"
	"database/sql"
	"log/slog"
	"sort"
	"time"

	"github.com/Rhionin/stockbot/internal/attention"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/inventory"
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
	Auth        *auth.Service
	EnvFallback ProviderEnv
}

type assembledList struct {
	Items           []ShoppingListEntryResponse
	StatusItems     []ShoppingListStatusEntryResponse
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
		pv, perr := readSvc.PlanView(req.Context, scope, now, requested)
		if perr == nil && faultOnSupplyPlanViewSuccess != nil {
			perr = faultOnSupplyPlanViewSuccess()
		}
		if perr != nil {
			if statusMode {
				return out, InternalError(perr)
			}
		} else {
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
		tripSeq, err := householdTripMeta(req.Context, a.DB, scope)
		if err != nil {
			return out, InternalError(err)
		}
		marks, err := attention.LoadMarks(req.Context, a.DB, scope)
		if err != nil {
			return out, InternalError(err)
		}
		activeZero, err := attention.LoadActiveZero(req.Context, a.DB, scope)
		if err != nil {
			return out, InternalError(err)
		}
		skips, err := batchActiveSkips(req.Context, a.DB, scope)
		if err != nil {
			return out, InternalError(err)
		}
		cutoff := attention.CutoffForSendRetention(now)
		sendRows, err := attention.LoadSendResults(req.Context, a.DB, scope, providerID, cutoff)
		if err != nil {
			return out, InternalError(err)
		}
		stagedByItem, unpurchasedStaged := unpurchasedStagedMaps(provision.Rows)
		itemIDs := sendItemIDs(sendRows)
		itemGroups, err := attention.LoadItemProductGroups(req.Context, a.DB, scope, itemIDs)
		if err != nil {
			return out, InternalError(err)
		}
		sendByLine := attention.MapSendResultsByLineKey(sendRows, stagedByItem, planViews, itemGroups)
		opening := false
		if readSvc != nil {
			settings, err := readSvc.Settings(req.Context, scope)
			if err == nil && faultOnSupplySettingsSuccess != nil {
				err = faultOnSupplySettingsSuccess()
			}
			if err != nil {
				return out, InternalError(err)
			}
			opening = settings.Opening
		}
		zftWork, err := classifyActiveZeroForTrip(req.Context, out.Items, providerID, a.Adjustments, scope)
		if err != nil {
			return out, InternalError(err)
		}
		if activeZero == nil {
			activeZero = map[string]struct{}{}
		}
		entryByKey := planViewEntryIDs(planViews, provision.Rows)
		gate := providerGate(req.Context, scope, a.Registry, a.Credentials)
		connSuppressed := krogerSendingDisabled(providerID, a.EnvFallback)
		connAuth := providerAuthCapability(a.Registry, providerID)
		var connFact attention.Reason
		if providerID != "" && (gate.Configured == nil || gate.Configured(providerID)) {
			var cerr error
			connFact, cerr = loadConnFactForAttention(req.Context, a.DB, scope, providerID, connAuth, connSuppressed)
			if cerr != nil {
				return out, InternalError(cerr)
			}
		}
		connectAllowed, err := connectAllowedForViewer(req.Context, a.Auth, scope.HouseholdID())
		if err != nil {
			return out, InternalError(err)
		}
		products := productIDByItemID(provision.Items)
		sendable := buildAttentionSendable(planViews, sendByLine, unpurchasedStaged, provision.Rows, products, lineCtx, providerID, gate)
		perLine, groups := attention.Evaluate(attention.EvaluateInput{
			TripSeq: tripSeq, Marks: marks, SendByLine: sendByLine,
			Skipped: skips, Opening: opening, ActiveZero: activeZero,
			PlanViews: planViews, UnpurchasedStaged: unpurchasedStaged, ProviderID: providerID,
			PlanViewEntryByKey: entryByKey,
			ConnFact:           connFact, Sendable: sendable, ConnectAllowed: connectAllowed,
		})
		out.StoreDisabled = storeDisabledResponse(providerID, a.EnvFallback)
		productNames, err := batchOffListProductNames(req.Context, a.DB, scope, planViews)
		if err != nil {
			return out, InternalError(err)
		}
		groupNames, err := batchOffListGroupNames(req.Context, a.DB, scope, planViews)
		if err != nil {
			return out, InternalError(err)
		}
		off, wireGroups := buildOffListAndGroups(planViews, changes, provision, perLine, groups, skips, opening, lineCtx, ledger, providerID, productNames, groupNames)
		out.Items, off, err = applyZeroForTrip(req.Context, a.DB, out.Items, off, skips, planViews, productNames, groupNames, providerID, a.Adjustments, scope, &zftWork)
		if err != nil {
			return out, InternalError(err)
		}
		out.OffList, out.AttentionGroups = off, wireGroups
		out.StatusItems = wireStatusItems(out.Items, perLine, planViews, productNames, groupNames)
		for i := range off {
			key := off[i].ID
			off[i].NeedsAttention = wireNeedsAttention(perLine[key])
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
	others := attention.FormatReasons(na.Others)
	fix := NeedsAttentionFixResponse{
		Code: na.Fix.Code, Allowed: na.Fix.Allowed, Who: nil,
		Target: NeedsAttentionFixTarget{
			EntryID: na.Fix.Target.EntryID, ItemID: na.Fix.Target.ItemID,
			GroupID: na.Fix.Target.GroupID, ProductID: na.Fix.Target.ProductID,
			ProviderID: na.Fix.Target.ProviderID,
		},
	}
	if na.Fix.Who != nil {
		fix.Who = &NeedsAttentionWho{Role: na.Fix.Who.Role}
	}
	return &NeedsAttentionResponse{
		Reason: string(na.Reason), Detail: na.Detail, Others: others, Stuck: na.Stuck, Fix: fix,
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
	if faultAfterHouseholdTripMeta != nil {
		if err := faultAfterHouseholdTripMeta(); err != nil {
			return 0, err
		}
	}
	var seq int
	err := db.QueryRowContext(ctx, `SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&seq)
	return seq, err
}

func batchActiveSkips(ctx context.Context, db *sql.DB, scope tenant.Scope) (map[string]struct{}, error) {
	if faultAfterBatchActiveSkips != nil {
		if err := faultAfterBatchActiveSkips(); err != nil {
			return nil, err
		}
	}
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

func unpurchasedStagedMaps(rows []shopping.ShoppingListItem) (map[string]attention.StagedRow, map[string]int) {
	byItem := map[string]attention.StagedRow{}
	byKey := map[string]int{}
	for _, row := range rows {
		if row.PurchasedAt != nil {
			continue
		}
		key := shopping.ItemLineKey(row.ItemID)
		if row.GroupID != "" {
			key = shopping.GroupLineKey(row.GroupID)
		}
		if row.Quantity >= 1 {
			if cur, ok := byKey[key]; !ok || row.Quantity > cur {
				byKey[key] = row.Quantity
			}
		}
		if row.Quantity >= 1 {
			byItem[row.ItemID] = attention.StagedRow{ItemID: row.ItemID, GroupID: row.GroupID, Qty: row.Quantity}
		}
	}
	return byItem, byKey
}

func sendItemIDs(rows []attention.SendResultRow) []string {
	seen := map[string]struct{}{}
	var ids []string
	for _, row := range rows {
		if _, ok := seen[row.ItemID]; ok {
			continue
		}
		seen[row.ItemID] = struct{}{}
		ids = append(ids, row.ItemID)
	}
	return ids
}

func planViewEntryIDs(planViews map[string]supply.LinePlanView, rows []shopping.ShoppingListItem) map[string]attention.PlanEntryIDs {
	out := map[string]attention.PlanEntryIDs{}
	for key, pv := range planViews {
		out[key] = attention.PlanEntryIDs{ItemID: pv.ItemID, GroupID: pv.GroupID}
	}
	for _, row := range rows {
		if row.PurchasedAt != nil || row.ID == "" {
			continue
		}
		key := shopping.ItemLineKey(row.ItemID)
		if row.GroupID != "" {
			key = shopping.GroupLineKey(row.GroupID)
		}
		ids := out[key]
		ids.ItemID = row.ItemID
		if row.GroupID != "" {
			ids.GroupID = row.GroupID
		}
		ids.EntryID = row.ID
		out[key] = ids
	}
	return out
}

func wireStatusItems(items []ShoppingListEntryResponse, perLine map[string]*attention.NeedsAttention, planViews map[string]supply.LinePlanView, productNames, groupNames map[string]string) []ShoppingListStatusEntryResponse {
	out := make([]ShoppingListStatusEntryResponse, len(items))
	for i, item := range items {
		key := item.LineKey
		if key == "" {
			key = shopping.ItemLineKey(item.ItemID)
		}
		pv := planViews[key]
		if pv.ItemID == "" {
			pv.ItemID = item.ItemID
		}
		if item.Group != nil && pv.GroupID == "" {
			pv.GroupID = item.Group.ID
		}
		name := offListDisplayName(pv, productNames, groupNames)
		if name == "" {
			name = shoppingListEntryLineName(item)
		}
		out[i] = ShoppingListStatusEntryResponse{
			ShoppingListEntryResponse: item,
			Name:                      name,
			NeedsAttention:            wireNeedsAttention(perLine[key]),
		}
	}
	return out
}

func buildOffListAndGroups(planViews map[string]supply.LinePlanView, changes map[string]shopping.LineChange, provision shoppingProvision, perLine map[string]*attention.NeedsAttention, groups []attention.AttentionGroup, skipped map[string]struct{}, opening bool, lineCtx *store.LineStoreContext, ledger map[string]cart.LedgerEntry, providerID string, productNames, groupNames map[string]string) ([]OffListRowResponse, []AttentionGroupResponse) {
	staged := map[string]struct{}{}
	for _, row := range provision.Rows {
		key := shopping.ItemLineKey(row.ItemID)
		if row.GroupID != "" {
			key = shopping.GroupLineKey(row.GroupID)
		}
		staged[key] = struct{}{}
	}
	var off []OffListRowResponse
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
		if name == key {
			name = ""
		}
		if name == "" {
			slog.Warn("off_list_row_unnamed", "line_key", key)
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
	wireGroups := make([]AttentionGroupResponse, len(groups))
	for i, g := range groups {
		wireGroups[i] = AttentionGroupResponse{
			Reason: string(g.Reason), Detail: g.Detail, Count: g.Count, Stuck: g.Stuck, RowKeys: g.RowKeys,
		}
	}
	return off, wireGroups
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

func countW3ShortNowStatus(items []ShoppingListStatusEntryResponse) int {
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
	if faultAfterBatchOffListNames != nil {
		if err := faultAfterBatchOffListNames(); err != nil {
			return nil, err
		}
	}
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
	if faultAfterBatchOffListNames != nil {
		if err := faultAfterBatchOffListNames(); err != nil {
			return nil, err
		}
	}
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
		StoreID: effectiveStoreID(lineCtx, shopping.ShoppingListItem{ItemID: pv.ItemID}, pv.ProductID),
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
