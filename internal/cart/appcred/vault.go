// Package appcred stores cart-provider application credentials.
// A provider's client secret is readable only inside this package. Callers that
// build an HTTP response use Public, which has no secret field.
package appcred

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// Saved is one provider's application credentials, including the secret.
// It is not safe to marshal.
type Saved struct {
	ProviderID   string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Modality     string
	// ActorUserID is who saved the row. Only operator actors configure the
	// server-wide adapter at startup.
	ActorUserID string
}

// Public is the view safe to return from GET. It records that a secret exists
// without carrying the secret.
type Public struct {
	ClientID    string
	RedirectURI string
	Modality    string
	SecretSet   bool
	Source      string
}

// Vault is the data-access type for provider application credentials.
type Vault struct {
	db *sql.DB
}

// NewVault creates a Vault backed by the given database.
func NewVault(db *sql.DB) *Vault {
	return &Vault{db: db}
}

// Load returns the saved credentials for one provider. ok is false when no row
// exists. The secret is included for applying it to the provider, not for an
// HTTP response.
func (v *Vault) Load(ctx context.Context, scope tenant.Scope, providerID string) (Saved, bool, error) {
	if err := scope.Require(); err != nil {
		return Saved{}, false, err
	}
	if v == nil || v.db == nil {
		return Saved{}, false, nil
	}
	var saved Saved
	err := v.db.QueryRowContext(ctx, `
		SELECT provider_id, client_id, client_secret, redirect_uri, modality
		FROM provider_app_credentials
		WHERE household_id = ? AND provider_id = ?`,
		scope.HouseholdID(), providerID,
	).Scan(&saved.ProviderID, &saved.ClientID, &saved.ClientSecret, &saved.RedirectURI, &saved.Modality)
	if err == sql.ErrNoRows {
		return Saved{}, false, nil
	}
	if err != nil {
		return Saved{}, false, fmt.Errorf("read provider credentials: %w", err)
	}
	return saved, true, nil
}

// Public returns the non-secret view of a saved row. ok is false when nothing
// is saved. Source is "saved" when a row exists.
func (v *Vault) Public(ctx context.Context, scope tenant.Scope, providerID string) (Public, bool, error) {
	saved, ok, err := v.Load(ctx, scope, providerID)
	if err != nil || !ok {
		return Public{}, ok, err
	}
	return Public{
		ClientID:    saved.ClientID,
		RedirectURI: saved.RedirectURI,
		Modality:    saved.Modality,
		SecretSet:   saved.ClientSecret != "",
		Source:      "saved",
	}, true, nil
}

// Save inserts or replaces the saved credentials. An empty secret keeps the
// secret already stored. The first save must include a secret.
func (v *Vault) Save(ctx context.Context, scope tenant.Scope, saved Saved) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if v == nil || v.db == nil {
		return fmt.Errorf("credential store is not available")
	}
	existing, ok, err := v.Load(ctx, scope, saved.ProviderID)
	if err != nil {
		return err
	}
	secret := saved.ClientSecret
	if secret == "" {
		if !ok || existing.ClientSecret == "" {
			return fmt.Errorf("client secret is required")
		}
		secret = existing.ClientSecret
	}
	householdID := scope.HouseholdID()
	id := householdID + ":" + saved.ProviderID
	actor := strings.TrimSpace(saved.ActorUserID)
	_, err = v.db.ExecContext(ctx, `
		INSERT INTO provider_app_credentials
			(id, household_id, user_id, actor_user_id, provider_id, client_id, client_secret, redirect_uri, modality, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, provider_id) DO UPDATE SET
			client_id = excluded.client_id,
			client_secret = excluded.client_secret,
			redirect_uri = excluded.redirect_uri,
			modality = excluded.modality,
			actor_user_id = excluded.actor_user_id,
			updated_at = excluded.updated_at
		WHERE provider_app_credentials.household_id = excluded.household_id`,
		id, householdID, householdID, actor, saved.ProviderID, saved.ClientID, secret, saved.RedirectURI, saved.Modality, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("save provider credentials: %w", err)
	}
	return nil
}

// LoadServerProvider returns saved credentials that may configure the
// server-wide grocery adapter. The returned household ID is the row's
// household when ok is true. Rows stored under a household the operator
// does not belong to are ignored. Rows saved by a non-operator are ignored
// unless they are legacy primary-household rows with no actor recorded.
func (v *Vault) LoadServerProvider(ctx context.Context, providerID string) (Saved, string, bool, error) {
	if v == nil || v.db == nil {
		return Saved{}, "", false, nil
	}
	var saved Saved
	var householdID string
	err := v.db.QueryRowContext(ctx, `
		SELECT pac.household_id, pac.provider_id, pac.client_id, pac.client_secret, pac.redirect_uri, pac.modality
		FROM provider_app_credentials pac
		JOIN households h ON h.id = pac.household_id
		WHERE pac.provider_id = ?
		  AND (
		    (
		      EXISTS (SELECT 1 FROM users WHERE operator = 1 AND deleted_at IS NULL)
		      AND EXISTS (
		        SELECT 1 FROM memberships m
		        JOIN users u ON u.id = m.user_id AND u.operator = 1 AND u.deleted_at IS NULL
		        WHERE m.household_id = pac.household_id
		      )
		      AND (
		        pac.actor_user_id IN (SELECT id FROM users WHERE operator = 1 AND deleted_at IS NULL)
		        OR ((pac.actor_user_id IS NULL OR pac.actor_user_id = '') AND h.is_primary = 1)
		        OR (h.is_primary = 1 AND pac.actor_user_id = h.legacy_user_id AND h.legacy_user_id != '')
		      )
		    )
		    OR (
		      NOT EXISTS (SELECT 1 FROM users WHERE operator = 1 AND deleted_at IS NULL)
		      AND h.is_primary = 1
		    )
		  )
		ORDER BY h.is_primary DESC, pac.updated_at DESC
		LIMIT 1`, providerID,
	).Scan(&householdID, &saved.ProviderID, &saved.ClientID, &saved.ClientSecret, &saved.RedirectURI, &saved.Modality)
	if err == sql.ErrNoRows {
		return Saved{}, "", false, nil
	}
	if err != nil {
		return Saved{}, "", false, fmt.Errorf("read server provider credentials: %w", err)
	}
	return saved, householdID, true, nil
}

// Clear removes the saved credentials for one provider.
func (v *Vault) Clear(ctx context.Context, scope tenant.Scope, providerID string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if v == nil || v.db == nil {
		return nil
	}
	_, err := v.db.ExecContext(ctx, `
		DELETE FROM provider_app_credentials WHERE household_id = ? AND provider_id = ?`,
		scope.HouseholdID(), providerID,
	)
	if err != nil {
		return fmt.Errorf("clear provider credentials: %w", err)
	}
	return nil
}
