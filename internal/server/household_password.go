package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// ImportHouseholdPassword copies the shared bcrypt hash out of the Caddy
// basic_auth file once. A later start leaves the stored hash alone, so the
// process no longer needs that file. An empty path does nothing.
func ImportHouseholdPassword(ctx context.Context, db *sql.DB, authPath, sessionSecret string) error {
	var existing string
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(pw_hash, '') FROM household_password
		WHERE household_id = (SELECT id FROM households WHERE is_primary = 1)`).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("could not read the household password: %w", err)
	}
	if existing != "" {
		return nil
	}
	authPath = strings.TrimSpace(authPath)
	if authPath == "" {
		return nil
	}
	data, err := os.ReadFile(authPath)
	if err != nil {
		return fmt.Errorf("read the household password file: %w", err)
	}
	user, hash, err := parseCaddyBasicAuth(data)
	if err != nil {
		return err
	}
	if _, err := bcrypt.Cost([]byte(hash)); err != nil {
		return fmt.Errorf("the household password hash is not bcrypt")
	}
	key, ok := sessionKey(sessionSecret)
	var warn error
	if !ok {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return fmt.Errorf("could not start household login")
		}
		warn = fmt.Errorf("PANTRY_SESSION_SECRET is missing; sign-in works until this process restarts")
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	_, err = db.ExecContext(ctx, `
		INSERT INTO household_password (household_id, login_name, pw_hash, state, session_key)
		VALUES ((SELECT id FROM households WHERE is_primary = 1), ?, ?, 'on', ?)
		ON CONFLICT (household_id) DO UPDATE SET
			login_name = excluded.login_name,
			pw_hash = excluded.pw_hash,
			session_key = excluded.session_key,
			state = 'on'
		WHERE household_password.pw_hash IS NULL OR household_password.pw_hash = ''`,
		user, hash, encoded)
	if err != nil {
		return fmt.Errorf("could not store the household password: %w", err)
	}
	return warn
}

// HouseholdAuthFromDB loads the shared password from household_password.
// It does not read the Caddy file. A missing hash, or a password that is
// turned off, refuses the public hostname.
func HouseholdAuthFromDB(ctx context.Context, db *sql.DB) (*HouseholdAuth, error) {
	var login, hash, key, state string
	err := db.QueryRowContext(ctx, `
		SELECT login_name, COALESCE(pw_hash, ''), COALESCE(session_key, ''), state
		FROM household_password
		WHERE household_id = (SELECT id FROM households WHERE is_primary = 1)`).Scan(&login, &hash, &key, &state)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && hash == "") {
		return failClosed(), fmt.Errorf("no household password is stored")
	}
	if err != nil {
		return failClosed(), fmt.Errorf("could not read the household password: %w", err)
	}
	if state != "on" {
		return failClosed(), fmt.Errorf("household login is turned off")
	}
	secret, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(secret) == 0 {
		return failClosed(), fmt.Errorf("could not start household login")
	}
	return &HouseholdAuth{
		Username:   login,
		hash:       []byte(hash),
		secret:     secret,
		Configured: true,
		limiter:    newAttemptLimiter(),
	}, nil
}
