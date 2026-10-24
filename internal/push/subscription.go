package push

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// Subscription is one browser registered for scan alerts.
// Origin is the site hostname that registered it. Empty means the row was
// stored before origins existed. UserID and DeviceID name the household
// member and browser so a later subscribe can replace older endpoints.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
	Origin   string
	UserID   string
	DeviceID string
}

// Problem is a sentence for the person who sent the subscription, or empty
// when the browser payload can be stored.
func (s Subscription) Problem() string {
	endpoint := strings.TrimSpace(s.Endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "Enter a push subscription from this browser."
	}
	if len(endpoint) > 2000 || !pushKey(s.P256dh, 200) || !pushKey(s.Auth, 100) {
		return "Enter a push subscription from this browser."
	}
	if !optionalToken(s.UserID, 100) || !optionalToken(s.DeviceID, 100) {
		return "Enter a push subscription from this browser."
	}
	return ""
}

func pushKey(value string, max int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > max || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	return true
}

func optionalToken(value string, max int) bool {
	if value == "" {
		return true
	}
	return pushKey(value, max)
}

// Subscriptions stores the browsers that asked for scan alerts.
type Subscriptions struct {
	db *sql.DB
}

// NewSubscriptions uses the pantry database.
func NewSubscriptions(db *sql.DB) *Subscriptions {
	return &Subscriptions{db: db}
}

// Save inserts or replaces the keys for an endpoint. A browser refreshes
// those keys without changing the endpoint. A non-empty origin, user, or
// device replaces a blank one already stored. When both the user and the
// device are set, older endpoints for that pair are removed.
func (s *Subscriptions) Save(ctx context.Context, scope tenant.Scope, sub Subscription) error {
	if err := scope.Require(); err != nil {
		return err
	}
	sub.Endpoint = strings.TrimSpace(sub.Endpoint)
	sub.P256dh = strings.TrimSpace(sub.P256dh)
	sub.Auth = strings.TrimSpace(sub.Auth)
	sub.Origin = canonicalHost(sub.Origin)
	sub.DeviceID = strings.TrimSpace(sub.DeviceID)
	sub.UserID = strings.TrimSpace(sub.UserID)
	// A household-password or LAN browser has no account. NULL keeps that
	// distinct from a member, and a key refresh without a user does not
	// wipe an account id already stored.
	var user any
	if sub.UserID != "" {
		user = sub.UserID
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save push subscription: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO push_subscriptions (endpoint, household_id, p256dh, auth, origin, user_id, device_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(household_id, endpoint) DO UPDATE SET
			p256dh = excluded.p256dh,
			auth = excluded.auth,
			origin = CASE WHEN excluded.origin != '' THEN excluded.origin ELSE push_subscriptions.origin END,
			user_id = COALESCE(excluded.user_id, push_subscriptions.user_id),
			device_id = CASE WHEN excluded.device_id != '' THEN excluded.device_id ELSE push_subscriptions.device_id END,
			updated_at = CURRENT_TIMESTAMP
		WHERE push_subscriptions.household_id = excluded.household_id`,
		sub.Endpoint, scope.HouseholdID(), sub.P256dh, sub.Auth, sub.Origin, user, sub.DeviceID,
	)
	if err != nil {
		return fmt.Errorf("save push subscription: %w", err)
	}
	if sub.DeviceID != "" {
		_, err = tx.ExecContext(ctx, `
			DELETE FROM push_subscriptions
			WHERE household_id = ? AND endpoint != ? AND device_id = ?`,
			scope.HouseholdID(), sub.Endpoint, sub.DeviceID,
		)
		if err != nil {
			return fmt.Errorf("save push subscription: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save push subscription: %w", err)
	}
	return nil
}

// Delete removes one endpoint. A missing row is not an error.
func (s *Subscriptions) Delete(ctx context.Context, scope tenant.Scope, endpoint string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE household_id = ? AND endpoint = ?`, scope.HouseholdID(), strings.TrimSpace(endpoint))
	if err != nil {
		return fmt.Errorf("delete push subscription: %w", err)
	}
	return nil
}

// DeleteHost removes every subscription registered at host. Zero rows is
// not an error. An empty host removes nothing, so a blank origin is kept.
func (s *Subscriptions) DeleteHost(ctx context.Context, scope tenant.Scope, host string) (int, error) {
	if err := scope.Require(); err != nil {
		return 0, err
	}
	host = canonicalHost(host)
	if host == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE household_id = ? AND origin = ?`, scope.HouseholdID(), host)
	if err != nil {
		return 0, fmt.Errorf("delete push subscriptions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete push subscriptions: %w", err)
	}
	return int(n), nil
}

// List returns every stored browser, oldest first.
func (s *Subscriptions) List(ctx context.Context, scope tenant.Scope) ([]Subscription, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT endpoint, p256dh, auth, origin, user_id, device_id
		FROM push_subscriptions
		WHERE household_id = ?
		ORDER BY created_at, endpoint`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		var sub Subscription
		var user sql.NullString
		if err := rows.Scan(&sub.Endpoint, &sub.P256dh, &sub.Auth, &sub.Origin, &user, &sub.DeviceID); err != nil {
			return nil, fmt.Errorf("list push subscriptions: %w", err)
		}
		if user.Valid {
			sub.UserID = user.String
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	return out, nil
}

// OperatorSubscription is a browser registered by an operator account.
type OperatorSubscription struct {
	Subscription
	HouseholdID string
}

// ListOperator returns subscriptions whose user_id is an operator account.
func (s *Subscriptions) ListOperator(ctx context.Context) ([]OperatorSubscription, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("list operator push subscriptions")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT push_subscriptions.endpoint, push_subscriptions.p256dh, push_subscriptions.auth,
		       push_subscriptions.origin, push_subscriptions.user_id, push_subscriptions.device_id,
		       push_subscriptions.household_id
		FROM push_subscriptions
		JOIN users ON users.id = push_subscriptions.user_id
		WHERE users.operator = 1 AND users.deleted_at IS NULL AND push_subscriptions.household_id != ''
		ORDER BY push_subscriptions.created_at, push_subscriptions.endpoint`)
	if err != nil {
		return nil, fmt.Errorf("list operator push subscriptions: %w", err)
	}
	defer rows.Close()
	var out []OperatorSubscription
	for rows.Next() {
		var sub OperatorSubscription
		if err := rows.Scan(&sub.Endpoint, &sub.P256dh, &sub.Auth, &sub.Origin, &sub.UserID, &sub.DeviceID, &sub.HouseholdID); err != nil {
			return nil, fmt.Errorf("list operator push subscriptions: %w", err)
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list operator push subscriptions: %w", err)
	}
	return out, nil
}
