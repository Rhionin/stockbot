package attention

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// NeedsAttention is server-computed attention for one list row.
type NeedsAttention struct {
	Reason Reason   `json:"reason"`
	Detail string   `json:"detail,omitempty"`
	Others []Reason `json:"others"`
	Fix    Fix      `json:"fix"`
	Stuck  bool     `json:"stuck"`
}

// AttentionGroup batches rows sharing a reason for status=1.
type AttentionGroup struct {
	Reason  Reason   `json:"reason"`
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

type lineFact struct {
	reason Reason
	detail string
}

// EvaluateInput is everything needed for one read pass.
type EvaluateInput struct {
	TripSeq            int
	Marks              map[string]MarkRow
	SendByLine         map[string][]SendResultRow
	Skipped            map[string]struct{}
	Opening            bool
	ActiveZero         map[string]struct{}
	PlanViews          map[string]supply.LinePlanView
	UnpurchasedStaged  map[string]int
	ProviderID         string
	PlanViewEntryByKey map[string]PlanEntryIDs
	ConnFact           Reason
	Sendable           map[string]bool
	ConnectAllowed     bool
}

// PlanEntryIDs holds wire fix target ids for one line key.
type PlanEntryIDs struct {
	EntryID string
	ItemID  string
	GroupID string
}

// Evaluate builds per-line attention and groups for status=1 (pure, no I/O).
func Evaluate(in EvaluateInput) (map[string]*NeedsAttention, []AttentionGroup) {
	candidates := collectCandidates(in)
	perLine := map[string]*NeedsAttention{}
	groups := map[string]*AttentionGroup{}

	for key := range candidates {
		if exempt(in, key) {
			continue
		}
		if !short(in, key) {
			continue
		}
		facts := collectFacts(in, key)
		if len(facts) == 0 {
			continue
		}
		sort.Slice(facts, func(i, j int) bool {
			return reasonPrecedence(facts[i].reason) < reasonPrecedence(facts[j].reason)
		})
		primary := facts[0]
		var others []Reason
		for _, f := range facts[1:] {
			others = append(others, f.reason)
		}
		if others == nil {
			others = []Reason{}
		}
		stuck := false
		if m, ok := in.Marks[key]; ok {
			stuck = m.FlaggedTrip < in.TripSeq
		}
		ids := in.PlanViewEntryByKey[key]
		entryID, itemID, groupID := ids.EntryID, ids.ItemID, ids.GroupID
		if row := primarySendRow(in, key, primary.reason); row.ItemID != "" {
			if row.EntryID != "" {
				entryID = row.EntryID
			}
			itemID = row.ItemID
		}
		na := &NeedsAttention{
			Reason: primary.reason,
			Detail: primary.detail,
			Others: others,
			Fix:    fixForFact(primary.reason, entryID, itemID, groupID, in.ProviderID, in.ConnectAllowed),
			Stuck:  stuck,
		}
		perLine[key] = na

		gk := string(primary.reason)
		if primary.detail != "" {
			gk += "\x00" + primary.detail
		}
		g := groups[gk]
		if g == nil {
			g = &AttentionGroup{Reason: primary.reason, Detail: primary.detail}
			groups[gk] = g
		}
		g.RowKeys = append(g.RowKeys, key)
		g.Count++
		if stuck {
			g.Stuck++
		}
	}

	outGroups := make([]AttentionGroup, 0, len(groups))
	for _, g := range groups {
		sort.Strings(g.RowKeys)
		outGroups = append(outGroups, *g)
	}
	sort.Slice(outGroups, func(i, j int) bool {
		if outGroups[i].Reason != outGroups[j].Reason {
			return reasonPrecedence(outGroups[i].Reason) < reasonPrecedence(outGroups[j].Reason)
		}
		return outGroups[i].Detail < outGroups[j].Detail
	})
	return perLine, outGroups
}

func collectCandidates(in EvaluateInput) map[string]struct{} {
	out := map[string]struct{}{}
	for k := range in.PlanViews {
		out[k] = struct{}{}
	}
	for k := range in.UnpurchasedStaged {
		out[k] = struct{}{}
	}
	for k := range in.SendByLine {
		out[k] = struct{}{}
	}
	return out
}

func exempt(in EvaluateInput, key string) bool {
	if in.Opening {
		return true
	}
	if _, ok := in.Skipped[key]; ok {
		return true
	}
	if _, ok := in.ActiveZero[key]; ok {
		return true
	}
	return false
}

func short(in EvaluateInput, key string) bool {
	if pv, ok := in.PlanViews[key]; ok {
		if pv.Buy >= 1 {
			return true
		}
		if pv.Excluded != "" {
			return true
		}
	}
	if in.UnpurchasedStaged[key] >= 1 {
		return true
	}
	return false
}

func collectFacts(in EvaluateInput, key string) []lineFact {
	var facts []lineFact
	if ConnectionFactForKey(in, key) {
		facts = append(facts, lineFact{reason: in.ConnFact})
	}
	if pv, ok := in.PlanViews[key]; ok && pv.Excluded != "" {
		facts = append(facts, lineFact{reason: ReasonNotPlanned, detail: pv.Excluded})
	}
	rows := in.SendByLine[key]
	byReason := map[Reason][]SendResultRow{}
	for _, row := range rows {
		reason, _, ok := WireFromStored(row.Status, row.Reason)
		if !ok || !isEmittable(reason) {
			continue
		}
		byReason[reason] = append(byReason[reason], row)
	}
	for reason, rs := range byReason {
		best := pickRepresentativeRow(rs)
		_, detail, _ := WireFromStored(best.Status, best.Reason)
		facts = append(facts, lineFact{reason: reason, detail: detail})
	}
	return facts
}

func primarySendRow(in EvaluateInput, key string, primary Reason) SendResultRow {
	rows := in.SendByLine[key]
	byReason := map[Reason][]SendResultRow{}
	for _, row := range rows {
		reason, _, ok := WireFromStored(row.Status, row.Reason)
		if !ok || !isEmittable(reason) {
			continue
		}
		byReason[reason] = append(byReason[reason], row)
	}
	return pickRepresentativeRow(byReason[primary])
}

func pickRepresentativeRow(rows []SendResultRow) SendResultRow {
	if len(rows) == 0 {
		return SendResultRow{}
	}
	best := rows[0]
	for _, r := range rows[1:] {
		if r.AttemptedAt > best.AttemptedAt {
			best = r
			continue
		}
		if r.AttemptedAt == best.AttemptedAt && r.ItemID < best.ItemID {
			best = r
		}
	}
	return best
}

// LoadMarks batch-loads attention_marks for the household.
func LoadMarks(ctx context.Context, db *sql.DB, scope tenant.Scope) (map[string]MarkRow, error) {
	if faultAfterLoadMarks != nil {
		if err := faultAfterLoadMarks(); err != nil {
			return nil, err
		}
	}
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, invokeFault(faultAfterLoadMarks)
}

// GroupKey builds attention group map key.
func GroupKey(reason Reason, detail string) string {
	if detail == "" {
		return string(reason)
	}
	return string(reason) + "\x00" + detail
}

// FormatReasons converts Reason slice to strings for JSON wire in server layer.
func FormatReasons(rs []Reason) []string {
	if len(rs) == 0 {
		return []string{}
	}
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = string(r)
	}
	return out
}
