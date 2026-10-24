package push

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestOneEndpointSubscribesToTwoHouseholds(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	primary := testScope(t, subs)
	const otherID = "0190aaaa-bbbb-7000-8000-0000000000e1"
	if _, err := subs.db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Household North', '', '2026-03-03T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, otherID); err != nil {
		t.Fatal(err)
	}
	other, err := tenant.New(otherID)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "https://push.example/shared-browser"
	account := sampleSub(endpoint)
	account.UserID = "user-alpha"
	if err := subs.Save(ctx, primary, account); err != nil {
		t.Fatal(err)
	}
	shared := sampleSub(endpoint)
	if err := subs.Save(ctx, other, shared); err != nil {
		t.Fatal(err)
	}
	primaryRows, err := subs.List(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}
	otherRows, err := subs.List(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if len(primaryRows) != 1 || primaryRows[0].UserID != "user-alpha" {
		t.Fatalf("primary = %#v", primaryRows)
	}
	if len(otherRows) != 1 || otherRows[0].UserID != "" || otherRows[0].Endpoint != endpoint {
		t.Fatalf("other = %#v", otherRows)
	}

	var mu sync.Mutex
	var sent []string
	cfg := Config{Settle: time.Millisecond, Post: func(_ context.Context, sub Subscription, _ []byte) (int, error) {
		mu.Lock()
		sent = append(sent, sub.Endpoint+"@"+sub.UserID)
		mu.Unlock()
		return 201, nil
	}}
	sender := NewSender(subs, cfg)
	b := events.NewBroadcaster()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Listen(ctx, primary, b, sender, nil)
	b.PublishScanProcessingFailedEvent(scan.ProcessingFailure{
		ID: "fail-north", HouseholdID: otherID, Barcode: "1111111", Message: "try again",
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(sent)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || sent[0] != endpoint+"@" {
		t.Fatalf("alerts = %#v", sent)
	}
}

func TestMemberRemovalAndRetirementDeletePushRows(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	primary := testScope(t, subs)
	svc := auth.NewService(subs.db, t.TempDir())
	owner := "user-owner"
	member := "user-member"
	now := "2026-03-03T00:00:00Z"
	hash := "$2a$04$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012"
	if _, err := subs.db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at) VALUES
		(?, 'Owner Alpha', 'alpha@example.test', ?, 'local', 1, ?),
		(?, 'Member North', 'north@example.test', ?, 'local', 0, ?)`,
		owner, hash, now, member, hash, now); err != nil {
		t.Fatal(err)
	}
	if _, err := subs.db.Exec(`
		INSERT INTO memberships (household_id, user_id, role, created_at) VALUES
		(?, ?, 'owner', ?), (?, ?, 'member', ?)`,
		primary.HouseholdID(), owner, now, primary.HouseholdID(), member, now); err != nil {
		t.Fatal(err)
	}
	memberSub := sampleSub("https://push.example/member")
	memberSub.UserID = member
	if err := subs.Save(ctx, primary, memberSub); err != nil {
		t.Fatal(err)
	}
	lan := sampleSub("https://push.example/lan")
	if err := subs.Save(ctx, primary, lan); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveMember(ctx, owner, primary.HouseholdID(), member); err != nil {
		t.Fatal(err)
	}
	left, err := subs.List(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Endpoint != lan.Endpoint || left[0].UserID != "" {
		t.Fatalf("after removal %#v", left)
	}
	if _, err := subs.db.Exec(`
		INSERT INTO household_password (household_id, state, pw_hash)
		VALUES (?, 'on', ?)
		ON CONFLICT(household_id) DO UPDATE SET state = 'on', pw_hash = excluded.pw_hash`,
		primary.HouseholdID(), hash); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RetireHouseholdPassword(ctx, owner, primary.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	left, err = subs.List(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("after retirement %#v", left)
	}
}
