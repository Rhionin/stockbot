// Package changes records a per-household cursor for each live scope.
// A cursor moves in the same transaction as the write, including deletes,
// so a later changes-since read cannot report that nothing changed.
package changes

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	ScopeScans   = "scans"
	ScopeStock   = "stock"
	ScopeList    = "list"
	ScopeDevices = "devices"
)

// Execer is the transaction or connection a write is already using.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Queryer reads the current version.
type Queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Bump advances each scope by one in the caller's transaction.
// An empty household or an unknown scope does not write.
func Bump(ctx context.Context, q Execer, household string, scopes ...string) error {
	household = strings.TrimSpace(household)
	if q == nil || household == "" || len(scopes) == 0 {
		return nil
	}
	for _, scope := range scopes {
		if !known(scope) {
			return fmt.Errorf("record change: unknown scope")
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO change_versions (household_id, scope, version)
			VALUES (?, ?, 1)
			ON CONFLICT(household_id, scope) DO UPDATE SET
				version = change_versions.version + 1`,
			household, scope); err != nil {
			return fmt.Errorf("record change: %w", err)
		}
	}
	return nil
}

// Version is the current counter. A household that has not written yet is 0.
func Version(ctx context.Context, q Queryer, household, scope string) (int64, error) {
	if q == nil || !known(scope) {
		return 0, fmt.Errorf("read change: unknown scope")
	}
	var version int64
	err := q.QueryRowContext(ctx, `
		SELECT version FROM change_versions WHERE household_id = ? AND scope = ?`,
		strings.TrimSpace(household), scope).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read change: %w", err)
	}
	return version, nil
}

// Cursor encodes household, scope, and version. It is opaque, ASCII, and
// short enough for a hint. It holds no names and no barcodes.
func Cursor(household, scope string, version int64) string {
	raw := strings.TrimSpace(household) + "|" + scope + "|" + strconv.FormatInt(version, 10)
	return "v1." + base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// Current is the cursor a client should send back as since.
func Current(ctx context.Context, q Queryer, household, scope string) (string, error) {
	version, err := Version(ctx, q, household, scope)
	if err != nil {
		return "", err
	}
	return Cursor(household, scope, version), nil
}

// Parse reads a cursor this server wrote. ok is false when it is not ours.
func Parse(cursor string) (household, scope string, version int64, ok bool) {
	cursor = strings.TrimSpace(cursor)
	if len(cursor) > 128 || !strings.HasPrefix(cursor, "v1.") {
		return "", "", 0, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor[3:])
	if err != nil {
		return "", "", 0, false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 || !known(parts[1]) || parts[0] == "" {
		return "", "", 0, false
	}
	version, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil || version < 0 {
		return "", "", 0, false
	}
	return parts[0], parts[1], version, true
}

// Outcome is how a since cursor compares with the household's current version.
type Outcome struct {
	Changed    bool
	Recognized bool
	Cursor     string
	Current    int64
}

// Compare treats a missing cursor as a full read. A cursor from another
// household or scope, or a version this household has not reached, is a full
// read too. An equal version is unchanged.
func Compare(ctx context.Context, q Queryer, household, scope, since string) (Outcome, error) {
	current, err := Version(ctx, q, household, scope)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Cursor: Cursor(household, scope, current), Current: current, Changed: true}
	since = strings.TrimSpace(since)
	if since == "" {
		return out, nil
	}
	gotHouse, gotScope, version, ok := Parse(since)
	if !ok || gotHouse != household || gotScope != scope || version > current {
		return out, nil
	}
	out.Recognized = true
	out.Changed = version != current
	return out, nil
}

func known(scope string) bool {
	switch scope {
	case ScopeScans, ScopeStock, ScopeList, ScopeDevices:
		return true
	default:
		return false
	}
}
