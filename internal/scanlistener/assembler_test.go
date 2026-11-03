package scanlistener

import (
	"testing"
)

func TestLineAssembler(t *testing.T) {
	tests := []struct {
		name   string
		events []KeyEvent
		want   []string // lines produced in order
	}{
		{
			name:   "single barcode",
			events: []KeyEvent{{Type: evKey, Code: 0x02, Value: valuePress}, {Type: evKey, Code: 0x03, Value: valuePress}, {Type: evKey, Code: 0x1c, Value: valuePress}}, // 1, 2, Enter
			want:   []string{"12"},
		},
		{
			name:   "multiple barcodes",
			events: []KeyEvent{{Type: evKey, Code: 0x02, Value: valuePress}, {Type: evKey, Code: 0x1c, Value: valuePress}, {Type: evKey, Code: 0x03, Value: valuePress}, {Type: evKey, Code: 0x1c, Value: valuePress}}, // 1, Enter, 2, Enter
			want:   []string{"1", "2"},
		},
		{
			name:   "empty line",
			events: []KeyEvent{{Type: evKey, Code: 0x1c, Value: valuePress}}, // Enter only
			want:   []string{""},
		},
		{
			name: "shift state",
			events: []KeyEvent{
				{Type: evKey, Code: 0x36, Value: valuePress}, // shift down
				{Type: evKey, Code: 0x1e, Value: valuePress}, // A
				{Type: evKey, Code: 0x36, Value: 0},          // shift up (release)
				{Type: evKey, Code: 0x1e, Value: valuePress}, // a
				{Type: evKey, Code: 0x1c, Value: valuePress}, // Enter
			},
			want: []string{"Aa"},
		},
		{
			name: "unmapped keycodes",
			events: []KeyEvent{
				{Type: evKey, Code: 0x02, Value: valuePress}, // 1
				{Type: evKey, Code: 0xff, Value: valuePress}, // unmapped
				{Type: evKey, Code: 0x03, Value: valuePress}, // 2
				{Type: evKey, Code: 0x1c, Value: valuePress}, // Enter
			},
			want: []string{"12"},
		},
		{
			name: "release events discarded",
			events: []KeyEvent{
				{Type: evKey, Code: 0x02, Value: valuePress}, // 1 press
				{Type: evKey, Code: 0x02, Value: 0},          // 1 release
				{Type: evKey, Code: 0x03, Value: valuePress}, // 2 press
				{Type: evKey, Code: 0x03, Value: 0},          // 2 release
				{Type: evKey, Code: 0x1c, Value: valuePress}, // Enter
			},
			want: []string{"12"},
		},
		{
			name: "non-EV_KEY events discarded",
			events: []KeyEvent{
				{Type: evKey, Code: 0x02, Value: valuePress}, // 1
				{Type: 0x00, Code: 0, Value: 0},              // SYN_REPORT
				{Type: evKey, Code: 0x03, Value: valuePress}, // 2
				{Type: evKey, Code: 0x1c, Value: valuePress}, // Enter
			},
			want: []string{"12"},
		},
		{
			name: "reset clears buffer",
			events: []KeyEvent{
				{Type: evKey, Code: 0x02, Value: valuePress}, // 1
				{Type: evKey, Code: 0x1c, Value: valuePress}, // Enter (to ensure there's something to reset)
			},
			want: []string{"1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &lineAssembler{}
			var lines []string
			for _, e := range tt.events {
				line, ok := a.feed(e)
				if ok {
					lines = append(lines, line)
				}
			}

			if len(lines) != len(tt.want) {
				t.Errorf("got %d lines, want %d", len(lines), len(tt.want))
			}
			for i, got := range lines {
				if i >= len(tt.want) {
					break
				}
				if got != tt.want[i] {
					t.Errorf("line %d = %q, want %q", i, got, tt.want[i])
				}
			}

			// Verify reset clears buffer and shift state
			if tt.name == "reset clears buffer" {
				a.reset()
				// Feed '1' then Enter - reset should have cleared previous state
				line, ok := a.feed(KeyEvent{Type: evKey, Code: 0x02, Value: valuePress}) // 1
				if ok && line != "" {
					t.Errorf("after reset, feed '1' = %q, true, want empty string, false", line)
				}
				line, ok = a.feed(KeyEvent{Type: evKey, Code: 0x1c, Value: valuePress}) // Enter
				if !ok || line != "1" {
					t.Errorf("after reset, feed Enter = %q, %v, want %q, true", line, ok, "1")
				}
				if a.shift {
					t.Errorf("after reset, shift = true, want false")
				}
			}
		})
	}
}

func TestLineAssembler_UnmappedCount(t *testing.T) {
	a := &lineAssembler{}
	a.feed(KeyEvent{Type: evKey, Code: 0x02, Value: valuePress})  // 1
	a.feed(KeyEvent{Type: evKey, Code: 0xff, Value: valuePress})  // unmapped
	a.feed(KeyEvent{Type: evKey, Code: 0x100, Value: valuePress}) // unmapped
	a.feed(KeyEvent{Type: evKey, Code: 0x1c, Value: valuePress})  // Enter

	if a.UnmappedCount() != 2 {
		t.Errorf("UnmappedCount = %d, want 2", a.UnmappedCount())
	}
}
