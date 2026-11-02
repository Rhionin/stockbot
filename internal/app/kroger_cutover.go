package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"
)

const krogerAppCutoverKey = "kroger_app_cutover_v1"

// KrogerCutoverEnv carries deploy-time Kroger app values used once at cutover.
type KrogerCutoverEnv struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Modality     string
}

// RunKrogerAppCutover copies the env app into the primary household when needed
// and removes other households' shared-app Kroger state. Idempotent.
func RunKrogerAppCutover(ctx context.Context, db *sql.DB, env KrogerCutoverEnv) error {
	if db == nil {
		return nil
	}
	var done string
	err := db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, krogerAppCutoverKey).Scan(&done)
	if err == nil && done == "1" {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read cutover flag: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var primaryID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM households WHERE is_primary = 1 LIMIT 1`).Scan(&primaryID)
	if err == sql.ErrNoRows {
		primaryID = ""
	} else if err != nil {
		return fmt.Errorf("read primary household: %w", err)
	}

	modality := strings.ToUpper(strings.TrimSpace(env.Modality))
	if modality == "" {
		modality = "PICKUP"
	}
	clientID := strings.TrimSpace(env.ClientID)
	clientSecret := strings.TrimSpace(env.ClientSecret)
	redirectURI := strings.TrimSpace(env.RedirectURI)
	envComplete := clientID != "" && clientSecret != "" && redirectURI != ""

	if primaryID != "" {
		var existing int
		_ = tx.QueryRowContext(ctx, `
			SELECT 1 FROM provider_app_credentials
			WHERE household_id = ? AND provider_id = 'kroger' LIMIT 1`, primaryID).Scan(&existing)
		if existing == 0 && envComplete {
			now := time.Now().UTC().Format(time.RFC3339)
			_, err = tx.ExecContext(ctx, `
				INSERT INTO provider_app_credentials
					(id, household_id, user_id, actor_user_id, provider_id, client_id, client_secret, redirect_uri, modality, updated_at)
				VALUES (?, ?, ?, NULL, 'kroger', ?, ?, ?, ?, ?)`,
				primaryID+":kroger", primaryID, primaryID, clientID, clientSecret, redirectURI, modality, now)
			if err != nil {
				return fmt.Errorf("seed primary kroger app: %w", err)
			}
		}
		var keySource string
		rowErr := tx.QueryRowContext(ctx, `
			SELECT client_id FROM provider_app_credentials
			WHERE household_id = ? AND provider_id = 'kroger'`, primaryID).Scan(&keySource)
		if rowErr == nil && keySource != "" {
			appKey := krogerAppKey(keySource)
			if _, err = tx.ExecContext(ctx, `
				UPDATE provider_connections
				SET app_key = ?
				WHERE household_id = ? AND provider_id = 'kroger' AND app_key IS NULL`,
				appKey, primaryID); err != nil {
				return fmt.Errorf("set primary app_key: %w", err)
			}
		}
	}

	res, err := tx.ExecContext(ctx, `
		DELETE FROM provider_connections
		WHERE provider_id = 'kroger'
		  AND household_id IN (SELECT id FROM households WHERE is_primary = 0)`)
	if err != nil {
		return fmt.Errorf("delete non-primary connections: %w", err)
	}
	connRemoved, _ := res.RowsAffected()

	res, err = tx.ExecContext(ctx, `
		DELETE FROM store_availability
		WHERE household_id IN (SELECT id FROM households WHERE is_primary = 0)`)
	if err != nil {
		return fmt.Errorf("delete non-primary availability: %w", err)
	}
	availRemoved, _ := res.RowsAffected()

	res, err = tx.ExecContext(ctx, `
		DELETE FROM store_checks
		WHERE household_id IN (SELECT id FROM households WHERE is_primary = 0)`)
	if err != nil {
		return fmt.Errorf("delete non-primary store checks: %w", err)
	}
	checksRemoved, _ := res.RowsAffected()

	res, err = tx.ExecContext(ctx, `
		DELETE FROM provider_app_credentials
		WHERE provider_id = 'kroger'
		  AND household_id IN (SELECT id FROM households WHERE is_primary = 0)`)
	if err != nil {
		return fmt.Errorf("delete non-primary app rows: %w", err)
	}
	credsRemoved, _ := res.RowsAffected()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO app_settings (key, value) VALUES (?, '1')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, krogerAppCutoverKey); err != nil {
		return fmt.Errorf("record cutover flag: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("kroger app cutover: connections removed=%d availability removed=%d store_checks removed=%d app rows removed=%d",
		connRemoved, availRemoved, checksRemoved, credsRemoved)
	return nil
}

func krogerAppKey(clientID string) string {
	sum := sha256.Sum256([]byte(clientID))
	return hex.EncodeToString(sum[:])[:16]
}
