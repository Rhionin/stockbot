// Package restock resolves a household's restock target for one item.
// A target of 0 means don't restock. The most specific place that sets a
// target wins: item, then group, then the higher of the item's categories,
// then the household default.
package restock

// Kind is months of supply or a fixed amount.
type Kind string

const (
	KindMonths Kind = "months"
	KindAmount Kind = "amount"
)

// Level is which place set the target the household will use.
type Level string

const (
	LevelItem      Level = "item"
	LevelGroup     Level = "group"
	LevelCategory  Level = "category"
	LevelHousehold Level = "household"
)

// HouseholdID is the single household until logins name a household row.
// New tables store it so a second household does not have to re-key them.
// It is the same id items.user_id and product_groups.user_id already use.
const HouseholdID = "user-1"

// Target is a months window or a fixed amount. Value 0 means don't restock.
type Target struct {
	Kind  Kind    `json:"kind"`
	Value float64 `json:"value"`
}

// CategoryTarget is one category that sets a target for an item.
type CategoryTarget struct {
	ID     string
	Name   string
	Target Target
}

// Input is everything Resolve needs. A nil item or group means that place
// did not set a target. Categories lists only categories that set one.
type Input struct {
	Item       *Target
	Group      *Target
	GroupName  string
	Categories []CategoryTarget
	Household  Target
}

// Shadow is a category target that lost to a higher one.
type Shadow struct {
	Name   string
	Target Target
}

// Resolution is the target an item will be shopped at, and the next target
// you would get by clearing the place that set it.
type Resolution struct {
	Target   Target
	Level    Level
	Name     string
	Shadowed []Shadow
	Next     *Resolution
}

// Higher reports the target that restocks more.
// Zero loses to any positive target. The same kind compares its number.
// A positive month window beats a positive fixed amount.
// This is the stand-in for an unanswered question and the only place that
// decides it.
func Higher(a, b Target) Target {
	if a.Value == 0 {
		return b
	}
	if b.Value == 0 {
		return a
	}
	if a.Kind == b.Kind {
		if b.Value > a.Value {
			return b
		}
		return a
	}
	if a.Kind == KindMonths {
		return a
	}
	return b
}

// Resolve applies item, then group, then the higher category, then the household.
func Resolve(in Input) Resolution {
	if in.Item != nil {
		next := resolveBelowItem(in)
		return Resolution{Target: *in.Item, Level: LevelItem, Next: &next}
	}
	return resolveBelowItem(in)
}

func resolveBelowItem(in Input) Resolution {
	if in.Group != nil {
		next := resolveCategories(in)
		return Resolution{Target: *in.Group, Level: LevelGroup, Name: in.GroupName, Next: &next}
	}
	return resolveCategories(in)
}

func resolveCategories(in Input) Resolution {
	if len(in.Categories) == 0 {
		return Resolution{Target: in.Household, Level: LevelHousehold}
	}
	best := in.Categories[0]
	var lost []Shadow
	for _, category := range in.Categories[1:] {
		winner := Higher(best.Target, category.Target)
		if winner != best.Target {
			lost = append(lost, Shadow{Name: best.Name, Target: best.Target})
			best = category
			continue
		}
		lost = append(lost, Shadow{Name: category.Name, Target: category.Target})
	}
	next := Resolution{Target: in.Household, Level: LevelHousehold}
	return Resolution{
		Target:   best.Target,
		Level:    LevelCategory,
		Name:     best.Name,
		Shadowed: lost,
		Next:     &next,
	}
}

// Same reports whether two targets would shop the same way.
func Same(a, b Target) bool {
	return a.Kind == b.Kind && a.Value == b.Value
}
