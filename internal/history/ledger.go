package history

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// Ledger is every stock move the household can see, newest first.
// SetupAt is when the opening stock-in finished, so the chart can start there.
type Ledger struct {
	Moves   []LedgerMove `json:"moves"`
	SetupAt string       `json:"setupAt,omitempty"`
}

// LedgerMove is one stock in or stock out, with the product it changed.
type LedgerMove struct {
	ID          string `json:"id"`
	Direction   string `json:"direction"`
	Quantity    int    `json:"quantity"`
	At          string `json:"at"`
	Source      string `json:"source"`
	ProductID   string `json:"productId"`
	ProductName string `json:"productName"`
	ImageURL    string `json:"imageUrl,omitempty"`
	GroupID     string `json:"groupId,omitempty"`
	GroupName   string `json:"groupName,omitempty"`
}

// LedgerFilter chooses which moves the household history includes.
// Direction is in, out, or all. Query matches a product name or a group name.
type LedgerFilter struct {
	Direction string
	Query     string
}

// historyListOrder is newest first. Moves that share a timestamp list the
// stock-in before the stock-out, then the id. A backfill pair is written at
// one scannedAt, so the id must not decide which move comes first.
const historyListOrder = "m.at DESC, CASE m.direction WHEN 'in' THEN 0 WHEN 'out' THEN 1 ELSE 2 END, m.id DESC"

// Household lists every stock move, newest first.
func (j *Journal) Household(ctx context.Context, scope tenant.Scope, filter LedgerFilter) (Ledger, error) {
	if err := scope.Require(); err != nil {
		return Ledger{}, err
	}
	direction, err := canonicalDirection(filter.Direction)
	if err != nil {
		return Ledger{}, err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return Ledger{}, err
	}
	defer tx.Rollback()
	if err := ensure(ctx, scope, tx); err != nil {
		return Ledger{}, err
	}
	moves, err := listHousehold(ctx, scope, tx, direction, strings.TrimSpace(filter.Query))
	if err != nil {
		return Ledger{}, err
	}
	setupAt, err := readSetupAt(ctx, tx)
	if err != nil {
		return Ledger{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ledger{}, err
	}
	return Ledger{Moves: moves, SetupAt: setupAt}, nil
}

func readSetupAt(ctx context.Context, tx *sql.Tx) (string, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'onboarding_started_at'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	started, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		// A bad stamp should not hide the history. The chart keeps its range.
		return "", nil
	}
	return started.UTC().Format(time.RFC3339), nil
}

func canonicalDirection(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return "", nil
	case "in":
		return "in", nil
	case "out":
		return "out", nil
	default:
		return "", &InputError{Message: "Choose in, out, or all."}
	}
}

func listHousehold(ctx context.Context, scope tenant.Scope, tx *sql.Tx, direction, query string) ([]LedgerMove, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	filter := ""
	args := []any{scope.HouseholdID()}
	if direction != "" {
		filter += ` AND m.direction = ?`
		args = append(args, direction)
	}
	if query != "" {
		filter += ` AND (INSTR(LOWER(p.name), LOWER(?)) > 0 OR INSTR(LOWER(COALESCE(g.name, '')), LOWER(?)) > 0)`
		args = append(args, query, query)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT m.id, m.direction, m.quantity, m.at, m.source,
			m.product_id, p.name, COALESCE(p.image_url, ''),
			COALESCE(g.id, ''), COALESCE(g.name, '')
		FROM stock_moves m
		JOIN items i ON i.id = m.item_id AND i.household_id = m.household_id
		JOIN products p ON p.id = m.product_id
		LEFT JOIN product_group_members gm ON gm.product_id = p.id AND gm.household_id = m.household_id
		LEFT JOIN product_groups g ON g.id = gm.group_id AND g.household_id = m.household_id
		WHERE m.household_id = ?`+filter+`
		ORDER BY `+historyListOrder, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	moves := []LedgerMove{}
	for rows.Next() {
		var move LedgerMove
		var at time.Time
		var image, groupID, groupName string
		if err := rows.Scan(
			&move.ID, &move.Direction, &move.Quantity, &at, &move.Source,
			&move.ProductID, &move.ProductName, &image, &groupID, &groupName,
		); err != nil {
			return nil, err
		}
		move.At = at.UTC().Format(time.RFC3339)
		move.ImageURL = product.SafeImageURL(image)
		move.GroupID = groupID
		move.GroupName = groupName
		moves = append(moves, move)
	}
	return moves, rows.Err()
}
