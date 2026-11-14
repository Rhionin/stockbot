package attention

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// Fix describes who can clear a needs-attention flag.
type Fix struct {
	Who []FixActor `json:"who"`
}

type FixActor struct {
	Role string `json:"role,omitempty"`
}

// NeedsAttention is server-computed attention for one list row.
type NeedsAttention struct {
	Reason string   `json:"reason"`
	Detail string   `json:"detail,omitempty"`
	Others []string `json:"others"`
	Fix    Fix      `json:"fix"`
	Stuck  bool     `json:"stuck"`
}

// AttentionGroup batches rows sharing a reason for status=1.
type AttentionGroup struct {
	Reason  string   `json:"reason"`
	Detail  string   `json:"detail,omitempty"`
	Count   int      `json:"count"`
	Stuck   int      `json:"stuck"`
	RowKeys []string `json:"rowKeys"`
}

// MarkRow is one attention_marks row.
type MarkRow struct {
	LineKey     string
	FlaggedTrip int
}


// EvaluateInput is everything needed for one read pass.
type EvaluateInput struct {
	TripSeq        int
	Marks          map[string]MarkRow
	SendFailures   map[string]SendFailRow
	Skipped        map[string]struct{}
	Opening        bool
	PlannedKeys    map[string]struct{}
	StagedKeys     map[string]struct{}
}

// Evaluate builds per-line attention and groups for status=1.
func Evaluate(in EvaluateInput) (map[string]*NeedsAttention, []AttentionGroup) {
	perLine := map[string]*NeedsAttention{}
	groups := map[string]*AttentionGroup{}
	add := func(key, reason, detail string, stuck bool) {
		if reason == "" {
			return
		}
		na := &NeedsAttention{
			Reason: reason,
			Detail: detail,
			Fix:    Fix{Who: []FixActor{{Role: "member"}}},
			Stuck:  stuck,
		}
		perLine[key] = na
		gk := reason
		if detail != "" {
			gk = reason + "\x00" + detail
		}
		g := groups[gk]
		if g == nil {
			g = &AttentionGroup{Reason: reason, Detail: detail}
			groups[gk] = g
		}
		g.RowKeys = append(g.RowKeys, key)
		g.Count++
		if stuck {
			g.Stuck++
		}
	}
	for key := range in.Marks {
		stuck := in.Marks[key].FlaggedTrip < in.TripSeq
		reason := "attention"
		if _, staged := in.StagedKeys[key]; !staged {
			if _, planned := in.PlannedKeys[key]; !planned {
				reason = "not_planned"
			}
		}
		add(key, reason, "", stuck)
	}
	for itemID, row := range in.SendFailures {
		key := "item:" + itemID
		add(key, "send_failed", row.Detail, false)
	}
	outGroups := make([]AttentionGroup, 0, len(groups))
	for _, g := range groups {
		outGroups = append(outGroups, *g)
	}
	return perLine, outGroups
}

// LoadSendFailRow is a failed send line for attention evaluation.
type SendFailRow struct {
	ItemID string
	Reason string
	Detail string
}

// LoadMarks batch-loads attention_marks for the household.
func LoadMarks(ctx context.Context, db *sql.DB, scope tenant.Scope) (map[string]MarkRow, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT line_key, flagged_trip FROM attention_marks WHERE household_id = ?`,
		scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not load attention marks: %w", err)
	}
	defer rows.Close()
	out := map[string]MarkRow{}
	for rows.Next() {
		var r MarkRow
		if err := rows.Scan(&r.LineKey, &r.FlaggedTrip); err != nil {
			return nil, err
		}
		out[r.LineKey] = r
	}
	return out, rows.Err()
}

// LoadSendFailures batch-loads failed cart_line_results.
func LoadSendFailures(ctx context.Context, db *sql.DB, scope tenant.Scope) (map[string]SendFailRow, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT item_id, COALESCE(reason,''), COALESCE(reason,'')
		FROM cart_line_results WHERE household_id = ? AND status = 'failed'`,
		scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not load send failures: %w", err)
	}
	defer rows.Close()
	out := map[string]SendFailRow{}
	for rows.Next() {
		var r SendFailRow
		if err := rows.Scan(&r.ItemID, &r.Reason, &r.Detail); err != nil {
			return nil, err
		}
		out[r.ItemID] = r
	}
	return out, rows.Err()
}