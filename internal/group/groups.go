// Package group stores product groups and picks which member to buy.
package group

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/google/uuid"
	"modernc.org/sqlite"
)

const (
	householdUser = "user-1"
	settingRule   = "default_group_rule"

	codeInvalid  = "invalid"
	codeMissing  = "missing"
	codeConflict = "conflict"
	codeTarget   = "target"

	targetRequiredMessage = "Choose what this group should keep on hand. These products already have their own supply setting."

	// SQLITE_CONSTRAINT_UNIQUE. Two creates can both pass the name check,
	// and the losing insert of the name hits this code.
	sqliteConstraintUnique = 2067
)

// Error is a problem the household can act on.
type Error struct {
	Code    string
	Msg     string
	Members []OverrideNotice
}

func (e *Error) Error() string { return e.Msg }

// OverrideNotice is one product that already has its own supply setting.
// Quantity is how many units that setting keeps on hand.
// Amount is that count in ounces or fluid ounces, and Dimension is mass or
// volume, only when the unit size is known. A count with no size is not an
// amount a group target can store.
type OverrideNotice struct {
	ProductID    string   `json:"productId"`
	Name         string   `json:"name"`
	WindowMonths *int     `json:"windowMonths,omitempty"`
	Quantity     *int     `json:"quantity,omitempty"`
	Amount       *float64 `json:"amount,omitempty"`
	Dimension    string   `json:"dimension,omitempty"`
}

// TargetInput is a group target from a request.
// A nil pointer means the caller did not send a target.
// Clear inherits the account window. Quantity is ounces or fluid ounces.
type TargetInput struct {
	Clear     bool
	Window    *int
	Quantity  *float64
	Dimension string
}

// Group is one named set of products.
type Group struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Rule            string   `json:"rule"`
	RuleConfirmed   bool     `json:"ruleConfirmed"`
	PinnedProductID string   `json:"pinnedProductId,omitempty"`
	WindowMonths    *int     `json:"windowMonths,omitempty"`
	Quantity        *float64 `json:"quantity,omitempty"`
	Dimension       string   `json:"dimension,omitempty"`
	Members         []Member `json:"members"`
	RunningLow      bool     `json:"runningLow"`

	hasQuantity  bool
	quantityBase float64
}

// BuyTarget converts the stored target. accountMonths is used when the group has none.
func (g Group) BuyTarget(accountMonths int) BuyTarget {
	if g.hasQuantity {
		return BuyTarget{HasQuantity: true, Base: g.quantityBase, Dimension: g.Dimension}
	}
	months := accountMonths
	if g.WindowMonths != nil {
		months = *g.WindowMonths
	}
	return BuyTarget{WindowMonths: months}
}

// IsLow reports whether this group would buy something today.
// A month window with no measured rate is not low.
func (g Group) IsLow(accountMonths int) bool {
	if len(g.Members) == 0 {
		return false
	}
	picked, err := Pick(Kind(g.Rule), Input{Members: g.Members, PinnedProductID: g.PinnedProductID})
	if err != nil || picked.ProductID == "" {
		return false
	}
	buy, _ := BuyCount(g.BuyTarget(accountMonths), g.Members, picked.ProductID, UsageRate{})
	return buy > 0
}

// Suggestion is one stored inbox card. Generation is separate.
type Suggestion struct {
	ID              string             `json:"id"`
	Kind            string             `json:"kind"`
	Title           string             `json:"title"`
	ProposedRule    string             `json:"proposedRule,omitempty"`
	PinnedProductID string             `json:"pinnedProductId,omitempty"`
	ExistingGroupID string             `json:"existingGroupId,omitempty"`
	Status          string             `json:"status"`
	Members         []SuggestionMember `json:"members"`
}

// SuggestionMember is one product on a card.
// Picture, brand, size, and barcode are copied from the product we already
// have. Empty fields stay empty so the page does not invent them.
type SuggestionMember struct {
	ProductID     string   `json:"productId"`
	Name          string   `json:"name"`
	Included      bool     `json:"included"`
	Caution       string   `json:"caution"`
	ImageURL      string   `json:"imageUrl,omitempty"`
	Brand         string   `json:"brand,omitempty"`
	Variety       string   `json:"variety,omitempty"`
	Category      string   `json:"category,omitempty"`
	UnitOfMeasure string   `json:"unitOfMeasure,omitempty"`
	NetAmount     *float64 `json:"netAmount,omitempty"`
	NetUnit       string   `json:"netUnit,omitempty"`
	PackCount     *int     `json:"packCount,omitempty"`
	Barcodes      []string `json:"barcodes,omitempty"`
}

// Groups is the household's product groups.
type Groups struct {
	db *sql.DB
}

// NewGroups returns a group store for db.
func NewGroups(db *sql.DB) *Groups {
	return &Groups{db: db}
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func invalid(msg string) error  { return &Error{Code: codeInvalid, Msg: msg} }
func missing(msg string) error  { return &Error{Code: codeMissing, Msg: msg} }
func conflict(msg string) error { return &Error{Code: codeConflict, Msg: msg} }

// ValidateTarget checks a target the caller sent.
// A nil input means they sent no target, which is allowed.
func ValidateTarget(in *TargetInput) error {
	if in == nil {
		return nil
	}
	set := 0
	if in.Clear {
		set++
	}
	if in.Window != nil {
		set++
	}
	if in.Quantity != nil {
		set++
	}
	if set != 1 {
		return invalid("Choose a window, a quantity, or clearing the target.")
	}
	if in.Window != nil && (*in.Window < 0 || *in.Window > 12) {
		return invalid("The supply window has to be between 0 and 12 months.")
	}
	if in.Quantity != nil {
		q := *in.Quantity
		if math.IsNaN(q) || math.IsInf(q, 0) || q <= 0 || q > 999 {
			return invalid("The quantity has to be more than zero and at most 999 ounces.")
		}
	}
	if in.Dimension != "" && in.Dimension != product.DimensionMass && in.Dimension != product.DimensionVolume {
		return invalid("Say whether that amount is weight or volume.")
	}
	return nil
}

// List returns every group, with members.
func (g *Groups) List(ctx context.Context) ([]Group, error) {
	rows, err := g.db.QueryContext(ctx, `
		SELECT id FROM product_groups WHERE user_id = ? ORDER BY name, id`, householdUser)
	if err != nil {
		return nil, fmt.Errorf("could not load groups: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("could not load groups: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not load groups: %w", err)
	}
	out := make([]Group, 0, len(ids))
	for _, id := range ids {
		view, err := g.load(ctx, g.db, id)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

// Get returns one group.
func (g *Groups) Get(ctx context.Context, id string) (Group, error) {
	return g.load(ctx, g.db, id)
}

// Create inserts a group. productIDs may be empty.
// A nil target leaves the account window, unless a product already has its own supply setting.
func (g *Groups) Create(ctx context.Context, name string, productIDs []string, target *TargetInput) (Group, error) {
	if err := ValidateTarget(target); err != nil {
		return Group{}, err
	}
	// BEGIN IMMEDIATE waits out a reserved lock held by someone else.
	// A duplicate name is a conflict the caller can fix, and a reader can
	// see it while that lock is held, so the answer does not have to wait.
	// insertGroup checks again inside the write transaction.
	if err := g.preflightName(ctx, name); err != nil {
		return Group{}, err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, fmt.Errorf("could not save the group: %w", err)
	}
	defer tx.Rollback()
	id, err := createInTx(ctx, tx, name, productIDs, target)
	if err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, fmt.Errorf("could not save the group: %w", err)
	}
	return g.load(ctx, g.db, id)
}

// Rename changes the group's name.
func (g *Groups) Rename(ctx context.Context, id, name string) (Group, error) {
	key, err := nameKey(name)
	if err != nil {
		return Group{}, err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, fmt.Errorf("could not rename the group: %w", err)
	}
	defer tx.Rollback()
	if err := ensureGroup(ctx, tx, id); err != nil {
		return Group{}, err
	}
	if err := ensureNameFree(ctx, tx, key, id); err != nil {
		return Group{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE product_groups SET name = ?, name_key = ? WHERE id = ?`, strings.TrimSpace(name), key, id); err != nil {
		if taken := nameConflict(err); taken != nil {
			return Group{}, taken
		}
		return Group{}, fmt.Errorf("could not rename the group: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Group{}, fmt.Errorf("could not rename the group: %w", err)
	}
	return g.load(ctx, g.db, id)
}

// Delete removes a group and its members.
func (g *Groups) Delete(ctx context.Context, id string) error {
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not delete the group: %w", err)
	}
	defer tx.Rollback()
	if err := ensureGroup(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM product_group_members WHERE group_id = ?`, id); err != nil {
		return fmt.Errorf("could not delete the group: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM product_groups WHERE id = ? AND user_id = ?`, id, householdUser); err != nil {
		return fmt.Errorf("could not delete the group: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not delete the group: %w", err)
	}
	return nil
}

// AddMembers puts products in the group.
// fromGroupID moves them out of that group. Without it, a product already in a group is refused.
func (g *Groups) AddMembers(ctx context.Context, id string, productIDs []string, fromGroupID string, target *TargetInput) (Group, error) {
	if err := ValidateTarget(target); err != nil {
		return Group{}, err
	}
	ids := dedupe(productIDs)
	if len(ids) == 0 {
		return Group{}, invalid("Pick at least one product.")
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, fmt.Errorf("could not update the group: %w", err)
	}
	defer tx.Rollback()
	if err := ensureGroup(ctx, tx, id); err != nil {
		return Group{}, err
	}
	if err := addMembersInTx(ctx, tx, id, ids, fromGroupID, target); err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, fmt.Errorf("could not update the group: %w", err)
	}
	return g.load(ctx, g.db, id)
}

// RemoveMember takes one product out. The last member deletes the group.
// Removing the pinned product clears the pin.
func (g *Groups) RemoveMember(ctx context.Context, id, productID string) (Group, bool, error) {
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, false, fmt.Errorf("could not update the group: %w", err)
	}
	defer tx.Rollback()
	if err := ensureGroup(ctx, tx, id); err != nil {
		return Group{}, false, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM product_group_members WHERE group_id = ? AND product_id = ?`, id, productID)
	if err != nil {
		return Group{}, false, fmt.Errorf("could not update the group: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Group{}, false, missing("That product is not in this group.")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE product_groups
		SET pinned_product_id = NULL, rule_confirmed = 0
		WHERE id = ? AND pinned_product_id = ?`, id, productID); err != nil {
		return Group{}, false, fmt.Errorf("could not update the group: %w", err)
	}
	var left int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM product_group_members WHERE group_id = ?`, id).Scan(&left); err != nil {
		return Group{}, false, fmt.Errorf("could not update the group: %w", err)
	}
	if left == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM product_groups WHERE id = ?`, id); err != nil {
			return Group{}, false, fmt.Errorf("could not update the group: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return Group{}, false, fmt.Errorf("could not update the group: %w", err)
		}
		return Group{}, true, nil
	}
	if err := tx.Commit(); err != nil {
		return Group{}, false, fmt.Errorf("could not update the group: %w", err)
	}
	view, err := g.load(ctx, g.db, id)
	return view, false, err
}

// SetRule saves the rule. A non-empty pin must be a current member.
// A nil pin keeps the pin already stored. confirm records that the sheet was saved.
func (g *Groups) SetRule(ctx context.Context, id, rule string, pin *string, confirm bool) (Group, error) {
	if !Valid(rule) {
		return Group{}, invalid("Pick one of the rules.")
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, fmt.Errorf("could not save the rule: %w", err)
	}
	defer tx.Rollback()
	view, err := g.load(ctx, tx, id)
	if err != nil {
		return Group{}, err
	}
	next := view.PinnedProductID
	if pin != nil {
		next = *pin
	}
	if next != "" {
		if _, ok := memberByID(view.Members, next); !ok {
			return Group{}, invalid("Star a product that is in this group.")
		}
	}
	confirmed := 0
	if confirm {
		confirmed = 1
	}
	var pinVal any
	if next != "" {
		pinVal = next
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE product_groups
		SET rule = ?, pinned_product_id = ?, rule_confirmed = ?
		WHERE id = ?`, rule, pinVal, confirmed, id); err != nil {
		return Group{}, fmt.Errorf("could not save the rule: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Group{}, fmt.Errorf("could not save the rule: %w", err)
	}
	return g.load(ctx, g.db, id)
}

// SetMemberRestock keeps a product in the group and out of every shopping rule.
// noRestock false puts it back in the rotation. The declaration is an item
// target of 0, the same store every other don't-restock action uses.
func (g *Groups) SetMemberRestock(ctx context.Context, groupID, productID string, noRestock bool) (Group, error) {
	if err := ensureGroup(ctx, g.db, groupID); err != nil {
		return Group{}, err
	}
	var exists int
	err := g.db.QueryRowContext(ctx, `
		SELECT 1 FROM product_group_members WHERE group_id = ? AND product_id = ?`, groupID, productID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, missing("That product is not in this group.")
	}
	if err != nil {
		return Group{}, fmt.Errorf("could not update the group: %w", err)
	}
	itemID, err := ensureHouseholdItem(ctx, g.db, productID)
	if err != nil {
		return Group{}, err
	}
	if noRestock {
		_, err = g.db.ExecContext(ctx, `
			INSERT INTO item_targets (household_id, item_id, kind, value)
			VALUES (?, ?, 'months', 0)
			ON CONFLICT (household_id, item_id) DO UPDATE SET kind = 'months', value = 0`,
			householdUser, itemID)
	} else {
		_, err = g.db.ExecContext(ctx, `
			DELETE FROM item_targets WHERE household_id = ? AND item_id = ? AND value = 0`,
			householdUser, itemID)
	}
	if err != nil {
		return Group{}, fmt.Errorf("could not update the group: %w", err)
	}
	return g.load(ctx, g.db, groupID)
}

func ensureHouseholdItem(ctx context.Context, q querier, productID string) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `
		SELECT id FROM items WHERE user_id = ? AND product_id = ?`, householdUser, productID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("could not update the group: %w", err)
	}
	id = uuid.NewString()
	if _, err := q.ExecContext(ctx, `
		INSERT INTO items (id, user_id, product_id) VALUES (?, ?, ?)`, id, householdUser, productID); err != nil {
		return "", fmt.Errorf("could not update the group: %w", err)
	}
	return id, nil
}

// SetTarget saves the group's target and does not change member supply settings.
func (g *Groups) SetTarget(ctx context.Context, id string, target *TargetInput) (Group, error) {
	if target == nil {
		return Group{}, invalid("Choose a window, a quantity, or clearing the target.")
	}
	if err := ValidateTarget(target); err != nil {
		return Group{}, err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, fmt.Errorf("could not save the target: %w", err)
	}
	defer tx.Rollback()
	view, err := g.load(ctx, tx, id)
	if err != nil {
		return Group{}, err
	}
	ids := memberIDs(view.Members)
	if err := applyTarget(ctx, tx, id, target, ids); err != nil {
		return Group{}, err
	}
	if err := tx.Commit(); err != nil {
		return Group{}, fmt.Errorf("could not save the target: %w", err)
	}
	return g.load(ctx, g.db, id)
}

// MemberGroupID returns the group a product belongs to.
// An empty id means the product is not in a group.
func (g *Groups) MemberGroupID(ctx context.Context, productID string) (string, error) {
	var id string
	err := g.db.QueryRowContext(ctx, `
		SELECT group_id FROM product_group_members WHERE product_id = ?`, productID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not check the group: %w", err)
	}
	return id, nil
}

// DefaultRule is the account rule. A missing setting means same as what ran out.
func (g *Groups) DefaultRule(ctx context.Context) (Kind, error) {
	return readDefault(ctx, g.db)
}

// SetDefaultRule stores the account rule.
func (g *Groups) SetDefaultRule(ctx context.Context, rule string) error {
	if !Valid(rule) {
		return invalid("Pick one of the rules.")
	}
	_, err := g.db.ExecContext(ctx, `
		INSERT INTO app_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, settingRule, rule)
	if err != nil {
		return fmt.Errorf("could not save the default rule: %w", err)
	}
	return nil
}

// ListSuggestions refreshes look-alike and same-need cards, then returns the open ones.
func (g *Groups) ListSuggestions(ctx context.Context) ([]Suggestion, error) {
	if err := g.RefreshSuggestions(ctx); err != nil {
		return nil, err
	}
	return g.listOpenSuggestions(ctx)
}

func (g *Groups) listOpenSuggestions(ctx context.Context) ([]Suggestion, error) {
	rows, err := g.db.QueryContext(ctx, `
		SELECT id, kind, title, proposed_rule, pinned_product_id, existing_group_id, status
		FROM group_suggestions
		WHERE user_id = ? AND status = 'open'
		ORDER BY created_at, id`, householdUser)
	if err != nil {
		return nil, fmt.Errorf("could not load suggestions: %w", err)
	}
	defer rows.Close()
	var out []Suggestion
	for rows.Next() {
		s, err := scanSuggestion(rows)
		if err != nil {
			return nil, fmt.Errorf("could not load suggestions: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not load suggestions: %w", err)
	}
	if out == nil {
		out = []Suggestion{}
	}
	for i := range out {
		members, err := suggestionMembers(ctx, g.db, out[i].ID)
		if err != nil {
			return nil, err
		}
		for j := range members {
			members[j].Brand = memberBrand(members[j].Name, out[i].Title)
			members[j].Variety = memberVariety(members[j].Name)
		}
		out[i].Members = members
	}
	return out, nil
}

// Accept creates a group or adds to the card's existing group.
// A nil productIDs uses the checked rows. Pairs between a checked product and an
// unchecked one are dismissed. The card stays open when a target is still required.
func (g *Groups) Accept(ctx context.Context, suggestionID string, productIDs []string, target *TargetInput, name string) (Group, error) {
	if err := ValidateTarget(target); err != nil {
		return Group{}, err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, fmt.Errorf("could not accept the suggestion: %w", err)
	}
	defer tx.Rollback()
	s, err := loadSuggestion(ctx, tx, suggestionID)
	if err != nil {
		return Group{}, err
	}
	if s.Status != "open" {
		return Group{}, conflict("That suggestion is already settled.")
	}
	members, err := suggestionMembers(ctx, tx, suggestionID)
	if err != nil {
		return Group{}, err
	}
	included, excluded, err := chooseIncluded(members, productIDs)
	if err != nil {
		return Group{}, err
	}
	var groupID string
	if s.ExistingGroupID != "" {
		if err := ensureGroup(ctx, tx, s.ExistingGroupID); err != nil {
			return Group{}, err
		}
		if err := addMembersInTx(ctx, tx, s.ExistingGroupID, included, "", target); err != nil {
			return Group{}, err
		}
		groupID = s.ExistingGroupID
	} else {
		rule := string(KindSameAsRanOut)
		if Valid(s.ProposedRule) {
			rule = s.ProposedRule
		} else {
			def, err := readDefault(ctx, tx)
			if err != nil {
				return Group{}, err
			}
			rule = string(def)
		}
		pin := ""
		if s.PinnedProductID != "" && contains(included, s.PinnedProductID) {
			pin = s.PinnedProductID
		}
		title := s.Title
		if strings.TrimSpace(name) != "" {
			title = name
		}
		groupID, err = insertGroup(ctx, tx, title, rule, pin, included, target)
		if err != nil {
			return Group{}, err
		}
	}
	if err := dismissPairs(ctx, tx, excluded, included); err != nil {
		return Group{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE group_suggestions SET status = 'accepted' WHERE id = ?`, suggestionID); err != nil {
		return Group{}, fmt.Errorf("could not accept the suggestion: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Group{}, fmt.Errorf("could not accept the suggestion: %w", err)
	}
	return g.load(ctx, g.db, groupID)
}

// Dismiss marks every pair in the card and closes it.
func (g *Groups) Dismiss(ctx context.Context, suggestionID string) error {
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not dismiss the suggestion: %w", err)
	}
	defer tx.Rollback()
	s, err := loadSuggestion(ctx, tx, suggestionID)
	if err != nil {
		return err
	}
	if s.Status != "open" {
		return conflict("That suggestion is already settled.")
	}
	members, err := suggestionMembers(ctx, tx, suggestionID)
	if err != nil {
		return err
	}
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.ProductID
	}
	if err := dismissPairs(ctx, tx, ids, ids); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE group_suggestions SET status = 'dismissed' WHERE id = ?`, suggestionID); err != nil {
		return fmt.Errorf("could not dismiss the suggestion: %w", err)
	}
	return tx.Commit()
}

// Skip leaves the card open.
func (g *Groups) Skip(ctx context.Context, suggestionID string) error {
	_, err := loadSuggestion(ctx, g.db, suggestionID)
	return err
}

// DealsFor returns sales for the household items of these products.
func (g *Groups) DealsFor(ctx context.Context, productIDs []string) ([]shopping.Deal, error) {
	ids := dedupe(productIDs)
	if len(ids) == 0 {
		return nil, nil
	}
	query := `
		SELECT d.item_id, d.price_cents, d.regular_price_cents, d.label, d.source, d.updated_at
		FROM item_deals d
		JOIN items i ON i.id = d.item_id AND i.user_id = d.user_id
		WHERE d.user_id = ? AND i.product_id IN (` + placeholders(len(ids)) + `)`
	args := make([]any, 0, len(ids)+1)
	args = append(args, householdUser)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := g.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("could not load sales: %w", err)
	}
	defer rows.Close()
	var deals []shopping.Deal
	for rows.Next() {
		var deal shopping.Deal
		var price, regular sql.NullInt64
		if err := rows.Scan(&deal.ItemID, &price, &regular, &deal.Label, &deal.Source, &deal.NotedAt); err != nil {
			return nil, fmt.Errorf("could not load sales: %w", err)
		}
		if price.Valid {
			n := int(price.Int64)
			deal.PriceCents = &n
		}
		if regular.Valid {
			n := int(regular.Int64)
			deal.RegularPriceCents = &n
		}
		deals = append(deals, deal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not load sales: %w", err)
	}
	return deals, nil
}

func createInTx(ctx context.Context, tx *sql.Tx, name string, productIDs []string, target *TargetInput) (string, error) {
	rule, err := readDefault(ctx, tx)
	if err != nil {
		return "", err
	}
	return insertGroup(ctx, tx, name, string(rule), "", dedupe(productIDs), target)
}

func insertGroup(ctx context.Context, tx *sql.Tx, name, rule, pin string, productIDs []string, target *TargetInput) (string, error) {
	key, err := nameKey(name)
	if err != nil {
		return "", err
	}
	if !Valid(rule) {
		return "", invalid("Pick one of the rules.")
	}
	if err := ensureNameFree(ctx, tx, key, ""); err != nil {
		return "", err
	}
	ids := dedupe(productIDs)
	if err := ensureProducts(ctx, tx, ids); err != nil {
		return "", err
	}
	if err := ensureJoinable(ctx, tx, ids, "", ""); err != nil {
		return "", err
	}
	if err := requireTargetOrNone(ctx, tx, ids, target); err != nil {
		return "", err
	}
	id := uuid.NewString()
	var pinVal any
	if pin != "" {
		pinVal = pin
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO product_groups (id, user_id, name, name_key, rule, pinned_product_id)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, householdUser, strings.TrimSpace(name), key, rule, pinVal); err != nil {
		if taken := nameConflict(err); taken != nil {
			return "", taken
		}
		return "", fmt.Errorf("could not save the group: %w", err)
	}
	for _, productID := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO product_group_members (product_id, group_id) VALUES (?, ?)`, productID, id); err != nil {
			return "", fmt.Errorf("could not save the group: %w", err)
		}
	}
	if target != nil {
		if err := applyTarget(ctx, tx, id, target, ids); err != nil {
			return "", err
		}
		if err := deleteOverrides(ctx, tx, ids); err != nil {
			return "", err
		}
	}
	return id, nil
}

func addMembersInTx(ctx context.Context, tx *sql.Tx, groupID string, productIDs []string, fromGroupID string, target *TargetInput) error {
	ids := dedupe(productIDs)
	if err := ensureProducts(ctx, tx, ids); err != nil {
		return err
	}
	joining, err := classifyJoining(ctx, tx, groupID, ids, fromGroupID)
	if err != nil {
		return err
	}
	if err := requireTargetOrNone(ctx, tx, joining, target); err != nil {
		return err
	}
	for _, productID := range joining {
		if err := moveMember(ctx, tx, groupID, productID, fromGroupID); err != nil {
			return err
		}
	}
	if target != nil {
		all, err := memberIDsOf(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if err := applyTarget(ctx, tx, groupID, target, all); err != nil {
			return err
		}
		if err := deleteOverrides(ctx, tx, joining); err != nil {
			return err
		}
	}
	return nil
}

func moveMember(ctx context.Context, tx *sql.Tx, dest, productID, fromGroupID string) error {
	var current sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT group_id FROM product_group_members WHERE product_id = ?`, productID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		if fromGroupID != "" {
			return invalid("That product is not in the group you named.")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO product_group_members (product_id, group_id) VALUES (?, ?)`, productID, dest); err != nil {
			return fmt.Errorf("could not update the group: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not update the group: %w", err)
	}
	if current.String == dest {
		return nil
	}
	if fromGroupID == "" || current.String != fromGroupID {
		return conflict("That product is already in another group.")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE product_groups
		SET pinned_product_id = NULL, rule_confirmed = 0
		WHERE id = ? AND pinned_product_id = ?`, fromGroupID, productID); err != nil {
		return fmt.Errorf("could not update the group: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM product_group_members WHERE product_id = ? AND group_id = ?`, productID, fromGroupID); err != nil {
		return fmt.Errorf("could not update the group: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO product_group_members (product_id, group_id) VALUES (?, ?)`, productID, dest); err != nil {
		return fmt.Errorf("could not update the group: %w", err)
	}
	var left int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM product_group_members WHERE group_id = ?`, fromGroupID).Scan(&left); err != nil {
		return fmt.Errorf("could not update the group: %w", err)
	}
	if left == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM product_groups WHERE id = ?`, fromGroupID); err != nil {
			return fmt.Errorf("could not update the group: %w", err)
		}
	}
	return nil
}

func classifyJoining(ctx context.Context, tx *sql.Tx, dest string, ids []string, fromGroupID string) ([]string, error) {
	if fromGroupID != "" {
		if err := ensureGroup(ctx, tx, fromGroupID); err != nil {
			return nil, err
		}
	}
	var joining []string
	for _, id := range ids {
		var current sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT group_id FROM product_group_members WHERE product_id = ?`, id).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			if fromGroupID != "" {
				return nil, invalid("That product is not in the group you named.")
			}
			joining = append(joining, id)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("could not update the group: %w", err)
		}
		if current.String == dest {
			continue
		}
		if fromGroupID == "" || current.String != fromGroupID {
			return nil, conflict("That product is already in another group.")
		}
		joining = append(joining, id)
	}
	return joining, nil
}

func ensureJoinable(ctx context.Context, tx *sql.Tx, ids []string, dest, fromGroupID string) error {
	_, err := classifyJoining(ctx, tx, dest, ids, fromGroupID)
	return err
}

func requireTargetOrNone(ctx context.Context, q querier, ids []string, target *TargetInput) error {
	if target != nil || len(ids) == 0 {
		return nil
	}
	notices, err := overrideNotices(ctx, q, ids)
	if err != nil {
		return err
	}
	if len(notices) == 0 {
		return nil
	}
	return &Error{Code: codeTarget, Msg: targetRequiredMessage, Members: notices}
}

func overrideNotices(ctx context.Context, q querier, ids []string) ([]OverrideNotice, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query := `
		SELECT i.product_id, p.name,
			CASE WHEN t.kind = 'months' THEN t.value END,
			CASE WHEN t.kind = 'amount' THEN t.value END,
			p.net_base_value, COALESCE(p.net_dimension, '')
		FROM item_targets t
		JOIN items i ON i.id = t.item_id AND i.user_id = t.household_id
		JOIN products p ON p.id = i.product_id
		WHERE t.household_id = ? AND t.value > 0 AND i.product_id IN (` + placeholders(len(ids)) + `)`
	args := make([]any, 0, len(ids)+1)
	args = append(args, householdUser)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("could not read supply settings: %w", err)
	}
	defer rows.Close()
	var notices []OverrideNotice
	for rows.Next() {
		var n OverrideNotice
		var window, qty sql.NullFloat64
		var net sql.NullFloat64
		var dim string
		if err := rows.Scan(&n.ProductID, &n.Name, &window, &qty, &net, &dim); err != nil {
			return nil, fmt.Errorf("could not read supply settings: %w", err)
		}
		if window.Valid && window.Float64 > 0 {
			v := int(math.Round(window.Float64))
			n.WindowMonths = &v
		}
		if qty.Valid && qty.Float64 > 0 {
			count := int(math.Round(qty.Float64))
			n.Quantity = &count
			if net.Valid && net.Float64 > 0 && count > 0 {
				if ounces, _, ok := product.DisplayNetSize(net.Float64*float64(count), dim); ok {
					n.Amount = &ounces
					n.Dimension = dim
				}
			}
		}
		notices = append(notices, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not read supply settings: %w", err)
	}
	sort.Slice(notices, func(i, j int) bool { return notices[i].ProductID < notices[j].ProductID })
	return notices, nil
}

func deleteOverrides(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	query := `DELETE FROM supply_overrides WHERE product_id IN (` + placeholders(len(ids)) + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("could not update supply settings: %w", err)
	}
	itemQuery := `DELETE FROM item_targets WHERE value > 0 AND item_id IN (
		SELECT id FROM items WHERE user_id = ? AND product_id IN (` + placeholders(len(ids)) + `))`
	itemArgs := make([]any, 0, len(ids)+1)
	itemArgs = append(itemArgs, householdUser)
	for _, id := range ids {
		itemArgs = append(itemArgs, id)
	}
	if _, err := tx.ExecContext(ctx, itemQuery, itemArgs...); err != nil {
		return fmt.Errorf("could not update supply settings: %w", err)
	}
	return nil
}

func applyTarget(ctx context.Context, tx *sql.Tx, groupID string, in *TargetInput, inferIDs []string) error {
	if in == nil {
		return nil
	}
	if err := ValidateTarget(in); err != nil {
		return err
	}
	if in.Clear {
		_, err := tx.ExecContext(ctx, `
			UPDATE product_groups
			SET window_months = NULL, quantity_base_value = NULL, quantity_dimension = NULL
			WHERE id = ?`, groupID)
		if err != nil {
			return fmt.Errorf("could not save the target: %w", err)
		}
		return nil
	}
	if in.Window != nil {
		_, err := tx.ExecContext(ctx, `
			UPDATE product_groups
			SET window_months = ?, quantity_base_value = NULL, quantity_dimension = NULL
			WHERE id = ?`, *in.Window, groupID)
		if err != nil {
			return fmt.Errorf("could not save the target: %w", err)
		}
		return nil
	}
	dim, err := inferDimension(ctx, tx, inferIDs, in.Dimension)
	if err != nil {
		return err
	}
	base, _, err := product.BaseFromAmount(*in.Quantity, unitFor(dim))
	if err != nil {
		return invalid("The quantity has to be more than zero and at most 999 ounces.")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE product_groups
		SET window_months = NULL, quantity_base_value = ?, quantity_dimension = ?
		WHERE id = ?`, base, dim, groupID); err != nil {
		return fmt.Errorf("could not save the target: %w", err)
	}
	return nil
}

func inferDimension(ctx context.Context, q querier, ids []string, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	if len(ids) == 0 {
		return product.DimensionMass, nil
	}
	query := `
		SELECT DISTINCT net_dimension FROM products
		WHERE id IN (` + placeholders(len(ids)) + `)
		  AND net_base_value IS NOT NULL
		  AND net_dimension IN ('mass', 'volume')`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return "", fmt.Errorf("could not save the target: %w", err)
	}
	defer rows.Close()
	var dims []string
	for rows.Next() {
		var dim string
		if err := rows.Scan(&dim); err != nil {
			return "", fmt.Errorf("could not save the target: %w", err)
		}
		dims = append(dims, dim)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("could not save the target: %w", err)
	}
	if len(dims) == 1 {
		return dims[0], nil
	}
	if len(dims) == 0 {
		return product.DimensionMass, nil
	}
	return "", invalid("Say whether that amount is weight or volume.")
}

func unitFor(dim string) string {
	if dim == product.DimensionVolume {
		return "fl oz"
	}
	return "oz"
}

func (g *Groups) load(ctx context.Context, q querier, id string) (Group, error) {
	var view Group
	var pin, dim sql.NullString
	var window sql.NullInt64
	var qty sql.NullFloat64
	var confirmed int
	err := q.QueryRowContext(ctx, `
		SELECT id, name, rule, rule_confirmed, pinned_product_id, window_months, quantity_base_value, quantity_dimension
		FROM product_groups WHERE id = ? AND user_id = ?`, id, householdUser).Scan(
		&view.ID, &view.Name, &view.Rule, &confirmed, &pin, &window, &qty, &dim)
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, missing("That group is not here.")
	}
	if err != nil {
		return Group{}, fmt.Errorf("could not load the group: %w", err)
	}
	view.RuleConfirmed = confirmed != 0
	if pin.Valid {
		view.PinnedProductID = pin.String
	}
	if window.Valid {
		n := int(window.Int64)
		view.WindowMonths = &n
	}
	if qty.Valid && dim.Valid {
		view.hasQuantity = true
		view.quantityBase = qty.Float64
		view.Dimension = dim.String
		if amount, _, ok := product.DisplayNetSize(qty.Float64, dim.String); ok {
			view.Quantity = &amount
		}
	}
	members, err := loadMembers(ctx, q, id)
	if err != nil {
		return Group{}, err
	}
	view.Members = members
	return view, nil
}

func loadMembers(ctx context.Context, q querier, groupID string) ([]Member, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT m.product_id, p.name,
		       (SELECT COUNT(*) FROM item_instances ii
		         JOIN items i ON i.id = ii.item_id
		         WHERE i.product_id = m.product_id AND i.user_id = ? AND ii.removed_at IS NULL),
		       (SELECT i2.id FROM items i2 WHERE i2.product_id = m.product_id AND i2.user_id = ? LIMIT 1),
		       p.net_base_value, p.net_dimension,
		       (SELECT MAX(c.consumed_at) FROM consumption_events c
		         JOIN items i ON i.id = c.item_id
		         WHERE i.product_id = m.product_id AND i.user_id = ?),
		       (SELECT MAX(s.at) FROM stock_in_events s WHERE s.product_id = m.product_id),
		       COALESCE((
		         SELECT 1 FROM item_targets t
		         JOIN items it ON it.id = t.item_id AND it.user_id = t.household_id
		         WHERE it.product_id = m.product_id AND t.household_id = ? AND t.value = 0
		         LIMIT 1
		       ), 0)
		FROM product_group_members m
		JOIN products p ON p.id = m.product_id
		WHERE m.group_id = ?
		ORDER BY p.name, m.product_id`, householdUser, householdUser, householdUser, householdUser, groupID)
	if err != nil {
		return nil, fmt.Errorf("could not load the group: %w", err)
	}
	defer rows.Close()
	members := []Member{}
	for rows.Next() {
		var m Member
		var itemID, dim sql.NullString
		var net sql.NullFloat64
		var consumed, stocked sql.NullString
		var noRestock int
		if err := rows.Scan(&m.ProductID, &m.Name, &m.OnHand, &itemID, &net, &dim, &consumed, &stocked, &noRestock); err != nil {
			return nil, fmt.Errorf("could not load the group: %w", err)
		}
		m.NoRestock = noRestock != 0
		if itemID.Valid {
			m.ItemID = itemID.String
		}
		if net.Valid {
			v := net.Float64
			m.NetBase = &v
		}
		if dim.Valid {
			m.Dimension = dim.String
		}
		if consumed.Valid {
			m.LastConsumedAt = parseDBTime(consumed.String)
		}
		if stocked.Valid {
			m.LastStockedAt = parseDBTime(stocked.String)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not load the group: %w", err)
	}
	return members, nil
}

func ensureGroup(ctx context.Context, q querier, id string) error {
	var found string
	err := q.QueryRowContext(ctx, `SELECT id FROM product_groups WHERE id = ? AND user_id = ?`, id, householdUser).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return missing("That group is not here.")
	}
	if err != nil {
		return fmt.Errorf("could not load the group: %w", err)
	}
	return nil
}

func ensureProducts(ctx context.Context, q querier, ids []string) error {
	for _, id := range ids {
		var found string
		err := q.QueryRowContext(ctx, `SELECT id FROM products WHERE id = ?`, id).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return invalid("That product is not in the pantry.")
		}
		if err != nil {
			return fmt.Errorf("could not save the group: %w", err)
		}
	}
	return nil
}

func (g *Groups) preflightName(ctx context.Context, name string) error {
	key, err := nameKey(name)
	if err != nil {
		return err
	}
	tx, err := g.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("could not save the group: %w", err)
	}
	defer tx.Rollback()
	return ensureNameFree(ctx, tx, key, "")
}

// nameConflict reports a duplicate group name. Only SQLITE_CONSTRAINT_UNIQUE
// (2067) is that conflict. Other constraint codes, such as a foreign key
// (787) or NOT NULL (1299), stay server errors.
func nameConflict(err error) error {
	var driverErr *sqlite.Error
	if !errors.As(err, &driverErr) || driverErr.Code() != sqliteConstraintUnique {
		return nil
	}
	return conflict("A group with that name already exists.")
}

func ensureNameFree(ctx context.Context, q querier, key, exceptID string) error {
	var existing string
	err := q.QueryRowContext(ctx, `SELECT id FROM product_groups WHERE user_id = ? AND name_key = ?`, householdUser, key).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not save the group: %w", err)
	}
	if existing != exceptID {
		return conflict("A group with that name already exists.")
	}
	return nil
}

func nameKey(name string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return "", invalid("The group needs a name.")
	}
	return key, nil
}

func readDefault(ctx context.Context, q querier) (Kind, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, settingRule).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return KindSameAsRanOut, nil
	}
	if err != nil {
		return "", fmt.Errorf("could not read the default rule: %w", err)
	}
	if !Valid(raw) {
		return KindSameAsRanOut, nil
	}
	return Kind(raw), nil
}

func memberIDs(members []Member) []string {
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.ProductID
	}
	return ids
}

func memberIDsOf(ctx context.Context, q querier, groupID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT product_id FROM product_group_members WHERE group_id = ? ORDER BY product_id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("could not update the group: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("could not update the group: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not update the group: %w", err)
	}
	return ids, nil
}

func dedupe(ids []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func contains(ids []string, id string) bool {
	for _, each := range ids {
		if each == id {
			return true
		}
	}
	return false
}

func parseDBTime(s string) time.Time {
	s = strings.TrimSpace(s)
	// time.Time.String appends " m=+…" while a monotonic reading is present.
	// The sqlite driver stores that whole string, and MAX returns it as text.
	if i := strings.Index(s, " m="); i >= 0 {
		s = s[:i]
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			return parsed.UTC()
		}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, strings.Replace(s, " ", "T", 1)); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func chooseIncluded(members []SuggestionMember, productIDs []string) (included, excluded []string, err error) {
	allowed := map[string]bool{}
	for _, m := range members {
		allowed[m.ProductID] = true
	}
	if productIDs == nil {
		for _, m := range members {
			if m.Included {
				included = append(included, m.ProductID)
			} else {
				excluded = append(excluded, m.ProductID)
			}
		}
	} else {
		for _, id := range dedupe(productIDs) {
			if !allowed[id] {
				return nil, nil, invalid("That product is not on this suggestion.")
			}
			included = append(included, id)
		}
		in := map[string]bool{}
		for _, id := range included {
			in[id] = true
		}
		for _, m := range members {
			if !in[m.ProductID] {
				excluded = append(excluded, m.ProductID)
			}
		}
	}
	if len(included) == 0 {
		return nil, nil, invalid("Pick at least one product.")
	}
	return included, excluded, nil
}

func dismissPairs(ctx context.Context, tx *sql.Tx, left, right []string) error {
	for _, a := range left {
		for _, b := range right {
			if a == b {
				continue
			}
			lo, hi := a, b
			if lo > hi {
				lo, hi = hi, lo
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO group_suggestion_dismissals (user_id, product_id_a, product_id_b)
				VALUES (?, ?, ?)
				ON CONFLICT(user_id, product_id_a, product_id_b) DO NOTHING`,
				householdUser, lo, hi); err != nil {
				return fmt.Errorf("could not remember that dismissal: %w", err)
			}
		}
	}
	return nil
}

type suggestionRow interface {
	Scan(dest ...any) error
}

func scanSuggestion(row suggestionRow) (Suggestion, error) {
	var s Suggestion
	var rule, pin, existing sql.NullString
	if err := row.Scan(&s.ID, &s.Kind, &s.Title, &rule, &pin, &existing, &s.Status); err != nil {
		return Suggestion{}, err
	}
	if rule.Valid {
		s.ProposedRule = rule.String
	}
	if pin.Valid {
		s.PinnedProductID = pin.String
	}
	if existing.Valid {
		s.ExistingGroupID = existing.String
	}
	s.Members = []SuggestionMember{}
	return s, nil
}

func loadSuggestion(ctx context.Context, q querier, id string) (Suggestion, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, kind, title, proposed_rule, pinned_product_id, existing_group_id, status
		FROM group_suggestions WHERE id = ? AND user_id = ?`, id, householdUser)
	s, err := scanSuggestion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Suggestion{}, missing("That suggestion is not here.")
	}
	if err != nil {
		return Suggestion{}, fmt.Errorf("could not load suggestions: %w", err)
	}
	return s, nil
}

func suggestionMembers(ctx context.Context, q querier, id string) ([]SuggestionMember, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT m.product_id, COALESCE(p.name, ''), m.included, m.caution,
		       COALESCE(p.image_url, ''), COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''),
		       p.net_base_value, COALESCE(p.net_dimension, ''), p.pack_count
		FROM group_suggestion_members m
		LEFT JOIN products p ON p.id = m.product_id
		WHERE m.suggestion_id = ?
		ORDER BY m.product_id`, id)
	if err != nil {
		return nil, fmt.Errorf("could not load suggestions: %w", err)
	}
	defer rows.Close()
	members := []SuggestionMember{}
	for rows.Next() {
		var m SuggestionMember
		var included int
		var net sql.NullFloat64
		var pack sql.NullInt64
		var dimension string
		if err := rows.Scan(&m.ProductID, &m.Name, &included, &m.Caution,
			&m.ImageURL, &m.Category, &m.UnitOfMeasure, &net, &dimension, &pack); err != nil {
			return nil, fmt.Errorf("could not load suggestions: %w", err)
		}
		m.Included = included != 0
		if net.Valid {
			if amount, unit, ok := product.DisplayNetSize(net.Float64, dimension); ok {
				m.NetAmount = &amount
				m.NetUnit = unit
			}
		}
		if pack.Valid && pack.Int64 > 0 {
			n := int(pack.Int64)
			m.PackCount = &n
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not load suggestions: %w", err)
	}
	if err := attachBarcodes(ctx, q, members); err != nil {
		return nil, err
	}
	return members, nil
}

func attachBarcodes(ctx context.Context, q querier, members []SuggestionMember) error {
	if len(members) == 0 {
		return nil
	}
	holders := make([]string, len(members))
	args := make([]any, len(members))
	index := make(map[string]int, len(members))
	for i, member := range members {
		holders[i] = "?"
		args[i] = member.ProductID
		index[member.ProductID] = i
	}
	rows, err := q.QueryContext(ctx, `
		SELECT product_id, barcode FROM barcodes
		WHERE product_id IN (`+strings.Join(holders, ",")+`)
		ORDER BY barcode`, args...)
	if err != nil {
		return fmt.Errorf("could not load suggestions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var productID, barcode string
		if err := rows.Scan(&productID, &barcode); err != nil {
			return fmt.Errorf("could not load suggestions: %w", err)
		}
		i, ok := index[productID]
		if !ok || barcode == "" {
			continue
		}
		members[i].Barcodes = append(members[i].Barcodes, barcode)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("could not load suggestions: %w", err)
	}
	return nil
}
