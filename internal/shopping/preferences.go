package shopping

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

const (
	// DealSourceRecorded is a sale the household noted themselves.
	DealSourceRecorded = "recorded"
	// DealSourceRetailer is a sale supplied by a store price adapter.
	DealSourceRetailer = "retailer"
)

// Deal is a sale price for one pantry item.
type Deal struct {
	ItemID            string
	PriceCents        *int
	RegularPriceCents *int
	Label             string
	Source            string
	// NotedAt is when the household recorded the sale. It is item_deals.updated_at.
	NotedAt time.Time `json:"-"`
}

// OnSale reports whether the household recorded this as a sale.
// A note with only a label, or a price and no higher regular price, counts.
func (d Deal) OnSale() bool {
	if strings.TrimSpace(d.Label) != "" {
		return true
	}
	if d.PriceCents == nil {
		return false
	}
	if d.RegularPriceCents == nil {
		return true
	}
	return *d.PriceCents < *d.RegularPriceCents
}

// ListDeals returns noted sales for the household, ordered by item.
func (s *Store) ListDeals(ctx context.Context, scope tenant.Scope) ([]Deal, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT item_id, price_cents, regular_price_cents, label, source, updated_at
		 FROM item_deals
		 WHERE household_id = ?
		 ORDER BY item_id`,
		scope.HouseholdID(),
	)
	if err != nil {
		return nil, fmt.Errorf("could not load sales: %w", err)
	}
	defer rows.Close()

	var deals []Deal
	for rows.Next() {
		deal, err := scanDeal(rows)
		if err != nil {
			return nil, fmt.Errorf("could not read sale: %w", err)
		}
		deals = append(deals, deal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not list sales: %w", err)
	}
	return deals, nil
}

// SaveDeal inserts or replaces the noted sale for one item.
// An empty source is stored as recorded.
func (s *Store) SaveDeal(ctx context.Context, scope tenant.Scope, deal Deal) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if deal.Source == "" {
		deal.Source = DealSourceRecorded
	}
	if deal.Source != DealSourceRecorded && deal.Source != DealSourceRetailer {
		return fmt.Errorf("could not save sale: unknown source %q", deal.Source)
	}
	household := scope.HouseholdID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not save sale: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO item_deals (household_id, user_id, item_id, price_cents, regular_price_cents, label, source, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, item_id) DO UPDATE SET
		   price_cents = excluded.price_cents,
		   regular_price_cents = excluded.regular_price_cents,
		   label = excluded.label,
		   source = excluded.source,
		   updated_at = excluded.updated_at`,
		household, household, deal.ItemID, nullableInt(deal.PriceCents), nullableInt(deal.RegularPriceCents),
		deal.Label, deal.Source, time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("could not save sale: %w", err)
	}
	if err := s.commitList(ctx, tx, household); err != nil {
		return fmt.Errorf("could not save sale: %w", err)
	}
	return nil
}

// DeleteDeal removes a noted sale. Missing rows are fine.
func (s *Store) DeleteDeal(ctx context.Context, scope tenant.Scope, itemID string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not clear sale: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM item_deals WHERE household_id = ? AND item_id = ?`,
		scope.HouseholdID(), itemID,
	); err != nil {
		return fmt.Errorf("could not clear sale: %w", err)
	}
	if err := s.commitList(ctx, tx, scope.HouseholdID()); err != nil {
		return fmt.Errorf("could not clear sale: %w", err)
	}
	return nil
}

func scanDeal(row scanner) (Deal, error) {
	var deal Deal
	var price, regular sql.NullInt64
	if err := row.Scan(&deal.ItemID, &price, &regular, &deal.Label, &deal.Source, &deal.NotedAt); err != nil {
		return Deal{}, err
	}
	deal.PriceCents = intPtrFromNull(price)
	deal.RegularPriceCents = intPtrFromNull(regular)
	return deal, nil
}

func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func intPtrFromNull(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}
