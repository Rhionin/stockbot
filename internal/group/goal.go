package group

import "github.com/Rhionin/stockbot/internal/product"

// Goal statuses for a saved amount goal.
const (
	StatusOK            = "ok"
	StatusSizeMissing   = "size_missing"
	StatusUnitsMismatch = "units_mismatch"
)

// Blocker is one member that stops a total by size from being counted.
// SizeKind is "weight", "volume", or nil when the product has no size.
type Blocker struct {
	ProductID string  `json:"productId"`
	Name      string  `json:"name"`
	Reason    string  `json:"reason"`
	SizeKind  *string `json:"sizeKind"`
}

// sizeKindOf is the display word for a stored dimension: weight, volume, or empty.
func sizeKindOf(m Member) string {
	if m.NetBase == nil || *m.NetBase <= 0 {
		return ""
	}
	switch m.Dimension {
	case product.DimensionMass:
		return "weight"
	case product.DimensionVolume:
		return "volume"
	}
	return ""
}

func sized(m Member) bool { return sizeKindOf(m) != "" }

// counted is the set of members whose sizes a total has to add up:
// every member that is restocked, and a don't-restock member only while it
// has stock on the shelf.
func counted(members []Member) []Member {
	var out []Member
	for _, m := range members {
		if m.NoRestock && m.OnHand <= 0 {
			continue
		}
		out = append(out, m)
	}
	return out
}

// AmountAvailability reports whether a total by size can be picked now, its
// stored dimension, and which members stop it when it can't.
// Mixed sizes name the smaller set (volume on a tie). Members with no size
// come first.
func AmountAvailability(members []Member) (bool, string, []Blocker) {
	set := counted(members)
	if len(set) == 0 {
		return false, "", []Blocker{}
	}
	var unsized, mass, volume []Member
	for _, m := range set {
		switch {
		case !sized(m):
			unsized = append(unsized, m)
		case m.Dimension == product.DimensionMass:
			mass = append(mass, m)
		default:
			volume = append(volume, m)
		}
	}
	blockers := []Blocker{}
	for _, m := range unsized {
		blockers = append(blockers, Blocker{ProductID: m.ProductID, Name: m.Name, Reason: StatusSizeMissing})
	}
	if len(mass) > 0 && len(volume) > 0 {
		smaller := volume
		if len(mass) < len(volume) {
			smaller = mass
		}
		for _, m := range smaller {
			blockers = append(blockers, Blocker{ProductID: m.ProductID, Name: m.Name, Reason: StatusUnitsMismatch, SizeKind: strPtr(sizeKindOf(m))})
		}
	}
	if len(blockers) > 0 {
		return false, "", blockers
	}
	if len(mass) > 0 {
		return true, product.DimensionMass, blockers
	}
	return true, product.DimensionVolume, blockers
}

// AmountStatus checks a saved amount goal in dimension against the members.
// A member with no size wins over a mismatch, and both kinds are listed.
func AmountStatus(dimension string, members []Member) (string, []Blocker) {
	var missing, other []Blocker
	for _, m := range counted(members) {
		switch {
		case !sized(m):
			missing = append(missing, Blocker{ProductID: m.ProductID, Name: m.Name, Reason: StatusSizeMissing})
		case m.Dimension != dimension:
			other = append(other, Blocker{ProductID: m.ProductID, Name: m.Name, Reason: StatusUnitsMismatch, SizeKind: strPtr(sizeKindOf(m))})
		}
	}
	blockers := append(missing, other...)
	switch {
	case len(missing) > 0:
		return StatusSizeMissing, blockers
	case len(other) > 0:
		return StatusUnitsMismatch, blockers
	}
	return StatusOK, []Blocker{}
}

// Goal is the group's saved goal as the editor reads it. A nil goal means the
// household default applies.
type Goal struct {
	Kind     string    `json:"kind"`
	Value    float64   `json:"value"`
	Unit     *string   `json:"unit"`
	Status   string    `json:"status"`
	Blockers []Blocker `json:"blockers"`
}

// MemberSize is one unit's size, for display.
type MemberSize struct {
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}

func strPtr(s string) *string { return &s }

// describe fills the editor fields that depend on the goal and the members.
func (g *Group) describe() {
	for i := range g.Members {
		m := &g.Members[i]
		if kind := sizeKindOf(*m); kind != "" {
			m.SizeKind = strPtr(kind)
			if amount, unit, ok := product.DisplayNetSize(*m.NetBase, m.Dimension); ok {
				m.Size = &MemberSize{Amount: amount, Unit: unit}
			}
		}
	}
	available, dim, blockers := AmountAvailability(g.Members)
	g.AmountAvailable = available
	g.AmountBlockers = blockers
	if available {
		g.AmountUnit = strPtr(unitFor(dim))
	}
	switch {
	case g.goalItems > 0:
		g.Target = &Goal{Kind: "items", Value: float64(g.goalItems), Unit: strPtr("items"), Status: StatusOK, Blockers: []Blocker{}}
	case g.WindowMonths != nil && *g.WindowMonths > 0:
		g.Target = &Goal{Kind: "months", Value: float64(*g.WindowMonths), Unit: strPtr("months"), Status: StatusOK, Blockers: []Blocker{}}
	case g.WindowMonths != nil, g.hasQuantity && g.quantityBase <= 0:
		g.Target = &Goal{Kind: "dont_restock", Value: 0, Status: StatusOK, Blockers: []Blocker{}}
	case g.hasQuantity:
		status, goalBlockers := AmountStatus(g.Dimension, g.Members)
		value := 0.0
		if g.Quantity != nil {
			value = *g.Quantity
		}
		g.Target = &Goal{Kind: "amount", Value: value, Unit: strPtr(unitFor(g.Dimension)), Status: status, Blockers: goalBlockers}
	}
}
