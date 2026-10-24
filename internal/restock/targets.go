package restock

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
)

// Queryer is the subset of *sql.DB and *sql.Tx the loader uses.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Targets stores household restock targets and categories.
type Targets struct {
	db *sql.DB
}

// NewTargets returns the household restock store.
func NewTargets(db *sql.DB) *Targets {
	return &Targets{db: db}
}

// Membership is a category an item belongs to.
type Membership struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ItemView is one item's resolved target, ready for a screen.
type ItemView struct {
	ItemID      string       `json:"itemId"`
	ProductID   string       `json:"productId"`
	Name        string       `json:"name"`
	Unit        string       `json:"unit"`
	GroupID     string       `json:"groupId,omitempty"`
	GroupName   string       `json:"groupName,omitempty"`
	Target      Target       `json:"target"`
	Level       Level        `json:"level"`
	LevelName   string       `json:"levelName,omitempty"`
	SourceLabel string       `json:"sourceLabel"`
	ValueLabel  string       `json:"valueLabel"`
	QuietLine   string       `json:"quietLine,omitempty"`
	RowSource   string       `json:"rowSource"`
	Chain       []ChainCell  `json:"chain"`
	UseAction   string       `json:"useAction,omitempty"`
	UseHint     string       `json:"useHint,omitempty"`
	Categories  []Membership `json:"categories"`
	SetLabel    string       `json:"setLabel"`
	Input       Input        `json:"-"`
}

// CategorySummary is one row of the settings category list.
type CategorySummary struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Items       int     `json:"items"`
	OwnTargets  int     `json:"ownTargets"`
	Target      *Target `json:"target,omitempty"`
	ValueLabel  string  `json:"valueLabel"`
	SourceLabel string  `json:"sourceLabel"`
}

// Snapshot is the household's targets for every screen.
type Snapshot struct {
	Household       Target            `json:"household"`
	HouseholdLabel  string            `json:"householdLabel"`
	AppliesTo       int               `json:"appliesTo"`
	ItemCount       int               `json:"itemCount"`
	Items           []ItemView        `json:"items"`
	Categories      []CategorySummary `json:"categories"`
	NotRestocked    int               `json:"notRestocked"`
	SuggestionCount int               `json:"suggestionCount"`
}

type loadedItem struct {
	view       ItemView
	window     sql.NullInt64
	quantity   sql.NullFloat64
	dimension  string
	categories []CategoryTarget
	memberOf   []Membership
}

// Load reads every item's resolved target for one household.
func Load(ctx context.Context, scope tenant.Scope, q Queryer) (Snapshot, error) {
	if err := scope.Require(); err != nil {
		return Snapshot{}, err
	}
	home, err := readHousehold(ctx, scope, q)
	if err != nil {
		return Snapshot{}, err
	}
	items, err := loadItems(ctx, scope, q)
	if err != nil {
		return Snapshot{}, err
	}
	if err := attachStoredTargets(ctx, scope, q, items); err != nil {
		return Snapshot{}, err
	}
	if err := attachCategories(ctx, scope, q, items); err != nil {
		return Snapshot{}, err
	}

	snap := Snapshot{
		Household:      home,
		HouseholdLabel: ValuePhrase(home, false),
		Items:          []ItemView{},
		Categories:     []CategorySummary{},
	}
	byCategory := map[string]*CategorySummary{}
	applies := 0
	for _, item := range items {
		input := Input{Household: home, Categories: item.categories}
		if item.window.Valid || item.quantity.Valid {
			target := groupTarget(item.window, item.quantity, item.dimension)
			input.Group = &target
			input.GroupName = item.view.GroupName
		}
		if item.view.Input.Item != nil {
			input.Item = item.view.Input.Item
		}
		res := Resolve(input)
		view := item.view
		view.Input = input
		view.Target = res.Target
		view.Level = res.Level
		view.LevelName = res.Name
		view.SourceLabel = SourceLabel(res)
		view.ValueLabel = ValuePhrase(res.Target, false)
		view.QuietLine = QuietLine(res)
		view.RowSource = RowSource(res)
		view.Chain = Chain(input, res, unitForAmount(view.Unit, res))
		if label, hint, ok := UseAction(res); ok && res.Level == LevelItem {
			view.UseAction = label
			view.UseHint = hint
		}
		view.Categories = item.memberOf
		if view.Categories == nil {
			view.Categories = []Membership{}
		}
		view.SetLabel = "Set for this item"
		if res.Level == LevelItem {
			view.SetLabel = "Change"
		}
		if res.Level == LevelHousehold {
			applies++
		}
		if res.Target.Value == 0 {
			snap.NotRestocked++
		}
		snap.Items = append(snap.Items, view)
		for _, member := range item.memberOf {
			summary := byCategory[member.ID]
			if summary == nil {
				summary = &CategorySummary{ID: member.ID, Name: member.Name, SourceLabel: "Household default", ValueLabel: ValuePhrase(home, false)}
				byCategory[member.ID] = summary
			}
			summary.Items++
			if res.Level == LevelItem {
				summary.OwnTargets++
			}
		}
	}
	snap.AppliesTo = applies
	snap.ItemCount = len(snap.Items)

	categories, err := loadCategoryRows(ctx, scope, q)
	if err != nil {
		return Snapshot{}, err
	}
	for _, row := range categories {
		summary := byCategory[row.ID]
		if summary == nil {
			summary = &CategorySummary{ID: row.ID, Name: row.Name, SourceLabel: "Household default", ValueLabel: ValuePhrase(home, false)}
			byCategory[row.ID] = summary
		}
		summary.Name = row.Name
		if row.Target != nil {
			summary.Target = row.Target
			summary.ValueLabel = ValuePhrase(*row.Target, row.Target.Kind == KindAmount)
			summary.SourceLabel = "Set on category"
		}
		snap.Categories = append(snap.Categories, *summary)
	}
	if snap.Categories == nil {
		snap.Categories = []CategorySummary{}
	}
	cards, err := suggest(ctx, scope, q, snap.Items)
	if err != nil {
		return Snapshot{}, err
	}
	for _, card := range cards {
		snap.SuggestionCount += len(card.Items)
	}
	return snap, nil
}

// Snapshot loads the household view. The first read seeds the starter
// categories when the household has none. Deleting them all is how a
// household opts out; the next read seeds them again only while the table
// is empty.
func (t *Targets) Snapshot(ctx context.Context, scope tenant.Scope) (Snapshot, error) {
	if err := scope.Require(); err != nil {
		return Snapshot{}, err
	}
	if err := t.ensureStarters(ctx, scope); err != nil {
		return Snapshot{}, err
	}
	snap, err := Load(ctx, scope, t.db)
	if err != nil {
		return Snapshot{}, err
	}
	sort.SliceStable(snap.Categories, func(i, j int) bool {
		left, right := categoryOrder(snap.Categories[i].Name), categoryOrder(snap.Categories[j].Name)
		if left != right {
			return left < right
		}
		return snap.Categories[i].Name < snap.Categories[j].Name
	})
	return snap, nil
}

func (t *Targets) ensureStarters(ctx context.Context, scope tenant.Scope) error {
	if err := scope.Require(); err != nil {
		return err
	}
	var count int
	if err := t.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM categories WHERE household_id = ?`, scope.HouseholdID()).Scan(&count); err != nil {
		return fmt.Errorf("could not read categories: %w", err)
	}
	if count > 0 {
		return nil
	}
	// One transaction so a canceled request does not leave a partial set of starters.
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, name := range StarterCategories {
		key, err := nameKey(name)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO categories (id, household_id, name, name_key) VALUES (?, ?, ?, ?)`,
			uuid.NewString(), scope.HouseholdID(), name, key)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return nil
			}
			return err
		}
	}
	return tx.Commit()
}

// prepare clamps a whole amount above 999 down to 999, then validates.
// Months above 12 and fractional values are still rejected.
func prepare(target Target) (Target, error) {
	if target.Kind == KindAmount && !math.IsNaN(target.Value) && !math.IsInf(target.Value, 0) &&
		target.Value == math.Trunc(target.Value) && target.Value > 999 {
		target.Value = 999
	}
	if err := Validate(target); err != nil {
		return Target{}, err
	}
	return target, nil
}

// Validate rejects a target the screens cannot store.
func Validate(target Target) error {
	if target.Kind != KindMonths && target.Kind != KindAmount {
		return fmt.Errorf("Choose months of supply or a fixed amount.")
	}
	if math.IsNaN(target.Value) || math.IsInf(target.Value, 0) || target.Value < 0 || target.Value > 999 {
		return fmt.Errorf("The target has to be from 0 to 999.")
	}
	if target.Value != math.Trunc(target.Value) {
		return fmt.Errorf("The target has to be a whole number.")
	}
	if target.Kind == KindMonths && target.Value > 12 {
		return fmt.Errorf("Months of supply have to be from 0 to 12.")
	}
	return nil
}

// SetHousehold stores the default target. The older supply_months key is not
// written; migration 019 copied it into these keys once.
func (t *Targets) SetHousehold(ctx context.Context, scope tenant.Scope, target Target) error {
	if err := scope.Require(); err != nil {
		return err
	}
	target, err := prepare(target)
	if err != nil {
		return err
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not save the household default: %w", err)
	}
	defer tx.Rollback()
	if err := putSetting(ctx, scope, tx, "supply_target_kind", string(target.Kind)); err != nil {
		return err
	}
	if err := putSetting(ctx, scope, tx, "supply_target_value", strconv.FormatFloat(target.Value, 'f', -1, 64)); err != nil {
		return err
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeStock); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not save the household default: %w", err)
	}
	return nil
}

// ItemTarget reads the target stored on the item. Nil means the item does not set one.
func (t *Targets) ItemTarget(ctx context.Context, scope tenant.Scope, itemID string) (*Target, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	var kind string
	var value float64
	err := t.db.QueryRowContext(ctx, `
		SELECT kind, value FROM item_targets WHERE household_id = ? AND item_id = ?`,
		scope.HouseholdID(), itemID).Scan(&kind, &value)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the restock target: %w", err)
	}
	target := Target{Kind: Kind(kind), Value: value}
	return &target, nil
}

// SetItem stores an item target. Zero means don't restock.
func (t *Targets) SetItem(ctx context.Context, scope tenant.Scope, itemID string, target Target) error {
	if err := scope.Require(); err != nil {
		return err
	}
	target, err := prepare(target)
	if err != nil {
		return err
	}
	if err := ensureItem(ctx, scope, t.db, itemID); err != nil {
		return err
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not save the restock target: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO item_targets (household_id, item_id, kind, value)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (household_id, item_id) DO UPDATE SET kind = excluded.kind, value = excluded.value`,
		scope.HouseholdID(), itemID, string(target.Kind), target.Value); err != nil {
		return fmt.Errorf("could not save the restock target: %w", err)
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeStock); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not save the restock target: %w", err)
	}
	return nil
}

// ClearItem removes the item target so a broader one applies.
func (t *Targets) ClearItem(ctx context.Context, scope tenant.Scope, itemID string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not clear the restock target: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_targets WHERE household_id = ? AND item_id = ?`, scope.HouseholdID(), itemID); err != nil {
		return fmt.Errorf("could not clear the restock target: %w", err)
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeStock); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not clear the restock target: %w", err)
	}
	return nil
}

// ItemIDByProduct is the household item for a product. An empty id means
// the product is not on the household's shelf.
func (t *Targets) ItemIDByProduct(ctx context.Context, scope tenant.Scope, productID string) (string, error) {
	if err := scope.Require(); err != nil {
		return "", err
	}
	var id string
	err := t.db.QueryRowContext(ctx, `
		SELECT id FROM items WHERE household_id = ? AND product_id = ?`, scope.HouseholdID(), productID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not read the restock target: %w", err)
	}
	return id, nil
}

// SetCategoryTarget stores or clears a category target. A nil target clears it.
func (t *Targets) SetCategoryTarget(ctx context.Context, scope tenant.Scope, categoryID string, target *Target) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if err := ensureCategory(ctx, scope, t.db, categoryID); err != nil {
		return err
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not save the category target: %w", err)
	}
	defer tx.Rollback()
	if target == nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM category_targets WHERE household_id = ? AND category_id = ?`, scope.HouseholdID(), categoryID); err != nil {
			return fmt.Errorf("could not clear the category target: %w", err)
		}
	} else {
		prepared, err := prepare(*target)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO category_targets (household_id, category_id, kind, value)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (household_id, category_id) DO UPDATE SET kind = excluded.kind, value = excluded.value`,
			scope.HouseholdID(), categoryID, string(prepared.Kind), prepared.Value); err != nil {
			return fmt.Errorf("could not save the category target: %w", err)
		}
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeStock); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not save the category target: %w", err)
	}
	return nil
}

// PreviewCategory shows what setting a category target would change.
func (t *Targets) PreviewCategory(ctx context.Context, scope tenant.Scope, categoryID string, target Target) (Preview, error) {
	if err := scope.Require(); err != nil {
		return Preview{}, err
	}
	target, err := prepare(target)
	if err != nil {
		return Preview{}, err
	}
	name, err := categoryName(ctx, scope, t.db, categoryID)
	if err != nil {
		return Preview{}, err
	}
	snap, err := Load(ctx, scope, t.db)
	if err != nil {
		return Preview{}, err
	}
	var subjects []Subject
	for _, item := range snap.Items {
		if !hasCategory(item, categoryID) {
			continue
		}
		subjects = append(subjects, Subject{
			ItemID: item.ItemID, Name: item.Name, GroupName: item.GroupName, Unit: item.Unit, Input: item.Input,
		})
	}
	return BuildPreview(name, target, subjects, func(in Input) Input {
		return WithCategoryTarget(in, categoryID, name, target)
	}), nil
}

// PreviewGroup shows what setting a group target would change.
func (t *Targets) PreviewGroup(ctx context.Context, scope tenant.Scope, groupID string, target Target) (Preview, error) {
	if err := scope.Require(); err != nil {
		return Preview{}, err
	}
	target, err := prepare(target)
	if err != nil {
		return Preview{}, err
	}
	var name string
	err = t.db.QueryRowContext(ctx, `SELECT name FROM product_groups WHERE id = ? AND household_id = ?`, groupID, scope.HouseholdID()).Scan(&name)
	if err == sql.ErrNoRows {
		return Preview{}, fmt.Errorf("That group isn't in the household.")
	}
	if err != nil {
		return Preview{}, fmt.Errorf("could not read the group: %w", err)
	}
	snap, err := Load(ctx, scope, t.db)
	if err != nil {
		return Preview{}, err
	}
	var subjects []Subject
	for _, item := range snap.Items {
		if item.GroupID != groupID {
			continue
		}
		subjects = append(subjects, Subject{
			ItemID: item.ItemID, Name: item.Name, GroupName: name, Unit: item.Unit, Input: item.Input,
		})
	}
	return BuildPreview(name, target, subjects, func(in Input) Input {
		return WithGroupTarget(in, name, target)
	}), nil
}

// CreateCategory adds a household category.
func (t *Targets) CreateCategory(ctx context.Context, scope tenant.Scope, name string) (CategorySummary, error) {
	if err := scope.Require(); err != nil {
		return CategorySummary{}, err
	}
	key, err := nameKey(name)
	if err != nil {
		return CategorySummary{}, err
	}
	id := uuid.NewString()
	_, err = t.db.ExecContext(ctx, `
		INSERT INTO categories (id, household_id, name, name_key) VALUES (?, ?, ?, ?)`,
		id, scope.HouseholdID(), strings.TrimSpace(name), key)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return CategorySummary{}, fmt.Errorf("A category named %s already exists.", strings.TrimSpace(name))
		}
		return CategorySummary{}, fmt.Errorf("could not add the category: %w", err)
	}
	return CategorySummary{ID: id, Name: strings.TrimSpace(name), SourceLabel: "Household default", ValueLabel: "3 months"}, nil
}

// RenameCategory changes a category's name.
func (t *Targets) RenameCategory(ctx context.Context, scope tenant.Scope, id, name string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	key, err := nameKey(name)
	if err != nil {
		return err
	}
	result, err := t.db.ExecContext(ctx, `
		UPDATE categories SET name = ?, name_key = ? WHERE id = ? AND household_id = ?`,
		strings.TrimSpace(name), key, id, scope.HouseholdID())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("A category named %s already exists.", strings.TrimSpace(name))
		}
		return fmt.Errorf("could not rename the category: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("That category isn't in the household.")
	}
	return nil
}

// DeleteCategory removes a category and its memberships.
func (t *Targets) DeleteCategory(ctx context.Context, scope tenant.Scope, id string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if err := ensureCategory(ctx, scope, t.db, id); err != nil {
		return err
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not delete the category: %w", err)
	}
	defer tx.Rollback()
	for _, query := range []string{
		`DELETE FROM item_categories WHERE household_id = ? AND category_id = ?`,
		`DELETE FROM category_targets WHERE household_id = ? AND category_id = ?`,
		`DELETE FROM categories WHERE household_id = ? AND id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, query, scope.HouseholdID(), id); err != nil {
			return fmt.Errorf("could not delete the category: %w", err)
		}
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeStock); err != nil {
		return err
	}
	return tx.Commit()
}

// MergeCategory moves memberships into another category and deletes the source.
func (t *Targets) MergeCategory(ctx context.Context, scope tenant.Scope, id, intoID string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if id == intoID {
		return fmt.Errorf("Choose a different category to merge into.")
	}
	if err := ensureCategory(ctx, scope, t.db, id); err != nil {
		return err
	}
	if err := ensureCategory(ctx, scope, t.db, intoID); err != nil {
		return err
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not merge the categories: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT item_id, source, confirmed_at, removed_at FROM item_categories
		WHERE household_id = ? AND category_id = ?`, scope.HouseholdID(), id)
	if err != nil {
		return fmt.Errorf("could not merge the categories: %w", err)
	}
	type row struct {
		item, source       string
		confirmed, removed sql.NullTime
	}
	var moved []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.item, &item.source, &item.confirmed, &item.removed); err != nil {
			rows.Close()
			return fmt.Errorf("could not merge the categories: %w", err)
		}
		moved = append(moved, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("could not merge the categories: %w", err)
	}
	rows.Close()
	for _, item := range moved {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO item_categories (household_id, item_id, category_id, source, confirmed_at, removed_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (household_id, item_id, category_id) DO NOTHING`,
			scope.HouseholdID(), item.item, intoID, item.source, nullableTime(item.confirmed), nullableTime(item.removed))
		if err != nil {
			return fmt.Errorf("could not merge the categories: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_categories WHERE household_id = ? AND category_id = ?`, scope.HouseholdID(), id); err != nil {
		return fmt.Errorf("could not merge the categories: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM category_targets WHERE household_id = ? AND category_id = ?`, scope.HouseholdID(), id); err != nil {
		return fmt.Errorf("could not merge the categories: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM categories WHERE household_id = ? AND id = ?`, scope.HouseholdID(), id); err != nil {
		return fmt.Errorf("could not merge the categories: %w", err)
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeStock); err != nil {
		return err
	}
	return tx.Commit()
}

// AddItemCategory puts an item in a category and keeps that choice.
func (t *Targets) AddItemCategory(ctx context.Context, scope tenant.Scope, itemID, categoryID string) error {
	return t.membership(ctx, scope, itemID, categoryID, "user", false)
}

// RemoveItemCategory sticks a removal so a later refresh does not put it back.
func (t *Targets) RemoveItemCategory(ctx context.Context, scope tenant.Scope, itemID, categoryID string) error {
	return t.membership(ctx, scope, itemID, categoryID, "user", true)
}

func (t *Targets) membership(ctx context.Context, scope tenant.Scope, itemID, categoryID, source string, removed bool) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if err := ensureItem(ctx, scope, t.db, itemID); err != nil {
		return err
	}
	if err := ensureCategory(ctx, scope, t.db, categoryID); err != nil {
		return err
	}
	var removedAt any
	if removed {
		removedAt = time.Now().UTC()
	}
	_, err := t.db.ExecContext(ctx, `
		INSERT INTO item_categories (household_id, item_id, category_id, source, confirmed_at, removed_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (household_id, item_id, category_id) DO UPDATE SET
			source = excluded.source,
			confirmed_at = excluded.confirmed_at,
			removed_at = excluded.removed_at`,
		scope.HouseholdID(), itemID, categoryID, source, time.Now().UTC(), removedAt)
	if err != nil {
		return fmt.Errorf("could not update the category: %w", err)
	}
	return nil
}

// SuggestionItem is one checkbox on a suggestion card.
type SuggestionItem struct {
	ItemID  string `json:"itemId"`
	Name    string `json:"name"`
	Reason  string `json:"reason"`
	Checked bool   `json:"checked"`
	Source  string `json:"source"`
}

// SuggestionCard is one category's pending suggestions.
type SuggestionCard struct {
	Category     string           `json:"category"`
	CategoryID   string           `json:"categoryId,omitempty"`
	New          bool             `json:"new"`
	FreshOnly    bool             `json:"freshOnly"`
	Note         string           `json:"note,omitempty"`
	Items        []SuggestionItem `json:"items"`
	NotSuggested []string         `json:"notSuggested,omitempty"`
}

// Suggestions lists category ideas the household has not confirmed.
func (t *Targets) Suggestions(ctx context.Context, scope tenant.Scope) ([]SuggestionCard, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	snap, err := Load(ctx, scope, t.db)
	if err != nil {
		return nil, err
	}
	return suggest(ctx, scope, t.db, snap.Items)
}

// ApplySuggestions adds the chosen items to a category.
func (t *Targets) ApplySuggestions(ctx context.Context, scope tenant.Scope, category string, itemIDs []string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if len(itemIDs) == 0 {
		return fmt.Errorf("Choose at least one item.")
	}
	id, err := t.categoryByName(ctx, scope, category)
	if err != nil {
		return err
	}
	snap, err := Load(ctx, scope, t.db)
	if err != nil {
		return err
	}
	cards, err := suggest(ctx, scope, t.db, snap.Items)
	if err != nil {
		return err
	}
	sourceOf := map[string]string{}
	for _, card := range cards {
		if card.Category != category {
			continue
		}
		for _, item := range card.Items {
			sourceOf[item.ItemID] = item.Source
		}
	}
	for _, itemID := range itemIDs {
		source := sourceOf[itemID]
		if source == "" {
			source = "user"
		}
		if err := t.membership(ctx, scope, itemID, id, source, false); err != nil {
			return err
		}
	}
	return nil
}

// DismissSuggestions hides a card's items until someone adds them by hand.
func (t *Targets) DismissSuggestions(ctx context.Context, scope tenant.Scope, category string, itemIDs []string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	key, err := nameKey(category)
	if err != nil {
		return err
	}
	for _, itemID := range itemIDs {
		if _, err := t.db.ExecContext(ctx, `
			INSERT INTO category_suggestion_dismissals (household_id, item_id, category_key)
			VALUES (?, ?, ?)
			ON CONFLICT (household_id, item_id, category_key) DO NOTHING`,
			scope.HouseholdID(), itemID, key); err != nil {
			return fmt.Errorf("could not dismiss the suggestion: %w", err)
		}
	}
	return nil
}

// SaveOffTags stores raw Open Food Facts tags for a product, dropping en:undefined.
func (t *Targets) SaveOffTags(ctx context.Context, productID string, categories, foodGroups []string) error {
	categories = cleanTags(categories)
	foodGroups = cleanTags(foodGroups)
	catJSON, err := json.Marshal(categories)
	if err != nil {
		return fmt.Errorf("could not save product tags: %w", err)
	}
	groupJSON, err := json.Marshal(foodGroups)
	if err != nil {
		return fmt.Errorf("could not save product tags: %w", err)
	}
	_, err = t.db.ExecContext(ctx, `
		INSERT INTO product_off_tags (product_id, categories_tags, food_groups_tags)
		VALUES (?, ?, ?)
		ON CONFLICT (product_id) DO UPDATE SET
			categories_tags = excluded.categories_tags,
			food_groups_tags = excluded.food_groups_tags`,
		productID, string(catJSON), string(groupJSON))
	if err != nil {
		return fmt.Errorf("could not save product tags: %w", err)
	}
	return nil
}

// CopyLegacy moves supply overrides, fixed item quantities, and don't-restock
// members into item targets. It is safe to run more than once.
func CopyLegacy(ctx context.Context, scope tenant.Scope, q Queryer) error {
	if err := scope.Require(); err != nil {
		return err
	}
	householdID := scope.HouseholdID()
	statements := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO item_targets (household_id, item_id, kind, value)
		 SELECT ?, i.id,
		   CASE WHEN o.window_months IS NOT NULL THEN 'months' ELSE 'amount' END,
		   CASE WHEN o.window_months IS NOT NULL THEN o.window_months ELSE MIN(o.quantity, 999) END
		 FROM supply_overrides o
		 JOIN items i ON i.product_id = o.product_id AND i.household_id = o.household_id
		 WHERE o.household_id = ?
		   AND NOT EXISTS (SELECT 1 FROM product_group_members m WHERE m.household_id = ? AND m.product_id = o.product_id)
		   AND NOT EXISTS (SELECT 1 FROM item_targets t WHERE t.household_id = ? AND t.item_id = i.id)
		   AND (o.window_months IS NOT NULL OR (o.quantity IS NOT NULL AND o.quantity > 0))`,
			args: []any{householdID, householdID, householdID, householdID},
		},
		{
			query: `INSERT INTO item_targets (household_id, item_id, kind, value)
		 SELECT ?, i.id, 'months', 0
		 FROM product_group_members m
		 JOIN items i ON i.product_id = m.product_id AND i.household_id = m.household_id
		 WHERE m.household_id = ? AND m.no_restock = 1
		   AND NOT EXISTS (SELECT 1 FROM item_targets t WHERE t.household_id = ? AND t.item_id = i.id)`,
			args: []any{householdID, householdID, householdID},
		},
		{
			query: `INSERT INTO item_targets (household_id, item_id, kind, value)
		 SELECT ?, i.id, 'amount', MIN(i.target_quantity, 999)
		 FROM items i
		 WHERE i.household_id = ?
		   AND i.target_quantity > 0
		   AND COALESCE(i.replenishment_mode, 'target') = 'target'
		   AND NOT EXISTS (SELECT 1 FROM item_targets t WHERE t.household_id = ? AND t.item_id = i.id)
		   AND NOT EXISTS (SELECT 1 FROM product_group_members m WHERE m.household_id = ? AND m.product_id = i.product_id)
		   AND NOT EXISTS (SELECT 1 FROM supply_overrides o WHERE o.household_id = ? AND o.product_id = i.product_id)`,
			args: []any{householdID, householdID, householdID, householdID, householdID},
		},
	}
	for _, statement := range statements {
		if _, err := q.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("could not move existing restock targets: %w", err)
		}
	}
	return nil
}

func suggest(ctx context.Context, scope tenant.Scope, q Queryer, items []ItemView) ([]SuggestionCard, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	tags, err := loadTags(ctx, q)
	if err != nil {
		return nil, err
	}
	barcodes, err := loadBarcodes(ctx, scope, q)
	if err != nil {
		return nil, err
	}
	dismissed, err := loadDismissals(ctx, scope, q)
	if err != nil {
		return nil, err
	}
	removed, err := loadRemoved(ctx, scope, q)
	if err != nil {
		return nil, err
	}
	existing := map[string]string{}
	rows, err := q.QueryContext(ctx, `SELECT name_key, id, name FROM categories WHERE household_id = ?`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not read categories: %w", err)
	}
	defer rows.Close()
	ids := map[string]string{}
	for rows.Next() {
		var key, id, name string
		if err := rows.Scan(&key, &id, &name); err != nil {
			return nil, fmt.Errorf("could not read categories: %w", err)
		}
		existing[key] = name
		ids[name] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not read categories: %w", err)
	}

	type bucket struct {
		card SuggestionCard
	}
	order := []string{}
	buckets := map[string]*bucket{}
	for _, item := range items {
		tag := tags[item.ProductID]
		matches, blocked := Classify(item.Name, tag.categories, tag.groups, barcodes[item.ProductID])
		member := map[string]bool{}
		for _, category := range item.Categories {
			member[category.Name] = true
		}
		for _, match := range matches {
			key, _ := nameKey(match.Category)
			if member[match.Category] || removed[item.ItemID+"\x00"+match.Category] || dismissed[item.ItemID+"\x00"+key] {
				continue
			}
			b := buckets[match.Category]
			if b == nil {
				b = &bucket{card: SuggestionCard{
					Category:   match.Category,
					CategoryID: ids[match.Category],
					New:        ids[match.Category] == "",
					FreshOnly:  match.Category == "Produce",
					Items:      []SuggestionItem{},
				}}
				if match.Category == "Produce" {
					b.card.Note = "Only items marked fresh count as Produce. Canned, frozen, and dried fruit and vegetables stay out."
				}
				buckets[match.Category] = b
				order = append(order, match.Category)
			}
			b.card.Items = append(b.card.Items, SuggestionItem{
				ItemID: item.ItemID, Name: item.Name, Reason: match.Reason, Checked: match.Checked, Source: match.Source,
			})
		}
		if len(blocked) > 0 {
			b := buckets["Produce"]
			if b == nil {
				b = &bucket{card: SuggestionCard{
					Category: "Produce", CategoryID: ids["Produce"], New: ids["Produce"] == "", FreshOnly: true,
					Note:  "Only items marked fresh count as Produce. Canned, frozen, and dried fruit and vegetables stay out.",
					Items: []SuggestionItem{},
				}}
				buckets["Produce"] = b
				order = append(order, "Produce")
			}
			b.card.NotSuggested = append(b.card.NotSuggested, item.Name)
		}
	}
	cards := []SuggestionCard{}
	for _, name := range order {
		card := buckets[name].card
		if len(card.Items) == 0 && len(card.NotSuggested) == 0 {
			continue
		}
		cards = append(cards, card)
	}
	return cards, nil
}

type tagPair struct {
	categories []string
	groups     []string
}

func loadTags(ctx context.Context, q Queryer) (map[string]tagPair, error) {
	rows, err := q.QueryContext(ctx, `SELECT product_id, categories_tags, food_groups_tags FROM product_off_tags`)
	if err != nil {
		return nil, fmt.Errorf("could not read product tags: %w", err)
	}
	defer rows.Close()
	out := map[string]tagPair{}
	for rows.Next() {
		var id, categories, groups string
		if err := rows.Scan(&id, &categories, &groups); err != nil {
			return nil, fmt.Errorf("could not read product tags: %w", err)
		}
		out[id] = tagPair{categories: decodeTags(categories), groups: decodeTags(groups)}
	}
	return out, rows.Err()
}

func loadBarcodes(ctx context.Context, scope tenant.Scope, q Queryer) (map[string]string, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT product_id, barcode FROM barcodes
		WHERE household_id = ? OR (source = 'global' AND user_id = '')
		ORDER BY product_id, CASE source WHEN 'user_override' THEN 0 ELSE 1 END, barcode`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not read barcodes: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, barcode string
		if err := rows.Scan(&id, &barcode); err != nil {
			return nil, fmt.Errorf("could not read barcodes: %w", err)
		}
		if _, ok := out[id]; !ok {
			out[id] = barcode
		}
	}
	return out, rows.Err()
}

func loadRemoved(ctx context.Context, scope tenant.Scope, q Queryer) (map[string]bool, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT ic.item_id, c.name
		FROM item_categories ic
		JOIN categories c ON c.id = ic.category_id AND c.household_id = ic.household_id
		WHERE ic.household_id = ? AND ic.removed_at IS NOT NULL`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not read categories: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var item, name string
		if err := rows.Scan(&item, &name); err != nil {
			return nil, fmt.Errorf("could not read categories: %w", err)
		}
		out[item+"\x00"+name] = true
	}
	return out, rows.Err()
}

func loadDismissals(ctx context.Context, scope tenant.Scope, q Queryer) (map[string]bool, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT item_id, category_key FROM category_suggestion_dismissals WHERE household_id = ?`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not read dismissed suggestions: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var item, key string
		if err := rows.Scan(&item, &key); err != nil {
			return nil, fmt.Errorf("could not read dismissed suggestions: %w", err)
		}
		out[item+"\x00"+key] = true
	}
	return out, rows.Err()
}

func (t *Targets) categoryByName(ctx context.Context, scope tenant.Scope, name string) (string, error) {
	if err := scope.Require(); err != nil {
		return "", err
	}
	key, err := nameKey(name)
	if err != nil {
		return "", err
	}
	var id string
	err = t.db.QueryRowContext(ctx, `SELECT id FROM categories WHERE household_id = ? AND name_key = ?`, scope.HouseholdID(), key).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("could not read categories: %w", err)
	}
	created, err := t.CreateCategory(ctx, scope, name)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func loadItems(ctx context.Context, scope tenant.Scope, q Queryer) (map[string]*loadedItem, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT i.id, i.product_id, `+product.DisplayNameSQL("p")+`, COALESCE(p.unit_of_measure, ''),
			COALESCE(g.id, ''), COALESCE(g.name, ''),
			g.window_months, g.quantity_base_value, COALESCE(g.quantity_dimension, '')
		FROM items i
		JOIN products p ON p.id = i.product_id
		LEFT JOIN product_group_members m ON m.product_id = i.product_id AND m.household_id = i.household_id
		LEFT JOIN product_groups g ON g.id = m.group_id AND g.household_id = i.household_id
		WHERE i.household_id = ? AND i.shelved = 0
		ORDER BY `+product.DisplayNameSQL("p")+`, i.id`,
		scope.HouseholdID(), scope.HouseholdID(), scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not read restock targets: %w", err)
	}
	defer rows.Close()
	out := map[string]*loadedItem{}
	for rows.Next() {
		var item loadedItem
		var unit string
		if err := rows.Scan(&item.view.ItemID, &item.view.ProductID, &item.view.Name, &unit, &item.view.GroupID, &item.view.GroupName, &item.window, &item.quantity, &item.dimension); err != nil {
			return nil, fmt.Errorf("could not read restock targets: %w", err)
		}
		if unit == "" {
			unit = "units"
		}
		item.view.Unit = unit
		out[item.view.ItemID] = &item
	}
	return out, rows.Err()
}

func attachStoredTargets(ctx context.Context, scope tenant.Scope, q Queryer, items map[string]*loadedItem) error {
	if err := scope.Require(); err != nil {
		return err
	}
	rows, err := q.QueryContext(ctx, `SELECT item_id, kind, value FROM item_targets WHERE household_id = ?`, scope.HouseholdID())
	if err != nil {
		return fmt.Errorf("could not read restock targets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind string
		var value float64
		if err := rows.Scan(&id, &kind, &value); err != nil {
			return fmt.Errorf("could not read restock targets: %w", err)
		}
		item := items[id]
		if item == nil {
			continue
		}
		target := Target{Kind: Kind(kind), Value: value}
		item.view.Input.Item = &target
	}
	return rows.Err()
}

func attachCategories(ctx context.Context, scope tenant.Scope, q Queryer, items map[string]*loadedItem) error {
	if err := scope.Require(); err != nil {
		return err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT ic.item_id, c.id, c.name, ct.kind, ct.value
		FROM item_categories ic
		JOIN categories c ON c.id = ic.category_id AND c.household_id = ic.household_id
		LEFT JOIN category_targets ct ON ct.category_id = c.id AND ct.household_id = c.household_id
		WHERE ic.household_id = ? AND ic.removed_at IS NULL
		ORDER BY c.name`, scope.HouseholdID())
	if err != nil {
		return fmt.Errorf("could not read categories: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var itemID, id, name string
		var kind sql.NullString
		var value sql.NullFloat64
		if err := rows.Scan(&itemID, &id, &name, &kind, &value); err != nil {
			return fmt.Errorf("could not read categories: %w", err)
		}
		item := items[itemID]
		if item == nil {
			continue
		}
		item.memberOf = append(item.memberOf, Membership{ID: id, Name: name})
		if kind.Valid && value.Valid {
			item.categories = append(item.categories, CategoryTarget{ID: id, Name: name, Target: Target{Kind: Kind(kind.String), Value: value.Float64}})
		}
	}
	return rows.Err()
}

type categoryRow struct {
	ID     string
	Name   string
	Target *Target
}

func loadCategoryRows(ctx context.Context, scope tenant.Scope, q Queryer) ([]categoryRow, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT c.id, c.name, ct.kind, ct.value
		FROM categories c
		LEFT JOIN category_targets ct ON ct.category_id = c.id AND ct.household_id = c.household_id
		WHERE c.household_id = ?
		ORDER BY c.name`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not read categories: %w", err)
	}
	defer rows.Close()
	var out []categoryRow
	for rows.Next() {
		var row categoryRow
		var kind sql.NullString
		var value sql.NullFloat64
		if err := rows.Scan(&row.ID, &row.Name, &kind, &value); err != nil {
			return nil, fmt.Errorf("could not read categories: %w", err)
		}
		if kind.Valid && value.Valid {
			target := Target{Kind: Kind(kind.String), Value: value.Float64}
			row.Target = &target
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func readHousehold(ctx context.Context, scope tenant.Scope, q Queryer) (Target, error) {
	if err := scope.Require(); err != nil {
		return Target{}, err
	}
	kind, err := readSetting(ctx, scope, q, "supply_target_kind")
	if err != nil {
		return Target{}, fmt.Errorf("could not read the household default: %w", err)
	}
	value, err := readSetting(ctx, scope, q, "supply_target_value")
	if err != nil {
		return Target{}, fmt.Errorf("could not read the household default: %w", err)
	}
	if kind == string(KindAmount) && value != "" {
		return Target{Kind: KindAmount, Value: parseNumber(value, 0)}, nil
	}
	if kind == string(KindMonths) && value != "" {
		return Target{Kind: KindMonths, Value: parseNumber(value, 3)}, nil
	}
	return Target{Kind: KindMonths, Value: 3}, nil
}

func readSetting(ctx context.Context, scope tenant.Scope, q Queryer, key string) (string, error) {
	if err := scope.Require(); err != nil {
		return "", err
	}
	var value string
	err := q.QueryRowContext(ctx, `
		SELECT value FROM household_settings WHERE household_id = ? AND key = ?`,
		scope.HouseholdID(), key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

func putSetting(ctx context.Context, scope tenant.Scope, q Queryer, key, value string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO household_settings (household_id, key, value) VALUES (?, ?, ?)
		ON CONFLICT (household_id, key) DO UPDATE SET value = excluded.value`,
		scope.HouseholdID(), key, value)
	if err != nil {
		return fmt.Errorf("could not save the household default: %w", err)
	}
	return nil
}

func groupTarget(window sql.NullInt64, quantity sql.NullFloat64, dimension string) Target {
	if window.Valid {
		return Target{Kind: KindMonths, Value: float64(window.Int64)}
	}
	if quantity.Valid {
		if ounces, _, ok := product.DisplayNetSize(quantity.Float64, dimension); ok {
			return Target{Kind: KindAmount, Value: ounces}
		}
		return Target{Kind: KindAmount, Value: quantity.Float64}
	}
	return Target{}
}

func unitForAmount(unit string, res Resolution) string {
	if res.Level == LevelItem && res.Target.Kind == KindAmount && res.Target.Value > 0 {
		return unit
	}
	return ""
}

func hasCategory(item ItemView, id string) bool {
	for _, category := range item.Categories {
		if category.ID == id {
			return true
		}
	}
	return false
}

func ensureItem(ctx context.Context, scope tenant.Scope, q Queryer, itemID string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM items WHERE id = ? AND household_id = ?`, itemID, scope.HouseholdID()).Scan(&id)
	if err == sql.ErrNoRows {
		return fmt.Errorf("That item isn't in the household.")
	}
	if err != nil {
		return fmt.Errorf("could not read the item: %w", err)
	}
	return nil
}

func ensureCategory(ctx context.Context, scope tenant.Scope, q Queryer, id string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	var name string
	err := q.QueryRowContext(ctx, `SELECT name FROM categories WHERE id = ? AND household_id = ?`, id, scope.HouseholdID()).Scan(&name)
	if err == sql.ErrNoRows {
		return fmt.Errorf("That category isn't in the household.")
	}
	if err != nil {
		return fmt.Errorf("could not read the category: %w", err)
	}
	return nil
}

func categoryName(ctx context.Context, scope tenant.Scope, q Queryer, id string) (string, error) {
	if err := scope.Require(); err != nil {
		return "", err
	}
	var name string
	err := q.QueryRowContext(ctx, `SELECT name FROM categories WHERE id = ? AND household_id = ?`, id, scope.HouseholdID()).Scan(&name)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("That category isn't in the household.")
	}
	if err != nil {
		return "", fmt.Errorf("could not read the category: %w", err)
	}
	return name, nil
}

// DeleteItem removes an item and its restock target in one transaction.
// The database trigger that used to do this is gone.
func DeleteItem(ctx context.Context, scope tenant.Scope, db *sql.DB, itemID string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not delete the item: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_targets WHERE household_id = ? AND item_id = ?`, scope.HouseholdID(), itemID); err != nil {
		return fmt.Errorf("could not delete the item: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE household_id = ? AND id = ?`, scope.HouseholdID(), itemID); err != nil {
		return fmt.Errorf("could not delete the item: %w", err)
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeStock); err != nil {
		return fmt.Errorf("could not delete the item: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not delete the item: %w", err)
	}
	return nil
}

func nameKey(name string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return "", fmt.Errorf("The category needs a name.")
	}
	return key, nil
}

func nullableTime(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time
}

func decodeTags(raw string) []string {
	if raw == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil
	}
	return tags
}

func parseNumber(raw string, fallback float64) float64 {
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return n
}
