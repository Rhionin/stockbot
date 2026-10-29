package supply

import (
	"math"
	"testing"
	"time"
)

func d(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func TestDeriveAmount(t *testing.T) {
	started := d(2026, 1, 1)
	firstIn := d(2026, 1, 2)
	farNow := d(2026, 6, 1)

	twoGapOuts := func() []AmountWithdrawal {
		return []AmountWithdrawal{
			{At: d(2026, 1, 12), Qty: 10},
			{At: d(2026, 1, 23), Qty: 11},
		}
	}

	tests := []struct {
		name       string
		now        time.Time
		started    time.Time
		ins        []time.Time
		outs       []AmountWithdrawal
		wantOK     bool
		wantPerDay float64
	}{
		{
			name:    "zero started",
			now:     farNow,
			started: time.Time{},
			ins:     []time.Time{firstIn},
			outs:    twoGapOuts(),
			wantOK:  false,
		},
		{
			name:    "before 30 days after started",
			now:     started.Add(29 * 24 * time.Hour),
			started: started,
			ins:     []time.Time{firstIn},
			outs:    twoGapOuts(),
			wantOK:  false,
		},
		{
			name:    "exactly 30 days after started still gated on other rules with no outs",
			now:     started.Add(30 * 24 * time.Hour),
			started: started,
			ins:     []time.Time{firstIn},
			wantOK:  false,
		},
		{
			name:       "exactly 30 days with valid history passes opening gate",
			now:        started.Add(30 * 24 * time.Hour),
			started:    started,
			ins:        []time.Time{firstIn},
			outs:       twoGapOuts(),
			wantOK:     true,
			wantPerDay: 1,
		},
		{
			name:    "no stock-ins",
			now:     farNow,
			started: started,
			ins:     nil,
			outs:    twoGapOuts(),
			wantOK:  false,
		},
		{
			name:    "withdrawal at first stock-in ignored",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: firstIn, Qty: 99},
				{At: d(2026, 1, 12), Qty: 10},
			},
			wantOK: false,
		},
		{
			name:    "withdrawal before first stock-in ignored",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: firstIn.Add(-time.Hour), Qty: 99},
				{At: d(2026, 1, 12), Qty: 10},
			},
			wantOK: false,
		},
		{
			name:    "non-positive withdrawal qty ignored",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: d(2026, 1, 12), Qty: 0},
				{At: d(2026, 1, 12), Qty: -3},
				{At: d(2026, 1, 23), Qty: 11},
			},
			wantOK: false,
		},
		{
			name:    "non-positive withdrawal qty ignored with enough valid gaps",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: d(2026, 1, 12), Qty: 0},
				{At: d(2026, 1, 12), Qty: 10},
				{At: d(2026, 1, 22), Qty: 0},
				{At: d(2026, 1, 23), Qty: -3},
				{At: d(2026, 1, 23), Qty: 11},
			},
			wantOK:     true,
			wantPerDay: 1,
		},
		{
			name:    "same-instant withdrawals merged into one gap",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: d(2026, 1, 12), Qty: 4},
				{At: d(2026, 1, 12), Qty: 6},
				{At: d(2026, 1, 23), Qty: 11},
			},
			wantOK:     true,
			wantPerDay: 1,
		},
		{
			name:    "fewer than two gaps",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: d(2026, 1, 12), Qty: 10},
			},
			wantOK: false,
		},
		{
			name:    "span one day short of 21 days",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: d(2026, 1, 12), Qty: 10},
				{At: d(2026, 1, 22), Qty: 10},
			},
			wantOK: false,
		},
		{
			name:       "span exactly 21 days",
			now:        farNow,
			started:    started,
			ins:        []time.Time{firstIn},
			outs:       twoGapOuts(),
			wantOK:     true,
			wantPerDay: 1,
		},
		{
			name:    "median odd gap count",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: d(2026, 1, 6), Qty: 5},
				{At: d(2026, 1, 11), Qty: 10},
				{At: d(2026, 1, 23), Qty: 24},
			},
			wantOK:     true,
			wantPerDay: 2,
		},
		{
			name:    "median even gap count lower middle",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: d(2026, 1, 6), Qty: 5},
				{At: d(2026, 1, 11), Qty: 10},
				{At: d(2026, 1, 16), Qty: 15},
				{At: d(2026, 1, 23), Qty: 32},
			},
			wantOK:     true,
			wantPerDay: 2,
		},
		{
			name:    "only last six gaps count",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: d(2026, 1, 3), Qty: 100},
				{At: d(2026, 1, 7), Qty: 8},
				{At: d(2026, 1, 11), Qty: 12},
				{At: d(2026, 1, 15), Qty: 16},
				{At: d(2026, 1, 19), Qty: 20},
				{At: d(2026, 1, 23), Qty: 24},
				{At: d(2026, 1, 27), Qty: 28},
			},
			wantOK:     true,
			wantPerDay: 4,
		},
		{
			name:    "burst fails with three gaps at exactly 4x median",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: d(2026, 1, 12), Qty: 10},
				{At: d(2026, 1, 22), Qty: 10},
				{At: d(2026, 1, 27), Qty: 20},
			},
			wantOK: false,
		},
		{
			name:    "burst passes with three gaps just under 4x median",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: d(2026, 1, 12), Qty: 10},
				{At: d(2026, 1, 22), Qty: 10},
				{At: d(2026, 1, 27), Qty: 19.999},
			},
			wantOK:     true,
			wantPerDay: 1,
		},
		{
			name:    "burst tolerated with four gaps at exactly 4x median",
			now:     farNow,
			started: started,
			ins:     []time.Time{firstIn},
			outs: []AmountWithdrawal{
				{At: d(2026, 1, 12), Qty: 10},
				{At: d(2026, 1, 22), Qty: 10},
				{At: d(2026, 1, 27), Qty: 20},
				{At: d(2026, 2, 6), Qty: 10},
			},
			wantOK:     true,
			wantPerDay: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPerDay, gotOK := DeriveAmount(tt.now, tt.started, tt.ins, tt.outs)
			if gotOK != tt.wantOK {
				t.Fatalf("ok = %v, want %v (perDay = %v)", gotOK, tt.wantOK, gotPerDay)
			}
			if tt.wantOK && math.Abs(gotPerDay-tt.wantPerDay) > 1e-9 {
				t.Fatalf("perDay = %v, want %v", gotPerDay, tt.wantPerDay)
			}
		})
	}
}
