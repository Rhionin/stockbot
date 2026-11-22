package attention

import (
	"testing"

	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
)

func assertEmittableAttention(t *testing.T, perLine map[string]*NeedsAttention, groups []AttentionGroup) {
	t.Helper()
	for key, na := range perLine {
		if na == nil {
			continue
		}
		if err := ValidateWireReasons(string(na.Reason), na.Others); err != nil {
			t.Fatalf("line %s: %v", key, err)
		}
		for _, o := range na.Others {
			if ContainsReserved(string(o)) {
				t.Fatalf("reserved other %q on %s", o, key)
			}
		}
		if na.Reason == ReasonNotPlanned && na.Detail == "" {
			t.Fatalf("not_planned requires detail on %s", key)
		}
	}
	for _, g := range groups {
		if err := ValidateWireReasons(string(g.Reason), nil); err != nil {
			t.Fatalf("group: %v", err)
		}
		if g.Reason == ReasonNotPlanned && g.Detail == "" {
			t.Fatal("group not_planned requires detail")
		}
	}
}

func TestNeedsAttention(t *testing.T) {
	t.Run("A1_mark_only", func(t *testing.T) {
		in := baseEvaluateInput()
		in.Marks["item:a"] = MarkRow{LineKey: "item:a", FlaggedTrip: 0}
		in.UnpurchasedStaged["item:a"] = 1
		perLine, groups := Evaluate(in)
		if perLine["item:a"] != nil {
			t.Fatalf("mark only want nil got %#v", perLine["item:a"])
		}
		assertEmittableAttention(t, perLine, groups)
	})

	t.Run("A2_mark_plus_fact_stuck", func(t *testing.T) {
		in := baseEvaluateInput()
		in.TripSeq = 2
		in.Marks["item:a"] = MarkRow{LineKey: "item:a", FlaggedTrip: 1}
		in.UnpurchasedStaged["item:a"] = 1
		in.SendByLine["item:a"] = []SendResultRow{{ItemID: "a", Status: "failed", Reason: "api_error", AttemptedAt: "2026-01-02T00:00:00Z"}}
		perLine, groups := Evaluate(in)
		if perLine["item:a"] == nil || string(perLine["item:a"].Reason) != string(ReasonSendFailed) || !perLine["item:a"].Stuck {
			t.Fatalf("stuck send_failed %#v", perLine["item:a"])
		}
		assertEmittableAttention(t, perLine, groups)
		in.Marks["item:a"] = MarkRow{FlaggedTrip: 2}
		perLine, groups = Evaluate(in)
		if perLine["item:a"] == nil || perLine["item:a"].Stuck {
			t.Fatalf("equal trip not stuck %#v", perLine["item:a"])
		}
		assertEmittableAttention(t, perLine, groups)
	})

	t.Run("A3_exemptions_cross_sources", func(t *testing.T) {
		key := shopping.ItemLineKey("x")
		exempts := []struct {
			name  string
			apply func(*EvaluateInput)
		}{
			{"removed", func(in *EvaluateInput) { in.Skipped[key] = struct{}{} }},
			{"opening", func(in *EvaluateInput) { in.Opening = true }},
			{"active_zero", func(in *EvaluateInput) { in.ActiveZero[key] = struct{}{} }},
		}
		sources := []struct {
			name  string
			apply func(*EvaluateInput)
		}{
			{"send_failed", func(in *EvaluateInput) {
				in.SendByLine[key] = []SendResultRow{{ItemID: "x", Status: "failed", Reason: "api_error", AttemptedAt: "2026-01-01T00:00:00Z"}}
			}},
			{"not_planned", func(in *EvaluateInput) {
				in.PlanViews[key] = supply.LinePlanView{GroupID: "g", Excluded: "no_product_picked"}
			}},
			{"mark", func(in *EvaluateInput) {
				in.Marks[key] = MarkRow{LineKey: key, FlaggedTrip: 0}
			}},
		}
		for _, ex := range exempts {
			for _, src := range sources {
				t.Run(ex.name+"/"+src.name, func(t *testing.T) {
					in := baseEvaluateInput()
					in.UnpurchasedStaged[key] = 1
					ex.apply(&in)
					src.apply(&in)
					perLine, groups := Evaluate(in)
					if perLine[key] != nil {
						t.Fatalf("exempt %#v", perLine[key])
					}
					assertEmittableAttention(t, perLine, groups)
				})
			}
		}
	})

	t.Run("A4_active_zero_exempt_without_placement", func(t *testing.T) {
		key := shopping.ItemLineKey("placement")
		in := baseEvaluateInput()
		in.ActiveZero[key] = struct{}{}
		in.SendByLine[key] = []SendResultRow{{ItemID: "placement", Status: "failed", Reason: "api_error", AttemptedAt: "2026-01-01T00:00:00Z"}}
		in.UnpurchasedStaged[key] = 1
		perLine, groups := Evaluate(in)
		if perLine[key] != nil {
			t.Fatalf("active zero exempt %#v", perLine[key])
		}
		assertEmittableAttention(t, perLine, groups)
	})

	t.Run("A6_not_short_no_send_flag", func(t *testing.T) {
		key := shopping.ItemLineKey("ghost")
		in := baseEvaluateInput()
		in.SendByLine[key] = []SendResultRow{{ItemID: "ghost", Status: "failed", Reason: "api_error", AttemptedAt: "2026-01-01T00:00:00Z"}}
		perLine, groups := Evaluate(in)
		if perLine[key] != nil {
			t.Fatalf("not short %#v", perLine[key])
		}
		assertEmittableAttention(t, perLine, groups)
	})

	t.Run("A7_hand_added_manual_row", func(t *testing.T) {
		key := shopping.ItemLineKey("manual")
		in := baseEvaluateInput()
		in.UnpurchasedStaged[key] = 1
		in.SendByLine[key] = []SendResultRow{{ItemID: "manual", Status: "failed", Reason: "store_sign_in", AttemptedAt: "2026-01-01T00:00:00Z"}}
		perLine, groups := Evaluate(in)
		if perLine[key] == nil || perLine[key].Reason != ReasonSendFailed {
			t.Fatalf("manual short line %#v", perLine[key])
		}
		assertEmittableAttention(t, perLine, groups)
	})

	t.Run("A8_precedence_and_others", func(t *testing.T) {
		key := shopping.ItemLineKey("x")
		in := baseEvaluateInput()
		in.UnpurchasedStaged[key] = 1
		in.SendByLine[key] = []SendResultRow{
			{ItemID: "x", Status: "skipped", Reason: "no_upc", AttemptedAt: "2026-01-01T00:00:00Z"},
			{ItemID: "x", Status: "failed", Reason: "api_error", AttemptedAt: "2026-01-02T00:00:00Z"},
		}
		perLine, groups := Evaluate(in)
		if perLine[key] == nil {
			t.Fatal("nil")
		}
		if perLine[key].Reason != ReasonNoBarcode {
			t.Fatalf("reason %s", perLine[key].Reason)
		}
		if len(perLine[key].Others) != 1 || perLine[key].Others[0] != ReasonSendFailed {
			t.Fatalf("others %#v", perLine[key].Others)
		}
		if perLine[key].Others == nil {
			t.Fatal("others must be non-nil slice")
		}
		assertEmittableAttention(t, perLine, groups)
	})

	t.Run("A10_group_send_detail_tie_break", func(t *testing.T) {
		key := shopping.GroupLineKey("G")
		in := baseEvaluateInput()
		in.UnpurchasedStaged[key] = 1
		t0 := "2026-06-01T12:00:00Z"
		t1 := "2026-06-01T12:00:01Z"
		cases := []struct {
			name string
			rows []SendResultRow
			want string
		}{
			{"i_newer_wins", []SendResultRow{
				{ItemID: "item-a", Status: "failed", Reason: "api_error", AttemptedAt: t0},
				{ItemID: "item-b", Status: "failed", Reason: "batch_rejected", AttemptedAt: t1},
			}, "batch_rejected"},
			{"ii_newer_wins_reversed_ids", []SendResultRow{
				{ItemID: "item-a", Status: "failed", Reason: "batch_rejected", AttemptedAt: t1},
				{ItemID: "item-b", Status: "failed", Reason: "api_error", AttemptedAt: t0},
			}, "batch_rejected"},
			{"iii_same_time_item_id", []SendResultRow{
				{ItemID: "item-b", Status: "failed", Reason: "batch_rejected", AttemptedAt: t0},
				{ItemID: "item-a", Status: "failed", Reason: "api_error", AttemptedAt: t0},
			}, "api_error"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				in.SendByLine[key] = tc.rows
				perLine, groups := Evaluate(in)
				if perLine[key] == nil || perLine[key].Detail != tc.want {
					t.Fatalf("%s %#v", tc.name, perLine[key])
				}
				assertEmittableAttention(t, perLine, groups)
				rev := make([]SendResultRow, len(tc.rows))
				copy(rev, tc.rows)
				for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
					rev[i], rev[j] = rev[j], rev[i]
				}
				in.SendByLine[key] = rev
				perLine2, groups2 := Evaluate(in)
				if perLine2[key] == nil || perLine2[key].Detail != tc.want {
					t.Fatalf("%s reversed order %#v", tc.name, perLine2[key])
				}
				assertEmittableAttention(t, perLine2, groups2)
			})
		}
	})

	t.Run("A12_conn_fact_sendable_gate", func(t *testing.T) {
		key := shopping.ItemLineKey("sendable")
		keyOff := shopping.ItemLineKey("off")
		in := baseEvaluateInput()
		in.ConnFact = ReasonReconnectRequired
		in.ProviderID = "kroger"
		in.ConnectAllowed = true
		in.UnpurchasedStaged[key] = 1
		in.UnpurchasedStaged[keyOff] = 1
		in.Sendable = map[string]bool{key: true, keyOff: false}
		in.SendByLine[keyOff] = []SendResultRow{{ItemID: "off", Status: "failed", Reason: "api_error", AttemptedAt: "2026-01-01T00:00:00Z"}}
		perLine, groups := Evaluate(in)
		if perLine[key] == nil || perLine[key].Reason != ReasonReconnectRequired {
			t.Fatalf("sendable line %#v", perLine[key])
		}
		if perLine[keyOff] == nil || perLine[keyOff].Reason != ReasonSendFailed {
			t.Fatalf("non-sendable keeps send fact %#v", perLine[keyOff])
		}
		assertEmittableAttention(t, perLine, groups)
	})
}
