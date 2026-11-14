package supply

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Rhionin/stockbot/internal/restock"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// PlanTargetKind is the read-time target shape for planBasis.
type PlanTarget struct {
	Kind string `json:"kind"`
	// amount
	Value int `json:"value,omitempty"`
	// months
	Par int `json:"par,omitempty"`
	// group_amount
	Goal int    `json:"goal,omitempty"`
	Unit string `json:"unit,omitempty"`
}

// LinePlanView is read-time planner output for one line key.
type LinePlanView struct {
	LineKey        string
	ItemID         string
	GroupID        string
	ProductID      string
	Buy            int
	Short          int
	OnHand         int
	OnHandAmount   *float64
	Unit           string
	Target         PlanTarget
	RatePerMonth   *float64
	Replacing      *int
	TakenOffReason string
	// Legacy basis inputs
	Mode           string
	TargetQuantity *int
	InstanceCount  int
	ConsumedUnits  int
}

// PlanView runs Plan on the read pool and builds per-line-key planner numbers.
func (s *Service) PlanView(ctx context.Context, scope tenant.Scope, now time.Time, requested map[string]int) (map[string]LinePlanView, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("could not read plan: %w", err)
	}
	defer tx.Rollback()
	phase, months, err := readPhaseAndMonths(ctx, scope, tx)
	if err != nil {
		return nil, err
	}
	facts, err := loadFacts(ctx, scope, tx, phase)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	lines := plan(now, phase, months, facts)
	byProduct := map[ProductID]Fact{}
	for _, f := range facts {
		byProduct[f.Product] = f
	}
	out := map[string]LinePlanView{}
	for _, line := range lines {
		view := linePlanViewFromLine(line, byProduct[line.Product], phase, months, now)
		key := itemLineKey(view.ItemID)
		if line.GroupID != "" {
			key = groupLineKey(line.GroupID)
			view.GroupID = line.GroupID
		}
		if requested != nil {
			if r, ok := requested[view.ItemID]; ok {
				view.ConsumedUnits = r
			}
		}
		out[key] = view
	}
	// Lines planner dropped with buy 0 still appear for off-list / taken_off.
	for _, fact := range facts {
		key := itemLineKey(fact.ItemID)
		if fact.Group != "" {
			key = groupLineKey(string(fact.Group))
		}
		if _, ok := out[key]; ok {
			continue
		}
		view := linePlanViewFromFact(fact, phase, months, now)
		view.TakenOffReason = takenOffReason(fact, view.Buy, view.OnHand)
		out[key] = view
	}
	return out, nil
}

func linePlanViewFromLine(line Line, fact Fact, phase Phase, months Months, now time.Time) LinePlanView {
	view := linePlanViewFromFact(fact, phase, months, now)
	view.Buy = int(line.Buy)
	view.Short = int(line.Buy)
	if view.Buy == 0 {
		view.TakenOffReason = takenOffReason(fact, 0, view.OnHand)
	}
	return view
}

func linePlanViewFromFact(fact Fact, phase Phase, months Months, now time.Time) LinePlanView {
	view := LinePlanView{
		LineKey:       itemLineKey(fact.ItemID),
		ItemID:        fact.ItemID,
		ProductID:     string(fact.Product),
		OnHand:        int(fact.OnHand),
		InstanceCount: int(fact.OnHand),
		Short:         0,
	}
	if fact.Group != "" {
		view.GroupID = string(fact.Group)
		view.LineKey = groupLineKey(view.GroupID)
	}
	view.Target = planTargetFromFact(fact, months)
	view.Mode = replenishmentMode(fact)
	if fact.Resolved.Target.Value > 0 && fact.Resolved.Target.Kind == restock.KindAmount {
		v := int(fact.Resolved.Target.Value)
		view.TargetQuantity = &v
	}
	if fact.NetBase != nil {
		view.OnHandAmount = fact.NetBase
		if fact.NetDimension == "mass" {
			view.Unit = "g"
		} else if fact.NetDimension == "volume" {
			view.Unit = "ml"
		}
	}
	if !phase.isOpening() {
		if u := usageFromMembers(now, phase, []Fact{fact}); u != nil {
			view.RatePerMonth = &u.PerMonth
		}
	}
	rep := int(unreplacedQty(mustStarted(phase), fact))
	if rep > 0 {
		view.Replacing = &rep
	}
	return view
}

func mustStarted(phase Phase) time.Time {
	if t, ok := phase.StartedAt(); ok {
		return t
	}
	return time.Time{}
}

func planTargetFromFact(fact Fact, months Months) PlanTarget {
	if fact.NoRestock || (fact.Resolved.Target.Value == 0 && fact.Resolved.Level != "") {
		return PlanTarget{Kind: "none"}
	}
	switch fact.Resolved.Target.Kind {
	case restock.KindAmount:
		if fact.Resolved.Level == "group" && fact.GroupHasQty {
			unit := "g"
			if fact.GroupDimension == "volume" {
				unit = "ml"
			}
			return PlanTarget{Kind: "group_amount", Goal: int(fact.GroupBase), Unit: unit}
		}
		return PlanTarget{Kind: "amount", Value: int(fact.Resolved.Target.Value)}
	case restock.KindMonths:
		return PlanTarget{Kind: "months", Value: int(fact.Resolved.Target.Value), Par: int(months)}
	default:
		return PlanTarget{Kind: "none"}
	}
}

func replenishmentMode(fact Fact) string {
	if fact.NoRestock {
		return "off"
	}
	if fact.Resolved.Target.Kind == restock.KindAmount {
		return "amount"
	}
	if fact.Resolved.Target.Kind == restock.KindMonths {
		return "rate"
	}
	return "manual"
}

func takenOffReason(fact Fact, buy, onHand int) string {
	if buy > 0 {
		return ""
	}
	if fact.NoRestock || fact.Resolved.Target.Value == 0 {
		return "target_zero"
	}
	if onHand >= int(fact.Resolved.Target.Value) && fact.Resolved.Target.Kind == restock.KindAmount {
		return "on_hand_meets_par"
	}
	return "nothing_to_replace"
}

func itemLineKey(itemID string) string { return "item:" + itemID }
func groupLineKey(groupID string) string { return "group:" + groupID }