package hub

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAgeMs(t *testing.T) {
	if got := ageMs(6500, 1000); got != 5500 {
		t.Fatalf("age = %d", got)
	}
	if got := ageMs(1000, 1000); got != 0 {
		t.Fatalf("zero age = %d", got)
	}
	if got := ageMs(500, 1000); got != 0 {
		t.Fatalf("negative age = %d", got)
	}
	if got := restartedAge(5000, 1000, 250); got != 4250 {
		t.Fatalf("restarted age = %d", got)
	}
	if got := restartedAge(100, 500, 10); got != 10 {
		t.Fatalf("restarted clamped = %d", got)
	}
}

func TestMono(t *testing.T) {
	start := time.Now()
	clock := NewMono(start)
	if got := clock.Millis(start.Add(1500 * time.Millisecond)); got != 1500 {
		t.Fatalf("millis = %d", got)
	}
	if got := clock.Millis(start.Add(-time.Second)); got != 0 {
		t.Fatalf("earlier = %d", got)
	}
	if got := (Mono{}).Millis(start); got != 0 {
		t.Fatalf("zero clock = %d", got)
	}
}

func TestParseChronyTracking(t *testing.T) {
	synced := `
Reference ID    : A9FEA97B (time.cloudflare.com)
Leap status     : Normal
`
	if !ParseChronyTracking(synced) {
		t.Fatal("expected a synced clock")
	}
	if ParseChronyTracking("Reference ID    : 00000000 ()\nLeap status     : Normal\n") {
		t.Fatal("zero reference id is not synced")
	}
	if ParseChronyTracking("Leap status     : Not synchronised\n") {
		t.Fatal("unsynchronised leap status")
	}
	if ParseChronyTracking("Leap status     : Not synchronized\n") {
		t.Fatal("unsynchronized leap status")
	}
	if ParseChronyTracking("no fields here") {
		t.Fatal("empty tracking output")
	}
	if ParseChronyTracking("Reference ID    : A9FEA97B (x)\nLeap status     : Insert second\n") {
		t.Fatal("a leap insert is not Normal")
	}
}

func TestChronySynced(t *testing.T) {
	ok, err := ChronySynced(context.Background(), func(context.Context, string, ...string) ([]byte, error) {
		return []byte("Reference ID    : ABCDEF01 (pool)\nLeap status     : Normal\n"), nil
	})
	if err != nil || !ok {
		t.Fatalf("synced = %v %v", ok, err)
	}
	ok, err = ChronySynced(context.Background(), func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("chronyc missing")
	})
	if err == nil || ok {
		t.Fatalf("missing chronyc = %v %v", ok, err)
	}
}

func TestBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		rng     float64
		after   time.Duration
		want    time.Duration
	}{
		{0, 1, 0, time.Second},
		{0, 0, 0, 500 * time.Millisecond},
		{1, 1, 0, 2 * time.Second},
		{2, 1, 0, 4 * time.Second},
		{10, 1, 0, BackoffCap},
		{-1, 1, 0, time.Second},
		{0, 2, 0, time.Second},
		{0, -1, 0, 500 * time.Millisecond},
		{0, 1, 45 * time.Second, 45 * time.Second},
		{0, 0, 100 * time.Millisecond, 500 * time.Millisecond},
	}
	for _, tc := range cases {
		got := Backoff(tc.attempt, func() float64 { return tc.rng }, tc.after)
		if got != tc.want {
			t.Fatalf("attempt %d rng %v after %s = %s, want %s", tc.attempt, tc.rng, tc.after, got, tc.want)
		}
	}
	if got := Backoff(3, nil, 0); got <= 0 || got > 8*time.Second {
		t.Fatalf("nil rng backoff = %s", got)
	}
}
