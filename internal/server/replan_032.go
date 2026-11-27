package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

const replan032Key = "list_replan_032"

// ReplanAfterGroupGoals refills each household's list once after migration
// 032, so a grouped product planned on its own before the move to one goal
// per group leaves the list at the first start. A household whose refill
// fails is tried again at the next start.
func ReplanAfterGroupGoals(ctx context.Context, db *sql.DB, now time.Time) error {
	rows, err := db.QueryContext(ctx, `
		SELECT h.id FROM households h
		WHERE h.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM household_settings s WHERE s.household_id = h.id AND s.key = ?)
		ORDER BY h.id`, replan032Key)
	if err != nil {
		return fmt.Errorf("could not read households to refresh: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("could not read households to refresh: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("could not read households to refresh: %w", err)
	}
	pantry := inventory.NewPantry(db)
	list := shopping.NewStore(db)
	plan := supply.Open(db)
	for _, id := range ids {
		scope, err := tenant.New(id)
		if err != nil {
			return err
		}
		if _, err := stageShoppingPlan(ctx, scope, pantry, list, plan, now); err != nil {
			log.Printf("list refresh after group goals: household %s: %v", id, err)
			continue
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO household_settings (household_id, key, value) VALUES (?, ?, ?)
			ON CONFLICT (household_id, key) DO NOTHING`,
			id, replan032Key, now.UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("could not record the list refresh: %w", err)
		}
	}
	return nil
}
