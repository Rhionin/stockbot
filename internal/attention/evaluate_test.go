package attention

import (
	"testing"

	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
)

func baseEvaluateInput() EvaluateInput {
	return EvaluateInput{
		TripSeq:            1,
		Marks:              map[string]MarkRow{},
		SendByLine:         map[string][]SendResultRow{},
		Skipped:            map[string]struct{}{},
		ActiveZero:         map[string]struct{}{},
		PlanViews:          map[string]supply.LinePlanView{},
		UnpurchasedStaged:  map[string]int{},
		PlanViewEntryByKey: map[string]PlanEntryIDs{},
	}
}

func TestEvaluateOpeningAndSkippedNotInAttention(t *testing.T) {
	in := baseEvaluateInput()
	in.Opening = true
	in.PlanViews["item:new"] = supply.LinePlanView{Buy: 1}
	in.Skipped["item:skip"] = struct{}{}
	perLine, groups := Evaluate(in)
	if perLine["item:new"] != nil || perLine["item:skip"] != nil {
		t.Fatalf("exempt lines not flagged: %#v", perLine)
	}
	if len(groups) != 0 {
		t.Fatalf("groups %d", len(groups))
	}
}

func TestEvaluateMarkOnlyNoFlag(t *testing.T) {
	in := baseEvaluateInput()
	in.Marks["item:a"] = MarkRow{LineKey: "item:a", FlaggedTrip: 0}
	in.UnpurchasedStaged["item:a"] = 1
	perLine, _ := Evaluate(in)
	if perLine["item:a"] != nil {
		t.Fatalf("mark only want nil got %#v", perLine["item:a"])
	}
}

func TestEvaluateMarkPlusFactStuck(t *testing.T) {
	in := baseEvaluateInput()
	in.TripSeq = 2
	in.Marks["item:a"] = MarkRow{LineKey: "item:a", FlaggedTrip: 1}
	in.UnpurchasedStaged["item:a"] = 1
	in.SendByLine["item:a"] = []SendResultRow{{ItemID: "a", Status: "failed", Reason: "api_error", AttemptedAt: "2026-01-02T00:00:00Z"}}
	perLine, _ := Evaluate(in)
	if perLine["item:a"] == nil || string(perLine["item:a"].Reason) != string(ReasonSendFailed) || !perLine["item:a"].Stuck {
		t.Fatalf("stuck send_failed %#v", perLine["item:a"])
	}
	in.Marks["item:a"] = MarkRow{FlaggedTrip: 2}
	perLine, _ = Evaluate(in)
	if perLine["item:a"] == nil || perLine["item:a"].Stuck {
		t.Fatalf("equal trip not stuck %#v", perLine["item:a"])
	}
}

func TestEvaluateActiveZeroExemption(t *testing.T) {
	key := shopping.ItemLineKey("send-z")
	in := baseEvaluateInput()
	in.UnpurchasedStaged[key] = 1
	in.ActiveZero[key] = struct{}{}
	in.SendByLine[key] = []SendResultRow{{ItemID: "send-z", Status: "failed", Reason: "store_sign_in", AttemptedAt: "2026-01-01T00:00:00Z"}}
	perLine, _ := Evaluate(in)
	if perLine[key] != nil {
		t.Fatalf("active zero exempt %#v", perLine[key])
	}
}

func TestEvaluateSendFailedOnShortLine(t *testing.T) {
	key := shopping.ItemLineKey("item-bad")
	in := baseEvaluateInput()
	in.UnpurchasedStaged[key] = 1
	in.SendByLine[key] = []SendResultRow{{ItemID: "item-bad", Status: "failed", Reason: "store_sign_in", AttemptedAt: "2026-01-01T00:00:00Z"}}
	perLine, groups := Evaluate(in)
	if perLine[key] == nil || perLine[key].Detail != "store_sign_in" {
		t.Fatalf("send %#v", perLine[key])
	}
	if len(groups) != 1 {
		t.Fatalf("groups %#v", groups)
	}
}

func TestEvaluateNotPlannedExcluded(t *testing.T) {
	key := shopping.GroupLineKey("g1")
	in := baseEvaluateInput()
	in.PlanViews[key] = supply.LinePlanView{GroupID: "g1", Excluded: "no_product_picked"}
	perLine, _ := Evaluate(in)
	if perLine[key] == nil || perLine[key].Reason != ReasonNotPlanned || perLine[key].Detail != "no_product_picked" {
		t.Fatalf("not_planned %#v", perLine[key])
	}
}

func TestEvaluatePrecedenceAndOthers(t *testing.T) {
	key := shopping.ItemLineKey("x")
	in := baseEvaluateInput()
	in.UnpurchasedStaged[key] = 1
	in.SendByLine[key] = []SendResultRow{
		{ItemID: "x", Status: "skipped", Reason: "no_upc", AttemptedAt: "2026-01-01T00:00:00Z"},
		{ItemID: "x", Status: "failed", Reason: "api_error", AttemptedAt: "2026-01-02T00:00:00Z"},
	}
	perLine, _ := Evaluate(in)
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
}

func TestEvaluateNotShortNoSendFlag(t *testing.T) {
	key := shopping.ItemLineKey("ghost")
	in := baseEvaluateInput()
	in.SendByLine[key] = []SendResultRow{{ItemID: "ghost", Status: "failed", Reason: "api_error", AttemptedAt: "2026-01-01T00:00:00Z"}}
	perLine, _ := Evaluate(in)
	if perLine[key] != nil {
		t.Fatalf("not short %#v", perLine[key])
	}
}

func TestEvaluateGroupSendDetailTieBreak(t *testing.T) {
	key := shopping.GroupLineKey("G")
	in := baseEvaluateInput()
	in.UnpurchasedStaged[key] = 1
	t0 := "2026-06-01T12:00:00Z"
	t1 := "2026-06-01T12:00:01Z"
	in.SendByLine[key] = []SendResultRow{
		{ItemID: "item-a", Status: "failed", Reason: "api_error", AttemptedAt: t0},
		{ItemID: "item-b", Status: "failed", Reason: "batch_rejected", AttemptedAt: t1},
	}
	perLine, _ := Evaluate(in)
	if perLine[key] == nil || perLine[key].Detail != "batch_rejected" {
		t.Fatalf("(i) %#v", perLine[key])
	}
	in.SendByLine[key] = []SendResultRow{
		{ItemID: "item-a", Status: "failed", Reason: "batch_rejected", AttemptedAt: t1},
		{ItemID: "item-b", Status: "failed", Reason: "api_error", AttemptedAt: t0},
	}
	perLine, _ = Evaluate(in)
	if perLine[key] == nil || perLine[key].Detail != "batch_rejected" {
		t.Fatalf("(ii) %#v", perLine[key])
	}
	in.SendByLine[key] = []SendResultRow{
		{ItemID: "item-b", Status: "failed", Reason: "batch_rejected", AttemptedAt: t0},
		{ItemID: "item-a", Status: "failed", Reason: "api_error", AttemptedAt: t0},
	}
	perLine, _ = Evaluate(in)
	if perLine[key] == nil || perLine[key].Detail != "api_error" {
		t.Fatalf("(iii) %#v", perLine[key])
	}
	in.SendByLine[key] = []SendResultRow{
		{ItemID: "item-b", Status: "failed", Reason: "batch_rejected", AttemptedAt: t0},
		{ItemID: "item-a", Status: "failed", Reason: "api_error", AttemptedAt: t0},
	}
	perLine, _ = Evaluate(in)
	if perLine[key] == nil || perLine[key].Detail != "api_error" {
		t.Fatalf("(iii) order b,a %#v", perLine[key])
	}
}