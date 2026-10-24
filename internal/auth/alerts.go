package auth

import (
	"context"
	"time"

	"github.com/Rhionin/stockbot/internal/changes"
)

const (
	alertBusy         = "busy"
	alertNoSpace      = "no_space"
	alertScansDropped = "scans_dropped"
)

// OperatorAlert is one row on the Server page. The name is resolved from the
// household id when the page is read. The stored row has no item or barcode.
type OperatorAlert struct {
	ID            string `json:"id"`
	HouseholdID   string `json:"householdId"`
	HouseholdName string `json:"householdName,omitempty"`
	Kind          string `json:"kind"`
	CreatedAt     string `json:"createdAt"`
}

// OperatorNote is the push body. It is built from the household name and a
// kind, so a scan barcode or an item name cannot be copied in.
type OperatorNote struct {
	HouseholdID   string `json:"householdId"`
	HouseholdName string `json:"householdName,omitempty"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	Body          string `json:"body"`
}

// OperatorPush delivers one note to the operator's browsers.
type OperatorPush func(ctx context.Context, note OperatorNote)

// HintBus publishes a household hint after a device change. The payload is
// the kind and the household id, never a name or a barcode.
type HintBus interface {
	PublishHint(householdID, kind string)
}

// RecordOperatorAlert stores a household-scoped notice and pushes it.
// scans_dropped is recorded once per household. busy and no_space repeat.
func (s *Service) RecordOperatorAlert(ctx context.Context, householdID, kind string) error {
	if s == nil || householdID == "" || !knownAlert(kind) {
		return nil
	}
	inserted, err := s.insertAlert(ctx, s.db, householdID, kind, kind == alertScansDropped)
	if err != nil {
		return err
	}
	if inserted || kind != alertScansDropped {
		s.announce(ctx, householdID, kind)
	}
	return nil
}

// ListOperatorAlerts is the Server page list. A caller who is not the
// operator gets the same not_found as a missing page.
func (s *Service) ListOperatorAlerts(ctx context.Context, userID string) ([]OperatorAlert, error) {
	var op int
	err := s.db.QueryRowContext(ctx, `SELECT operator FROM users WHERE id = ? AND deleted_at IS NULL`, userID).Scan(&op)
	if err != nil || op != 1 {
		return nil, notFound()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT operator_alerts.id, operator_alerts.household_id, households.name,
		       operator_alerts.kind, operator_alerts.created_at
		FROM operator_alerts
		JOIN households ON households.id = operator_alerts.household_id
		WHERE operator_alerts.household_id != ''
		ORDER BY operator_alerts.created_at, operator_alerts.id`)
	if err != nil {
		return nil, AsError(err)
	}
	defer rows.Close()
	out := []OperatorAlert{}
	for rows.Next() {
		var alert OperatorAlert
		if err := rows.Scan(&alert.ID, &alert.HouseholdID, &alert.HouseholdName, &alert.Kind, &alert.CreatedAt); err != nil {
			return nil, AsError(err)
		}
		out = append(out, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, AsError(err)
	}
	return out, nil
}

func (s *Service) noteDropped(ctx context.Context, q queryer, householdID string) error {
	inserted, err := s.insertAlert(ctx, q, householdID, alertScansDropped, true)
	if err != nil {
		return err
	}
	if inserted {
		s.announce(ctx, householdID, alertScansDropped)
	}
	return nil
}

func (s *Service) insertAlert(ctx context.Context, q queryer, householdID, kind string, once bool) (bool, error) {
	if once {
		var n int
		if err := q.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM operator_alerts WHERE household_id = ? AND kind = ?`,
			householdID, kind).Scan(&n); err != nil {
			return false, AsError(err)
		}
		if n > 0 {
			return false, nil
		}
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO operator_alerts (id, household_id, kind, created_at)
		VALUES (?, ?, ?, ?)`,
		newID(), householdID, kind, stamp(s.clock()))
	if err != nil {
		return false, AsError(err)
	}
	return true, nil
}

func (s *Service) announce(ctx context.Context, householdID, kind string) {
	if s == nil || s.operatorPush == nil {
		return
	}
	name := s.householdName(ctx, householdID)
	title, body := operatorCopy(name, kind)
	s.operatorPush(ctx, OperatorNote{
		HouseholdID:   householdID,
		HouseholdName: name,
		Kind:          kind,
		Title:         title,
		Body:          body,
	})
}

func (s *Service) householdName(ctx context.Context, householdID string) string {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT name FROM households WHERE id = ?`, householdID).Scan(&name)
	if err != nil {
		return ""
	}
	return name
}

func operatorCopy(name, kind string) (string, string) {
	title := "Stockbot needs attention"
	switch kind {
	case alertBusy:
		title = "Stockbot is busy"
	case alertNoSpace:
		title = "Stockbot is out of storage"
	case alertScansDropped:
		title = "A hub dropped unsent scans"
	}
	if name == "" {
		return title, title + "."
	}
	return title, name + " — " + title + "."
}

func knownAlert(kind string) bool {
	switch kind {
	case alertBusy, alertNoSpace, alertScansDropped:
		return true
	default:
		return false
	}
}

// SweepOffline flips hubs that have been quiet for 300 seconds and publishes
// a devices hint. The clock is the service clock, so tests move time directly.
func (s *Service) SweepOffline() []string {
	if s == nil {
		return nil
	}
	now := s.clock()
	ids := s.presence.dueOffline(now, onlineWindow)
	for _, id := range ids {
		house := s.presence.household(id)
		if t, ok := s.presence.last(id); ok && house != "" {
			tx, err := s.db.Begin()
			if err == nil {
				_, _ = tx.Exec(`UPDATE devices SET last_seen_at = ? WHERE id = ? AND household_id = ?`, stamp(t), id, house)
				_ = changes.Bump(context.Background(), tx, house, changes.ScopeDevices)
				_ = tx.Commit()
			}
		}
		if s.hints != nil && house != "" {
			s.hints.PublishHint(house, "devices")
		}
	}
	return ids
}

// RunSweeper runs SweepOffline on each tick. Production uses a 30 second ticker.
func (s *Service) RunSweeper(ctx context.Context, ticks <-chan time.Time) {
	if s == nil || ticks == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ticks:
			if !ok {
				return
			}
			s.SweepOffline()
		}
	}
}

// SetHints publishes device and scan hints. Nil skips them.
func (s *Service) SetHints(bus HintBus) {
	if s == nil {
		return
	}
	s.hints = bus
}

// SetOperatorPush sends operator alerts. Nil stores the row and does not push.
func (s *Service) SetOperatorPush(fn OperatorPush) {
	if s == nil {
		return
	}
	s.operatorPush = fn
}

// SetBarcodeLookup queues product lookup after a device batch commits.
func (s *Service) SetBarcodeLookup(fn func(ctx context.Context, householdID, scanID, barcode string)) {
	if s == nil {
		return
	}
	s.barcodeLookup = fn
}
