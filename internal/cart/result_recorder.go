package cart

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// ResultRecorder persists the latest public send outcome per shopping line.
type ResultRecorder struct {
	db  *sql.DB
	now func() time.Time
}

// NewResultRecorder creates a recorder backed by the writer database.
func NewResultRecorder(db *sql.DB) *ResultRecorder {
	return &ResultRecorder{db: db, now: time.Now}
}

// SetClock replaces the UTC clock used for attempted_at and retention pruning (tests).
func (r *ResultRecorder) SetClock(now func() time.Time) {
	if now != nil {
		r.now = now
	}
}

// Record upserts one row per item outcome and prunes expired rows for this household.
func (r *ResultRecorder) Record(ctx context.Context, scope tenant.Scope, providerID ProviderID, confirm ConfirmationCapability, outcomes []EntryOutcome) error {
	if r == nil || r.db == nil {
		return nil
	}
	if err := scope.Require(); err != nil {
		return err
	}
	if len(outcomes) == 0 {
		return nil
	}
	rows := collapseSendResultOutcomes(outcomes, confirm)
	if len(rows) == 0 {
		return nil
	}

	at := r.now().UTC()
	attemptedAt := formatSendAttemptedAt(at)
	cutoff := at.Add(-SendResultRetention).Format(time.RFC3339)
	householdID := scope.HouseholdID()
	provider := string(providerID)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM cart_line_results WHERE household_id = ? AND attempted_at < ?`,
		householdID, cutoff); err != nil {
		return err
	}

	const upsert = `INSERT INTO cart_line_results (household_id, provider_id, item_id, entry_id, status, reason, quantity, attempted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(household_id, provider_id, item_id) DO UPDATE SET
			entry_id = excluded.entry_id,
			status = excluded.status,
			reason = excluded.reason,
			quantity = excluded.quantity,
			attempted_at = excluded.attempted_at
		WHERE cart_line_results.household_id = ?`

	for _, row := range rows {
		stored := storedLineReason(row.status, row.reason)
		var reason any
		if stored != "" {
			reason = string(stored)
		}
		var entryID any
		if row.entryID != "" {
			entryID = row.entryID
		}
		if _, err := tx.ExecContext(ctx, upsert,
			householdID, provider, row.itemID, entryID, string(row.status), reason, row.quantity, attemptedAt,
			householdID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// StoredSendResult is one visible cart_line_results row keyed by item id.
type StoredSendResult struct {
	LastSend
	EntryID string
}

// LatestResults returns visible send outcomes for one provider at or after cutoff.
func (r *ResultRecorder) LatestResults(ctx context.Context, scope tenant.Scope, providerID ProviderID, cutoff time.Time) (map[string]StoredSendResult, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	if err := scope.Require(); err != nil {
		return nil, err
	}
	cutoffStr := cutoff.UTC().Format(time.RFC3339)
	rows, err := r.db.QueryContext(ctx,
		`SELECT item_id, entry_id, status, reason, quantity, attempted_at
		 FROM cart_line_results
		 WHERE household_id = ? AND provider_id = ? AND attempted_at >= ?
		 ORDER BY item_id`,
		scope.HouseholdID(), string(providerID), cutoffStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]StoredSendResult)
	for rows.Next() {
		var itemID, status, attemptedAt string
		var entryID sql.NullString
		var reason sql.NullString
		var quantity int
		if err := rows.Scan(&itemID, &entryID, &status, &reason, &quantity, &attemptedAt); err != nil {
			return nil, err
		}
		row := StoredSendResult{
			LastSend: lastSendFromStored(string(providerID), PublicStatus(status), reason, quantity, attemptedAt),
		}
		if entryID.Valid {
			row.EntryID = entryID.String
		}
		out[itemID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type sendResultRow struct {
	entryID  string
	itemID   string
	status   PublicStatus
	reason   PublicReason
	quantity int
}

func collapseSendResultOutcomes(outcomes []EntryOutcome, confirm ConfirmationCapability) []sendResultRow {
	byItem := make(map[string]sendResultRow)
	order := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		if o.ItemID == "" {
			continue
		}
		row := outcomeToSendResultRow(o, confirm)
		existing, ok := byItem[o.ItemID]
		if !ok {
			byItem[o.ItemID] = row
			order = append(order, o.ItemID)
			continue
		}
		if publicStatusSeverity(row.status) > publicStatusSeverity(existing.status) {
			byItem[o.ItemID] = row
		}
	}
	result := make([]sendResultRow, 0, len(order))
	for _, id := range order {
		result = append(result, byItem[id])
	}
	return result
}

func outcomeToSendResultRow(o EntryOutcome, confirm ConfirmationCapability) sendResultRow {
	status := ProvisionResultStatus(o.Outcome, o.Reason, confirm)
	reason := PublicProvisionReason(o.Outcome, o.Reason)
	if o.Reason == ReasonStoreSignIn {
		reason = PublicReasonStoreSignIn
	}
	reason = storedLineReason(status, reason)
	return sendResultRow{
		entryID:  o.EntryID,
		itemID:   o.ItemID,
		status:   status,
		reason:   reason,
		quantity: o.Quantity,
	}
}

func publicStatusSeverity(status PublicStatus) int {
	switch status {
	case PublicStatusUnknown:
		return 4
	case PublicStatusFailed:
		return 3
	case PublicStatusSkipped:
		return 2
	case PublicStatusSent, PublicStatusAdded:
		return 1
	default:
		return 0
	}
}

func rewriteBatchOutcomesUnknown(outcomes []EntryOutcome, req ProvisionRequest) []EntryOutcome {
	wantEntry := make(map[string]struct{})
	for _, line := range req.Lines {
		for _, item := range line.Accounts {
			wantEntry[item.EntryID] = struct{}{}
		}
	}
	for i := range outcomes {
		if _, ok := wantEntry[outcomes[i].EntryID]; !ok {
			continue
		}
		if outcomes[i].Outcome != OutcomeConfirmed {
			continue
		}
		outcomes[i].Outcome = OutcomeUnknown
		outcomes[i].Reason = ReasonRequestIncomplete
		outcomes[i].Detail = requestIncompleteDetail
	}
	return outcomes
}

func appendStoreAuthOutcomes(outcomes []EntryOutcome, resolved []ResolvedItem) []EntryOutcome {
	for _, item := range resolved {
		outcomes = append(outcomes, EntryOutcome{
			EntryID:  item.EntryID,
			ItemID:   item.ItemID,
			Name:     item.Name,
			Quantity: item.Quantity,
			Outcome:  OutcomeFailed,
			Reason:   ReasonStoreSignIn,
		})
	}
	return outcomes
}

func shouldRecordSendResults(err error, report ProvisionReport, outcomes []EntryOutcome) bool {
	if len(outcomes) == 0 {
		return false
	}
	var conflict *ProvisionConflict
	if errors.As(err, &conflict) {
		return false
	}
	var reconnect *ErrReconnectRequired
	if errors.As(err, &reconnect) && len(report.Entries) == 0 {
		return false
	}
	return true
}

func (e *Engine) finishProvisionRecording(ctx context.Context, scope tenant.Scope, providerID ProviderID, confirm ConfirmationCapability, outcomes []EntryOutcome, report ProvisionReport, err error) {
	defer func() {
		if r := recover(); r != nil {
			logRecorderFailure(fmt.Errorf("panic: %v", r), len(outcomes))
		}
	}()
	if e.resultRecorder == nil || !shouldRecordSendResults(err, report, outcomes) {
		return
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if recErr := e.resultRecorder.Record(recordCtx, scope, providerID, confirm, outcomes); recErr != nil {
		logRecorderFailure(recErr, len(outcomes))
	}
}
