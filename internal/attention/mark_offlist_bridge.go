package attention

import (
	"strings"

)

// SupplementOffListMarkSummary augments perLine and groups for skipped lines that
// carry a mark but no D1 fact, so W3 summary notOnList accounting matches #684
// (flagged removed rows keep needsAttention and are excluded from notOnList).
// Ticket B will replace this with mark semantics inside Evaluate.
func SupplementOffListMarkSummary(
	perLine map[string]*NeedsAttention,
	groups []AttentionGroup,
	marks map[string]MarkRow,
	tripSeq int,
	skipped map[string]struct{},
	activeZero map[string]struct{},
	providerID string,
) []AttentionGroup {
	for key, mark := range marks {
		if _, rem := skipped[key]; !rem {
			continue
		}
		if _, z := activeZero[key]; z {
			continue
		}
		if perLine[key] != nil {
			continue
		}
		stuck := mark.FlaggedTrip < tripSeq
		var itemID, groupID string
		if g, ok := strings.CutPrefix(key, "group:"); ok {
			groupID = g
		} else if i, ok := strings.CutPrefix(key, "item:"); ok {
			itemID = i
		}
		perLine[key] = &NeedsAttention{
			Reason: ReasonSendUnsure,
			Others: []Reason{},
			Fix:    fixForFact(ReasonSendUnsure, "", itemID, groupID, providerID),
			Stuck:  stuck,
		}
		st := 0
		if stuck {
			st = 1
		}
		groups = append(groups, AttentionGroup{
			Reason: ReasonSendUnsure, Count: 1, Stuck: st, RowKeys: []string{key},
		})
	}
	return groups
}