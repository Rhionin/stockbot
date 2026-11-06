package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

func TestLimiterKrogerAppTestCap(t *testing.T) {
	lim := NewLimiter()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	key := KrogerAppTestKey("household-1")
	for i := 0; i < krogerAppTestMax; i++ {
		if _, blocked := lim.Hit(now.Add(time.Duration(i)*time.Second), key, krogerAppTestMax, krogerAppTestWindow); blocked {
			t.Fatalf("hit %d blocked early", i)
		}
	}
	if _, blocked := lim.Blocked(now.Add(10*time.Second), key, krogerAppTestMax, krogerAppTestWindow); !blocked {
		t.Fatal("expected cap block")
	}
}

func TestServiceKrogerAppTestRateLimit(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, t.TempDir())
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	household := "0190aaaa-bbbb-7000-8000-0000000000aa"
	for i := 0; i < krogerAppTestMax; i++ {
		if err := svc.NoteKrogerAppTest(household); err != nil {
			t.Fatalf("note %d: %v", i, err)
		}
	}
	err = svc.BlockKrogerAppTest(household)
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeRateLimited {
		t.Fatalf("block: %v", err)
	}
	svc.SetClock(func() time.Time { return now.Add(krogerAppTestWindow + time.Second) })
	if err := svc.NoteKrogerAppTest(household); err != nil {
		t.Fatalf("after window: %v", err)
	}
}
