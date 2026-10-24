package history

import (
	"database/sql"
	"math"
	"strconv"
	"strings"

	"github.com/Rhionin/stockbot/internal/product"
)

// View is the history screen for one product or one group.
type View struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	OnHand int    `json:"onHand"`
	Pace   Pace   `json:"pace"`
	Moves  []Move `json:"moves"`
}

// Move is one stock in or stock out on the trail.
type Move struct {
	ID          string `json:"id"`
	Direction   string `json:"direction"`
	Quantity    int    `json:"quantity"`
	At          string `json:"at"`
	Source      string `json:"source"`
	ProductID   string `json:"productId"`
	ProductName string `json:"productName"`
}

// InputError is a quantity the household cannot save.
type InputError struct{ Message string }

func (e *InputError) Error() string { return e.Message }

// ConflictError is a correction the shelf cannot absorb.
type ConflictError struct{ Message string }

func (e *ConflictError) Error() string { return e.Message }

// NotFoundError is a missing product, group, or move.
type NotFoundError struct{ Message string }

func (e *NotFoundError) Error() string { return e.Message }

func productDetail(unit string, base sql.NullFloat64, dimension string, pack sql.NullInt64) string {
	size := ""
	if base.Valid && base.Float64 > 0 && dimension != "" {
		amount, shown, ok := product.DisplayNetSize(base.Float64, dimension)
		if ok {
			size = formatAmount(amount) + " " + shown
		}
	}
	measure := strings.TrimSpace(unit)
	if strings.EqualFold(measure, "unit") {
		measure = ""
	}
	label := size
	if measure != "" {
		if label != "" {
			label += " "
		}
		label += measure
	}
	if pack.Valid && pack.Int64 > 1 && label != "" {
		return strconv.FormatInt(pack.Int64, 10) + " x " + label
	}
	return label
}

func formatAmount(v float64) string {
	if math.Abs(v-math.Round(v)) < 0.05 {
		return strconv.FormatInt(int64(math.Round(v)), 10)
	}
	return strconv.FormatFloat(roundTenth(v), 'f', 1, 64)
}

func sameDetail(details []string) string {
	if len(details) == 0 {
		return ""
	}
	first := details[0]
	for _, detail := range details[1:] {
		if detail != first {
			return strconv.Itoa(len(details)) + " products"
		}
	}
	if first == "" {
		if len(details) == 1 {
			return ""
		}
		return strconv.Itoa(len(details)) + " products"
	}
	return first
}
