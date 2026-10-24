package auth

import (
	"context"
	"database/sql"
)

// queryer is the database or a transaction. Lookup SQL that cannot bind a
// single household lives in this file so the tenant lint can see why.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type membership struct {
	HouseholdID string
	Name        string
	Role        string
	DeletedAt   string
}

func listMemberships(ctx context.Context, q queryer, userID string) ([]membership, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT m.household_id, h.name, m.role, COALESCE(h.deleted_at, '')
		FROM memberships m
		JOIN households h ON h.id = m.household_id
		WHERE m.user_id = ?
		ORDER BY h.is_primary DESC, h.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []membership
	for rows.Next() {
		var m membership
		if err := rows.Scan(&m.HouseholdID, &m.Name, &m.Role, &m.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func liveOwnerHouseholds(ctx context.Context, q queryer, userID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT m.household_id
		FROM memberships m
		JOIN households h ON h.id = m.household_id
		WHERE m.user_id = ? AND m.role = 'owner' AND h.deleted_at IS NULL`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type tokenRow struct {
	TokenID      string
	DeviceID     string
	HouseholdID  string
	CreatedAt    string
	SupersededAt string
	GraceUntil   string
	RevokedAt    string
	Kind         string
	Label        string
}

func deviceByTokenHash(ctx context.Context, q queryer, hash string) (tokenRow, bool, error) {
	var row tokenRow
	var superseded, grace, revoked sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT t.id, t.device_id, t.household_id, t.created_at,
		       t.superseded_at, t.grace_until, d.revoked_at, d.kind, d.label
		FROM device_tokens t
		JOIN devices d ON d.id = t.device_id
		WHERE t.token_hash = ?`, hash).Scan(
		&row.TokenID, &row.DeviceID, &row.HouseholdID, &row.CreatedAt,
		&superseded, &grace, &revoked, &row.Kind, &row.Label,
	)
	if err == sql.ErrNoRows {
		return tokenRow{}, false, nil
	}
	if err != nil {
		return tokenRow{}, false, err
	}
	row.SupersededAt = superseded.String
	row.GraceUntil = grace.String
	row.RevokedAt = revoked.String
	return row, true, nil
}

type inviteRow struct {
	ID          string
	Kind        string
	HouseholdID string
	CreatedBy   string
	ExpiresAt   string
	UsedAt      string
	CancelledAt string
	Attempts    int
	Role        string
}

func inviteByHash(ctx context.Context, q queryer, hash string) (inviteRow, bool, error) {
	var row inviteRow
	var household, created, used, cancelled sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT id, kind, household_id, created_by, expires_at, used_at, cancelled_at, attempts, role
		FROM invites
		WHERE invites.code_hash = ?`, hash).Scan(
		&row.ID, &row.Kind, &household, &created, &row.ExpiresAt, &used, &cancelled, &row.Attempts, &row.Role,
	)
	if err == sql.ErrNoRows {
		return inviteRow{}, false, nil
	}
	if err != nil {
		return inviteRow{}, false, err
	}
	row.HouseholdID = household.String
	row.CreatedBy = created.String
	row.UsedAt = used.String
	row.CancelledAt = cancelled.String
	return row, true, nil
}

type pairingRow struct {
	ID          string
	HouseholdID string
	ExpiresAt   string
	Attempts    int
	DeviceID    string
}

func pairingByHash(ctx context.Context, q queryer, hash string) (pairingRow, bool, error) {
	var row pairingRow
	var device sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT id, household_id, expires_at, attempts, completed_device_id
		FROM pairing_codes
		WHERE pairing_codes.code_hash = ?`, hash).Scan(
		&row.ID, &row.HouseholdID, &row.ExpiresAt, &row.Attempts, &device,
	)
	if err == sql.ErrNoRows {
		return pairingRow{}, false, nil
	}
	if err != nil {
		return pairingRow{}, false, err
	}
	row.DeviceID = device.String
	return row, true, nil
}
