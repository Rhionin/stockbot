package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// HouseholdGone reports whether the household is missing or soft-deleted.
func (s *Service) HouseholdGone(ctx context.Context, householdID string) (bool, error) {
	var deleted string
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(deleted_at, '') FROM households WHERE id = ?`, householdID).Scan(&deleted)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, AsError(err)
	}
	return deleted != "", nil
}

// ExportAllowed is owner-only, including during the restore window.
// After purge_after, or for anyone who is not an owner, the household is not exportable.
func (s *Service) ExportAllowed(ctx context.Context, userID, householdID string) error {
	role, deleted, ok, err := s.membershipRole(ctx, s.db, userID, householdID)
	if err != nil || !ok {
		if err != nil {
			return err
		}
		return notFound()
	}
	if deleted != "" {
		open, err := s.restoreOpen(ctx, s.db, householdID)
		if err != nil {
			return err
		}
		if !open {
			return notFound()
		}
	}
	if role != "owner" {
		return fail(CodeForbiddenRole, SentenceForbidden)
	}
	return nil
}

// RecordExport writes export.downloaded. The row names the household id, not its contents.
func (s *Service) RecordExport(ctx context.Context, actorKind, actorID, householdID string) error {
	if actorKind == "" {
		actorKind = "account"
	}
	return AsError(writeAudit(ctx, s.db, householdID, actorKind, actorID, "export.downloaded", "household", householdID, stamp(s.clock())))
}

func (s *Service) restoreOpen(ctx context.Context, q queryer, householdID string) (bool, error) {
	var purge string
	if err := q.QueryRowContext(ctx, `
		SELECT COALESCE(purge_after, '') FROM households WHERE id = ?`, householdID).Scan(&purge); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, AsError(err)
	}
	until, ok := parseStamp(purge)
	if !ok {
		return false, nil
	}
	return s.clock().Before(until), nil
}

func (s *Service) DeleteHousehold(ctx context.Context, userID, householdID, confirmation string) (HouseholdDetail, error) {
	role, deleted, ok, err := s.membershipRole(ctx, s.db, userID, householdID)
	if err != nil || !ok {
		if err != nil {
			return HouseholdDetail{}, err
		}
		return HouseholdDetail{}, notFound()
	}
	if deleted != "" {
		return HouseholdDetail{}, notFound()
	}
	if role != "owner" {
		return HouseholdDetail{}, fail(CodeForbiddenRole, SentenceForbidden)
	}
	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM households WHERE id = ?`, householdID).Scan(&name); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if !equalFoldTrim(confirmation, name) {
		return HouseholdDetail{}, fail(CodeConfirm, SentenceConfirm)
	}
	key, err := channelKey()
	if err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	now := s.clock()
	nowStamp := stamp(now)
	purge := stamp(now.Add(30 * 24 * time.Hour))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE households SET deleted_at = ?, purge_after = ?, channel_key = ? WHERE id = ?`,
		nowStamp, purge, key, householdID); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	// revoked_at makes a presented device token answer device_revoked.
	// The token row stays until purge so that answer is still possible.
	// superseded_at marks the token revoked as well.
	if _, err := tx.ExecContext(ctx, `
		UPDATE devices SET revoked_at = ? WHERE household_id = ? AND revoked_at IS NULL`,
		nowStamp, householdID); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE device_tokens SET superseded_at = ?, grace_until = ?
		WHERE household_id = ? AND superseded_at IS NULL`,
		nowStamp, nowStamp, householdID); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = ?, active_household_id = ''
		WHERE origin = 'household_password' AND active_household_id = ? AND revoked_at IS NULL`,
		nowStamp, householdID); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions SET active_household_id = '' WHERE active_household_id = ?`, householdID); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM push_subscriptions WHERE household_id = ?`, householdID); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE invites SET cancelled_at = ?
		WHERE household_id = ? AND used_at IS NULL AND cancelled_at IS NULL`,
		nowStamp, householdID); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE pairing_codes SET expires_at = ? WHERE household_id = ? AND completed_device_id IS NULL`,
		nowStamp, householdID); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	members, err := memberIDs(ctx, tx, householdID)
	if err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if err := writeAudit(ctx, tx, householdID, "account", userID, "household.deleted", "household", householdID, nowStamp); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if err := tx.Commit(); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	s.kickHouseholdPeople(householdID, members)
	detail, err := s.GetHousehold(ctx, userID, householdID)
	if err != nil {
		return HouseholdDetail{}, err
	}
	detail.Message = SentenceDeleteHouse
	return detail, nil
}

func (s *Service) RestoreHousehold(ctx context.Context, userID, householdID string) (HouseholdDetail, error) {
	role, deleted, ok, err := s.membershipRole(ctx, s.db, userID, householdID)
	if err != nil || !ok || deleted == "" {
		if err != nil {
			return HouseholdDetail{}, err
		}
		return HouseholdDetail{}, notFound()
	}
	open, err := s.restoreOpen(ctx, s.db, householdID)
	if err != nil {
		return HouseholdDetail{}, err
	}
	if !open {
		return HouseholdDetail{}, notFound()
	}
	if role != "owner" {
		return HouseholdDetail{}, fail(CodeForbiddenRole, SentenceForbidden)
	}
	nowStamp := stamp(s.clock())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	defer tx.Rollback()
	// Devices stay revoked. People pair them again.
	if _, err := tx.ExecContext(ctx, `
		UPDATE households SET deleted_at = NULL, purge_after = NULL WHERE id = ?`, householdID); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if err := writeAudit(ctx, tx, householdID, "account", userID, "household.restored", "household", householdID, nowStamp); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if err := tx.Commit(); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	return s.GetHousehold(ctx, userID, householdID)
}

func (s *Service) DeleteAccount(ctx context.Context, userID, confirmation string) error {
	var email string
	if err := s.db.QueryRowContext(ctx, `
		SELECT email_norm FROM users WHERE id = ? AND deleted_at IS NULL`, userID).Scan(&email); err != nil {
		if err == sql.ErrNoRows {
			return unauthorized()
		}
		return AsError(err)
	}
	if !equalFoldTrim(confirmation, email) {
		return fail(CodeConfirm, SentenceConfirm)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AsError(err)
	}
	defer tx.Rollback()
	var blocking string
	err = tx.QueryRowContext(ctx, `
		SELECT h.name
		FROM memberships m
		JOIN households h ON h.id = m.household_id
		WHERE m.user_id = ? AND m.role = 'owner' AND h.deleted_at IS NULL
		  AND m.household_id = h.id
		  AND (
		    SELECT COUNT(*) FROM memberships o
		    WHERE o.household_id = m.household_id AND o.role = 'owner'
		  ) <= 1
		ORDER BY h.name
		LIMIT 1`, userID).Scan(&blocking)
	if err != nil && err != sql.ErrNoRows {
		return AsError(err)
	}
	if err == nil {
		return fail(CodeLastOwner, LastOwnerSentence(blocking))
	}
	mem, err := listMemberships(ctx, tx, userID)
	if err != nil {
		return AsError(err)
	}
	houses := make([]string, 0, len(mem))
	for _, m := range mem {
		houses = append(houses, m.HouseholdID)
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM memberships WHERE household_id = ? AND user_id = ?`, m.HouseholdID, userID); err != nil {
			return AsError(err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM push_subscriptions WHERE household_id = ? AND user_id = ?`, m.HouseholdID, userID); err != nil {
			return AsError(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM push_subscriptions WHERE user_id = ? AND household_id IS NOT NULL`, userID); err != nil {
		return AsError(err)
	}
	now := stamp(s.clock())
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, userID); err != nil {
		return AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM reset_links WHERE user_id = ?`, userID); err != nil {
		return AsError(err)
	}
	pseudo := pseudonym(userID)
	if _, err := tx.ExecContext(ctx, `
		UPDATE audit_log SET actor_id = ? WHERE actor_id = ?`, pseudo, userID); err != nil {
		return AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		return AsError(err)
	}
	if err := writeAudit(ctx, tx, "", "account", pseudo, "account.deleted", "user", pseudo, now); err != nil {
		return AsError(err)
	}
	if err := tx.Commit(); err != nil {
		return AsError(err)
	}
	s.streams.Kick(UserStreamKey(userID))
	s.kickLive("", userID)
	for _, id := range houses {
		s.streams.KickHousehold(id, userID)
		s.kickLive(id, userID)
	}
	return nil
}

func (s *Service) kickHouseholdPeople(householdID string, userIDs []string) {
	s.streams.KickHousehold(householdID, "")
	s.kickLive(householdID, "")
	for _, id := range userIDs {
		s.streams.KickHousehold(householdID, id)
		s.kickLive(householdID, id)
	}
}

func memberIDs(ctx context.Context, q queryer, householdID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT user_id FROM memberships WHERE household_id = ? ORDER BY user_id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func pseudonym(userID string) string {
	sum := sha256.Sum256([]byte(userID))
	return "deleted:" + hex.EncodeToString(sum[:8])
}

// LastOwnerSentence is the account-delete copy for one household name.
func LastOwnerSentence(name string) string {
	return fmt.Sprintf("You're the only owner of %s. Make someone else an owner or delete the household first.", name)
}
