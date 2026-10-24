package auth

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// InviteCreated is a new hand-shared invite. Email is always null in stage 1.
type InviteCreated struct {
	ID        string  `json:"id"`
	Code      string  `json:"code"`
	URL       string  `json:"url"`
	Email     *string `json:"email"`
	Role      string  `json:"role"`
	ExpiresAt string  `json:"expiresAt"`
}

// InviteSummary is one invite on the household list.
type InviteSummary struct {
	ID        string  `json:"id"`
	Email     *string `json:"email"`
	Role      string  `json:"role"`
	CreatedBy string  `json:"createdBy"`
	ExpiresAt string  `json:"expiresAt"`
	Status    string  `json:"status"`
}

// InvitePreview is what a code shows before it is accepted.
type InvitePreview struct {
	Kind          string  `json:"kind"`
	Role          string  `json:"role"`
	HouseholdName *string `json:"householdName,omitempty"`
	InvitedBy     *string `json:"invitedBy,omitempty"`
	ExpiresAt     string  `json:"expiresAt"`
}

// AcceptResult is the session plus the household the invite joined.
type AcceptResult struct {
	Session     SessionView
	HouseholdID string
	Name        string
	Role        string
}

// ResetLink is the hand-shared reset URL.
type ResetLink struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expiresAt"`
	Code      string `json:"-"`
}

func (s *Service) CreateJoinInvite(ctx context.Context, actor, householdID, role string) (InviteCreated, error) {
	if role == "" {
		role = "member"
	}
	if role != "owner" && role != "member" {
		return InviteCreated{}, validation()
	}
	actorRole, deleted, ok, err := s.membershipRole(ctx, s.db, actor, householdID)
	if err != nil || !ok || deleted != "" {
		if err != nil {
			return InviteCreated{}, err
		}
		return InviteCreated{}, notFound()
	}
	if role == "owner" && actorRole != "owner" {
		return InviteCreated{}, fail(CodeForbiddenRole, SentenceForbidden)
	}
	return s.insertInvite(ctx, "join_household", householdID, actor, role)
}

func (s *Service) CreateStartInvite(ctx context.Context, operatorID string, operator bool) (InviteCreated, error) {
	if !operator {
		return InviteCreated{}, notFound()
	}
	return s.insertInvite(ctx, "start_household", "", operatorID, "owner")
}

func (s *Service) insertInvite(ctx context.Context, kind, householdID, actor, role string) (InviteCreated, error) {
	code, err := NewInviteCode()
	if err != nil {
		return InviteCreated{}, AsError(err)
	}
	id := newID()
	exp := s.clock().Add(inviteLife)
	var household any
	if householdID == "" {
		household = nil
	} else {
		household = householdID
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO invites (id, kind, household_id, code_hash, created_by, expires_at, role)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, kind, household, HashOpaque(code), actor, stamp(exp), role)
	if err != nil {
		return InviteCreated{}, AsError(err)
	}
	return InviteCreated{ID: id, Code: code, Role: role, ExpiresAt: stamp(exp)}, nil
}

func (s *Service) ListInvites(ctx context.Context, actor, householdID string) ([]InviteSummary, error) {
	role, deleted, ok, err := s.membershipRole(ctx, s.db, actor, householdID)
	if err != nil || !ok || deleted != "" {
		if err != nil {
			return nil, err
		}
		return nil, notFound()
	}
	if role != "owner" && role != "member" {
		return nil, notFound()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.role, COALESCE(u.name, ''), i.expires_at, i.used_at, i.cancelled_at
		FROM invites i
		LEFT JOIN users u ON u.id = i.created_by
		WHERE i.household_id = ? AND i.kind = 'join_household'
		ORDER BY i.expires_at`, householdID)
	if err != nil {
		return nil, AsError(err)
	}
	defer rows.Close()
	now := s.clock()
	out := []InviteSummary{}
	for rows.Next() {
		var item InviteSummary
		var used, cancelled sql.NullString
		if err := rows.Scan(&item.ID, &item.Role, &item.CreatedBy, &item.ExpiresAt, &used, &cancelled); err != nil {
			return nil, AsError(err)
		}
		item.Status = inviteStatus(item.ExpiresAt, used.String, cancelled.String, now)
		out = append(out, item)
	}
	return out, AsError(rows.Err())
}

func inviteStatus(expires, used, cancelled string, now time.Time) string {
	if cancelled != "" {
		return "revoked"
	}
	if used != "" {
		return "used"
	}
	if exp, ok := parseStamp(expires); ok && !now.Before(exp) {
		return "expired"
	}
	return "open"
}

func (s *Service) RevokeInvite(ctx context.Context, actor, householdID, inviteID string, operator bool) error {
	var kind, ownerHouse, cancelled, used string
	var house sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT kind, household_id, COALESCE(cancelled_at, ''), COALESCE(used_at, '')
		FROM invites WHERE id = ?`, inviteID).Scan(&kind, &house, &cancelled, &used)
	if err == sql.ErrNoRows || used != "" || cancelled != "" {
		return notFound()
	}
	if err != nil {
		return AsError(err)
	}
	ownerHouse = house.String
	if kind == "start_household" {
		if !operator {
			return notFound()
		}
	} else {
		householdID = strings.TrimSpace(householdID)
		if householdID == "" {
			return failField(CodeValidation, "X-Household-Id is required.", "householdId")
		}
		if householdID != ownerHouse {
			return notFound()
		}
		role, deleted, ok, err := s.membershipRole(ctx, s.db, actor, ownerHouse)
		if err != nil || !ok || deleted != "" {
			if err != nil {
				return err
			}
			return notFound()
		}
		if role != "owner" && role != "member" {
			return fail(CodeForbiddenRole, SentenceForbidden)
		}
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE invites SET cancelled_at = ?
		WHERE id = ? AND used_at IS NULL AND cancelled_at IS NULL
		  AND (household_id = ? OR (household_id IS NULL AND ? = ''))`,
		stamp(s.clock()), inviteID, ownerHouse, ownerHouse)
	if err != nil {
		return AsError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound()
	}
	return nil
}

func (s *Service) PreviewInvite(ctx context.Context, code string) (InvitePreview, error) {
	row, err := s.openInvite(ctx, code, false)
	if err != nil {
		return InvitePreview{}, err
	}
	return s.previewFrom(ctx, row)
}

func (s *Service) previewFrom(ctx context.Context, row inviteRow) (InvitePreview, error) {
	out := InvitePreview{Kind: row.Kind, Role: row.Role, ExpiresAt: row.ExpiresAt}
	if row.Kind == "join_household" && row.HouseholdID != "" {
		var name string
		if err := s.db.QueryRowContext(ctx, `SELECT name FROM households WHERE id = ?`, row.HouseholdID).Scan(&name); err == nil {
			out.HouseholdName = &name
		}
	}
	if row.CreatedBy != "" {
		var name string
		if err := s.db.QueryRowContext(ctx, `SELECT name FROM users WHERE id = ?`, row.CreatedBy).Scan(&name); err == nil && name != "" {
			out.InvitedBy = &name
		}
	}
	return out, nil
}

// AcceptInvite consumes a code. accountEmail empty means the caller is already signed in.
func (s *Service) AcceptInvite(ctx context.Context, signedInUser, code, acctName, acctEmail, acctPassword, houseName, restock, kind string) (AcceptResult, error) {
	row, err := s.openInvite(ctx, code, true)
	if err != nil {
		return AcceptResult{}, err
	}
	if signedInUser != "" && acctEmail != "" {
		return AcceptResult{}, validation()
	}
	if signedInUser == "" && acctEmail == "" {
		return AcceptResult{}, validation()
	}
	var userID, userEmail, userName string
	var operator bool
	var passwordHash string
	if signedInUser == "" {
		userName, err = normalizeName(acctName, "name")
		if err != nil {
			return AcceptResult{}, err
		}
		var ok bool
		userEmail, ok = normalizeEmail(acctEmail)
		if !ok {
			return AcceptResult{}, validation()
		}
		if err := passwordOK(acctPassword); err != nil {
			return AcceptResult{}, err
		}
		passwordHash, err = HashPassword(ctx, acctPassword)
		if err != nil {
			return AcceptResult{}, AsError(err)
		}
	} else {
		userID = signedInUser
	}
	if row.Kind == "start_household" {
		houseName, err = normalizeName(houseName, "name")
		if err != nil {
			return AcceptResult{}, err
		}
		restock, ok := normalizeRestock(restock)
		if !ok {
			return AcceptResult{}, validation()
		}
		_ = restock
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AcceptResult{}, AsError(err)
	}
	defer tx.Rollback()
	fresh, found, err := inviteByHash(ctx, tx, HashOpaque(normalizeOrRaw(code)))
	if err != nil || !found || fresh.ID != row.ID {
		return AcceptResult{}, notFound()
	}
	if reason := s.inviteBlock(fresh); reason != nil {
		_ = s.bumpInvite(ctx, tx, fresh)
		return AcceptResult{}, reason
	}
	now := s.clock()
	var householdID, householdName, role string
	role = fresh.Role
	if role == "" {
		role = "member"
	}
	if fresh.Kind == "start_household" {
		mode, _ := normalizeRestock(restock)
		stored := ""
		if mode != "auto" {
			stored = mode
		}
		householdID = newID()
		key, err := channelKey()
		if err != nil {
			return AcceptResult{}, AsError(err)
		}
		householdName = houseName
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary, legacy_user_id)
			VALUES (?, ?, ?, ?, ?, 0, '')`,
			householdID, houseName, stored, stamp(now), key); err != nil {
			return AcceptResult{}, AsError(err)
		}
		role = "owner"
	} else {
		householdID = fresh.HouseholdID
		if err := tx.QueryRowContext(ctx, `SELECT name FROM households WHERE id = ? AND deleted_at IS NULL`, householdID).Scan(&householdName); err != nil {
			return AcceptResult{}, notFound()
		}
	}
	if userID == "" {
		var existing int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email_norm = ? AND deleted_at IS NULL`, userEmail).Scan(&existing); err != nil {
			return AcceptResult{}, AsError(err)
		}
		if existing > 0 {
			return AcceptResult{}, fail(CodeAccountExists, SentenceAccountExists)
		}
		userID = newID()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
			VALUES (?, ?, ?, ?, 'local', 0, ?)`,
			userID, userName, userEmail, passwordHash, stamp(now)); err != nil {
			return AcceptResult{}, AsError(err)
		}
	} else {
		var email, name string
		var op int
		if err := tx.QueryRowContext(ctx, `
			SELECT email_norm, name, operator FROM users WHERE id = ? AND deleted_at IS NULL`, userID).Scan(&email, &name, &op); err != nil {
			return AcceptResult{}, unauthorized()
		}
		userEmail, userName, operator = email, name, op == 1
		var already int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM memberships WHERE household_id = ? AND user_id = ?`, householdID, userID).Scan(&already); err != nil {
			return AcceptResult{}, AsError(err)
		}
		if already > 0 {
			return AcceptResult{}, fail(CodeInviteExpired, SentenceInviteExpired)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memberships (household_id, user_id, role, created_at)
		VALUES (?, ?, ?, ?)`, householdID, userID, role, stamp(now)); err != nil {
		return AcceptResult{}, AsError(err)
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE invites SET used_at = ?
		WHERE id = ? AND used_at IS NULL AND cancelled_at IS NULL
		  AND (household_id = ? OR (household_id IS NULL AND ? = ''))`,
		stamp(now), fresh.ID, fresh.HouseholdID, fresh.HouseholdID)
	if err != nil {
		return AcceptResult{}, AsError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return AcceptResult{}, fail(CodeInviteExpired, SentenceInviteExpired)
	}
	var token string
	active := householdID
	if signedInUser != "" {
		// Keep the caller's current session. Do not mint or rotate a token.
	} else {
		token, err = s.insertSession(ctx, tx, userID, sessionKind(kind), "personal", active)
		if err != nil {
			return AcceptResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AcceptResult{}, AsError(err)
	}
	var view SessionView
	if signedInUser != "" {
		view, err = s.accountView(ctx, s.db, userID, "")
		if err != nil {
			return AcceptResult{}, err
		}
		mem, _ := listMemberships(ctx, s.db, userID)
		if len(mem) == 1 {
			if _, err := s.db.ExecContext(ctx, `
				UPDATE sessions SET active_household_id = ?
				WHERE user_id = ? AND revoked_at IS NULL AND active_household_id = ''`, householdID, userID); err != nil {
				return AcceptResult{}, AsError(err)
			}
			view, err = s.accountView(ctx, s.db, userID, householdID)
			if err != nil {
				return AcceptResult{}, err
			}
		}
	} else {
		view, err = s.accountView(ctx, s.db, userID, active)
		if err != nil {
			return AcceptResult{}, err
		}
		view.Token = token
	}
	view.Operator = operator || view.Operator
	return AcceptResult{Session: view, HouseholdID: householdID, Name: householdName, Role: role}, nil
}

func (s *Service) CreateHousehold(ctx context.Context, userID, sessionToken, name, restock, inviteCode string) (HouseholdDetail, error) {
	if inviteCode == "" {
		return HouseholdDetail{}, fail(CodeInviteRequired, SentenceInviteRequired)
	}
	name, err := normalizeName(name, "name")
	if err != nil {
		return HouseholdDetail{}, err
	}
	mode, ok := normalizeRestock(restock)
	if !ok {
		return HouseholdDetail{}, validation()
	}
	row, err := s.openInvite(ctx, inviteCode, true)
	if err != nil {
		return HouseholdDetail{}, err
	}
	if row.Kind != "start_household" {
		return HouseholdDetail{}, notFound()
	}
	stored := ""
	if mode != "auto" {
		stored = mode
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	defer tx.Rollback()
	fresh, found, err := inviteByHash(ctx, tx, HashOpaque(normalizeOrRaw(inviteCode)))
	if err != nil || !found {
		return HouseholdDetail{}, notFound()
	}
	if reason := s.inviteBlock(fresh); reason != nil {
		_ = s.bumpInvite(ctx, tx, fresh)
		return HouseholdDetail{}, reason
	}
	if fresh.Kind != "start_household" {
		return HouseholdDetail{}, notFound()
	}
	now := s.clock()
	id := newID()
	key, err := channelKey()
	if err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary, legacy_user_id)
		VALUES (?, ?, ?, ?, ?, 0, '')`, id, name, stored, stamp(now), key); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memberships (household_id, user_id, role, created_at)
		VALUES (?, ?, 'owner', ?)`, id, userID, stamp(now)); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE invites SET used_at = ?
		WHERE id = ? AND used_at IS NULL AND cancelled_at IS NULL
		  AND (household_id = ? OR (household_id IS NULL AND ? = ''))`,
		stamp(now), fresh.ID, fresh.HouseholdID, fresh.HouseholdID)
	if err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return HouseholdDetail{}, fail(CodeInviteExpired, SentenceInviteExpired)
	}
	mem, err := listMemberships(ctx, tx, userID)
	if err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	if len(mem) == 1 && sessionToken != "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE sessions SET active_household_id = ? WHERE id_hash = ?`, id, HashOpaque(sessionToken)); err != nil {
			return HouseholdDetail{}, AsError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return HouseholdDetail{}, AsError(err)
	}
	return HouseholdDetail{ID: id, Name: name, Role: "owner"}, nil
}

func (s *Service) openInvite(ctx context.Context, code string, consume bool) (inviteRow, error) {
	norm, ok := NormalizeInviteCode(code)
	if !ok {
		if foldCrockford(code) != "" {
			return inviteRow{}, notFound()
		}
		return inviteRow{}, validation()
	}
	row, found, err := inviteByHash(ctx, s.db, HashOpaque(norm))
	if err != nil {
		return inviteRow{}, AsError(err)
	}
	if !found {
		return inviteRow{}, notFound()
	}
	if row.Attempts >= maxAttempts {
		return inviteRow{}, rateLimited(inviteIPWindow)
	}
	if reason := s.inviteBlock(row); reason != nil {
		if !consume {
			if _, err := s.db.ExecContext(ctx, `
				UPDATE invites SET attempts = attempts + 1
				WHERE id = ? AND (household_id = ? OR (household_id IS NULL AND ? = ''))`,
				row.ID, row.HouseholdID, row.HouseholdID); err != nil {
				return inviteRow{}, AsError(err)
			}
		}
		return inviteRow{}, reason
	}
	return row, nil
}

func (s *Service) inviteBlock(row inviteRow) *Error {
	if row.CancelledAt != "" {
		return notFound()
	}
	if row.UsedAt != "" {
		return fail(CodeInviteExpired, SentenceInviteExpired)
	}
	if exp, ok := parseStamp(row.ExpiresAt); ok && !s.clock().Before(exp) {
		return fail(CodeInviteExpired, SentenceInviteExpired)
	}
	return nil
}

func (s *Service) bumpInvite(ctx context.Context, q queryer, row inviteRow) error {
	_, err := q.ExecContext(ctx, `
		UPDATE invites SET attempts = attempts + 1
		WHERE id = ? AND (household_id = ? OR (household_id IS NULL AND ? = ''))`,
		row.ID, row.HouseholdID, row.HouseholdID)
	return err
}

func normalizeOrRaw(code string) string {
	if norm, ok := NormalizeInviteCode(code); ok {
		return norm
	}
	return code
}

func (s *Service) CreateResetLink(ctx context.Context, actor, householdID, target string, operator bool) (ResetLink, error) {
	role, deleted, member, err := s.membershipRole(ctx, s.db, target, householdID)
	if err != nil || !member || deleted != "" {
		if err != nil {
			return ResetLink{}, err
		}
		return ResetLink{}, notFound()
	}
	_ = role
	actorRole, _, actorMember, err := s.membershipRole(ctx, s.db, actor, householdID)
	if err != nil {
		return ResetLink{}, err
	}
	if !operator && (!actorMember || actorRole != "owner") {
		if actorMember {
			return ResetLink{}, fail(CodeForbiddenRole, SentenceForbidden)
		}
		return ResetLink{}, notFound()
	}
	code, err := NewSessionToken()
	if err != nil {
		return ResetLink{}, AsError(err)
	}
	code = "sbr_" + code[len("sbs_"):]
	exp := s.clock().Add(codeLife)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO reset_links (id, user_id, code_hash, created_by, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		newID(), target, HashOpaque(code), actor, stamp(exp)); err != nil {
		return ResetLink{}, AsError(err)
	}
	return ResetLink{Code: code, ExpiresAt: stamp(exp)}, nil
}

func (s *Service) ResetPassword(ctx context.Context, code, password, kind string) (SessionView, error) {
	if err := passwordOK(password); err != nil {
		return SessionView{}, err
	}
	if code == "" {
		return SessionView{}, validation()
	}
	hash := HashOpaque(code)
	s.mu.Lock()
	if s.resetAttempts[hash] >= maxAttempts {
		s.mu.Unlock()
		return SessionView{}, rateLimited(inviteIPWindow)
	}
	s.mu.Unlock()
	var id, userID, expires, used string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, expires_at, COALESCE(used_at, '')
		FROM reset_links WHERE code_hash = ?`, hash).Scan(&id, &userID, &expires, &used)
	if err == sql.ErrNoRows {
		return SessionView{}, notFound()
	}
	if err != nil {
		return SessionView{}, AsError(err)
	}
	if used != "" || !beforeStamp(s.clock(), expires) {
		s.noteResetAttempt(hash)
		if used != "" || !beforeStamp(s.clock(), expires) {
			return SessionView{}, failCount(CodeResetExpired, SentenceResetExpired)
		}
	}
	pw, err := HashPassword(ctx, password)
	if err != nil {
		return SessionView{}, AsError(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionView{}, AsError(err)
	}
	defer tx.Rollback()
	now := stamp(s.clock())
	res, err := tx.ExecContext(ctx, `
		UPDATE reset_links SET used_at = ? WHERE id = ? AND used_at IS NULL`, now, id)
	if err != nil {
		return SessionView{}, AsError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return SessionView{}, fail(CodeResetExpired, SentenceResetExpired)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET pw_hash = ? WHERE id = ?`, pw, userID); err != nil {
		return SessionView{}, AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, userID); err != nil {
		return SessionView{}, AsError(err)
	}
	active, err := s.pickActive(ctx, tx, userID)
	if err != nil {
		return SessionView{}, err
	}
	token, err := s.insertSession(ctx, tx, userID, sessionKind(kind), "personal", active)
	if err != nil {
		return SessionView{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionView{}, AsError(err)
	}
	s.streams.Kick(UserStreamKey(userID))
	s.kickLive("", userID)
	view, err := s.accountView(ctx, s.db, userID, active)
	if err != nil {
		return SessionView{}, err
	}
	view.Token = token
	return view, nil
}

func (s *Service) noteResetAttempt(hash string) {
	s.mu.Lock()
	s.resetAttempts[hash]++
	s.mu.Unlock()
}

func beforeStamp(now time.Time, raw string) bool {
	t, ok := parseStamp(raw)
	return ok && now.Before(t)
}
