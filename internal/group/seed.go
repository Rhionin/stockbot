package group

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
)

const seededSetting = "group_suggestions_seeded_v1"

type seedProduct struct {
	id       string
	name     string
	unit     string
	category string
}

type seedCluster struct {
	title    string
	ids      []string
	caution  map[string]string
	existing string
}

type seedPref struct {
	needKey   string
	productID string
	ignore    bool
	gone      bool
}

// Seed inserts one inbox card for each combination the old shopping plan
// already treated as one need, plus the wider look-alike clusters.
// It runs once. A second call does nothing.
func (g *Groups) Seed(ctx context.Context, scope tenant.Scope) error {
	if err := scope.Require(); err != nil {
		return err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not prepare group suggestions: %w", err)
	}
	defer tx.Rollback()

	var existing string
	err = tx.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, seededSetting).Scan(&existing)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("could not prepare group suggestions: %w", err)
	}
	if _, err := seedSuggestions(ctx, scope, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES (?, '1')`, seededSetting); err != nil {
		return fmt.Errorf("could not prepare group suggestions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not prepare group suggestions: %w", err)
	}
	return nil
}

func seedSuggestions(ctx context.Context, scope tenant.Scope, tx *sql.Tx) ([]string, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	products, err := loadSeedProducts(ctx, tx)
	if err != nil {
		return nil, err
	}
	groupOf, extraKeys, err := loadMembership(ctx, scope, tx)
	if err != nil {
		return nil, err
	}
	dismissed, err := loadDismissed(ctx, scope, tx)
	if err != nil {
		return nil, err
	}
	openSets, err := loadOpenSets(ctx, scope, tx)
	if err != nil {
		return nil, err
	}
	prefs, err := loadSeedPrefs(ctx, scope, tx)
	if err != nil {
		return nil, err
	}

	matches := similarMatches(products, extraKeys)
	lookalikes := clustersFrom(products, matches, groupOf, dismissed, func(key string) string {
		return titleFromKey(key)
	})

	needOf := map[string]string{}
	needMembers := map[string][]string{}
	for _, p := range products {
		key, ok := needKey(p.name, p.unit)
		if !ok {
			continue
		}
		needOf[p.id] = key
		needMembers[key] = append(needMembers[key], p.id)
	}
	sameNeed := sameNeedClusters(products, needOf, groupOf, dismissed)

	var cards []seedCluster
	cards = append(cards, lookalikes...)
	for _, card := range sameNeed {
		if coveredBy(card.ids, lookalikes) {
			continue
		}
		cards = append(cards, card)
	}

	var created []string
	for _, card := range cards {
		sig := strings.Join(card.ids, "\n")
		if _, already := openSets[sig]; already {
			continue
		}
		id, err := insertSeeded(ctx, scope, tx, card, matchPref(card.ids, prefs, needMembers))
		if err != nil {
			return nil, err
		}
		created = append(created, id)
		openSets[sig] = struct{}{}
	}
	return created, nil
}

func loadSeedProducts(ctx context.Context, q querier) ([]seedProduct, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, name, COALESCE(unit_of_measure, ''), COALESCE(category, '')
		FROM products ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("could not prepare group suggestions: %w", err)
	}
	defer rows.Close()
	var out []seedProduct
	for rows.Next() {
		var p seedProduct
		if err := rows.Scan(&p.id, &p.name, &p.unit, &p.category); err != nil {
			return nil, fmt.Errorf("could not prepare group suggestions: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not prepare group suggestions: %w", err)
	}
	return out, nil
}

func loadSeedPrefs(ctx context.Context, scope tenant.Scope, q querier) ([]seedPref, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT bp.need_key, bp.ignore_price, i.product_id
		FROM brand_preferences bp
		LEFT JOIN items i ON i.id = bp.item_id AND i.household_id = bp.household_id
		WHERE bp.household_id = ?
		ORDER BY bp.need_key`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not prepare group suggestions: %w", err)
	}
	defer rows.Close()
	var out []seedPref
	for rows.Next() {
		var p seedPref
		var ignore int
		var productID sql.NullString
		if err := rows.Scan(&p.needKey, &ignore, &productID); err != nil {
			return nil, fmt.Errorf("could not prepare group suggestions: %w", err)
		}
		p.ignore = ignore == 1
		if productID.Valid {
			p.productID = productID.String
		} else {
			p.gone = true
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not prepare group suggestions: %w", err)
	}
	return out, nil
}

func clustersFrom(products []seedProduct, matches []Match, groupOf map[string]string, dismissed map[string]struct{}, title func(string) string) []seedCluster {
	byKey := map[string][]int{}
	for i, match := range matches {
		if match.Key == "" {
			continue
		}
		byKey[match.Key] = append(byKey[match.Key], i)
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []seedCluster
	for _, key := range keys {
		indexes := byKey[key]
		if len(indexes) < 2 {
			continue
		}
		ids := make([]string, len(indexes))
		cautionOf := map[string]string{}
		for i, idx := range indexes {
			ids[i] = products[idx].id
			cautionOf[ids[i]] = matches[idx].Caution
		}
		sort.Strings(ids)
		for _, piece := range splitDismissed(ids, dismissed) {
			card, ok := acceptPiece(piece, title(key), cautionOf, groupOf)
			if ok {
				out = append(out, card)
			}
		}
	}
	return out
}

func sameNeedClusters(products []seedProduct, needOf map[string]string, groupOf map[string]string, dismissed map[string]struct{}) []seedCluster {
	byKey := map[string][]string{}
	for _, p := range products {
		key := needOf[p.id]
		if key == "" {
			continue
		}
		byKey[key] = append(byKey[key], p.id)
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []seedCluster
	for _, key := range keys {
		ids := append([]string(nil), byKey[key]...)
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		name, _, _ := strings.Cut(key, "\x00")
		for _, piece := range splitDismissed(ids, dismissed) {
			card, ok := acceptPiece(piece, titleFromKey(name), nil, groupOf)
			if ok {
				out = append(out, card)
			}
		}
	}
	return out
}

func acceptPiece(ids []string, title string, cautionOf map[string]string, groupOf map[string]string) (seedCluster, bool) {
	if len(ids) < 2 {
		return seedCluster{}, false
	}
	existing, skip := existingGroup(ids, groupOf)
	if skip {
		return seedCluster{}, false
	}
	return seedCluster{title: title, ids: ids, caution: cautionOf, existing: existing}, true
}

func coveredBy(ids []string, wider []seedCluster) bool {
	have := map[string]struct{}{}
	for _, id := range ids {
		have[id] = struct{}{}
	}
	for _, card := range wider {
		if len(card.ids) < len(ids) {
			continue
		}
		n := 0
		for _, id := range card.ids {
			if _, ok := have[id]; ok {
				n++
			}
		}
		if n == len(ids) {
			return true
		}
	}
	return false
}

func matchPref(ids []string, prefs []seedPref, needMembers map[string][]string) *seedPref {
	in := map[string]struct{}{}
	for _, id := range ids {
		in[id] = struct{}{}
	}
	for i := range prefs {
		pref := &prefs[i]
		if pref.gone || pref.productID == "" {
			continue
		}
		if _, ok := in[pref.productID]; !ok {
			continue
		}
		members := needMembers[pref.needKey]
		if len(members) < 2 {
			continue
		}
		inside := true
		for _, id := range members {
			if _, ok := in[id]; !ok {
				inside = false
				break
			}
		}
		if inside {
			return pref
		}
	}
	return nil
}

func insertSeeded(ctx context.Context, scope tenant.Scope, tx *sql.Tx, card seedCluster, pref *seedPref) (string, error) {
	if err := scope.Require(); err != nil {
		return "", err
	}
	id := uuid.NewString()
	kind := "looks_alike"
	var rule, pin any
	if pref != nil {
		kind = "from_old_plan"
		rule = "best_deal"
		if pref.ignore {
			rule = "favorite"
		}
		pin = pref.productID
	}
	var existing any
	if card.existing != "" {
		existing = card.existing
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO group_suggestions (id, household_id, user_id, kind, title, proposed_rule, pinned_product_id, existing_group_id, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'open')`,
		id, scope.HouseholdID(), scope.HouseholdID(), kind, card.title, rule, pin, existing); err != nil {
		return "", fmt.Errorf("could not prepare group suggestions: %w", err)
	}
	style := majorityCaution(card.ids, card.caution)
	for _, productID := range card.ids {
		caution := ""
		if card.caution != nil {
			caution = card.caution[productID]
		}
		included := 1
		if card.caution != nil && caution != style {
			included = 0
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO group_suggestion_members (household_id, suggestion_id, product_id, included, caution)
			VALUES (?, ?, ?, ?, ?)`,
			scope.HouseholdID(), id, productID, included, caution); err != nil {
			return "", fmt.Errorf("could not prepare group suggestions: %w", err)
		}
	}
	return id, nil
}
