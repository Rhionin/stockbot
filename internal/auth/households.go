package auth

import (
	"context"
	"database/sql"
	"strings"
)

// HouseholdDetail is one household the caller belongs to.
type HouseholdDetail struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	DeletedAt *string `json:"deletedAt"`
	PurgeAt   *string `json:"purgeAt"`
	Message   string  `json:"message,omitempty"`
}

// HouseholdSettings is the state a member can read.
type HouseholdSettings struct {
	RestockMode string `json:"restockMode"`
}

// Member is one person in a household.
type Member struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

func (s *Service) ListHouseholds(ctx context.Context, userID string) ([]SessionHousehold, error) {
	mem, err := listMemberships(ctx, s.db, userID)
	if err != nil {
		return nil, AsError(err)
	}
	out := make([]SessionHousehold, 0, len(mem))
	for _, m := range mem {
		out = append(out, SessionHousehold{
			HouseholdID: m.HouseholdID, Name: m.Name, Role: m.Role, DeletedAt: strPtr(m.DeletedAt),
		})
	}
	return out, nil
}

func (s *Service) GetHousehold(ctx context.Context, userID, householdID string) (HouseholdDetail, error) {
	role, deleted, ok, err := s.membershipRole(ctx, s.db, userID, householdID)
	if err != nil || !ok {
		if err != nil {
			return HouseholdDetail{}, err
		}
		return HouseholdDetail{}, notFound()
	}
	var name, purge string
	if err := s.db.QueryRowContext(ctx, `
		SELECT name, COALESCE(purge_after, '') FROM households WHERE id = ?`, householdID).Scan(&name, &purge); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	return HouseholdDetail{
		ID: householdID, Name: name, Role: role, DeletedAt: strPtr(deleted), PurgeAt: strPtr(purge),
	}, nil
}

func (s *Service) RenameHousehold(ctx context.Context, userID, householdID, name string) (HouseholdDetail, error) {
	role, deleted, ok, err := s.membershipRole(ctx, s.db, userID, householdID)
	if err != nil || !ok {
		if err != nil {
			return HouseholdDetail{}, err
		}
		return HouseholdDetail{}, fail(CodeHouseholdNotFound, SentenceNotFound)
	}
	if deleted != "" {
		return HouseholdDetail{}, fail(CodeHouseholdNotFound, SentenceNotFound)
	}
	if role != "owner" {
		return HouseholdDetail{}, fail(CodeForbiddenOwner, SentenceForbidden)
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 || strings.ContainsAny(name, "\r\n") {
		return HouseholdDetail{}, fail(CodeInvalidInput, SentenceValidation)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE households SET name = ? WHERE id = ?`, name, householdID); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	return s.GetHousehold(ctx, userID, householdID)
}

func (s *Service) Settings(ctx context.Context, householdID string) (HouseholdSettings, error) {
	var mode string
	if err := s.db.QueryRowContext(ctx, `
		SELECT restock_mode FROM households WHERE id = ?`, householdID).Scan(&mode); err != nil {
		return HouseholdSettings{}, AsError(err)
	}
	if mode == "" {
		mode = "auto"
	}
	return HouseholdSettings{RestockMode: mode}, nil
}

func (s *Service) UpdateRestock(ctx context.Context, userID, householdID, mode string) (HouseholdSettings, error) {
	role, deleted, ok, err := s.membershipRole(ctx, s.db, userID, householdID)
	if err != nil || !ok || deleted != "" {
		if err != nil {
			return HouseholdSettings{}, err
		}
		return HouseholdSettings{}, notFound()
	}
	if role != "owner" {
		return HouseholdSettings{}, fail(CodeForbiddenRole, SentenceForbidden)
	}
	mode, ok = normalizeRestock(mode)
	if !ok || mode == "" {
		return HouseholdSettings{}, validation()
	}
	stored := mode
	if mode == "auto" {
		stored = ""
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE households SET restock_mode = ? WHERE id = ?`, stored, householdID); err != nil {
		return HouseholdSettings{}, AsError(err)
	}
	return s.Settings(ctx, householdID)
}

func (s *Service) ListMembers(ctx context.Context, userID, householdID string) ([]Member, error) {
	if _, _, ok, err := s.membershipRole(ctx, s.db, userID, householdID); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, notFound()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.user_id, u.email_norm, m.role
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.household_id = ? AND u.deleted_at IS NULL
		ORDER BY u.email_norm`, householdID)
	if err != nil {
		return nil, AsError(err)
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role); err != nil {
			return nil, AsError(err)
		}
		out = append(out, m)
	}
	return out, AsError(rows.Err())
}

func (s *Service) ChangeRole(ctx context.Context, actor, householdID, target, role string) (Member, error) {
	if role != "owner" && role != "member" {
		return Member{}, validation()
	}
	actorRole, deleted, ok, err := s.membershipRole(ctx, s.db, actor, householdID)
	if err != nil || !ok || deleted != "" {
		if err != nil {
			return Member{}, err
		}
		return Member{}, notFound()
	}
	if actorRole != "owner" {
		return Member{}, fail(CodeForbiddenRole, SentenceForbidden)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Member{}, AsError(err)
	}
	defer tx.Rollback()
	var current string
	err = tx.QueryRowContext(ctx, `
		SELECT role FROM memberships WHERE household_id = ? AND user_id = ?`, householdID, target).Scan(&current)
	if err == sql.ErrNoRows {
		return Member{}, notFound()
	}
	if err != nil {
		return Member{}, AsError(err)
	}
	if current == "owner" && role != "owner" {
		var owners int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM memberships WHERE household_id = ? AND role = 'owner'`, householdID).Scan(&owners); err != nil {
			return Member{}, AsError(err)
		}
		if owners <= 1 {
			return Member{}, fail(CodeLastOwner, SentenceLastOwner)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE memberships SET role = ? WHERE household_id = ? AND user_id = ?`, role, householdID, target); err != nil {
		return Member{}, AsError(err)
	}
	if err := tx.Commit(); err != nil {
		return Member{}, AsError(err)
	}
	var email string
	if err := s.db.QueryRowContext(ctx, `SELECT email_norm FROM users WHERE id = ?`, target).Scan(&email); err != nil {
		return Member{}, AsError(err)
	}
	return Member{UserID: target, Email: email, Role: role}, nil
}

func (s *Service) RemoveMember(ctx context.Context, actor, householdID, target string) error {
	actorRole, deleted, ok, err := s.membershipRole(ctx, s.db, actor, householdID)
	if err != nil || !ok || deleted != "" {
		if err != nil {
			return err
		}
		return notFound()
	}
	if actor != target && actorRole != "owner" {
		return fail(CodeForbiddenRole, SentenceForbidden)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AsError(err)
	}
	defer tx.Rollback()
	var role string
	err = tx.QueryRowContext(ctx, `
		SELECT role FROM memberships WHERE household_id = ? AND user_id = ?`, householdID, target).Scan(&role)
	if err == sql.ErrNoRows {
		return notFound()
	}
	if err != nil {
		return AsError(err)
	}
	if role == "owner" {
		var owners int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM memberships WHERE household_id = ? AND role = 'owner'`, householdID).Scan(&owners); err != nil {
			return AsError(err)
		}
		if owners <= 1 {
			return fail(CodeLastOwner, SentenceLastOwner)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM memberships WHERE household_id = ? AND user_id = ?`, householdID, target); err != nil {
		return AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions SET active_household_id = ''
		WHERE user_id = ? AND active_household_id = ?`, target, householdID); err != nil {
		return AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM push_subscriptions WHERE household_id = ? AND user_id = ?`, householdID, target); err != nil {
		return AsError(err)
	}
	if err := tx.Commit(); err != nil {
		return AsError(err)
	}
	s.streams.KickHousehold(householdID, target)
	s.kickLive(householdID, target)
	return nil
}

func writeAudit(ctx context.Context, q queryer, householdID, actorKind, actorID, action, targetKind, targetID, at string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO audit_log (id, household_id, actor_kind, actor_id, action, target_kind, target_id, at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		newID(), householdID, actorKind, actorID, action, targetKind, targetID, at)
	return err
}

func equalFoldTrim(got, want string) bool {
	return strings.EqualFold(strings.TrimSpace(got), strings.TrimSpace(want))
}
