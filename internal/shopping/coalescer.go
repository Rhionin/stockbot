package shopping

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

const defaultCoalescerDebounce = 50 * time.Millisecond

// BurstMeta carries stock-move burst fields for one coalescer job.
type BurstMeta struct {
	N         int
	Actor     *string
	Direction string // taken_out or put_away
}

// ListCoalescer debounces automatic list replans per household.
// PlanSnapshot runs a read-only supply plan for one household.
type PlanSnapshot func(ctx context.Context, scope tenant.Scope, now time.Time) ([]PlannedLine, []ShelfMember, error)

type ListCoalescer struct {
	db       *sql.DB
	readDB   *sql.DB
	plan     PlanSnapshot
	store    *Store
	debounce time.Duration
	clock    func() time.Time

	mu      sync.Mutex
	house   map[string]*coalescerHouse
	dirty   map[string]struct{}
	started bool

	PlanCallCount    atomic.Int64
	EnqueueCallCount atomic.Int64
	// DrainGate blocks the writer job until released (T20).
	DrainGate func()
	// AfterJob runs after a job finishes (tests).
	AfterJob func(household string)

	onUsageLearned func(ctx context.Context, household string) error
}

type coalescerHouse struct {
	keys    map[string]struct{}
	cause   Cause
	meta    BurstMeta
	timer   *time.Timer
	running bool
	done    chan struct{}
	waiters []chan struct{}
}

// NewListCoalescer wires the automatic list writer.
func NewListCoalescer(db, readDB *sql.DB, plan PlanSnapshot, store *Store) *ListCoalescer {
	read := readDB
	if read == nil {
		read = db
	}
	c := &ListCoalescer{
		db:       db,
		readDB:   read,
		plan:     plan,
		store:    store,
		debounce: defaultCoalescerDebounce,
		clock:    func() time.Time { return time.Now().UTC() },
		house:    map[string]*coalescerHouse{},
		dirty:    map[string]struct{}{},
	}
	return c
}

// SetClock overrides time for tests (T17 Denver day 30).
func (c *ListCoalescer) SetClock(fn func() time.Time) {
	if c != nil && fn != nil {
		c.clock = fn
	}
}

// SetDebounce overrides the debounce interval (tests).
func (c *ListCoalescer) SetDebounce(d time.Duration) {
	if c != nil && d > 0 {
		c.debounce = d
	}
}

// SetUsageLearnedHook runs on the periodic tick when day-30 eligibility fires.
func (c *ListCoalescer) SetUsageLearnedHook(fn func(ctx context.Context, household string) error) {
	if c != nil {
		c.onUsageLearned = fn
	}
}

// Enqueue schedules one debounced scoped save for the household.
func (c *ListCoalescer) Enqueue(ctx context.Context, scope tenant.Scope, cause Cause, keys []string, meta BurstMeta) error {
	if c == nil {
		return nil
	}
	if err := scope.Require(); err != nil {
		return err
	}
	c.EnqueueCallCount.Add(1)
	household := scope.HouseholdID()
	c.mu.Lock()
	defer c.mu.Unlock()
	h := c.house[household]
	if h == nil {
		h = &coalescerHouse{keys: map[string]struct{}{}}
		c.house[household] = h
	}
	if cause != "" {
		h.cause = cause
	}
	if meta.N > h.meta.N {
		h.meta.N = meta.N
	}
	if meta.Actor != nil {
		h.meta.Actor = meta.Actor
	}
	if meta.Direction != "" {
		h.meta.Direction = meta.Direction
	}
	for _, k := range keys {
		if k != "" {
			h.keys[k] = struct{}{}
		}
	}
	if h.timer != nil {
		h.timer.Stop()
	}
	h.timer = time.AfterFunc(c.debounce, func() {
		c.runHousehold(context.Background(), household)
	})
	return nil
}

// MarkDirty records a household without scheduling a job (tests, startup).
func (c *ListCoalescer) MarkDirty(household string) {
	if c == nil || household == "" {
		return
	}
	c.mu.Lock()
	c.dirty[household] = struct{}{}
	c.mu.Unlock()
}

// StartupCatchup replans dirty households after restart (T18).
func (c *ListCoalescer) StartupCatchup(ctx context.Context) {
	if c == nil {
		return
	}
	c.mu.Lock()
	var ids []string
	for id := range c.dirty {
		ids = append(ids, id)
	}
	c.dirty = map[string]struct{}{}
	c.mu.Unlock()
	for _, id := range ids {
		scope, _ := tenant.New(id)
		_ = c.Enqueue(ctx, scope, CauseListUpdated, nil, BurstMeta{})
	}
}

// Drain waits up to timeout for one pending writer job on the household.
// Returns true when the job finished, false on timeout (list stale on read).
func (c *ListCoalescer) Drain(ctx context.Context, household string, timeout time.Duration) bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	h := c.house[household]
	if h == nil || (!h.running && h.timer == nil) {
		c.mu.Unlock()
		return true
	}
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
		if !h.running {
			h.running = true
			h.done = make(chan struct{})
			go c.runHousehold(ctx, household)
		}
	}
	ch := make(chan struct{})
	h.waiters = append(h.waiters, ch)
	running := h.running
	c.mu.Unlock()
	if !running {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

const denverTZ = "America/Denver"

// UsageLearnedEligible is true on Denver local calendar day 30 since onboarding_started_at.
func UsageLearnedEligible(db *sql.DB, household string, now time.Time) (bool, error) {
	if db == nil || household == "" {
		return false, nil
	}
	var raw string
	err := db.QueryRow(`SELECT value FROM household_settings WHERE household_id = ? AND key = 'onboarding_started_at'`,
		household).Scan(&raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	started, err := parseStoredTime(raw)
	if err != nil {
		return false, err
	}
	loc, err := time.LoadLocation(denverTZ)
	if err != nil {
		return false, err
	}
	startDay := time.Date(started.In(loc).Year(), started.In(loc).Month(), started.In(loc).Day(), 0, 0, 0, 0, loc)
	nowDay := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	days := int(nowDay.Sub(startDay).Hours() / 24)
	return days >= 30, nil
}

// TickUsageLearned runs the day-30 job when UsageLearnedEligible (T17).
func (c *ListCoalescer) TickUsageLearned(ctx context.Context, household string) error {
	if c == nil {
		return nil
	}
	ok, err := UsageLearnedEligible(c.db, household, c.clock())
	if err != nil || !ok {
		return err
	}
	return c.RunUsageLearned(ctx, household)
}

// RunUsageLearned enqueues usage_learned for one household (T17).
func (c *ListCoalescer) RunUsageLearned(ctx context.Context, household string) error {
	if c == nil {
		return nil
	}
	scope, _ := tenant.New(household)
	return c.Enqueue(ctx, scope, CauseUsageLearned, nil, BurstMeta{})
}

func (c *ListCoalescer) runHousehold(ctx context.Context, household string) {
	c.mu.Lock()
	h := c.house[household]
	if h == nil {
		c.mu.Unlock()
		return
	}
	keys := h.keys
	cause := h.cause
	meta := h.meta
	h.keys = map[string]struct{}{}
	h.cause = ""
	h.meta = BurstMeta{}
	h.running = true
	h.done = make(chan struct{})
	c.mu.Unlock()

	if c.DrainGate != nil {
		c.DrainGate()
	}

	c.PlanCallCount.Add(1)
	scope, _ := tenant.New(household)
	now := c.clock()
	if c.plan == nil {
		c.finishHousehold(household, h)
		return
	}
	planned, members, err := c.plan(ctx, scope, now)
	if err != nil {
		c.finishHousehold(household, h)
		return
	}
	tripSeq, err := householdTripSeq(ctx, c.db, household)
	if err != nil {
		c.finishHousehold(household, h)
		return
	}
	before := map[string]int{}
	if err := loadOpenAutoQtyHousehold(ctx, c.db, household, before); err != nil {
		c.finishHousehold(household, h)
		return
	}
	affected := map[string]struct{}{}
	if len(keys) == 0 {
		for _, line := range planned {
			key := lineKeyForPlanned(line)
			affected[key] = struct{}{}
		}
	} else {
		for k := range keys {
			affected[k] = struct{}{}
		}
	}
	if cause == "" {
		if meta.Direction == "put_away" {
			cause = CausePutAway
		} else if meta.Direction == "taken_out" {
			cause = CauseTakenOut
		} else {
			cause = CauseListUpdated
		}
	}
	var actor *string
	if meta.N > 0 && meta.Actor != nil {
		actor = meta.Actor
	}
	params := ScopedSaveParams{
		Lines:        planned,
		Members:      members,
		AffectedKeys: affected,
		Cause:        cause,
		Actor:        actor,
		N:            intPtrOrNil(meta.N),
		TripSeq:      tripSeq,
		Now:          now,
	}
	for key := range affected {
		from := before[key]
		line := plannedByKey(planned, key)
		to := 0
		if line.ItemID != "" {
			to = line.Quantity
		}
		ch := buildScopedChange(key, from, to, cause, actor, meta.N)
		if ch != nil {
			params.Changes = append(params.Changes, *ch)
		}
	}
	_ = c.store.ScopedSavePlannedLines(ctx, scope, params)
	c.finishHousehold(household, h)
	if c.AfterJob != nil {
		c.AfterJob(household)
	}
}

func (c *ListCoalescer) finishHousehold(household string, h *coalescerHouse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h.running = false
	if h.done != nil {
		select {
		case <-h.done:
		default:
			close(h.done)
		}
	}
	waiters := h.waiters
	h.waiters = nil
	for _, ch := range waiters {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func buildScopedChange(key string, from, to int, cause Cause, actor *string, n int) *ScopedChangeInput {
	if from == to && from == 0 {
		return nil
	}
	var effect Effect
	switch {
	case from == 0 && to > 0:
		effect = EffectAdded
	case to == 0 && from > 0:
		effect = EffectTakenOff
	case to > from:
		effect = EffectAmountUp
	case to < from:
		effect = EffectAmountDown
	default:
		return nil
	}
	fromCopy, toCopy := from, to
	ch := ScopedChangeInput{
		Key: key, Effect: effect, Cause: cause,
		FromQty: &fromCopy, ToQty: &toCopy, Actor: actor,
	}
	if n > 0 {
		ch.N = &n
	}
	return &ch
}

func intPtrOrNil(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

func householdTripSeq(ctx context.Context, db *sql.DB, household string) (int, error) {
	var seq int
	err := db.QueryRowContext(ctx, `SELECT trip_seq FROM households WHERE id = ?`, household).Scan(&seq)
	return seq, err
}

func loadOpenAutoQtyHousehold(ctx context.Context, db *sql.DB, household string, out map[string]int) error {
	scope, _ := tenant.New(household)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := loadOpenAutoQty(ctx, scope, tx, out); err != nil {
		return err
	}
	return tx.Commit()
}

func plannedByKey(lines []PlannedLine, key string) PlannedLine {
	for _, line := range lines {
		if lineKeyForPlanned(line) == key {
			return line
		}
	}
	return PlannedLine{}
}

func lineKeyForPlanned(line PlannedLine) string {
	if line.GroupKey != "" {
		return GroupLineKey(line.GroupKey)
	}
	return ItemLineKey(line.ItemID)
}

// CoalescerAfterStockCommit enqueues from scan commit callbacks.
func (c *ListCoalescer) CoalescerAfterStockCommit(ctx context.Context, scope tenant.Scope, productIDs []string, stockOut bool, units int) {
	if c == nil || len(productIDs) == 0 {
		return
	}
	itemIDs := itemIDsForProducts(ctx, c.db, scope, productIDs)
	keys := affectedKeysForItems(ctx, c.db, scope, itemIDs)
	cause := CausePutAway
	direction := "put_away"
	if stockOut {
		cause = CauseTakenOut
		direction = "taken_out"
	}
	meta := BurstMeta{N: units, Direction: direction, Actor: ActorFromContext(ctx)}
	_ = c.Enqueue(ctx, scope, cause, keys, meta)
}

func itemIDsForProducts(ctx context.Context, db *sql.DB, scope tenant.Scope, productIDs []string) []string {
	var out []string
	for _, pid := range productIDs {
		var itemID string
		err := db.QueryRowContext(ctx, `SELECT id FROM items WHERE household_id = ? AND product_id = ? LIMIT 1`,
			scope.HouseholdID(), pid).Scan(&itemID)
		if err == nil && itemID != "" {
			out = append(out, itemID)
		}
	}
	return out
}

func affectedKeysForItems(ctx context.Context, db *sql.DB, scope tenant.Scope, itemIDs []string) []string {
	keys := make([]string, 0, len(itemIDs))
	seen := map[string]struct{}{}
	for _, itemID := range itemIDs {
		key := ItemLineKey(itemID)
		var groupID sql.NullString
		_ = db.QueryRowContext(ctx, `
			SELECT group_id FROM product_group_members
			WHERE household_id = ? AND product_id = (SELECT product_id FROM items WHERE id = ? AND household_id = ?)
			LIMIT 1`, scope.HouseholdID(), itemID, scope.HouseholdID()).Scan(&groupID)
		if groupID.Valid && groupID.String != "" {
			key = GroupLineKey(groupID.String)
		}
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	return keys
}

// ListStaleError is the export/send refusal when the writer cannot catch up.
const ListStaleCode = "list_stale"

// ListStaleSentence is proposed CJ copy for list_stale.
const ListStaleSentence = "The shopping list is still updating. Try again in a moment."

func ListStaleError() error {
	return fmt.Errorf("%s|%s", ListStaleCode, ListStaleSentence)
}