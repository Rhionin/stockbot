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
	house  map[string]string
}

type contact struct {
	write      bool
	cameOnline bool
}

func newPresence() *presence {
	return &presence{
		seen:   map[string]time.Time{},
		dbAt:   map[string]time.Time{},
		wasOn:  map[string]bool{},
		queued: map[string]int{},
		house:  map[string]string{},
	}
}

func (p *presence) touch(id string, now time.Time) (write bool) {
	return p.note(id, "", now).write
}

func (p *presence) note(id, household string, now time.Time) contact {
	if p == nil || id == "" {
		return contact{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	prev := p.wasOn[id]
	p.seen[id] = now
	p.wasOn[id] = true
	if household != "" {
		p.house[id] = household
	}
	last := p.dbAt[id]
	write := !prev || last.IsZero() || now.Sub(last) >= presenceSave
	if write {
		p.dbAt[id] = now
	}
	return contact{write: write, cameOnline: !prev}
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

func (p *presence) household(id string) string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.house[id]
}

// dueOffline flips hubs whose last contact is at least window old.
func (p *presence) dueOffline(now time.Time, window time.Duration) []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for id, on := range p.wasOn {
		if !on {
			continue
		}
		seen, ok := p.seen[id]
		if !ok || now.Sub(seen) < window {
			continue
		}
		p.wasOn[id] = false
		out = append(out, id)
	}
	return out
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
	ID           string  `json:"id"`
	Label        string  `json:"label"`
	Name         string  `json:"name,omitempty"`
	CreatedAt    string  `json:"createdAt"`
	LastSeenAt   *string `json:"lastSeenAt"`
	RevokedAt    *string `json:"revokedAt"`
	Kind         string  `json:"kind"`
	Online       bool    `json:"online"`
	Queued       *int    `json:"queued,omitempty"`
	QueuedScans  *int    `json:"queuedScans,omitempty"`
	DroppedTotal int     `json:"droppedTotal"`
	DroppedCount int     `json:"droppedCount"`
	Version      string  `json:"version,omitempty"`
}

// PairedDevice is the one-time token from pairing complete.
type PairedDevice struct {
	DeviceID    string
	HouseholdID string
	Label       string
	Token       string
	Hub         bool
}

// RotatedToken is the one-time replacement bearer. GraceUntil is when the
// previous token stops working if the new one is never used.
type RotatedToken struct {
	Token      string
	GraceUntil string
}

// ScanInput is one device scan.
type ScanInput struct {
	ID               string
	Barcode          string
	Direction        string
	UnitCount        int
	Heartbeat        bool
	HasBarcode       bool
	HasScannedAt     bool
	ScannedAt        time.Time
	ScannedAtInvalid bool
	AgeMs            *int64
	BootID           string
}

// ScanResult is one item outcome.
type ScanResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	ScanID string `json:"scanId,omitempty"`
}

// Heartbeat is one hub check-in.
type Heartbeat struct {
	Version     string
	BootID      string
	UptimeS     int
	Queued      int
	Dropped     int
	ClockSynced bool
	Rollback    bool
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

func (s *Service) CompletePairing(ctx context.Context, code, label, hubKind, hubVersion, bootID string) (PairedDevice, error) {
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
		INSERT INTO devices (id, household_id, kind, label, created_at, hub_version, boot_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		deviceID, row.HouseholdID, kind, label, stamp(now), hubVersion, bootID); err != nil {
		return PairedDevice{}, AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO device_tokens (id, device_id, household_id, token_hash, prefix, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		newID(), deviceID, row.HouseholdID, HashOpaque(token), DevicePrefix(token), stamp(now)); err != nil {
		return PairedDevice{}, AsError(err)
	}
	if err := writeAudit(ctx, tx, row.HouseholdID, "device", deviceID, "device.paired", "device", deviceID, stamp(now)); err != nil {
		return PairedDevice{}, AsError(err)
	}
	if err := tx.Commit(); err != nil {
		return PairedDevice{}, AsError(err)
	}
	if s.hints != nil {
		s.hints.PublishHint(row.HouseholdID, "devices")
	}
	return PairedDevice{DeviceID: deviceID, HouseholdID: row.HouseholdID, Label: label, Token: token, Hub: hub}, nil
}

func (s *Service) ListDevices(ctx context.Context, userID, householdID string) ([]DeviceView, error) {
	if _, _, ok, err := s.membershipRole(ctx, s.db, userID, householdID); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, notFound()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, label, kind, created_at, COALESCE(last_seen_at, ''), COALESCE(revoked_at, ''),
		       COALESCE(hub_version, ''), queued, dropped_total
		FROM devices WHERE household_id = ? ORDER BY created_at`, householdID)
	if err != nil {
		return nil, AsError(err)
	}
	defer rows.Close()
	now := s.clock()
	out := []DeviceView{}
	for rows.Next() {
		var d DeviceView
		var seen, revoked, version string
		var queued, dropped int
		if err := rows.Scan(&d.ID, &d.Label, &d.Kind, &d.CreatedAt, &seen, &revoked, &version, &queued, &dropped); err != nil {
			return nil, AsError(err)
		}
		d.Name = d.Label
		d.Version = version
		d.DroppedTotal = dropped
		d.DroppedCount = dropped
		d.RevokedAt = strPtr(revoked)
		if t, ok := s.presence.last(d.ID); ok {
			stamp := stamp(t)
			d.LastSeenAt = &stamp
			d.Online = revoked == "" && now.Sub(t) < onlineWindow
		} else if seen != "" {
			d.LastSeenAt = &seen
			if at, ok := parseStamp(seen); ok {
				d.Online = revoked == "" && now.Sub(at) < onlineWindow
			}
		}
		if d.Kind == "direct" && revoked == "" {
			d.Online = true
		}
		if n, ok := s.presence.queue(d.ID); ok {
			queued = n
		}
		q := queued
		d.Queued = &q
		d.QueuedScans = &q
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
	now := stamp(s.clock())
	if _, err := s.db.ExecContext(ctx, `
		UPDATE devices SET label = ? WHERE id = ? AND household_id = ?`, label, deviceID, householdID); err != nil {
		return DeviceView{}, AsError(err)
	}
	if err := writeAudit(ctx, s.db, householdID, "account", userID, "device.renamed", "device", deviceID, now); err != nil {
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
	if err := writeAudit(ctx, tx, householdID, "account", userID, "device.revoked", "device", deviceID, now); err != nil {
		return AsError(err)
	}
	if s.hints != nil {
		s.hints.PublishHint(householdID, "rekey")
	}
	if err := tx.Commit(); err != nil {
		return AsError(err)
	}
	if s.hints != nil {
		s.hints.PublishHint(householdID, "devices")
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
func (s *Service) RotateDeviceToken(ctx context.Context, token string) (RotatedToken, error) {
	p, err := s.AuthenticateDevice(ctx, token)
	if err != nil {
		return RotatedToken{}, err
	}
	next, err := NewDeviceToken()
	if err != nil {
		return RotatedToken{}, AsError(err)
	}
	now := s.clock()
	grace := now.Add(tokenGrace)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RotatedToken{}, AsError(err)
	}
	defer tx.Rollback()
	id := newID()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO device_tokens (id, device_id, household_id, token_hash, prefix, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, p.DeviceID, p.HouseholdID, HashOpaque(next), DevicePrefix(next), stamp(now)); err != nil {
		return RotatedToken{}, AsError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE device_tokens SET superseded_at = ?, grace_until = ?
		WHERE device_id = ? AND household_id = ? AND id != ? AND superseded_at IS NULL`,
		stamp(now), stamp(grace), p.DeviceID, p.HouseholdID, id); err != nil {
		return RotatedToken{}, AsError(err)
	}
	if err := writeAudit(ctx, tx, p.HouseholdID, "device", p.DeviceID, "device.token_rotated", "device", p.DeviceID, stamp(now)); err != nil {
		return RotatedToken{}, AsError(err)
	}
	if err := tx.Commit(); err != nil {
		return RotatedToken{}, AsError(err)
	}
	return RotatedToken{Token: next, GraceUntil: stamp(grace)}, nil
}

// NoteContact records a heartbeat. rotate is true when the token is 30 days old.
func (s *Service) NoteContact(ctx context.Context, p Principal, version string, queued, dropped int) (bool, error) {
	return s.Heartbeat(ctx, p, Heartbeat{Version: version, Queued: queued, Dropped: dropped})
}

// Heartbeat records liveness. last_seen_at is written on a status flip or
// every 5 minutes. queued is the hub's unsent depth and is stored every time.
func (s *Service) Heartbeat(ctx context.Context, p Principal, beat Heartbeat) (bool, error) {
	now := s.clock()
	touch := s.presence.note(p.DeviceID, p.HouseholdID, now)
	s.presence.setQueued(p.DeviceID, beat.Queued)
	clockBit := 0
	if beat.ClockSynced {
		clockBit = 1
	}
	rollbackBit := 0
	if beat.Rollback {
		rollbackBit = 1
	}
	if touch.write {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE devices SET last_seen_at = ?, hub_version = CASE WHEN ? != '' THEN ? ELSE hub_version END,
				boot_id = CASE WHEN ? != '' THEN ? ELSE boot_id END,
				uptime_s = ?, queued = ?, clock_synced = ?, rollback = ?,
				dropped_total = dropped_total + ?
			WHERE id = ? AND household_id = ?`,
			stamp(now), beat.Version, beat.Version, beat.BootID, beat.BootID, beat.UptimeS, beat.Queued,
			clockBit, rollbackBit, beat.Dropped, p.DeviceID, p.HouseholdID); err != nil {
			return false, AsError(err)
		}
	} else {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE devices SET hub_version = CASE WHEN ? != '' THEN ? ELSE hub_version END,
				boot_id = CASE WHEN ? != '' THEN ? ELSE boot_id END,
				uptime_s = ?, queued = ?, clock_synced = ?, rollback = ?,
				dropped_total = dropped_total + ?
			WHERE id = ? AND household_id = ?`,
			beat.Version, beat.Version, beat.BootID, beat.BootID, beat.UptimeS, beat.Queued,
			clockBit, rollbackBit, beat.Dropped, p.DeviceID, p.HouseholdID); err != nil {
			return false, AsError(err)
		}
	}
	if beat.Dropped > 0 {
		if err := s.noteDropped(ctx, s.db, p.HouseholdID); err != nil {
			return false, err
		}
	}
	if touch.cameOnline && s.hints != nil {
		s.hints.PublishHint(p.HouseholdID, "devices")
	}
	created, err := s.deviceTokenAge(ctx, p.HouseholdID, p.SessionID)
	if err != nil {
		return false, err
	}
	return !now.Before(created.Add(tokenRotate)), nil
}
