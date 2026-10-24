package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"strings"
	"sync"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
	"golang.org/x/crypto/bcrypt"
)

// Service is the stage-1 directory, sessions, and household membership.
type Service struct {
	db       *sql.DB
	now      func() time.Time
	limits   *Limiter
	setupDir string
	streams  *Streams
	presence *presence

	mu            sync.Mutex
	resetAttempts map[string]int

	healthMu    sync.Mutex
	setupHealth string

	hints         HintBus
	live          liveKicker
	operatorPush  OperatorPush
	barcodeLookup func(ctx context.Context, householdID, scanID, barcode string)
}

// NewService uses the pantry database. setupDir is the display-file directory.
func NewService(db *sql.DB, setupDir string) *Service {
	return &Service{
		db:            db,
		now:           func() time.Time { return time.Now().UTC() },
		limits:        NewLimiter(),
		setupDir:      setupDir,
		streams:       NewStreams(),
		presence:      newPresence(),
		resetAttempts: map[string]int{},
	}
}

func (s *Service) clock() time.Time {
	if s == nil || s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

// SetClock replaces the clock. Tests use it to expire sessions and codes.
func (s *Service) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	s.now = now
}

func (s *Service) Streams() *Streams { return s.streams }

// DB is the pantry database. Changes-since reads versions from it.
func (s *Service) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

// liveKicker closes SSE streams. The events hub implements it.
type liveKicker interface {
	Kick(householdID, userID string)
	DropSession(sessionID string)
}

// SetLive closes matching event streams when a member or session ends.
func (s *Service) SetLive(k liveKicker) {
	if s != nil {
		s.live = k
	}
}

func (s *Service) kickLive(householdID, userID string) {
	if s != nil && s.live != nil {
		s.live.Kick(householdID, userID)
	}
}

// endAccountStreams closes every live stream for the account.
// Password reset, account deletion, and operator recovery revoke every session.
func (s *Service) endAccountStreams(userID string) {
	if s == nil || userID == "" {
		return
	}
	s.streams.Kick(UserStreamKey(userID))
	s.streams.KickHousehold("", userID)
	s.kickLive("", userID)
}

// dropSession closes streams bound to one session hash.
func (s *Service) dropSession(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.streams.Kick(SessionStreamKey(sessionID))
	if s.live != nil {
		s.live.DropSession(sessionID)
	}
}

func (s *Service) Limiter() *Limiter { return s.limits }

func (s *Service) Now() time.Time { return s.clock() }

func (s *Service) Primary(ctx context.Context) (tenant.Scope, error) {
	return PrimaryScope(ctx, s.db)
}

func (s *Service) Membership(ctx context.Context, userID, householdID string) (role, deleted string, ok bool, err error) {
	return s.membershipRole(ctx, s.db, userID, householdID)
}

// View is the session payload for a principal already resolved on this request.
// The active household is the one stored on the session, not the header.
func (s *Service) View(ctx context.Context, p Principal) (SessionView, error) {
	switch p.Kind {
	case "lan":
		view, err := s.householdPasswordView(ctx, p.HouseholdID)
		if err != nil {
			return SessionView{}, err
		}
		view.Kind = "lan"
		return view, nil
	case "household_password":
		return s.householdPasswordView(ctx, p.HouseholdID)
	case "account":
		return s.accountView(ctx, s.db, p.UserID, p.StoredHousehold)
	default:
		return emptySession("signed_out"), nil
	}
}

// SessionUser is the signed-in account. It is null for LAN and household-password sessions.
type SessionUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// SessionHousehold is one membership on the session payload.
type SessionHousehold struct {
	HouseholdID string  `json:"householdId"`
	Name        string  `json:"name"`
	Role        string  `json:"role"`
	DeletedAt   *string `json:"deletedAt"`
}

// HouseholdCard is the same membership with the switcher field name.
type HouseholdCard struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	DeletedAt *string `json:"deletedAt"`
}

// SessionView is GET /api/auth/session and the login payload, without the token.
type SessionView struct {
	User              *SessionUser       `json:"user"`
	Kind              string             `json:"kind"`
	Memberships       []SessionHousehold `json:"memberships"`
	Households        []HouseholdCard    `json:"households"`
	ActiveHouseholdID *string            `json:"activeHouseholdId"`
	Operator          bool               `json:"operator"`
	Token             string             `json:"token,omitempty"`
}

func emptySession(kind string) SessionView {
	return SessionView{
		Kind:        kind,
		Memberships: []SessionHousehold{},
		Households:  []HouseholdCard{},
	}
}

// Authenticate checks the local directory. A missing account and a wrong
// password are the same failure. A missing account does not run argon2:
// that slot is for a password that belongs to someone.
func (s *Service) Authenticate(ctx context.Context, email, password string) (Account, error) {
	email, ok := normalizeEmail(email)
	if !ok {
		return Account{}, unauthorized()
	}
	var acct Account
	var hash string
	var op int
	err := s.db.QueryRowContext(ctx, `
		SELECT id, email_norm, name, pw_hash, operator
		FROM users
		WHERE email_norm = ? AND deleted_at IS NULL`, email).Scan(
		&acct.ID, &acct.Email, &acct.Name, &hash, &op)
	if err == sql.ErrNoRows {
		return Account{}, unauthorized()
	}
	if err != nil {
		return Account{}, AsError(err)
	}
	match, err := VerifyPassword(ctx, hash, password)
	if err != nil {
		return Account{}, AsError(err)
	}
	if !match {
		return Account{}, unauthorized()
	}
	acct.Operator = op == 1
	return acct, nil
}

// SetPassword replaces one account's password outside a larger transaction.
func (s *Service) SetPassword(ctx context.Context, userID, password string) error {
	if err := passwordOK(password); err != nil {
		return err
	}
	hash, err := HashPassword(ctx, password)
	if err != nil {
		return AsError(err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET pw_hash = ? WHERE id = ? AND deleted_at IS NULL`, hash, userID)
	if err != nil {
		return AsError(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return notFound()
	}
	return nil
}

// LoginAccount starts a personal session. The raw token is set on the view.
func (s *Service) LoginAccount(ctx context.Context, email, password, kind string) (SessionView, error) {
	acct, err := s.Authenticate(ctx, email, password)
	if err != nil {
		if ae := classify(err); ae != nil && ae.Code == CodeUnauthorized {
			ae.Count = true
			return SessionView{}, ae
		}
		return SessionView{}, err
	}
	active, err := s.pickActive(ctx, s.db, acct.ID)
	if err != nil {
		return SessionView{}, err
	}
	token, err := s.insertSession(ctx, s.db, acct.ID, sessionKind(kind), "personal", active)
	if err != nil {
		return SessionView{}, err
	}
	view, err := s.accountView(ctx, s.db, acct.ID, active)
	if err != nil {
		return SessionView{}, err
	}
	view.Token = token
	return view, nil
}

// LoginHousehold starts a household-password session for household 1.
func (s *Service) LoginHousehold(ctx context.Context, user, pass, kind string) (SessionView, error) {
	if strings.TrimSpace(user) == "" {
		gate, err := s.LoadHouseholdGate(ctx)
		if err != nil {
			return SessionView{}, err
		}
		user = gate.User
	}
	ok, err := s.householdPasswordMatches(ctx, user, pass)
	if err != nil {
		return SessionView{}, err
	}
	if !ok {
		return SessionView{}, failCount(CodeUnauthorized, SentenceUnauthorized)
	}
	scope, err := PrimaryScope(ctx, s.db)
	if err != nil {
		return SessionView{}, AsError(err)
	}
	token, err := s.insertSession(ctx, s.db, "", sessionKind(kind), "household_password", scope.HouseholdID())
	if err != nil {
		return SessionView{}, err
	}
	view, err := s.householdPasswordView(ctx, scope.HouseholdID())
	if err != nil {
		return SessionView{}, err
	}
	view.Token = token
	return view, nil
}

func sessionKind(kind string) string {
	if kind == "cookie" {
		return "cookie"
	}
	return "bearer"
}

func (s *Service) insertSession(ctx context.Context, q queryer, userID, kind, origin, active string) (string, error) {
	token, err := NewSessionToken()
	if err != nil {
		return "", AsError(err)
	}
	now := s.clock()
	var user any
	if userID == "" {
		user = nil
	} else {
		user = userID
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO sessions (
			id_hash, user_id, kind, origin, active_household_id, created_at, last_seen_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		HashOpaque(token), user, kind, origin, active, stamp(now), stamp(now), stamp(now.Add(sessionIdle)))
	if err != nil {
		return "", AsError(err)
	}
	return token, nil
}

type sessionRow struct {
	UserID   string
	Kind     string
	Origin   string
	Active   string
	Created  time.Time
	LastSeen time.Time
	Expires  time.Time
	Revoked  bool
}

// LookupSession loads a token and slides it at most once an hour.
// Revoked, idle, and 180-day sessions are unauthorized. Nothing is cached.
func (s *Service) LookupSession(ctx context.Context, token string) (Principal, SessionView, error) {
	if !strings.HasPrefix(token, "sbs_") {
		return Principal{}, SessionView{}, unauthorized()
	}
	row, ok, err := s.sessionByHash(ctx, HashOpaque(token))
	if err != nil || !ok {
		if err != nil {
			return Principal{}, SessionView{}, err
		}
		return Principal{}, SessionView{}, unauthorized()
	}
	now := s.clock()
	if row.Revoked || !now.Before(row.Expires) || !now.Before(row.Created.Add(sessionCap)) {
		return Principal{}, SessionView{}, unauthorized()
	}
	if now.Sub(row.LastSeen) >= sessionSlide {
		next := now.Add(sessionIdle)
		cap := row.Created.Add(sessionCap)
		if next.After(cap) {
			next = cap
		}
		if _, err := s.db.ExecContext(ctx, `
			UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id_hash = ?`,
			stamp(now), stamp(next), HashOpaque(token)); err != nil {
			return Principal{}, SessionView{}, AsError(err)
		}
	}
	if row.Origin == "household_password" {
		on, err := s.HouseholdPasswordOn(ctx)
		if err != nil {
			return Principal{}, SessionView{}, err
		}
		if !on {
			return Principal{}, SessionView{}, unauthorized()
		}
		view, err := s.householdPasswordView(ctx, row.Active)
		if err != nil {
			return Principal{}, SessionView{}, err
		}
		return Principal{
			Kind:            "household_password",
			HouseholdID:     row.Active,
			StoredHousehold: row.Active,
			Role:            "member",
			SessionID:       HashOpaque(token),
		}, view, nil
	}
	view, err := s.accountView(ctx, s.db, row.UserID, row.Active)
	if err != nil {
		return Principal{}, SessionView{}, err
	}
	return Principal{
		Kind:            "account",
		UserID:          row.UserID,
		Email:           view.User.Email,
		Name:            view.User.Name,
		Operator:        view.Operator,
		HouseholdID:     row.Active,
		StoredHousehold: row.Active,
		SessionID:       HashOpaque(token),
	}, view, nil
}

func (s *Service) sessionByHash(ctx context.Context, hash string) (sessionRow, bool, error) {
	var user, revoked sql.NullString
	var created, seen, expires, active string
	var kind, origin string
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, kind, origin, active_household_id, created_at, last_seen_at, expires_at, revoked_at
		FROM sessions WHERE id_hash = ?`, hash).Scan(&user, &kind, &origin, &active, &created, &seen, &expires, &revoked)
	if err == sql.ErrNoRows {
		return sessionRow{}, false, nil
	}
	if err != nil {
		return sessionRow{}, false, AsError(err)
	}
	cAt, ok1 := parseStamp(created)
	sAt, ok2 := parseStamp(seen)
	eAt, ok3 := parseStamp(expires)
	if !ok1 || !ok2 || !ok3 {
		return sessionRow{}, false, unauthorized()
	}
	return sessionRow{
		UserID: user.String, Kind: kind, Origin: origin, Active: active,
		Created: cAt, LastSeen: sAt, Expires: eAt, Revoked: revoked.Valid && revoked.String != "",
	}, true, nil
}

// RevokeSession ends one session immediately and closes its event streams.
func (s *Service) RevokeSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	hash := HashOpaque(token)
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = ? WHERE id_hash = ? AND revoked_at IS NULL`,
		stamp(s.clock()), hash)
	if err != nil {
		return AsError(err)
	}
	s.dropSession(hash)
	return nil
}

// RevokeSessionByID ends one of the caller's sessions. The id is the stored
// session hash. Another account's session is the same result as a missing one.
func (s *Service) RevokeSessionByID(ctx context.Context, userID, idHash string) error {
	if userID == "" || strings.TrimSpace(idHash) == "" {
		return notFound()
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = ?
		WHERE id_hash = ? AND user_id = ? AND revoked_at IS NULL`,
		stamp(s.clock()), idHash, userID)
	if err != nil {
		return AsError(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return AsError(err)
	}
	if n == 0 {
		return notFound()
	}
	s.dropSession(idHash)
	return nil
}

// SwitchHousehold stores the active household on this session only.
func (s *Service) SwitchHousehold(ctx context.Context, token, householdID string) (SessionView, error) {
	p, view, err := s.LookupSession(ctx, token)
	if err != nil {
		return SessionView{}, err
	}
	if p.Kind != "account" {
		return SessionView{}, notFound()
	}
	role, _, ok, err := s.membershipRole(ctx, s.db, p.UserID, householdID)
	if err != nil {
		return SessionView{}, err
	}
	if !ok {
		return SessionView{}, notFound()
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET active_household_id = ? WHERE id_hash = ?`,
		householdID, HashOpaque(token)); err != nil {
		return SessionView{}, AsError(err)
	}
	view, err = s.accountView(ctx, s.db, p.UserID, householdID)
	if err != nil {
		return SessionView{}, err
	}
	_ = role
	return view, nil
}

func (s *Service) pickActive(ctx context.Context, q queryer, userID string) (string, error) {
	var active string
	err := q.QueryRowContext(ctx, `
		SELECT active_household_id FROM sessions
		WHERE user_id = ? AND active_household_id != ''
		ORDER BY created_at DESC LIMIT 1`, userID).Scan(&active)
	if err != nil && err != sql.ErrNoRows {
		return "", AsError(err)
	}
	if active != "" {
		_, _, ok, err := s.membershipRole(ctx, q, userID, active)
		if err != nil {
			return "", err
		}
		if ok {
			return active, nil
		}
	}
	mem, err := listMemberships(ctx, q, userID)
	if err != nil {
		return "", AsError(err)
	}
	if len(mem) == 1 {
		return mem[0].HouseholdID, nil
	}
	return "", nil
}

func (s *Service) accountView(ctx context.Context, q queryer, userID, active string) (SessionView, error) {
	var email, name string
	var op int
	err := q.QueryRowContext(ctx, `
		SELECT email_norm, name, operator FROM users WHERE id = ? AND deleted_at IS NULL`, userID).Scan(&email, &name, &op)
	if err == sql.ErrNoRows {
		return SessionView{}, unauthorized()
	}
	if err != nil {
		return SessionView{}, AsError(err)
	}
	mem, err := listMemberships(ctx, q, userID)
	if err != nil {
		return SessionView{}, AsError(err)
	}
	view := emptySession("account")
	view.User = &SessionUser{ID: userID, Email: email, Name: name}
	view.Operator = op == 1
	still := false
	for _, m := range mem {
		deleted := strPtr(m.DeletedAt)
		view.Memberships = append(view.Memberships, SessionHousehold{
			HouseholdID: m.HouseholdID, Name: m.Name, Role: m.Role, DeletedAt: deleted,
		})
		view.Households = append(view.Households, HouseholdCard{
			ID: m.HouseholdID, Name: m.Name, Role: m.Role, DeletedAt: deleted,
		})
		if m.HouseholdID == active {
			still = true
		}
	}
	if still && active != "" {
		view.ActiveHouseholdID = &active
	}
	return view, nil
}

func (s *Service) householdPasswordView(ctx context.Context, householdID string) (SessionView, error) {
	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM households WHERE id = ?`, householdID).Scan(&name); err != nil {
		return SessionView{}, AsError(err)
	}
	view := emptySession("household_password")
	view.Memberships = []SessionHousehold{{HouseholdID: householdID, Name: name, Role: "member"}}
	view.Households = []HouseholdCard{{ID: householdID, Name: name, Role: "member"}}
	view.ActiveHouseholdID = &householdID
	return view, nil
}

// LANView is the :8080 principal while the shared password is on.
func (s *Service) LANView(ctx context.Context) (Principal, SessionView, error) {
	scope, err := PrimaryScope(ctx, s.db)
	if err != nil {
		return Principal{}, SessionView{}, AsError(err)
	}
	view, err := s.householdPasswordView(ctx, scope.HouseholdID())
	if err != nil {
		return Principal{}, SessionView{}, err
	}
	view.Kind = "lan"
	return Principal{
		Kind:        "lan",
		HouseholdID: scope.HouseholdID(),
		Role:        "member",
	}, view, nil
}

// HouseholdGate is the shared password as it is stored now.
// On is public shared-password sign-in: state on and a nonempty hash.
// LANOpen is :8080 before retirement. A missing row is still open.
// Retirement (state off) closes the LAN even while the hash remains for restore.
type HouseholdGate struct {
	On           bool
	LANOpen      bool
	User         string
	Hash         []byte
	Secret       []byte
	RestoreUntil string
}

func (s *Service) LoadHouseholdGate(ctx context.Context) (HouseholdGate, error) {
	scope, err := PrimaryScope(ctx, s.db)
	if err != nil {
		return HouseholdGate{}, AsError(err)
	}
	var login, hash, key, state, until string
	err = s.db.QueryRowContext(ctx, `
		SELECT login_name, COALESCE(pw_hash, ''), COALESCE(session_key, ''), state, COALESCE(restore_until, '')
		FROM household_password WHERE household_id = ?`, scope.HouseholdID()).Scan(&login, &hash, &key, &state, &until)
	if err == sql.ErrNoRows {
		return HouseholdGate{LANOpen: true}, nil
	}
	if err != nil {
		return HouseholdGate{}, AsError(err)
	}
	secret, _ := base64.StdEncoding.DecodeString(key)
	return HouseholdGate{
		On:           state == "on" && hash != "",
		LANOpen:      state == "on",
		User:         login,
		Hash:         []byte(hash),
		Secret:       secret,
		RestoreUntil: until,
	}, nil
}

// HouseholdPasswordOn reports whether household 1 still signs in with the shared password.
// A missing row is off: public sign-in fails closed until a hash is stored.
func (s *Service) HouseholdPasswordOn(ctx context.Context) (bool, error) {
	gate, err := s.LoadHouseholdGate(ctx)
	if err != nil {
		return false, err
	}
	return gate.On, nil
}

// LANOpen reports whether :8080 still serves household 1 without a personal session.
// No household_password row, or state on, stays open. State off (retired) closes it.
func (s *Service) LANOpen(ctx context.Context) (bool, error) {
	gate, err := s.LoadHouseholdGate(ctx)
	if err != nil {
		return false, err
	}
	return gate.LANOpen, nil
}

// CheckHouseholdPassword reports whether the shared household password matches.
func (s *Service) CheckHouseholdPassword(ctx context.Context, user, pass string) (bool, error) {
	return s.householdPasswordMatches(ctx, user, pass)
}

func (s *Service) householdPasswordMatches(ctx context.Context, user, pass string) (bool, error) {
	gate, err := s.LoadHouseholdGate(ctx)
	if err != nil {
		return false, err
	}
	if !gate.On || len(gate.Hash) == 0 {
		return false, nil
	}
	if user != gate.User {
		// Still compare so a wrong username is not faster in a way that
		// skips bcrypt. The username must match as well.
		_ = bcrypt.CompareHashAndPassword(gate.Hash, []byte(pass))
		return false, nil
	}
	if bcrypt.CompareHashAndPassword(gate.Hash, []byte(pass)) != nil {
		return false, nil
	}
	return true, nil
}

func (s *Service) membershipRole(ctx context.Context, q queryer, userID, householdID string) (role, deleted string, ok bool, err error) {
	if _, terr := tenant.New(householdID); terr != nil {
		return "", "", false, nil
	}
	err = q.QueryRowContext(ctx, `
		SELECT m.role, COALESCE(h.deleted_at, '')
		FROM memberships m
		JOIN households h ON h.id = m.household_id
		WHERE m.household_id = ? AND m.user_id = ?`, householdID, userID).Scan(&role, &deleted)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, AsError(err)
	}
	return role, deleted, true, nil
}

func (s *Service) hasAccount(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`).Scan(&n)
	if err != nil {
		return false, AsError(err)
	}
	return n > 0, nil
}

func (s *Service) HasAccount(ctx context.Context) (bool, error) { return s.hasAccount(ctx) }

func hasAccountQ(ctx context.Context, q queryer) (bool, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Service) hasOperator(ctx context.Context, q queryer) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM users WHERE operator = 1 AND deleted_at IS NULL`).Scan(&n)
	if err != nil {
		return false, AsError(err)
	}
	return n > 0, nil
}

func randomKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

func channelKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 32)
	for i, b := range buf {
		out[i*2] = hexdigits[b>>4]
		out[i*2+1] = hexdigits[b&0x0f]
	}
	return string(out), nil
}
