package history

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

const trailLimit = 40

// Journal reads and corrects stock moves.
type Journal struct {
	db *sql.DB
}

// NewJournal returns the household history.
func NewJournal(db *sql.DB) *Journal {
	return &Journal{db: db}
}

// Product is the history of one inventory item in this household.
func (j *Journal) Product(ctx context.Context, userID, itemID string, now time.Time) (View, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return View{}, err
	}
	defer tx.Rollback()
	if err := ensure(ctx, tx); err != nil {
		return View{}, err
	}
	view, err := productView(ctx, tx, userID, itemID, now)
	if err != nil {
		return View{}, err
	}
	if err := tx.Commit(); err != nil {
		return View{}, err
	}
	return view, nil
}

// Group is the history of every product in a group, added together.
func (j *Journal) Group(ctx context.Context, groupID string, now time.Time) (View, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return View{}, err
	}
	defer tx.Rollback()
	if err := ensure(ctx, tx); err != nil {
		return View{}, err
	}
	view, err := groupView(ctx, tx, groupID, now)
	if err != nil {
		return View{}, err
	}
	if err := tx.Commit(); err != nil {
		return View{}, err
	}
	return view, nil
}

func productView(ctx context.Context, tx *sql.Tx, userID, itemID string, now time.Time) (View, error) {
	var name, unit, dimension string
	var base sql.NullFloat64
	var pack sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT p.name, COALESCE(p.unit_of_measure, ''), p.net_base_value, COALESCE(p.net_dimension, ''), p.pack_count
		FROM items i
		JOIN products p ON p.id = i.product_id
		WHERE i.id = ? AND i.user_id = ?`, itemID, userID).Scan(&name, &unit, &base, &dimension, &pack)
	if err == sql.ErrNoRows {
		return View{}, &NotFoundError{Message: "That product was not found."}
	}
	if err != nil {
		return View{}, err
	}
	onHand, err := countOnHand(ctx, tx, []string{itemID})
	if err != nil {
		return View{}, err
	}
	uses, err := listUses(ctx, tx, []string{itemID}, now)
	if err != nil {
		return View{}, err
	}
	moves, err := listMoves(ctx, tx, []string{itemID})
	if err != nil {
		return View{}, err
	}
	return View{
		Kind:   "product",
		ID:     itemID,
		Name:   name,
		Detail: productDetail(unit, base, dimension, pack),
		OnHand: onHand,
		Pace:   PaceFromUses(now, onHand, uses),
		Moves:  moves,
	}, nil
}

func groupView(ctx context.Context, tx *sql.Tx, groupID string, now time.Time) (View, error) {
	var name string
	err := tx.QueryRowContext(ctx, `
		SELECT name FROM product_groups WHERE id = ? AND user_id = ?`, groupID, householdUser).Scan(&name)
	if err == sql.ErrNoRows {
		return View{}, &NotFoundError{Message: "That group was not found."}
	}
	if err != nil {
		return View{}, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT p.id, COALESCE(p.unit_of_measure, ''), p.net_base_value, COALESCE(p.net_dimension, ''), p.pack_count,
			i.id
		FROM product_group_members m
		JOIN products p ON p.id = m.product_id
		LEFT JOIN items i ON i.product_id = p.id AND i.user_id = ?
		WHERE m.group_id = ?
		ORDER BY p.name, p.id`, householdUser, groupID)
	if err != nil {
		return View{}, err
	}
	var details []string
	var itemIDs []string
	for rows.Next() {
		var productID, unit, dimension string
		var base sql.NullFloat64
		var pack sql.NullInt64
		var itemID sql.NullString
		if err := rows.Scan(&productID, &unit, &base, &dimension, &pack, &itemID); err != nil {
			rows.Close()
			return View{}, err
		}
		details = append(details, productDetail(unit, base, dimension, pack))
		if itemID.Valid && itemID.String != "" {
			itemIDs = append(itemIDs, itemID.String)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return View{}, err
	}
	rows.Close()

	onHand, err := countOnHand(ctx, tx, itemIDs)
	if err != nil {
		return View{}, err
	}
	uses, err := listUses(ctx, tx, itemIDs, now)
	if err != nil {
		return View{}, err
	}
	moves, err := listMoves(ctx, tx, itemIDs)
	if err != nil {
		return View{}, err
	}
	return View{
		Kind:   "group",
		ID:     groupID,
		Name:   name,
		Detail: sameDetail(details),
		OnHand: onHand,
		Pace:   PaceFromUses(now, onHand, uses),
		Moves:  moves,
	}, nil
}

func countOnHand(ctx context.Context, tx *sql.Tx, itemIDs []string) (int, error) {
	if len(itemIDs) == 0 {
		return 0, nil
	}
	query := `SELECT COUNT(*) FROM item_instances ii
		JOIN items i ON i.id = ii.item_id
		WHERE ii.removed_at IS NULL AND i.shelved = 0 AND ii.item_id IN (` + placeholders(len(itemIDs)) + `)`
	var n int
	err := tx.QueryRowContext(ctx, query, idsAsArgs(itemIDs)...).Scan(&n)
	return n, err
}

func listUses(ctx context.Context, tx *sql.Tx, itemIDs []string, now time.Time) ([]time.Time, error) {
	if len(itemIDs) == 0 {
		return nil, nil
	}
	query := `
		SELECT consumed_at FROM consumption_events
		WHERE consumed_at >= ? AND item_id IN (` + placeholders(len(itemIDs)) + `)
		ORDER BY consumed_at`
	args := append([]any{now.UTC().Add(-2 * paceWindow)}, idsAsArgs(itemIDs)...)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uses []time.Time
	for rows.Next() {
		var at time.Time
		if err := rows.Scan(&at); err != nil {
			return nil, err
		}
		uses = append(uses, at.UTC())
	}
	return uses, rows.Err()
}

func listMoves(ctx context.Context, tx *sql.Tx, itemIDs []string) ([]Move, error) {
	if len(itemIDs) == 0 {
		return []Move{}, nil
	}
	query := `
		SELECT m.id, m.direction, m.quantity, m.at, m.source, m.product_id, p.name
		FROM stock_moves m
		JOIN products p ON p.id = m.product_id
		WHERE m.item_id IN (` + placeholders(len(itemIDs)) + `)
		ORDER BY m.at DESC, m.id DESC
		LIMIT ?`
	args := append(idsAsArgs(itemIDs), trailLimit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	moves := []Move{}
	for rows.Next() {
		var move Move
		var at time.Time
		if err := rows.Scan(&move.ID, &move.Direction, &move.Quantity, &at, &move.Source, &move.ProductID, &move.ProductName); err != nil {
			return nil, err
		}
		move.At = at.UTC().Format(time.RFC3339)
		moves = append(moves, move)
	}
	return moves, rows.Err()
}

func loadMove(ctx context.Context, tx *sql.Tx, userID, id string) (moveRow, error) {
	var move moveRow
	var tracked, unshelved int
	err := tx.QueryRowContext(ctx, `
		SELECT m.id, m.item_id, m.product_id, m.direction, m.quantity, m.at, m.source,
			m.scan_entry_id, m.tracked, m.unshelved
		FROM stock_moves m
		JOIN items i ON i.id = m.item_id
		WHERE m.id = ? AND i.user_id = ?`, id, userID).Scan(
		&move.ID, &move.ItemID, &move.ProductID, &move.Direction, &move.Quantity, &move.At,
		&move.Source, &move.ScanEntryID, &tracked, &unshelved,
	)
	if err == sql.ErrNoRows {
		return moveRow{}, &NotFoundError{Message: "That move is no longer in the history."}
	}
	if err != nil {
		return moveRow{}, err
	}
	move.Tracked = tracked == 1
	move.Unshelved = unshelved == 1
	return move, nil
}

func placeholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ", ")
}

func idsAsArgs(ids []string) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}
