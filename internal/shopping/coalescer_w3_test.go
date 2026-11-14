package shopping_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// TestW3T1CoalescerOnePlanPerBurst — T1: debounced burst runs Plan once.
func TestW3T1CoalescerOnePlanPerBurst(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	var planCalls atomic.Int64
	coalescer := shopping.NewListCoalescer(deps.db, deps.db, func(context.Context, tenant.Scope, time.Time) ([]shopping.PlannedLine, []shopping.ShelfMember, error) {
		planCalls.Add(1)
		return nil, nil, nil
	}, deps.shopping)
	coalescer.SetDebounce(5 * time.Millisecond)

	keys := make([]string, 12)
	for i := range keys {
		keys[i] = shopping.ItemLineKey(fmt.Sprintf("item-571-%02d", i+1))
	}
	for _, k := range keys {
		if err := coalescer.Enqueue(ctx, scope, shopping.CauseTakenOut, []string{k}, shopping.BurstMeta{N: 1, Direction: "taken_out"}); err != nil {
			t.Fatal(err)
		}
	}
	if !coalescer.Drain(ctx, scope.HouseholdID(), 2*time.Second) {
		t.Fatal("drain timed out")
	}
	if got := planCalls.Load(); got != 1 {
		t.Fatalf("plan calls = %d, want 1", got)
	}
	if got := coalescer.PlanCallCount.Load(); got != 1 {
		t.Fatalf("PlanCallCount = %d, want 1", got)
	}
}

// TestW3T2CoalescerPutAwayBurst — T2: large put-away burst still one Plan.
func TestW3T2CoalescerPutAwayBurst(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	coalescer := shopping.NewListCoalescer(deps.db, deps.db, emptyPlan(), deps.shopping)
	coalescer.SetDebounce(5 * time.Millisecond)
	for i := 0; i < 30; i++ {
		key := shopping.ItemLineKey("item-571-p" + string(rune('a'+i%26)))
		_ = coalescer.Enqueue(ctx, scope, shopping.CausePutAway, []string{key}, shopping.BurstMeta{N: 1, Direction: "put_away"})
	}
	if !coalescer.Drain(ctx, scope.HouseholdID(), 2*time.Second) {
		t.Fatal("drain timed out")
	}
	if got := coalescer.PlanCallCount.Load(); got != 1 {
		t.Fatalf("PlanCallCount = %d, want 1", got)
	}
}

// TestW3T3ScopedSaveSkipsDeleteUnplannedAuto — T3: scoped save never calls deleteUnplannedAuto.
func TestW3T3ScopedSaveSkipsDeleteUnplannedAuto(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	itemA := createTestItem(t, deps, ctx, "prod-571-a", "571 A")
	itemUnrelated := createTestItem(t, deps, ctx, "prod-571-u", "571 Unrelated")
	keyA := shopping.ItemLineKey(itemA)
	mustExecShopping(t, deps.db, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, note, created_at)
		VALUES ('unrelated-571', ?, ?, ?, 1, 'auto', '', datetime('now'))`, scope.HouseholdID(), scope.HouseholdID(), itemUnrelated)
	zero, one := 0, 1
	if err := deps.shopping.ScopedSavePlannedLines(ctx, scope, shopping.ScopedSaveParams{
		Lines:        []shopping.PlannedLine{{ItemID: itemA, Quantity: 1}},
		AffectedKeys: map[string]struct{}{keyA: {}},
		Cause:        shopping.CauseTakenOut,
		Changes: []shopping.ScopedChangeInput{{
			Key: keyA, Effect: shopping.EffectAdded, Cause: shopping.CauseTakenOut, FromQty: &zero, ToQty: &one,
		}},
		Now: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	var unrelatedCount int
	if err := deps.db.QueryRow(`SELECT COUNT(*) FROM shopping_list_items WHERE item_id = ?`, itemUnrelated).Scan(&unrelatedCount); err != nil {
		t.Fatal(err)
	}
	if unrelatedCount != 1 {
		t.Fatalf("unrelated count = %d", unrelatedCount)
	}
	if shopping.DeleteUnplannedAutoCalls != 0 {
		t.Fatalf("deleteUnplannedAuto calls = %d", shopping.DeleteUnplannedAutoCalls)
	}
	var changeCount int
	if err := deps.db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&changeCount); err != nil {
		t.Fatal(err)
	}
	if changeCount != 1 {
		t.Fatalf("list_line_changes count = %d, want 1", changeCount)
	}
	var onlyKey string
	if err := deps.db.QueryRow(`SELECT line_key FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&onlyKey); err != nil {
		t.Fatal(err)
	}
	if onlyKey != keyA {
		t.Fatalf("line_key %q want %q", onlyKey, keyA)
	}
}

// TestW3T6BurstMetaUsesMaxN — T6: coalesced burst keeps larger n.
func TestW3T6BurstMetaUsesMaxN(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	itemX := createTestItem(t, deps, ctx, "prod-571-x", "X")
	coalescer := shopping.NewListCoalescer(deps.db, deps.db, func(_ context.Context, _ tenant.Scope, _ time.Time) ([]shopping.PlannedLine, []shopping.ShelfMember, error) {
		return []shopping.PlannedLine{{ItemID: itemX, Quantity: 2}}, nil, nil
	}, deps.shopping)
	coalescer.SetDebounce(5 * time.Millisecond)
	_ = coalescer.Enqueue(ctx, scope, shopping.CauseTakenOut, []string{shopping.ItemLineKey(itemX)}, shopping.BurstMeta{N: 1, Direction: "taken_out"})
	_ = coalescer.Enqueue(ctx, scope, shopping.CauseTakenOut, []string{shopping.ItemLineKey(itemX)}, shopping.BurstMeta{N: 3, Direction: "taken_out"})
	if !coalescer.Drain(ctx, scope.HouseholdID(), 2*time.Second) {
		t.Fatal("drain")
	}
	rows, err := deps.shopping.ListLineChanges(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := rows[shopping.ItemLineKey(itemX)]
	if !ok || row.N == nil || *row.N != 3 {
		t.Fatalf("expected n=3 on change row, got %#v", rows)
	}
}

// TestW3T18StartupCatchupListUpdated — T18: StartupCatchup uses list_updated with null actor.
func TestW3T18StartupCatchupListUpdated(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	itemID := createTestItem(t, deps, ctx, "prod-571-catchup", "Catchup")
	coalescer := shopping.NewListCoalescer(deps.db, deps.db, func(_ context.Context, _ tenant.Scope, _ time.Time) ([]shopping.PlannedLine, []shopping.ShelfMember, error) {
		return []shopping.PlannedLine{{ItemID: itemID, Quantity: 1}}, nil, nil
	}, deps.shopping)
	coalescer.SetDebounce(5 * time.Millisecond)
	coalescer.MarkDirty(scope.HouseholdID())
	coalescer.StartupCatchup(ctx)
	if !coalescer.Drain(ctx, scope.HouseholdID(), 2*time.Second) {
		t.Fatal("drain")
	}
	if coalescer.PlanCallCount.Load() < 1 {
		t.Fatal("catchup did not run plan")
	}
	rows, err := deps.shopping.ListLineChanges(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range rows {
		if row.Cause != shopping.CauseListUpdated {
			continue
		}
		if row.ActorUserID != nil {
			t.Fatalf("list_updated should have null actor, got %#v", row)
		}
		found = true
		break
	}
	if !found {
		t.Fatal("no list_updated row with null actor after drain")
	}
}

// TestW3CoalescerCompletesAfterDrainTimeout — short Drain times out; later Drain finishes the job.
func TestW3CoalescerCompletesAfterDrainTimeout(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	itemID := createTestItem(t, deps, ctx, "prod-drain-w3", "Drain W3")
	key := shopping.ItemLineKey(itemID)
	coalescer := shopping.NewListCoalescer(deps.db, deps.db, func(_ context.Context, _ tenant.Scope, _ time.Time) ([]shopping.PlannedLine, []shopping.ShelfMember, error) {
		return []shopping.PlannedLine{{ItemID: itemID, Quantity: 3}}, nil, nil
	}, deps.shopping)
	coalescer.SetDebounce(time.Millisecond)
	coalescer.DrainGate = func() { time.Sleep(300 * time.Millisecond) }
	if err := coalescer.Enqueue(ctx, scope, shopping.CauseTakenOut, []string{key}, shopping.BurstMeta{N: 1, Direction: "taken_out"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if coalescer.Drain(ctx, scope.HouseholdID(), 50*time.Millisecond) {
		t.Fatal("expected first drain to time out")
	}
	if !coalescer.Drain(ctx, scope.HouseholdID(), 2*time.Second) {
		t.Fatal("expected second drain to complete")
	}
	var qty int
	if err := deps.db.QueryRow(`SELECT quantity FROM shopping_list_items WHERE household_id = ? AND item_id = ? AND source = 'auto'`,
		scope.HouseholdID(), itemID).Scan(&qty); err != nil || qty != 3 {
		t.Fatalf("auto line qty %d err %v", qty, err)
	}
	rows, err := deps.shopping.ListLineChanges(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows[key]; !ok {
		t.Fatalf("expected line change for %q", key)
	}
}

// TestW3T29PlanBasisOnlyBumpsListVersion — T29: version bumps when plan writes run.
func TestW3T29PlanBasisOnlyBumpsListVersion(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	v0, err := changes.Version(ctx, deps.db, scope.HouseholdID(), changes.ScopeList)
	if err != nil {
		t.Fatal(err)
	}
	itemID := createTestItem(t, deps, ctx, "prod-571-pb", "PB item")
	key := shopping.ItemLineKey(itemID)
	coalescer := shopping.NewListCoalescer(deps.db, deps.db, func(_ context.Context, _ tenant.Scope, _ time.Time) ([]shopping.PlannedLine, []shopping.ShelfMember, error) {
		return []shopping.PlannedLine{{ItemID: itemID, Quantity: 1}}, nil, nil
	}, deps.shopping)
	coalescer.SetDebounce(time.Millisecond)
	_ = coalescer.Enqueue(ctx, scope, shopping.CauseListUpdated, []string{key}, shopping.BurstMeta{})
	_ = coalescer.Drain(ctx, scope.HouseholdID(), time.Second)
	v1, err := changes.Version(ctx, deps.db, scope.HouseholdID(), changes.ScopeList)
	if err != nil {
		t.Fatal(err)
	}
	if v1 != v0+1 {
		t.Fatalf("version %d -> %d, want +1", v0, v1)
	}
}

func emptyPlan() shopping.PlanSnapshot {
	return func(context.Context, tenant.Scope, time.Time) ([]shopping.PlannedLine, []shopping.ShelfMember, error) {
		return nil, nil, nil
	}
}

func TestUsageLearnedEligibleAndRun(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, loc)
	if _, err := deps.db.Exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, 'onboarding_started_at', ?)`,
		scope.HouseholdID(), start.UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	coalescer := shopping.NewListCoalescer(deps.db, deps.db, emptyPlan(), deps.shopping)
	day29 := start.AddDate(0, 0, 29)
	coalescer.SetClock(func() time.Time { return day29 })
	ok, err := shopping.UsageLearnedEligible(deps.db, scope.HouseholdID(), day29)
	if err != nil || ok {
		t.Fatalf("day29 eligible=%v err=%v", ok, err)
	}
	day30 := start.AddDate(0, 0, 30)
	coalescer.SetClock(func() time.Time { return day30 })
	if err := coalescer.TickUsageLearned(context.Background(), scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	if coalescer.EnqueueCallCount.Load() < 1 {
		t.Fatal("usage learned did not enqueue")
	}
}

func TestCoalescerMarkDirtyStartupCatchup(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	var ran atomic.Int64
	coalescer := shopping.NewListCoalescer(deps.db, deps.db, func(context.Context, tenant.Scope, time.Time) ([]shopping.PlannedLine, []shopping.ShelfMember, error) {
		ran.Add(1)
		return nil, nil, nil
	}, deps.shopping)
	coalescer.MarkDirty(scope.HouseholdID())
	coalescer.SetDebounce(time.Millisecond)
	coalescer.StartupCatchup(context.Background())
	if !coalescer.Drain(context.Background(), scope.HouseholdID(), 2*time.Second) {
		t.Fatal("drain failed")
	}
	if ran.Load() < 1 {
		t.Fatal("startup catchup did not plan")
	}
}

func TestCoalescerWritesAllEffectKinds(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	itemA := createTestItem(t, deps, ctx, "prod-eff-a", "Eff A")
	itemB := createTestItem(t, deps, ctx, "prod-eff-b", "Eff B")
	keyA := shopping.ItemLineKey(itemA)
	keyB := shopping.ItemLineKey(itemB)
	plan := func(qtyA, qtyB int) shopping.PlanSnapshot {
		return func(context.Context, tenant.Scope, time.Time) ([]shopping.PlannedLine, []shopping.ShelfMember, error) {
			return []shopping.PlannedLine{
				{ItemID: itemA, Quantity: qtyA},
				{ItemID: itemB, Quantity: qtyB},
			}, nil, nil
		}
	}
	coalescer := shopping.NewListCoalescer(deps.db, deps.db, plan(2, 0), deps.shopping)
	coalescer.SetDebounce(time.Millisecond)
	_ = coalescer.Enqueue(ctx, scope, shopping.CauseTakenOut, []string{keyA, keyB}, shopping.BurstMeta{N: 2, Direction: "taken_out"})
	_ = coalescer.Drain(ctx, scope.HouseholdID(), 2*time.Second)
	var effect string
	if err := deps.db.QueryRow(`SELECT effect FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), keyA).Scan(&effect); err != nil || effect != "added" {
		t.Fatalf("added row %q err %v", effect, err)
	}
	coalescer = shopping.NewListCoalescer(deps.db, deps.db, plan(5, 0), deps.shopping)
	coalescer.SetDebounce(time.Millisecond)
	_ = coalescer.Enqueue(ctx, scope, shopping.CauseTakenOut, []string{keyA}, shopping.BurstMeta{N: 1, Direction: "taken_out"})
	_ = coalescer.Drain(ctx, scope.HouseholdID(), 2*time.Second)
	if err := deps.db.QueryRow(`SELECT effect FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), keyA).Scan(&effect); err != nil || effect != "amount_up" {
		t.Fatalf("amount_up %q", effect)
	}
	coalescer = shopping.NewListCoalescer(deps.db, deps.db, plan(0, 0), deps.shopping)
	coalescer.SetDebounce(time.Millisecond)
	_ = coalescer.Enqueue(ctx, scope, shopping.CauseTargetChanged, []string{keyA}, shopping.BurstMeta{})
	_ = coalescer.Drain(ctx, scope.HouseholdID(), 2*time.Second)
	if err := deps.db.QueryRow(`SELECT effect FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), keyA).Scan(&effect); err != nil || effect != "taken_off" {
		t.Fatalf("taken_off %q", effect)
	}
}

func TestCoalescerPlanErrorStillDrains(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	coalescer := shopping.NewListCoalescer(deps.db, deps.db, func(context.Context, tenant.Scope, time.Time) ([]shopping.PlannedLine, []shopping.ShelfMember, error) {
		return nil, nil, fmt.Errorf("plan failed")
	}, deps.shopping)
	coalescer.SetDebounce(time.Millisecond)
	_ = coalescer.Enqueue(context.Background(), scope, shopping.CauseListUpdated, nil, shopping.BurstMeta{})
	if !coalescer.Drain(context.Background(), scope.HouseholdID(), 2*time.Second) {
		t.Fatal("drain should finish after plan error")
	}
	var count int
	if err := deps.db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("plan error should not write changes, got %d", count)
	}
}

func mustExecShopping(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}