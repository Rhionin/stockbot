package push

import (
	"context"
	"strings"
	"testing"
)

func TestOperatorPushOmitsScanContents(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	scope := testScope(t, subs)
	if _, err := subs.db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES ('op-1', 'Nia North', 'nia-op@example.test', 'hash', 'local', 1, '2026-06-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	sub := sampleSub("https://push.example/operator")
	sub.UserID = "op-1"
	if err := subs.Save(ctx, scope, sub); err != nil {
		t.Fatal(err)
	}
	member := sampleSub("https://push.example/member")
	if err := subs.Save(ctx, scope, member); err != nil {
		t.Fatal(err)
	}
	var payloads []string
	sender := NewSender(subs, Config{Post: func(_ context.Context, _ Subscription, body []byte) (int, error) {
		payloads = append(payloads, string(body))
		return 201, nil
	}})
	sender.NotifyOperators(ctx, Notice{
		Title: "A hub dropped unsent scans",
		Body:  "North Cupboard — A hub dropped unsent scans.",
		Names: []string{"ZZ-FAKE-ITEM-9"},
		Count: 1,
		URL:   "/",
	})
	if len(payloads) != 1 {
		t.Fatalf("payloads %d", len(payloads))
	}
	if !strings.Contains(payloads[0], "North Cupboard") || strings.Contains(payloads[0], "ZZ-FAKE-ITEM-9") || strings.Contains(payloads[0], "scans are waiting") {
		t.Fatalf("payload %s", payloads[0])
	}
}
