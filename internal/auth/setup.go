package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// setupCommit is the transaction commit. Tests replace it to simulate a failure
// after the display file has been renamed.
var setupCommit = func(tx *sql.Tx) error { return tx.Commit() }

var setupCodeLine = regexp.MustCompile(`Stockbot setup code: ([0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4})`)

const setupHost = "pi"

func (s *Service) codePath() string { return filepath.Join(s.setupDir, "code") }
func (s *Service) tmpPath() string  { return filepath.Join(s.setupDir, "code.tmp") }

// SetupHealth is "unwritable" or "no_space" while no account exists.
func (s *Service) SetupHealth(ctx context.Context) string {
	has, err := s.hasAccount(ctx)
	if err != nil || has {
		return ""
	}
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	return s.setupHealth
}

func (s *Service) noteFile(err error) {
	kind := "unwritable"
	if errors.Is(err, syscall.ENOSPC) || sqliteNoSpace(err) {
		kind = "no_space"
	}
	var ae *Error
	if errors.As(err, &ae) && ae.Code == CodeNoSpace {
		kind = "no_space"
	}
	s.healthMu.Lock()
	s.setupHealth = kind
	s.healthMu.Unlock()
}

func (s *Service) clearHealth() {
	s.healthMu.Lock()
	s.setupHealth = ""
	s.healthMu.Unlock()
}

// Boot issues a setup code when nobody has an account, and removes a leftover
// display file once an account exists and no recovery code is outstanding.
func (s *Service) Boot(ctx context.Context) error {
	has, err := s.hasAccount(ctx)
	if err != nil {
		return err
	}
	if has {
		s.clearHealth()
		mode, valid, err := s.validUnused(ctx)
		if err != nil {
			return err
		}
		if !valid || mode == "setup" {
			_ = os.Remove(s.codePath())
		}
		return nil
	}
	return s.Ensure(ctx)
}

// Ensure self-heals a setup code. It does not replace a valid matching code
// or an outstanding recovery code.
func (s *Service) Ensure(ctx context.Context) error {
	return s.issue(ctx, "setup", true)
}

// AdminIssue writes a setup or recovery code. A setup code is refused once
// an account exists. Recovery requires an operator. A failure leaves the
// display file as it was.
func (s *Service) AdminIssue(ctx context.Context, recover bool) error {
	has, err := s.hasAccount(ctx)
	if err != nil {
		return err
	}
	if !recover && has {
		return errors.New("an account already exists")
	}
	if recover && !has {
		return errors.New("there is no account to recover")
	}
	if recover {
		op, err := s.hasOperator(ctx, s.db)
		if err != nil {
			return err
		}
		if !op {
			return errors.New("there is no operator account to recover")
		}
	}
	mode := "setup"
	if recover {
		mode = "recover"
	}
	return s.issue(ctx, mode, false)
}

// RedeemSetup creates the first operator or replaces the operator password.
// The code is marked used in that same transaction. No session is started.
func (s *Service) RedeemSetup(ctx context.Context, setupCode, name, email, password, newPassword string) error {
	norm, ok := NormalizeSetupCode(setupCode)
	if !ok {
		return notFound()
	}
	emailNorm, ok := normalizeEmail(email)
	if !ok {
		return validation()
	}
	recover := newPassword != ""
	if recover && (password != "" || strings.TrimSpace(name) != "") {
		return validation()
	}
	if !recover && password == "" {
		return validation()
	}
	secret := password
	if recover {
		secret = newPassword
	}
	if err := passwordOK(secret); err != nil {
		return err
	}
	var person string
	if !recover {
		var nerr error
		person, nerr = normalizeName(name, "name")
		if nerr != nil {
			return nerr
		}
	}
	pwHash, err := HashPassword(ctx, secret)
	if err != nil {
		return AsError(err)
	}
	match, found, err := s.matchSetup(ctx, norm)
	if err != nil {
		return err
	}
	if !found {
		return notFound()
	}
	if match.mode == "recover" && !recover || match.mode == "setup" && recover {
		return notFound()
	}
	var operatorID string
	if recover {
		var op int
		err := s.db.QueryRowContext(ctx, `
			SELECT id, operator FROM users WHERE email_norm = ? AND deleted_at IS NULL`, emailNorm).Scan(&operatorID, &op)
		if err == sql.ErrNoRows || op != 1 {
			return notFound()
		}
		if err != nil && err != sql.ErrNoRows {
			return AsError(err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AsError(err)
	}
	defer tx.Rollback()
	var storedHash, mode, issued string
	var used int
	err = tx.QueryRowContext(ctx, `
		SELECT code_hash, mode, issued_at, used FROM setup_codes WHERE id = ?`, match.id).Scan(&storedHash, &mode, &issued, &used)
	if err != nil || used != 0 || storedHash != match.hash {
		return notFound()
	}
	now := s.clock()
	if mode == "setup" {
		has, err := hasAccountQ(ctx, tx)
		if err != nil {
			return AsError(err)
		}
		if has {
			return notFound()
		}
		scope, err := PrimaryScope(ctx, tx)
		if err != nil {
			return AsError(err)
		}
		userID := newID()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
			VALUES (?, ?, ?, ?, 'local', 1, ?)`,
			userID, person, emailNorm, pwHash, stamp(now)); err != nil {
			return AsError(err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO memberships (household_id, user_id, role, created_at)
			VALUES (?, ?, 'owner', ?)`, scope.HouseholdID(), userID, stamp(now)); err != nil {
			return AsError(err)
		}
	} else {
		res, err := tx.ExecContext(ctx, `
			UPDATE users SET pw_hash = ?
			WHERE id = ? AND operator = 1 AND deleted_at IS NULL`, pwHash, operatorID)
		if err != nil {
			return AsError(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound()
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, stamp(now), operatorID); err != nil {
			return AsError(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM reset_links WHERE user_id = ?`, operatorID); err != nil {
			return AsError(err)
		}
		mem, err := listMemberships(ctx, tx, operatorID)
		if err != nil {
			return AsError(err)
		}
		for _, m := range mem {
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM push_subscriptions WHERE household_id = ? AND user_id = ?`, m.HouseholdID, operatorID); err != nil {
				return AsError(err)
			}
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE setup_codes SET used = 1 WHERE id = ? AND used = 0`, match.id)
	if err != nil {
		return AsError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound()
	}
	if err := setupCommit(tx); err != nil {
		return AsError(err)
	}
	s.clearHealth()
	if recover {
		s.streams.Kick(UserStreamKey(operatorID))
		s.kickLive("", operatorID)
	}
	return nil
}

type setupMatch struct {
	id   string
	hash string
	mode string
}

func (s *Service) matchSetup(ctx context.Context, code string) (setupMatch, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, code_hash, mode, issued_at, used FROM setup_codes WHERE used = 0`)
	if err != nil {
		return setupMatch{}, false, AsError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, hash, mode, issued string
		var used int
		if err := rows.Scan(&id, &hash, &mode, &issued, &used); err != nil {
			return setupMatch{}, false, AsError(err)
		}
		at, ok := parseStamp(issued)
		if !ok || setupExpired(at, s.clock()) {
			continue
		}
		match, err := VerifyPassword(ctx, hash, code)
		if err != nil {
			return setupMatch{}, false, AsError(err)
		}
		if match {
			return setupMatch{id: id, hash: hash, mode: mode}, true, nil
		}
	}
	return setupMatch{}, false, AsError(rows.Err())
}

func (s *Service) issue(ctx context.Context, mode string, auto bool) error {
	if auto {
		keep, err := s.keepCurrent(ctx)
		if err != nil || keep {
			return err
		}
	}
	code, err := NewSetupCode()
	if err != nil {
		return AsError(err)
	}
	hash, err := HashPassword(ctx, code)
	if err != nil {
		return AsError(err)
	}
	issued := s.clock()
	if err := s.writeTemp(displayBody(code, mode, issued)); err != nil {
		s.noteFile(err)
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.removeTemp()
		return AsError(err)
	}
	defer tx.Rollback()
	if auto {
		keep, err := s.reusable(ctx, tx)
		if err != nil {
			s.removeTemp()
			return err
		}
		if keep {
			s.removeTemp()
			return nil
		}
		has, err := hasAccountQ(ctx, tx)
		if err != nil {
			s.removeTemp()
			return AsError(err)
		}
		if has {
			s.removeTemp()
			return nil
		}
	} else if mode == "setup" {
		has, err := hasAccountQ(ctx, tx)
		if err != nil {
			s.removeTemp()
			return AsError(err)
		}
		if has {
			s.removeTemp()
			return errors.New("an account already exists")
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE setup_codes SET used = 1 WHERE used = 0`); err != nil {
		s.removeTemp()
		return AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO setup_codes (id, code_hash, mode, issued_at, used)
		VALUES (?, ?, ?, ?, 0)`, newID(), hash, mode, stamp(issued)); err != nil {
		s.removeTemp()
		return AsError(err)
	}
	if err := os.Rename(s.tmpPath(), s.codePath()); err != nil {
		s.removeTemp()
		s.noteFile(err)
		return fileErr(err)
	}
	if err := setupCommit(tx); err != nil {
		s.removeTemp()
		return AsError(err)
	}
	s.clearHealth()
	slog.Info("setup code issued", "path", s.codePath(), "expires", stamp(issued.Add(codeLife)))
	return nil
}

func (s *Service) keepCurrent(ctx context.Context) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, AsError(err)
	}
	defer tx.Rollback()
	keep, err := s.reusable(ctx, tx)
	if err != nil || !keep {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, AsError(err)
	}
	return true, nil
}

func (s *Service) reusable(ctx context.Context, q queryer) (bool, error) {
	var hash, mode, issued string
	err := q.QueryRowContext(ctx, `
		SELECT code_hash, mode, issued_at FROM setup_codes
		WHERE used = 0 ORDER BY issued_at DESC LIMIT 1`).Scan(&hash, &mode, &issued)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, AsError(err)
	}
	at, ok := parseStamp(issued)
	if !ok || setupExpired(at, s.clock()) {
		return false, nil
	}
	if mode == "recover" {
		return true, nil
	}
	return s.fileMatches(ctx, hash, mode, issued), nil
}

func (s *Service) fileMatches(ctx context.Context, hash, mode, issued string) bool {
	body, err := os.ReadFile(s.codePath())
	if err != nil {
		return false
	}
	text := string(body)
	m := setupCodeLine.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	if !strings.Contains(text, "mode: "+mode) || !strings.Contains(text, "issued_at: "+issued) {
		return false
	}
	ok, err := VerifyPassword(ctx, hash, m[1])
	return err == nil && ok
}

// ActiveSetupMode reports the unused unexpired setup or recover code, if one is current.
func (s *Service) ActiveSetupMode(ctx context.Context) (string, bool, error) {
	return s.validUnused(ctx)
}

func (s *Service) validUnused(ctx context.Context) (string, bool, error) {
	var mode, issued string
	err := s.db.QueryRowContext(ctx, `
		SELECT mode, issued_at FROM setup_codes WHERE used = 0 ORDER BY issued_at DESC LIMIT 1`).Scan(&mode, &issued)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, AsError(err)
	}
	at, ok := parseStamp(issued)
	if !ok || setupExpired(at, s.clock()) {
		return mode, false, nil
	}
	return mode, true, nil
}

func (s *Service) writeTemp(body []byte) error {
	if strings.TrimSpace(s.setupDir) == "" {
		return fileErr(errors.New("setup directory is not configured"))
	}
	f, err := os.OpenFile(s.tmpPath(), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return fileErr(err)
	}
	_, werr := f.Write(body)
	cerr := f.Close()
	if werr != nil {
		s.removeTemp()
		return fileErr(werr)
	}
	if cerr != nil {
		s.removeTemp()
		return fileErr(cerr)
	}
	if err := os.Chmod(s.tmpPath(), 0o640); err != nil {
		s.removeTemp()
		return fileErr(err)
	}
	return nil
}

func (s *Service) removeTemp() { _ = os.Remove(s.tmpPath()) }

func fileErr(err error) *Error {
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ENOSPC) {
		return noSpace(err)
	}
	return &Error{Code: "unwritable", Message: "The setup code could not be written.", Err: err}
}

func displayBody(code, mode string, issued time.Time) []byte {
	text := "Stockbot setup code: " + code + " (open http://" + setupHost + ":8080/setup on your home network; expires in 24 h)\n" +
		"mode: " + mode + "\n" +
		"issued_at: " + stamp(issued) + "\n"
	return []byte(text)
}

func setupExpired(issued, now time.Time) bool {
	if now.Sub(issued) >= codeLife {
		return true
	}
	if issued.Sub(now) > codeLife {
		return true
	}
	return false
}

// ReadSetupCode returns the display code. Tests use it; the server does not log it.
func ReadSetupCode(dir string) (string, error) {
	body, err := os.ReadFile(filepath.Join(dir, "code"))
	if err != nil {
		return "", err
	}
	m := setupCodeLine.FindStringSubmatch(string(body))
	if m == nil {
		return "", errors.New("setup code file has no code")
	}
	return m[1], nil
}
