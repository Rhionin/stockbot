package supply

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Rhionin/stockbot/internal/restock"
	"github.com/Rhionin/stockbot/internal/shopping"
)

const (
	keyStarted    = "onboarding_started_at"
	keyMonths     = "supply_months"
	householdUser = "user-1"
)

func readPhaseAndMonths(ctx context.Context, tx *sql.Tx) (Phase, Months, error) {
	phase, err := readPhase(ctx, tx)
	if err != nil {
		return Phase{}, 0, err
	}
	months, err := readMonths(ctx, tx)
	if err != nil {
		return Phase{}, 0, err
	}
	return phase, months, nil
}

func readSettings(ctx context.Context, db *sql.DB) (Settings, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Settings{}, fmt.Errorf("could not read supply settings: %w", err)
	}
	defer tx.Rollback()
	phase, months, err := readPhaseAndMonths(ctx, tx)
	if err != nil {
		return Settings{}, err
	}
	if err := tx.Commit(); err != nil {
		return Settings{}, fmt.Errorf("could not read supply settings: %w", err)
	}
	settings := Settings{
		Months:     months,
		Opening:    phase.isOpening(),
		WipePhrase: WipePhrase,
	}
	if started, ok := phase.StartedAt(); ok {
		settings.StartedAt = started
	}
	return settings, nil
}

func readPhase(ctx context.Context, q queryer) (Phase, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, keyStarted).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Phase{}, nil
	}
	if err != nil {
		return Phase{}, fmt.Errorf("could not read the supply start date: %w", err)
	}
	started, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return Phase{}, fmt.Errorf("could not read the supply start date: %w", err)
	}
	return Phase{started: started.UTC()}, nil
}

func readMonths(ctx context.Context, q queryer) (Months, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, keyMonths).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultMonths, nil
	}
	if err != nil {
		return 0, fmt.Errorf("could not read the supply length: %w", err)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("could not read the supply length: %w", err)
	}
	months, err := ParseMonths(n)
	if err != nil {
		return 0, fmt.Errorf("could not read the supply length: %w", err)
	}
	return months, nil
}

func completeOpening(ctx context.Context, db *sql.DB, now time.Time) (Phase, error) {
	stamp := now.UTC().Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO app_settings (key, value)
		SELECT ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM app_settings WHERE key = ?)`,
		keyStarted, stamp, keyStarted,
	); err != nil {
		return Phase{}, fmt.Errorf("could not save the supply start date: %w", err)
	}
	return readPhase(ctx, db)
}

func writeMonths(ctx context.Context, db *sql.DB, months Months) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO app_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		keyMonths, strconv.Itoa(int(months)),
	)
	if err != nil {
		return fmt.Errorf("could not save the supply length: %w", err)
	}
	return nil
}

func writeOverride(ctx context.Context, db *sql.DB, id ProductID, o Override) error {
	if o.kind == overrideInherit {
		if _, err := db.ExecContext(ctx, `DELETE FROM supply_overrides WHERE product_id = ?`, string(id)); err != nil {
			return fmt.Errorf("could not clear the supply override: %w", err)
		}
		return nil
	}
	var window, qty any
	if months, ok := o.Window(); ok {
		window = int(months)
	}
	if units, ok := o.Quantity(); ok {
		qty = int(units)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO supply_overrides (product_id, window_months, quantity) VALUES (?, ?, ?)
		ON CONFLICT(product_id) DO UPDATE SET
			window_months = excluded.window_months,
			quantity = excluded.quantity`,
		string(id), window, qty,
	)
	if err != nil {
		return fmt.Errorf("could not save the supply override: %w", err)
	}
	return nil
}

func readOverride(ctx context.Context, db *sql.DB, id ProductID) (Override, error) {
	var window, qty sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT window_months, quantity FROM supply_overrides WHERE product_id = ?`,
		string(id),
	).Scan(&window, &qty)
	if errors.Is(err, sql.ErrNoRows) {
		return Override{}, nil
	}
	if err != nil {
		return Override{}, fmt.Errorf("could not read the supply override: %w", err)
	}
	if window.Valid {
		return Window(Months(window.Int64))
	}
	if qty.Valid {
		return Quantity(Units(qty.Int64))
	}
	return Override{}, nil
}

func wipeHousehold(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not wipe inventory: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`DELETE FROM shopping_list_entry_adjustments
		 WHERE entry_id IN (SELECT id FROM shopping_list_items WHERE user_id = ?)`,
		`DELETE FROM staged_cart_skips WHERE user_id = ?`,
		`DELETE FROM shopping_list_items WHERE user_id = ?`,
		`DELETE FROM consumption_events WHERE item_id IN (SELECT id FROM items WHERE user_id = ?)`,
		`DELETE FROM item_instances WHERE item_id IN (SELECT id FROM items WHERE user_id = ?)`,
		`DELETE FROM stock_in_events WHERE product_id IN (SELECT product_id FROM items WHERE user_id = ?)`,
		`DELETE FROM stock_moves WHERE item_id IN (SELECT id FROM items WHERE user_id = ?)`,
		`DELETE FROM item_targets WHERE item_id IN (SELECT id FROM items WHERE user_id = ?)`,
		`DELETE FROM item_categories WHERE item_id IN (SELECT id FROM items WHERE user_id = ?)`,
		`DELETE FROM category_suggestion_dismissals WHERE item_id IN (SELECT id FROM items WHERE user_id = ?)`,
		`DELETE FROM items WHERE user_id = ?`,
	}
	for _, query := range statements {
		if _, err := tx.ExecContext(ctx, query, householdUser); err != nil {
			return fmt.Errorf("could not wipe inventory: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_settings WHERE key = ?`, keyStarted); err != nil {
		return fmt.Errorf("could not wipe inventory: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not wipe inventory: %w", err)
	}
	return nil
}

func loadFacts(ctx context.Context, tx *sql.Tx, phase Phase) ([]Fact, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT i.id, i.product_id, p.name,
			p.net_base_value, COALESCE(p.net_dimension, ''),
			g.id, COALESCE(g.rule, ''), COALESCE(g.pinned_product_id, ''),
			g.window_months, g.quantity_base_value, COALESCE(g.quantity_dimension, ''),
			(SELECT COUNT(*) FROM item_instances inst
			 WHERE inst.item_id = i.id AND inst.removed_at IS NULL)
		FROM items i
		JOIN products p ON p.id = i.product_id
		LEFT JOIN product_group_members gm ON gm.product_id = i.product_id
		LEFT JOIN product_groups g ON g.id = gm.group_id AND g.user_id = i.user_id
		WHERE i.user_id = ?
		ORDER BY i.product_id, i.id`, householdUser)
	if err != nil {
		return nil, fmt.Errorf("could not read supply: %w", err)
	}
	defer rows.Close()

	facts := map[ProductID]*Fact{}
	var order []ProductID
	for rows.Next() {
		var itemID, id, name, netDimension, rule, pin, qtyDimension string
		var onHand int
		var netBase, qtyBase sql.NullFloat64
		var groupID sql.NullString
		var window sql.NullInt64
		if err := rows.Scan(
			&itemID, &id, &name,
			&netBase, &netDimension,
			&groupID, &rule, &pin,
			&window, &qtyBase, &qtyDimension,
			&onHand,
		); err != nil {
			return nil, fmt.Errorf("could not read supply: %w", err)
		}
		product := ProductID(id)
		fact := facts[product]
		if fact == nil {
			fact = &Fact{Product: product, Name: name, ItemID: itemID, NetDimension: netDimension}
			if netBase.Valid {
				value := netBase.Float64
				fact.NetBase = &value
			}
			if groupID.Valid {
				fact.Group = GroupID(groupID.String)
				fact.Rule = rule
				fact.Pinned = pin
				if window.Valid {
					fact.GroupWindow = int(window.Int64)
				}
				if qtyBase.Valid {
					fact.GroupHasQty = true
					fact.GroupBase = qtyBase.Float64
					fact.GroupDimension = qtyDimension
				}
			}
			facts[product] = fact
			order = append(order, product)
		}
		fact.OnHand += Units(onHand)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not read supply: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("could not read supply: %w", err)
	}

	if err := attachResolved(ctx, tx, facts); err != nil {
		return nil, err
	}
	if err := attachManuals(ctx, tx, facts); err != nil {
		return nil, err
	}
	if err := attachGroupSignals(ctx, tx, facts); err != nil {
		return nil, err
	}
	if started, ok := phase.StartedAt(); ok {
		if err := attachEvents(ctx, tx, facts, started); err != nil {
			return nil, err
		}
	}

	out := make([]Fact, 0, len(order))
	for _, id := range order {
		out = append(out, *facts[id])
	}
	return out, nil
}

func attachResolved(ctx context.Context, tx *sql.Tx, facts map[ProductID]*Fact) error {
	snap, err := restock.Load(ctx, tx, restock.HouseholdID)
	if err != nil {
		return err
	}
	byItem := map[string]restock.ItemView{}
	for _, item := range snap.Items {
		byItem[item.ItemID] = item
	}
	for _, fact := range facts {
		view, ok := byItem[fact.ItemID]
		if !ok {
			continue
		}
		fact.Resolved = restock.Resolution{
			Target: view.Target,
			Level:  view.Level,
			Name:   view.LevelName,
		}
	}
	return nil
}

func attachManuals(ctx context.Context, tx *sql.Tx, facts map[ProductID]*Fact) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT i.product_id, SUM(s.quantity)
		FROM shopping_list_items s
		JOIN items i ON i.id = s.item_id
		WHERE s.user_id = ? AND s.source = 'manual' AND s.purchased_at IS NULL
		GROUP BY i.product_id`, householdUser)
	if err != nil {
		return fmt.Errorf("could not read supply: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var qty int
		if err := rows.Scan(&id, &qty); err != nil {
			return fmt.Errorf("could not read supply: %w", err)
		}
		fact := facts[ProductID(id)]
		if fact == nil || qty <= 0 {
			continue
		}
		units := Units(qty)
		fact.Manual = &units
	}
	return rows.Err()
}

func attachEvents(ctx context.Context, tx *sql.Tx, facts map[ProductID]*Fact, started time.Time) error {
	// The sqlite driver stores each instant as a zone-specific string
	// ("2006-01-02 15:04:05 -0700 MST"). A SQL comparison of those strings
	// is alphabetical, so a Denver evening sorts before the UTC morning of
	// the same instant and a use after opening disappears from the plan.
	// Load the rows and compare the parsed instants.
	ins, err := tx.QueryContext(ctx, `
		SELECT product_id, at FROM stock_in_events
		WHERE product_id IN (SELECT product_id FROM items WHERE user_id = ?)
		ORDER BY product_id, at`, householdUser)
	if err != nil {
		return fmt.Errorf("could not read supply: %w", err)
	}
	defer ins.Close()
	for ins.Next() {
		var id string
		var at time.Time
		if err := ins.Scan(&id, &at); err != nil {
			return fmt.Errorf("could not read supply: %w", err)
		}
		if !at.After(started) {
			continue
		}
		if fact := facts[ProductID(id)]; fact != nil {
			fact.StockIns = append(fact.StockIns, at.UTC())
		}
	}
	if err := ins.Err(); err != nil {
		return fmt.Errorf("could not read supply: %w", err)
	}
	if err := ins.Close(); err != nil {
		return fmt.Errorf("could not read supply: %w", err)
	}

	outs, err := tx.QueryContext(ctx, `
		SELECT i.product_id, c.consumed_at
		FROM consumption_events c
		JOIN items i ON i.id = c.item_id
		WHERE i.user_id = ?
		ORDER BY i.product_id, c.consumed_at`, householdUser)
	if err != nil {
		return fmt.Errorf("could not read supply: %w", err)
	}
	defer outs.Close()
	for outs.Next() {
		var id string
		var at time.Time
		if err := outs.Scan(&id, &at); err != nil {
			return fmt.Errorf("could not read supply: %w", err)
		}
		if !at.After(started) {
			continue
		}
		if fact := facts[ProductID(id)]; fact != nil {
			fact.StockOuts = append(fact.StockOuts, Withdrawal{At: at.UTC(), Qty: 1})
		}
	}
	return outs.Err()
}

func attachGroupSignals(ctx context.Context, tx *sql.Tx, facts map[ProductID]*Fact) error {
	stocked, err := tx.QueryContext(ctx, `
		SELECT product_id, MAX(at) FROM stock_in_events
		WHERE product_id IN (SELECT product_id FROM items WHERE user_id = ?)
		GROUP BY product_id`, householdUser)
	if err != nil {
		return fmt.Errorf("could not read supply: %w", err)
	}
	defer stocked.Close()
	for stocked.Next() {
		var id, raw string
		if err := stocked.Scan(&id, &raw); err != nil {
			return fmt.Errorf("could not read supply: %w", err)
		}
		at, err := parseSQLiteTime(raw)
		if err != nil {
			return fmt.Errorf("could not read supply: %w", err)
		}
		if fact := facts[ProductID(id)]; fact != nil {
			fact.LastStocked = at
		}
	}
	if err := stocked.Err(); err != nil {
		return fmt.Errorf("could not read supply: %w", err)
	}
	if err := stocked.Close(); err != nil {
		return fmt.Errorf("could not read supply: %w", err)
	}

	used, err := tx.QueryContext(ctx, `
		SELECT i.product_id, MAX(c.consumed_at)
		FROM consumption_events c
		JOIN items i ON i.id = c.item_id
		WHERE i.user_id = ?
		GROUP BY i.product_id`, householdUser)
	if err != nil {
		return fmt.Errorf("could not read supply: %w", err)
	}
	defer used.Close()
	for used.Next() {
		var id, raw string
		if err := used.Scan(&id, &raw); err != nil {
			return fmt.Errorf("could not read supply: %w", err)
		}
		at, err := parseSQLiteTime(raw)
		if err != nil {
			return fmt.Errorf("could not read supply: %w", err)
		}
		if fact := facts[ProductID(id)]; fact != nil {
			fact.LastConsumed = at
		}
	}
	if err := used.Err(); err != nil {
		return fmt.Errorf("could not read supply: %w", err)
	}
	if err := used.Close(); err != nil {
		return fmt.Errorf("could not read supply: %w", err)
	}

	deals, err := tx.QueryContext(ctx, `
		SELECT item_id, price_cents, regular_price_cents, label, updated_at
		FROM item_deals WHERE user_id = ?`, householdUser)
	if err != nil {
		return fmt.Errorf("could not read supply: %w", err)
	}
	defer deals.Close()
	byItem := map[string]*Fact{}
	for _, fact := range facts {
		if fact.ItemID != "" {
			byItem[fact.ItemID] = fact
		}
	}
	for deals.Next() {
		var itemID, label string
		var price, regular sql.NullInt64
		var noted time.Time
		if err := deals.Scan(&itemID, &price, &regular, &label, &noted); err != nil {
			return fmt.Errorf("could not read supply: %w", err)
		}
		fact := byItem[itemID]
		if fact == nil {
			continue
		}
		deal := shoppingDeal(itemID, label, noted, price, regular)
		fact.Deal = &deal
	}
	return deals.Err()
}

func parseSQLiteTime(raw string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
	}
	var last error
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed.UTC(), nil
		}
		last = err
	}
	return time.Time{}, last
}

func shoppingDeal(itemID, label string, noted time.Time, price, regular sql.NullInt64) shopping.Deal {
	deal := shopping.Deal{ItemID: itemID, Label: label, NotedAt: noted.UTC(), Source: shopping.DealSourceRecorded}
	if price.Valid {
		cents := int(price.Int64)
		deal.PriceCents = &cents
	}
	if regular.Valid {
		cents := int(regular.Int64)
		deal.RegularPriceCents = &cents
	}
	return deal
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
