package auth

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"
)

type presence struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	dbAt   map[string]time.Time
	wasOn  map[string]bool
	queued map[string]int
}

func newPresence() *presence {
	return &presence{
		seen:   map[string]time.Time{},
		dbAt:   map[string]time.Time{},
		wasOn:  map[string]bool{},
		queued: map[string]int{},
	}
}

func (p *presence) touch(id string, now time.Time) (write bool) {
	if p == nil || id == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	prev := p.wasOn[id]
	p.seen[id] = now
	p.wasOn[id] = true
	last := p.dbAt[id]
	if !prev || last.IsZero() || now.Sub(last) >= presenceSave {
		p.dbAt[id] = now
		return true
	}
	return false
}

func (p *presence) markOffline(id string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.wasOn[id] = false
	p.mu.Unlock()
}

func (p *presence) last(id string) (time.Time, bool) {
	if p == nil {
		return time.Time{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.seen[id]
	return t, ok
}

func (p *presence) setQueued(id string, n int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.queued[id] = n
	p.mu.Unlock()
}

func (p *presence) queue(id string) (int, bool) {
	if p == nil {
		return 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	n, ok := p.queued[id]
	return n, ok
}

// PairingCode is a new code a hub can type.
type PairingCode struct {
	Code         string `json:"code"`
	QRPayload    string `json:"qrPayload"`
	ExpiresAt    string `json:"expiresAt"`
	PairingID    string `json:"pairingId"`
	BarcodeValue string `json:"barcodeValue"`
	QRValue      string `json:"qrValue"`
}

// PairingStatus is the poll payload.
type PairingStatus struct {
	PairingID string  `json:"pairingId"`
	Status    string  `json:"status"`
	ExpiresAt string  `json:"expiresAt"`
	DeviceID  *string `json:"deviceId,omitempty"`
}

// DeviceView is one paired device.
type DeviceView struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	CreatedAt  string  `json:"createdAt"`
	LastSeenAt *string `json:"lastSeenAt"`
	RevokedAt  *string `json:"revokedAt"`
	Kind       string  `json:"kind"`
	Online     bool    `json:"online"`
	Queued     *int    `json:"queued,omitempty"`
}

// PairedDevice is the one-time token from pairing complete.
type PairedDevice struct {
	DeviceID string
	Token    string
	Hub      bool
}

// ScanInput is one device scan.
type ScanInput struct {
	ID         string
	Barcode    string
	Direction  string
	UnitCount  int
	Heartbeat  bool
	HasBarcode bool
}

// ScanResult is one item outcome.
type ScanResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

func (s *Service) StartPairing(ctx context.Context, userID, householdID string) (PairingCode, error) {
	if _, _, ok, err := s.membershipRole(ctx, s.db, userID, householdID); err != nil || !ok {
		if err != nil {
			return PairingCode{}, err
		}
		return PairingCode{}, notFound()
	}
	code, err := NewPairingCode()
	if err != nil {
		return PairingCode{}, AsError(err)
	}
	id := newID()
	exp := s.clock().Add(pairLife)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO pairing_codes (id, household_id, code_hash, created_by, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, householdID, HashOpaque(code), userID, stamp(exp)); err != nil {
		return PairingCode{}, AsError(err)
	}
	return PairingCode{
		Code: code, QRPayload: code, ExpiresAt: stamp(exp), PairingID: id,
		BarcodeValue: code, QRValue: code,
	}, nil
}

func (s *Service) PairingStatus(ctx context.Context, userID, householdID, id string) (PairingStatus, error) {
	if _, _, ok, err := s.membershipRole(ctx, s.db, userID, householdID); err != nil || !ok {
		if err != nil {
			return PairingStatus{}, err
		}
		return PairingStatus{}, notFound()
	}
	var exp string
	var device sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT expires_at, completed_device_id FROM pairing_codes
		WHERE id = ? AND household_id = ?`, id, householdID).Scan(&exp, &device)
	if err != nil {
		return PairingStatus{}, notFound()
	}
	status := "pending"
	if device.Valid && device.String != "" {
		status = "complete"
	} else if at, ok := parseStamp(exp); ok && !s.clock().Before(at) {
		status = "expired"
	}
	out := PairingStatus{PairingID: id, Status: status, ExpiresAt: exp}
	if device.Valid && device.String != "" {
		out.DeviceID = &device.String
	}
	return out, nil
}

func (s *Service) CompletePairing(ctx context.Context, code, label, hubKind, hubVersion string) (PairedDevice, error) {
	norm, well, checkOK := NormalizePairingCode(code)
	if !well {
		return PairedDevice{}, validation()
	}
	if !checkOK {
		return PairedDevice{}, notFound()
	}
	row, found, err := pairingByHash(ctx, s.db, HashOpaque(norm))
	if err != nil {
		return PairedDevice{}, AsError(err)
	}
	if !found {
		return PairedDevice{}, notFound()
	}
	if row.Attempts >= maxAttempts {
		return PairedDevice{}, rateLimited(pairIPWindow)
	}
	expired := false
	if at, ok := parseStamp(row.ExpiresAt); ok && !s.clock().Before(at) {
		expired = true
	}
	if expired || row.DeviceID != "" {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE pairing_codes SET attempts = attempts + 1 WHERE id = ? AND household_id = ?`,
			row.ID, row.HouseholdID); err != nil {
			return PairedDevice{}, AsError(err)
		}
		return PairedDevice{}, notFound()
	}
	if strings.TrimSpace(label) == "" {
		label = "Scanner"
	}
	label, nerr := normalizeName(label, "label")
	if nerr != nil {
		return PairedDevice{}, nerr
	}
	kind := "pi_hub"
	hub := false
	if hubKind != "" {
		if hubKind != "pi_hub" && hubKind != "box_hub" {
			return PairedDevice{}, validation()
		}
		kind = hubKind
		hub = true
	}
	token, err := NewDeviceToken()
	if err != nil {
		return PairedDevice{}, AsError(err)
	}
	now := s.clock()
	deviceID := newID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PairedDevice{}, AsError(err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE pairing_codes SET completed_device_id = ?
		WHERE id = ? AND household_id = ? AND completed_device_id IS NULL`,
		deviceID, row.ID, row.HouseholdID)
	if err != nil {
		return PairedDevice{}, AsError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return PairedDevice{}, notFound()
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO devices (id, household_id, kind, label, created_at, hub_version)
		VALUES (?, ?, ?, ?, ?, ?)`,
		deviceID, row.HouseholdID, kind, label, stamp(now), hubVersion); err != nil {
		return PairedDevice{}, AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO device_tokens (id, device_id, household_id, token_hash, prefix, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		newID(), deviceID, row.HouseholdID, HashOpaque(token), DevicePrefix(token), stamp(now)); err != nil {
		return PairedDevice{}, AsError(err)
	}
	if err := tx.Commit(); err != nil {
		return PairedDevice{}, AsError(err)
	}
	return PairedDevice{DeviceID: deviceID, Token: token, Hub: hub}, nil
}

func (s *Service) ListDevices(ctx context.Context, userID, householdID string) ([]DeviceView, error) {
	if _, _, ok, err := s.membershipRole(ctx, s.db, userID, householdID); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, notFound()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, label, kind, created_at, COALESCE(last_seen_at, ''), COALESCE(revoked_at, '')
		FROM devices WHERE household_id = ? ORDER BY created_at`, householdID)
	if err != nil {
		return nil, AsError(err)
	}
	defer rows.Close()
	now := s.clock()
	out := []DeviceView{}
	for rows.Next() {
		var d DeviceView
		var seen, revoked string
		if err := rows.Scan(&d.ID, &d.Label, &d.Kind, &d.CreatedAt, &seen, &revoked); err != nil {
			return nil, AsError(err)
		}
		d.RevokedAt = strPtr(revoked)
		if t, ok := s.presence.last(d.ID); ok {
			stamp := stamp(t)
			d.LastSeenAt = &stamp
			d.Online = revoked == "" && now.Sub(t) <= onlineWindow
		} else if seen != "" {
			d.LastSeenAt = &seen
			if at, ok := parseStamp(seen); ok {
				d.Online = revoked == "" && now.Sub(at) <= onlineWindow
			}
		}
		if n, ok := s.presence.queue(d.ID); ok {
			d.Queued = &n
		}
		out = append(out, d)
	}
	return out, AsError(rows.Err())
}

func (s *Service) RenameDevice(ctx context.Context, userID, householdID, deviceID, label string) (DeviceView, error) {
	if err := s.mutableDevice(ctx, userID, householdID, deviceID, false); err != nil {
		return DeviceView{}, err
	}
	label, err := normalizeName(label, "label")
	if err != nil {
		return DeviceView{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE devices SET label = ? WHERE id = ? AND household_id = ?`, label, deviceID, householdID); err != nil {
		return DeviceView{}, AsError(err)
	}
	return s.oneDevice(ctx, userID, householdID, deviceID)
}

func (s *Service) RevokeDevice(ctx context.Context, userID, householdID, deviceID string) error {
	if err := s.mutableDevice(ctx, userID, householdID, deviceID, true); err != nil {
		return err
	}
	key, err := channelKey()
	if err != nil {
		return AsError(err)
	}
	now := stamp(s.clock())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AsError(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE devices SET revoked_at = ? WHERE id = ? AND household_id = ? AND revoked_at IS NULL`,
		now, deviceID, householdID); err != nil {
		return AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE households SET channel_key = ? WHERE id = ?`, key, householdID); err != nil {
		return AsError(err)
	}
	if err := tx.Commit(); err != nil {
		return AsError(err)
	}
	return nil
}

func (s *Service) mutableDevice(ctx context.Context, userID, householdID, deviceID string, ownerOnly bool) error {
	role, deleted, ok, err := s.membershipRole(ctx, s.db, userID, householdID)
	if err != nil || !ok || deleted != "" {
		if err != nil {
			return err
		}
		return notFound()
	}
	if ownerOnly && role != "owner" {
		return fail(CodeForbiddenRole, SentenceForbidden)
	}
	var kind, label string
	err = s.db.QueryRowContext(ctx, `
		SELECT kind, label FROM devices WHERE id = ? AND household_id = ?`, deviceID, householdID).Scan(&kind, &label)
	if err != nil {
		return notFound()
	}
	if kind == "direct" {
		return notFound()
	}
	return nil
}

func (s *Service) oneDevice(ctx context.Context, userID, householdID, deviceID string) (DeviceView, error) {
	list, err := s.ListDevices(ctx, userID, householdID)
	if err != nil {
		return DeviceView{}, err
	}
	for _, d := range list {
		if d.ID == deviceID {
			return d, nil
		}
	}
	return DeviceView{}, notFound()
}

// AuthenticateDevice resolves a device token. Unknown tokens are unauthorized.
// A revoked device is device_revoked.
func (s *Service) AuthenticateDevice(ctx context.Context, token string) (Principal, error) {
	if !strings.HasPrefix(token, "sbd_") {
		return Principal{}, unauthorized()
	}
	row, found, err := deviceByTokenHash(ctx, s.db, HashOpaque(token))
	if err != nil {
		return Principal{}, AsError(err)
	}
	if !found {
		return Principal{}, unauthorized()
	}
	if row.RevokedAt != "" {
		return Principal{}, fail(CodeDeviceRevoked, SentenceDeviceRevoked)
	}
	now := s.clock()
	if row.SupersededAt != "" {
		until, ok := parseStamp(row.GraceUntil)
		if !ok || !now.Before(until) {
			return Principal{}, unauthorized()
		}
	} else {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE device_tokens SET grace_until = ?
			WHERE device_id = ? AND household_id = ? AND id != ? AND superseded_at IS NOT NULL`,
			stamp(now), row.DeviceID, row.HouseholdID, row.TokenID); err != nil {
			return Principal{}, AsError(err)
		}
	}
	return Principal{
		Kind:        "device",
		HouseholdID: row.HouseholdID,
		DeviceID:    row.DeviceID,
		SessionID:   row.TokenID,
	}, nil
}

func (s *Service) deviceTokenAge(ctx context.Context, householdID, tokenID string) (time.Time, error) {
	var created string
	err := s.db.QueryRowContext(ctx, `
		SELECT created_at FROM device_tokens WHERE household_id = ? AND id = ?`, householdID, tokenID).Scan(&created)
	if err != nil {
		return time.Time{}, AsError(err)
	}
	at, ok := parseStamp(created)
	if !ok {
		return time.Time{}, unauthorized()
	}
	return at, nil
}

// RotateDeviceToken issues a new token. The previous one stays valid for 24h
// or until this new token is used, whichever comes first.
func (s *Service) RotateDeviceToken(ctx context.Context, token string) (string, error) {
	p, err := s.AuthenticateDevice(ctx, token)
	if err != nil {
		return "", err
	}
	next, err := NewDeviceToken()
	if err != nil {
		return "", AsError(err)
	}
	now := s.clock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", AsError(err)
	}
	defer tx.Rollback()
	id := newID()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO device_tokens (id, device_id, household_id, token_hash, prefix, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, p.DeviceID, p.HouseholdID, HashOpaque(next), DevicePrefix(next), stamp(now)); err != nil {
		return "", AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE device_tokens SET superseded_at = ?, grace_until = ?
		WHERE device_id = ? AND household_id = ? AND id != ? AND superseded_at IS NULL`,
		stamp(now), stamp(now.Add(tokenGrace)), p.DeviceID, p.HouseholdID, id); err != nil {
		return "", AsError(err)
	}
	if err := tx.Commit(); err != nil {
		return "", AsError(err)
	}
	return next, nil
}

// NoteContact records a heartbeat. rotate is true when the token is older than 30 days.
func (s *Service) NoteContact(ctx context.Context, p Principal, version string, queued, dropped int) (bool, error) {
	now := s.clock()
	if s.presence.touch(p.DeviceID, now) {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE devices SET last_seen_at = ?, hub_version = CASE WHEN ? != '' THEN ? ELSE hub_version END,
				dropped_total = dropped_total + ?
			WHERE id = ? AND household_id = ?`,
			stamp(now), version, version, dropped, p.DeviceID, p.HouseholdID); err != nil {
			return false, AsError(err)
		}
	} else if dropped > 0 || version != "" {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE devices SET hub_version = CASE WHEN ? != '' THEN ? ELSE hub_version END,
				dropped_total = dropped_total + ?
			WHERE id = ? AND household_id = ?`,
			version, version, dropped, p.DeviceID, p.HouseholdID); err != nil {
			return false, AsError(err)
		}
	}
	s.presence.setQueued(p.DeviceID, queued)
	created, err := s.deviceTokenAge(ctx, p.HouseholdID, p.SessionID)
	if err != nil {
		return false, err
	}
	return now.Sub(created) > tokenRotate, nil
}

// IngestScans writes a batch or refuses the whole batch when the device is over 600/min.
func (s *Service) IngestScans(ctx context.Context, p Principal, items []ScanInput) ([]ScanResult, error) {
	if len(items) == 0 || len(items) > 50 {
		return nil, validation()
	}
	if retry, blocked := s.limits.Blocked(s.clock(), "scan:"+p.DeviceID, scanLimit, scanWindow); blocked {
		return nil, rateLimited(retry)
	}
	if _, blocked := s.limits.Hit(s.clock(), "scan:"+p.DeviceID, scanLimit, scanWindow); blocked {
		return nil, rateLimited(time.Second)
	}
	// Count every item, but the cap is scans not requests. Record len(items)-1 extra hits.
	for i := 1; i < len(items); i++ {
		if _, blocked := s.limits.Hit(s.clock(), "scan:"+p.DeviceID, scanLimit, scanWindow); blocked {
			return nil, rateLimited(time.Second)
		}
	}
	now := s.clock()
	s.presence.touch(p.DeviceID, now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, AsError(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE devices SET last_seen_at = ? WHERE id = ? AND household_id = ?`,
		stamp(now), p.DeviceID, p.HouseholdID); err != nil {
		return nil, AsError(err)
	}
	out := make([]ScanResult, 0, len(items))
	for _, item := range items {
		result, err := s.oneScan(ctx, tx, p, item, now)
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	if err := tx.Commit(); err != nil {
		return nil, AsError(err)
	}
	return out, nil
}

func (s *Service) oneScan(ctx context.Context, q queryer, p Principal, item ScanInput, now time.Time) (ScanResult, error) {
	if item.ID == "" {
		return ScanResult{Status: "rejected", Code: CodeValidation}, nil
	}
	if item.Heartbeat && !item.HasBarcode {
		return ScanResult{ID: item.ID, Status: "accepted", Cursor: item.ID}, nil
	}
	if !item.HasBarcode {
		return ScanResult{ID: item.ID, Status: "rejected", Code: CodeValidation}, nil
	}
	if item.Direction != "" && item.Direction != "stock_in" && item.Direction != "stock_out" {
		return ScanResult{ID: item.ID, Status: "rejected", Code: CodeValidation}, nil
	}
	unit := item.UnitCount
	if unit == 0 {
		unit = 1
	}
	if unit < 1 {
		return ScanResult{ID: item.ID, Status: "rejected", Code: CodeValidation}, nil
	}
	var dir any
	if item.Direction == "" {
		dir = nil
	} else {
		dir = item.Direction
	}
	id := newID()
	_, err := q.ExecContext(ctx, `
		INSERT INTO scan_entries (
			id, user_id, household_id, device_id, client_scan_id, barcode, scanned_at, direction, unit_count, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending')`,
		id, p.DeviceID, p.HouseholdID, p.DeviceID, item.ID, item.Barcode, stamp(now), dir, unit)
	if err == nil {
		return ScanResult{ID: item.ID, Status: "created", Cursor: id}, nil
	}
	var barcode, direction string
	var units int
	qerr := q.QueryRowContext(ctx, `
		SELECT barcode, COALESCE(direction, ''), unit_count FROM scan_entries
		WHERE household_id = ? AND device_id = ? AND client_scan_id = ?`,
		p.HouseholdID, p.DeviceID, item.ID).Scan(&barcode, &direction, &units)
	if qerr != nil {
		return ScanResult{}, AsError(err)
	}
	if barcode == item.Barcode && direction == item.Direction && units == unit {
		return ScanResult{ID: item.ID, Status: "duplicate", Cursor: item.ID}, nil
	}
	return ScanResult{ID: item.ID, Status: "conflict"}, nil
}
