package shopping

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ScopedChangeInput is one line change to record with a scoped save.
type ScopedChangeInput struct {
	Key     string
	Effect  Effect
	Reason  TakenOffReason
	Cause   Cause
	FromQty *int
	ToQty   *int
	N       *int
	Actor   *string
}

// ScopedSaveParams configures one scoped plan write.
type ScopedSaveParams struct {
	Lines         []PlannedLine
	Members       []ShelfMember
	AffectedKeys  map[string]struct{}
	Changes       []ScopedChangeInput
	Cause         Cause
	Actor         *string
	N             *int
	TripSeq       int
	Now           time.Time
}

// DeleteUnplannedAutoCalls counts deleteUnplannedAuto invocations in tests.
var DeleteUnplannedAutoCalls int

// ScopedSavePlannedLines upserts only affected auto lines; never calls deleteUnplannedAuto.
func (s *Store) ScopedSavePlannedLines(ctx context.Context, scope tenant.Scope, params ScopedSaveParams) error {
	if err := scope.Require(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not begin scoped list update: %w", err)
	}
	defer tx.Rollback()

	membersOf := map[string]map[string]struct{}{}
	for _, member := range params.Members {
		if member.GroupKey == "" {
			continue
		}
		if membersOf[member.GroupKey] == nil {
			membersOf[member.GroupKey] = map[string]struct{}{}
		}
		membersOf[member.GroupKey][member.ItemID] = struct{}{}
	}

	touched, touchedGroups, err := listTouchedAutoItems(ctx, scope, tx)
	if err != nil {
		return err
	}
	skipped, skippedGroups, err := listSkippedItems(ctx, scope, tx)
	if err != nil {
		return err
	}
	groupOf := map[string]string{}
	for _, member := range params.Members {
		if member.GroupKey == "" {
			continue
		}
		groupOf[member.ItemID] = member.GroupKey
	}

	plannedByKey := map[string]PlannedLine{}
	for _, line := range params.Lines {
		if line.Manual {
			continue
		}
		key := ItemLineKey(line.ItemID)
		if line.GroupKey != "" {
			key = GroupLineKey(line.GroupKey)
		}
		if _, ok := params.AffectedKeys[key]; !ok {
			continue
		}
		plannedByKey[key] = line
	}

	for key := range params.AffectedKeys {
		line, ok := plannedByKey[key]
		buy := 0
		if ok {
			buy = line.Quantity
		}
		if stringsHasPrefix(key, "group:") {
			if err := s.scopedUpsertGroup(ctx, scope, tx, key, line, buy, touched, touchedGroups, skipped, skippedGroups, membersOf); err != nil {
				return err
			}
			continue
		}
		itemID := key[len("item:"):]
		if buy < 1 {
			// Stock-in can satisfy par before the shopper checks the line off.
			// Keep the open row until fill or purchase finishes the bought-line
			// lifecycle; still record taken_off in list_line_changes.
			if params.Cause != CausePutAway {
				if err := deleteScopedAutoForKey(ctx, scope, tx, itemID, touched); err != nil {
					return err
				}
			}
			continue
		}
		if _, held := touched[itemID]; held {
			continue
		}
		if groupSkipped(skipped, skippedGroups, groupOf[itemID], itemID, membersOf) {
			continue
		}
		if err := upsertAutoLine(ctx, scope, tx, itemID, line.GroupKey, line.Quantity, line.Note); err != nil {
			return err
		}
	}

	for _, ch := range params.Changes {
		lc := LineChange{
			LineKey:     ch.Key,
			Effect:      ch.Effect,
			Reason:      ch.Reason,
			Cause:       ch.Cause,
			FromQty:     ch.FromQty,
			ToQty:       ch.ToQty,
			N:           ch.N,
			ActorUserID: ch.Actor,
			At:          params.Now,
			TripSeq:     params.TripSeq,
		}
		if lc.Cause == "" {
			lc.Cause = params.Cause
		}
		if lc.ActorUserID == nil {
			lc.ActorUserID = params.Actor
		}
		if lc.N == nil {
			lc.N = params.N
		}
		if err := upsertLineChange(ctx, scope, tx, lc); err != nil {
			return err
		}
	}

	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeList); err != nil {
		return fmt.Errorf("could not save scoped list: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not save scoped list: %w", err)
	}
	s.noteList(scope.HouseholdID())
	return nil
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func (s *Store) scopedUpsertGroup(ctx context.Context, scope tenant.Scope, tx *sql.Tx, key string, line PlannedLine, buy int, touched map[string]struct{}, touchedGroups map[string]struct{}, skipped map[string]struct{}, skippedGroups map[string]struct{}, membersOf map[string]map[string]struct{}) error {
	groupID := key[len("group:"):]
	if _, held := touchedGroups[groupID]; held {
		return nil
	}
	if buy < 1 {
		return deleteScopedGroupAuto(ctx, scope, tx, groupID, touched)
	}
	if groupSkipped(skipped, skippedGroups, groupID, "", membersOf) {
		return nil
	}
	return upsertAutoLine(ctx, scope, tx, line.ItemID, groupID, line.Quantity, line.Note)
}

func deleteScopedAutoForKey(ctx context.Context, scope tenant.Scope, tx *sql.Tx, itemID string, touched map[string]struct{}) error {
	if _, held := touched[itemID]; held {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM shopping_list_items
		WHERE household_id = ? AND item_id = ? AND source = 'auto' AND purchased_at IS NULL AND touched = 0`,
		scope.HouseholdID(), itemID)
	if err != nil {
		return fmt.Errorf("could not clear unpurchased line: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("could not read line id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := deleteShoppingRow(ctx, scope, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func deleteScopedGroupAuto(ctx context.Context, scope tenant.Scope, tx *sql.Tx, groupID string, touched map[string]struct{}) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, item_id FROM shopping_list_items
		WHERE household_id = ? AND group_id = ? AND source = 'auto' AND purchased_at IS NULL AND touched = 0`,
		scope.HouseholdID(), groupID)
	if err != nil {
		return fmt.Errorf("could not clear group line: %w", err)
	}
	defer rows.Close()
	type row struct{ id, itemID string }
	var candidates []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.itemID); err != nil {
			return err
		}
		if _, held := touched[r.itemID]; held {
			continue
		}
		candidates = append(candidates, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range candidates {
		if err := deleteShoppingRow(ctx, scope, tx, r.id); err != nil {
			return err
		}
	}
	return nil
}

// SavePlannedLinesWithChanges is a full fill that also writes list_updated changes.
func (s *Store) SavePlannedLinesWithChanges(ctx context.Context, scope tenant.Scope, lines []PlannedLine, members []ShelfMember, cause Cause, actor *string, tripSeq int, now time.Time) error {
	if err := scope.Require(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not begin list update: %w", err)
	}
	defer tx.Rollback()

	membersOf := map[string]map[string]struct{}{}
	for _, member := range members {
		if member.GroupKey == "" {
			continue
		}
		if membersOf[member.GroupKey] == nil {
			membersOf[member.GroupKey] = map[string]struct{}{}
		}
		membersOf[member.GroupKey][member.ItemID] = struct{}{}
	}

	touched, touchedGroups, err := listTouchedAutoItems(ctx, scope, tx)
	if err != nil {
		return err
	}
	skipped, skippedGroups, err := listSkippedItems(ctx, scope, tx)
	if err != nil {
		return err
	}
	groupOf := map[string]string{}
	for _, member := range members {
		if member.GroupKey == "" {
			continue
		}
		groupOf[member.ItemID] = member.GroupKey
	}
	for itemID := range touched {
		if key := groupOf[itemID]; key != "" {
			touchedGroups[key] = struct{}{}
		}
	}

	beforeQty := map[string]int{}
	if err := loadOpenAutoQty(ctx, scope, tx, beforeQty); err != nil {
		return err
	}

	keep := map[string]struct{}{}
	for _, line := range lines {
		if line.Manual || line.Quantity < 1 || line.ItemID == "" {
			continue
		}
		itemID := line.ItemID
		key := line.GroupKey
		if key == "" {
			key = groupOf[itemID]
		}
		if groupSkipped(skipped, skippedGroups, key, itemID, membersOf) {
			continue
		}
		if _, held := touched[itemID]; held {
			continue
		}
		if key != "" {
			if _, held := touchedGroups[key]; held {
				continue
			}
		}
		if err := upsertAutoLine(ctx, scope, tx, itemID, line.GroupKey, line.Quantity, line.Note); err != nil {
			return err
		}
		keep[itemID] = struct{}{}
	}
	DeleteUnplannedAutoCalls++
	if err := deleteUnplannedAuto(ctx, scope, tx, keep); err != nil {
		return err
	}

	afterQty := map[string]int{}
	if err := loadOpenAutoQty(ctx, scope, tx, afterQty); err != nil {
		return err
	}
	keys := map[string]struct{}{}
	for k := range beforeQty {
		keys[k] = struct{}{}
	}
	for k := range afterQty {
		keys[k] = struct{}{}
	}
	for key := range keys {
		from := beforeQty[key]
		to := afterQty[key]
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
			continue
		}
		fromCopy, toCopy := from, to
		ch := LineChange{
			LineKey: key,
			Effect:  effect,
			Cause:   cause,
			FromQty: &fromCopy,
			ToQty:   &toCopy,
			At:      now,
			TripSeq: tripSeq,
		}
		if ch.ActorUserID == nil {
			ch.ActorUserID = actor
		}
		if err := upsertLineChange(ctx, scope, tx, ch); err != nil {
			return err
		}
	}

	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeList); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.noteList(scope.HouseholdID())
	return nil
}

func loadOpenAutoQty(ctx context.Context, scope tenant.Scope, tx *sql.Tx, out map[string]int) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT item_id, COALESCE(group_id,''), quantity FROM shopping_list_items
		WHERE household_id = ? AND source = 'auto' AND purchased_at IS NULL`,
		scope.HouseholdID())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var itemID, groupID string
		var qty int
		if err := rows.Scan(&itemID, &groupID, &qty); err != nil {
			return err
		}
		key := ItemLineKey(itemID)
		if groupID != "" {
			key = GroupLineKey(groupID)
		}
		out[key] = qty
	}
	return rows.Err()
}