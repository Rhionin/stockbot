package app

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
)

// krogerLegacyConnectionCleanupKey is a one-time completion marker in app_settings.
// The cleanup runs in a single transaction with this key written only on success.
const krogerLegacyConnectionCleanupKey = "kroger_legacy_connection_cleanup_v1"

const legacyKrogerConnectionID = "user-1:kroger"

// RunKrogerLegacyConnectionCleanup removes duplicate legacy Kroger connection rows
// (PRIMARY KEY id user-1:kroger) when a household-scoped row already exists, or
// re-keys a lone legacy row to the canonical id and user_id (householdID:kroger
// / householdID) without changing tokens, app_key, or other payload columns.
// It is idempotent and fail-closed on ambiguous data.
func RunKrogerLegacyConnectionCleanup(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return nil
	}
	var done string
	err := db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, krogerLegacyConnectionCleanupKey).Scan(&done)
	if err == nil && done == "1" {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read kroger legacy connection cleanup flag: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var legacyHousehold, legacyUser string
	rowErr := tx.QueryRowContext(ctx, `
		SELECT pc.household_id, pc.user_id FROM provider_connections pc
		WHERE pc.id = ? AND pc.provider_id = 'kroger'
		  AND pc.household_id = pc.household_id`, legacyKrogerConnectionID).Scan(&legacyHousehold, &legacyUser)
	if rowErr == sql.ErrNoRows {
		if err := insertCleanupFlag(ctx, tx); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		log.Println("kroger legacy connection cleanup: no legacy row; marked complete")
		return nil
	}
	if rowErr != nil {
		return fmt.Errorf("read legacy kroger connection: %w", rowErr)
	}

	if strings.TrimSpace(legacyHousehold) == "" {
		return fmt.Errorf("kroger legacy connection cleanup: legacy row household_id is empty")
	}

	if _, err := uuid.Parse(legacyHousehold); err != nil {
		return fmt.Errorf("kroger legacy connection cleanup: legacy row household_id is not a valid household id")
	}
	var householdOK int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM households WHERE id = ?`, legacyHousehold).Scan(&householdOK); err == sql.ErrNoRows {
		return fmt.Errorf("kroger legacy connection cleanup: legacy row references an unknown household")
	} else if err != nil {
		return fmt.Errorf("kroger legacy connection cleanup: %w", err)
	}

	var canonCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM provider_connections
		WHERE provider_id = 'kroger' AND household_id = ? AND id != ?`,
		legacyHousehold, legacyKrogerConnectionID).Scan(&canonCount); err != nil {
		return fmt.Errorf("kroger legacy connection cleanup: count canonical rows: %w", err)
	}
	if canonCount > 1 {
		return fmt.Errorf("kroger legacy connection cleanup: multiple kroger rows for one household; manual review required")
	}

	if canonCount == 1 {
		var canonID, canonHousehold string
		if err := tx.QueryRowContext(ctx, `
			SELECT id, household_id FROM provider_connections
			WHERE provider_id = 'kroger' AND household_id = ? AND id != ?`,
			legacyHousehold, legacyKrogerConnectionID).Scan(&canonID, &canonHousehold); err != nil {
			return fmt.Errorf("kroger legacy connection cleanup: read canonical row: %w", err)
		}
		wantID := legacyHousehold + ":kroger"
		if canonID != wantID || canonHousehold != legacyHousehold {
			return fmt.Errorf("kroger legacy connection cleanup: canonical row id or household does not match expected form")
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM provider_connections
			WHERE id = ? AND provider_id = 'kroger' AND household_id = ?`,
			legacyKrogerConnectionID, legacyHousehold); err != nil {
			return fmt.Errorf("kroger legacy connection cleanup: delete duplicate legacy row: %w", err)
		}
		log.Printf("kroger legacy connection cleanup: removed duplicate legacy row for household %s", legacyHousehold)
	} else {
		if legacyUser != "user-1" {
			return fmt.Errorf("kroger legacy connection cleanup: lone legacy row has unexpected user_id")
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE provider_connections
			SET id = ?, user_id = ?
			WHERE id = ? AND provider_id = 'kroger' AND household_id = ?`,
			legacyHousehold+":kroger", legacyHousehold, legacyKrogerConnectionID, legacyHousehold)
		if err != nil {
			return fmt.Errorf("kroger legacy connection cleanup: rekey legacy row: %w", err)
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return fmt.Errorf("kroger legacy connection cleanup: rekey expected 1 row, got %d", n)
		}
		log.Printf("kroger legacy connection cleanup: rekeyed lone legacy row for household %s", legacyHousehold)
	}

	var badHouseholds int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT household_id FROM provider_connections WHERE provider_id = 'kroger'
			GROUP BY household_id HAVING COUNT(*) > 1
		)`).Scan(&badHouseholds); err != nil {
		return fmt.Errorf("kroger legacy connection cleanup: post-check duplicate households: %w", err)
	}
	if badHouseholds != 0 {
		return fmt.Errorf("kroger legacy connection cleanup: post-check found multiple kroger rows for a household")
	}

	var legacyLeft int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM provider_connections
		WHERE id = ? AND provider_id = 'kroger' AND household_id = ?`,
		legacyKrogerConnectionID, legacyHousehold).Scan(&legacyLeft); err != nil {
		return fmt.Errorf("kroger legacy connection cleanup: post-check legacy id: %w", err)
	}
	if legacyLeft != 0 {
		return fmt.Errorf("kroger legacy connection cleanup: legacy id still present after cleanup")
	}

	if err := insertCleanupFlag(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func insertCleanupFlag(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO app_settings (key, value) VALUES (?, '1')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, krogerLegacyConnectionCleanupKey)
	if err != nil {
		return fmt.Errorf("record kroger legacy connection cleanup flag: %w", err)
	}
	return nil
}
