package cart

import (
	"context"
	"log"
	"time"
)

const sendResultRetentionBatch = 500

// SendResultRetentionResult counts one sweep pass over expired send-result rows.
type SendResultRetentionResult struct {
	Batches int
	Deleted int
	Largest int
}

// SweepExpiredSendResults deletes cart_line_results rows older than SendResultRetention.
func (r *ResultRecorder) SweepExpiredSendResults(ctx context.Context) (SendResultRetentionResult, error) {
	var result SendResultRetentionResult
	if r == nil || r.db == nil {
		return result, nil
	}
	cutoff := r.now().UTC().Add(-SendResultRetention).Format(time.RFC3339)
	const deleteExpired = `DELETE FROM cart_line_results WHERE rowid IN (
		SELECT rowid FROM cart_line_results WHERE attempted_at < ? LIMIT ?)`
	for {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		res, err := r.db.ExecContext(ctx, deleteExpired, cutoff, sendResultRetentionBatch)
		if err != nil {
			return result, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return result, err
		}
		if n == 0 {
			return result, nil
		}
		result.Batches++
		result.Deleted += int(n)
		if int(n) > result.Largest {
			result.Largest = int(n)
		}
		if n < sendResultRetentionBatch {
			return result, nil
		}
	}
}

// RunSendResultRetention runs SweepExpiredSendResults once, then on every tick until ctx is cancelled.
func (r *ResultRecorder) RunSendResultRetention(ctx context.Context, every time.Duration) {
	if r == nil {
		return
	}
	if every <= 0 {
		every = 24 * time.Hour
	}
	run := func() {
		if ctx.Err() != nil {
			return
		}
		if _, err := r.SweepExpiredSendResults(ctx); err != nil && ctx.Err() == nil {
			log.Printf("send-result retention: %s", err.Error())
		}
	}
	run()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
