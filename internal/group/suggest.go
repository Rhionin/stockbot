package group

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// RefreshSuggestions inserts look-alike and same-need cards that are not
// already grouped, already open, or fully dismissed. A second call does not
// copy them. Products added after the first seed are included.
func (g *Groups) RefreshSuggestions(ctx context.Context, scope tenant.Scope) error {
	if err := scope.Require(); err != nil {
		return err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not refresh suggestions: %w", err)
	}
	defer tx.Rollback()
	if err := refreshSuggestions(ctx, scope, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not refresh suggestions: %w", err)
	}
	return nil
}

func refreshSuggestions(ctx context.Context, scope tenant.Scope, tx *sql.Tx) error {
	if err := scope.Require(); err != nil {
		return err
	}
	_, err := seedSuggestions(ctx, scope, tx)
	return err
}

// Rescan runs the same suggestion pass as a refresh and returns only the cards
// that pass created. An empty slice means nothing new was found.
func (g *Groups) Rescan(ctx context.Context, scope tenant.Scope) ([]Suggestion, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	defer tx.Rollback()
	ids, err := seedSuggestions(ctx, scope, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	if len(ids) == 0 {
		return []Suggestion{}, nil
	}
	open, err := g.listOpenSuggestions(ctx, scope)
	if err != nil {
		return nil, err
	}
	want := map[string]struct{}{}
	for _, id := range ids {
		want[id] = struct{}{}
	}
	var created []Suggestion
	for _, card := range open {
		if _, ok := want[card.ID]; ok {
			created = append(created, card)
		}
	}
	if created == nil {
		created = []Suggestion{}
	}
	return created, nil
}

// ConsiderProduct checks one newly created or recognized product against the
// pantry. Matching groups and ungrouped products become suggestion cards.
func (g *Groups) ConsiderProduct(ctx context.Context, scope tenant.Scope, productID string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	productID = strings.TrimSpace(productID)
	if productID == "" {
		return nil
	}
	var exists int
	err := g.db.QueryRowContext(ctx, `SELECT 1 FROM products WHERE id = ?`, productID).Scan(&exists)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not refresh suggestions: %w", err)
	}
	return g.RefreshSuggestions(ctx, scope)
}

func loadMembership(ctx context.Context, scope tenant.Scope, q querier) (map[string]string, []string, error) {
	if err := scope.Require(); err != nil {
		return nil, nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT m.product_id, m.group_id, g.name
		FROM product_group_members m
		JOIN product_groups g ON g.id = m.group_id AND g.household_id = m.household_id
		WHERE g.household_id = ? AND m.household_id = ?`, scope.HouseholdID(), scope.HouseholdID())
	if err != nil {
		return nil, nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	defer rows.Close()
	groupOf := map[string]string{}
	seen := map[string]struct{}{}
	var extra []string
	for rows.Next() {
		var productID, groupID, name string
		if err := rows.Scan(&productID, &groupID, &name); err != nil {
			return nil, nil, fmt.Errorf("could not refresh suggestions: %w", err)
		}
		groupOf[productID] = groupID
		key, _ := LookAlikeKey(name, nil)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		extra = append(extra, key)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	nameRows, err := q.QueryContext(ctx, `SELECT name FROM product_groups WHERE household_id = ?`, scope.HouseholdID())
	if err != nil {
		return nil, nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	defer nameRows.Close()
	for nameRows.Next() {
		var name string
		if err := nameRows.Scan(&name); err != nil {
			return nil, nil, fmt.Errorf("could not refresh suggestions: %w", err)
		}
		key, _ := LookAlikeKey(name, nil)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		extra = append(extra, key)
	}
	if err := nameRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	return groupOf, extra, nil
}

func loadDismissed(ctx context.Context, scope tenant.Scope, q querier) (map[string]struct{}, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT product_id_a, product_id_b FROM group_suggestion_dismissals WHERE household_id = ?`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, fmt.Errorf("could not refresh suggestions: %w", err)
		}
		out[pairKey(a, b)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	return out, nil
}

func loadOpenSets(ctx context.Context, scope tenant.Scope, q querier) (map[string]struct{}, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT m.suggestion_id, m.product_id
		FROM group_suggestion_members m
		JOIN group_suggestions s ON s.id = m.suggestion_id AND s.household_id = m.household_id
		WHERE s.household_id = ? AND m.household_id = ? AND s.status = 'open'
		ORDER BY m.suggestion_id, m.product_id`, scope.HouseholdID(), scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	defer rows.Close()
	sets := map[string][]string{}
	var order []string
	for rows.Next() {
		var sid, pid string
		if err := rows.Scan(&sid, &pid); err != nil {
			return nil, fmt.Errorf("could not refresh suggestions: %w", err)
		}
		if _, ok := sets[sid]; !ok {
			order = append(order, sid)
		}
		sets[sid] = append(sets[sid], pid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	out := map[string]struct{}{}
	for _, sid := range order {
		ids := sets[sid]
		sort.Strings(ids)
		out[strings.Join(ids, "\n")] = struct{}{}
	}
	return out, nil
}

func existingGroup(ids []string, groupOf map[string]string) (string, bool) {
	seen := map[string]struct{}{}
	var only string
	ungrouped := 0
	for _, id := range ids {
		gid := groupOf[id]
		if gid == "" {
			ungrouped++
			continue
		}
		seen[gid] = struct{}{}
		only = gid
	}
	if len(seen) > 1 {
		return "", true
	}
	if len(seen) == 1 && ungrouped == 0 {
		return "", true
	}
	if len(seen) == 1 {
		return only, false
	}
	return "", false
}

func splitDismissed(ids []string, dismissed map[string]struct{}) [][]string {
	rest := append([]string(nil), ids...)
	sort.Strings(rest)
	var cards [][]string
	for len(rest) >= 2 {
		keep := append([]string(nil), rest...)
		for {
			drop := dismissedIndex(keep, dismissed)
			if drop < 0 {
				break
			}
			keep = append(keep[:drop], keep[drop+1:]...)
		}
		if len(keep) >= 2 {
			cards = append(cards, keep)
		}
		next := difference(rest, keep)
		if len(next) == len(rest) {
			break
		}
		rest = next
	}
	return cards
}

func dismissedIndex(ids []string, dismissed map[string]struct{}) int {
	best := -1
	bestCount := 0
	for i, id := range ids {
		n := 0
		for j, other := range ids {
			if i == j {
				continue
			}
			if _, ok := dismissed[pairKey(id, other)]; ok {
				n++
			}
		}
		if n == 0 {
			continue
		}
		if best < 0 || n > bestCount || (n == bestCount && id > ids[best]) {
			best = i
			bestCount = n
		}
	}
	return best
}

func difference(all, keep []string) []string {
	in := map[string]struct{}{}
	for _, id := range keep {
		in[id] = struct{}{}
	}
	var out []string
	for _, id := range all {
		if _, ok := in[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "\n" + b
}

func titleFromKey(key string) string {
	r := []rune(strings.TrimSpace(key))
	if len(r) == 0 {
		return "Group"
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func majorityCaution(ids []string, cautionOf map[string]string) string {
	counts := map[string]int{}
	for _, id := range ids {
		counts[cautionOf[id]]++
	}
	labels := make([]string, 0, len(counts))
	for label := range counts {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		if counts[labels[i]] != counts[labels[j]] {
			return counts[labels[i]] > counts[labels[j]]
		}
		if labels[i] == "" {
			return true
		}
		if labels[j] == "" {
			return false
		}
		return labels[i] < labels[j]
	})
	return labels[0]
}

func loadProductGroupOf(ctx context.Context, scope tenant.Scope, q querier) (map[string]string, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT product_id, group_id FROM product_group_members WHERE household_id = ?`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var productID, groupID string
		if err := rows.Scan(&productID, &groupID); err != nil {
			return nil, fmt.Errorf("could not refresh suggestions: %w", err)
		}
		out[productID] = groupID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	return out, nil
}

func suggestionProductIDs(ctx context.Context, scope tenant.Scope, q querier, suggestionID string) ([]string, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT product_id FROM group_suggestion_members
		WHERE suggestion_id = ? AND household_id = ?
		ORDER BY product_id`, suggestionID, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("could not refresh suggestions: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	return ids, nil
}

func groupExists(ctx context.Context, scope tenant.Scope, q querier, groupID string) (bool, error) {
	if err := scope.Require(); err != nil {
		return false, err
	}
	if groupID == "" {
		return false, nil
	}
	var found string
	err := q.QueryRowContext(ctx, `SELECT id FROM product_groups WHERE id = ? AND household_id = ?`, groupID, scope.HouseholdID()).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("could not refresh suggestions: %w", err)
	}
	return true, nil
}

func groupIDByTitle(ctx context.Context, scope tenant.Scope, q querier, title string) (string, error) {
	if err := scope.Require(); err != nil {
		return "", err
	}
	key, err := nameKey(title)
	if err != nil {
		return "", nil
	}
	var id string
	err = q.QueryRowContext(ctx, `SELECT id FROM product_groups WHERE household_id = ? AND name_key = ?`, scope.HouseholdID(), key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not refresh suggestions: %w", err)
	}
	return id, nil
}

func groupedProductsShareOneGroup(productIDs []string, groupOf map[string]string) string {
	seen := map[string]struct{}{}
	var only string
	for _, id := range productIDs {
		gid := groupOf[id]
		if gid == "" {
			continue
		}
		seen[gid] = struct{}{}
		only = gid
	}
	if len(seen) != 1 {
		return ""
	}
	return only
}

// sharedGroupForSuggestion links a card to a group when every grouped product
// on the card is already in one group and at least two of the card's products
// are there, so a lone overlap with an unrelated group does not hijack the card.
func sharedGroupForSuggestion(productIDs []string, groupOf map[string]string) string {
	gid := groupedProductsShareOneGroup(productIDs, groupOf)
	if gid == "" {
		return ""
	}
	n := 0
	for _, id := range productIDs {
		if groupOf[id] == gid {
			n++
		}
	}
	if n < 2 {
		return ""
	}
	return gid
}

func countUngrouped(productIDs []string, groupOf map[string]string) int {
	n := 0
	for _, id := range productIDs {
		if groupOf[id] == "" {
			n++
		}
	}
	return n
}

func (g *Groups) reconcileOpenSuggestions(ctx context.Context, scope tenant.Scope, q querier) error {
	if err := scope.Require(); err != nil {
		return err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT id, title, existing_group_id
		FROM group_suggestions
		WHERE household_id = ? AND status = 'open'`, scope.HouseholdID())
	if err != nil {
		return fmt.Errorf("could not refresh suggestions: %w", err)
	}
	defer rows.Close()
	type openRow struct {
		id       string
		title    string
		existing sql.NullString
	}
	var pending []openRow
	for rows.Next() {
		var row openRow
		if err := rows.Scan(&row.id, &row.title, &row.existing); err != nil {
			return fmt.Errorf("could not refresh suggestions: %w", err)
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("could not refresh suggestions: %w", err)
	}
	groupOf, err := loadProductGroupOf(ctx, scope, q)
	if err != nil {
		return err
	}
	for _, row := range pending {
		ids, err := suggestionProductIDs(ctx, scope, q, row.id)
		if err != nil {
			return err
		}
		if err := reconcileOneOpenSuggestion(ctx, scope, q, row.id, row.title, row.existing.String, ids, groupOf); err != nil {
			return err
		}
	}
	return nil
}

func reconcileOneOpenSuggestion(ctx context.Context, scope tenant.Scope, q querier, suggestionID, title, existingGroupID string, productIDs []string, groupOf map[string]string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if len(productIDs) == 0 {
		return nil
	}
	ungrouped := countUngrouped(productIDs, groupOf)
	if ungrouped == 0 {
		_, err := q.ExecContext(ctx, `
			UPDATE group_suggestions SET status = 'accepted'
			WHERE id = ? AND household_id = ?`, suggestionID, scope.HouseholdID())
		return err
	}

	matchID := ""
	existingValid := false
	if existingGroupID != "" {
		ok, err := groupExists(ctx, scope, q, existingGroupID)
		if err != nil {
			return err
		}
		existingValid = ok
		if ok {
			matchID = existingGroupID
		}
	}
	if matchID == "" {
		byName, err := groupIDByTitle(ctx, scope, q, title)
		if err != nil {
			return err
		}
		if byName != "" {
			matchID = byName
		} else {
			matchID = sharedGroupForSuggestion(productIDs, groupOf)
		}
	}

	if matchID != "" {
		if !existingValid || existingGroupID != matchID {
			_, err := q.ExecContext(ctx, `
				UPDATE group_suggestions SET existing_group_id = ?
				WHERE id = ? AND household_id = ?`, matchID, suggestionID, scope.HouseholdID())
			if err != nil {
				return fmt.Errorf("could not refresh suggestions: %w", err)
			}
		}
		return nil
	}

	if existingGroupID != "" && !existingValid {
		if _, err := q.ExecContext(ctx, `
			UPDATE group_suggestions SET existing_group_id = NULL
			WHERE id = ? AND household_id = ?`, suggestionID, scope.HouseholdID()); err != nil {
			return fmt.Errorf("could not refresh suggestions: %w", err)
		}
	}

	if ungrouped < 2 {
		_, err := q.ExecContext(ctx, `
			UPDATE group_suggestions SET status = 'dismissed'
			WHERE id = ? AND household_id = ?`, suggestionID, scope.HouseholdID())
		return err
	}
	return nil
}
