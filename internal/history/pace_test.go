package history

import (
	"testing"
	"time"
)

func TestPaceFromUses(t *testing.T) {
	now := time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	tests := []struct {
		name      string
		onHand    int
		uses      []time.Time
		known     bool
		days      float64
		left      int
		leftKnown bool
		trend     string
		sparkSum  int
	}{
		{
			name:     "one use is not a pace",
			uses:     []time.Time{now.Add(-10 * day)},
			known:    false,
			sparkSum: 1,
		},
		{
			name:     "two uses in the same week are not a pace",
			uses:     []time.Time{now.Add(-3 * day), now.Add(-1 * day)},
			known:    false,
			sparkSum: 2,
		},
		{
			name:      "uses spread over the month",
			onHand:    4,
			uses:      []time.Time{now.Add(-30 * day), now.Add(-20 * day), now.Add(-10 * day), now.Add(-1 * day)},
			known:     true,
			days:      7.5,
			left:      30,
			leftKnown: true,
			sparkSum:  3,
		},
		{
			name: "faster than the previous 30 days",
			uses: []time.Time{
				now.Add(-50 * day), now.Add(-40 * day),
				now.Add(-20 * day), now.Add(-14 * day), now.Add(-8 * day), now.Add(-2 * day),
			},
			known:     true,
			days:      5,
			leftKnown: true,
			trend:     "faster",
			sparkSum:  4,
		},
		{
			name: "slower than the previous 30 days",
			uses: []time.Time{
				now.Add(-55 * day), now.Add(-50 * day), now.Add(-45 * day), now.Add(-40 * day),
				now.Add(-35 * day), now.Add(-32 * day), now.Add(-31 * day), now.Add(-30*day - time.Hour),
				now.Add(-20 * day), now.Add(-1 * day),
			},
			known:     true,
			days:      10,
			leftKnown: true,
			trend:     "slower",
			sparkSum:  2,
		},
		{
			name:   "use order does not change the pace",
			onHand: 4,
			uses: []time.Time{
				now.Add(-1 * day), now.Add(-10 * day), now.Add(-30 * day), now.Add(-20 * day),
			},
			known: true, days: 7.5, left: 30, leftKnown: true, sparkSum: 3,
		},
		{
			name: "steady when the rate matches",
			uses: []time.Time{
				now.Add(-50 * day), now.Add(-40 * day), now.Add(-35 * day), now.Add(-31 * day),
				now.Add(-30 * day), now.Add(-20 * day), now.Add(-10 * day), now.Add(-1 * day),
			},
			known:     true,
			days:      7.5,
			leftKnown: true,
			trend:     "steady",
			sparkSum:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PaceFromUses(now, tt.onHand, tt.uses)
			if got.Known != tt.known {
				t.Fatalf("known = %v, want %v", got.Known, tt.known)
			}
			if got.DaysBetweenUses != tt.days {
				t.Fatalf("days = %v, want %v", got.DaysBetweenUses, tt.days)
			}
			if got.DaysLeftKnown != tt.leftKnown || got.DaysLeft != tt.left {
				t.Fatalf("days left = %d known %v, want %d known %v", got.DaysLeft, got.DaysLeftKnown, tt.left, tt.leftKnown)
			}
			if got.Trend != tt.trend {
				t.Fatalf("trend = %q, want %q", got.Trend, tt.trend)
			}
			sum := 0
			for _, n := range got.Sparkline {
				sum += n
			}
			if sum != tt.sparkSum {
				t.Fatalf("sparkline sum = %d, want %d (%v)", sum, tt.sparkSum, got.Sparkline)
			}
			if tt.sparkSum > 0 && len(got.Sparkline) != 30 {
				t.Fatalf("sparkline len = %d, want 30", len(got.Sparkline))
			}
		})
	}
}
