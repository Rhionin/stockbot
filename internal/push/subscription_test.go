package push

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	_ "modernc.org/sqlite"
)

func newTestSubscriptions(t *testing.T) *Subscriptions {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	// A :memory: database lives on one connection. Save uses a transaction.
	conn.SetMaxOpenConns(1)
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewSubscriptions(conn)
}

func sampleSub(endpoint string) Subscription {
	return Subscription{
		Endpoint: endpoint,
		P256dh:   "p256dh-key",
		Auth:     "auth-key",
	}
}

func TestSubscriptionSaveReplacesKeys(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	first := sampleSub("https://push.example/device")
	if problem := first.Problem(); problem != "" {
		t.Fatal(problem)
	}
	if err := subs.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	updated := first
	updated.Auth = "auth-rotated"
	if err := subs.Save(ctx, updated); err != nil {
		t.Fatal(err)
	}
	got, err := subs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Auth != "auth-rotated" || got[0].P256dh != first.P256dh {
		t.Fatalf("stored = %#v", got)
	}
	if err := subs.Delete(ctx, first.Endpoint); err != nil {
		t.Fatal(err)
	}
	if err := subs.Delete(ctx, first.Endpoint); err != nil {
		t.Fatal(err)
	}
	got, err = subs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("after delete = %#v", got)
	}
}

func TestResubscribeReplacesThatDeviceAndKeepsARowWithNoOrigin(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	older := sampleSub("https://push.example/old")
	older.Origin = "https://stockbot.rhionin.com"
	older.UserID = "user-1"
	older.DeviceID = "phone-a"
	otherDevice := sampleSub("https://push.example/other-phone")
	otherDevice.Origin = "stockbot.rhionin.com"
	otherDevice.UserID = "user-1"
	otherDevice.DeviceID = "phone-b"
	otherUser := sampleSub("https://push.example/other-user")
	otherUser.Origin = "stockbot.rhionin.com"
	otherUser.UserID = "user-2"
	otherUser.DeviceID = "phone-a"
	// A row from before origins existed. Re-subscribe must not delete it.
	live := sampleSub("https://push.example/live")
	for _, sub := range []Subscription{older, otherDevice, otherUser, live} {
		if err := subs.Save(ctx, sub); err != nil {
			t.Fatal(err)
		}
	}
	next := sampleSub("https://push.example/new")
	next.Origin = "stockbot.rhionin.com:443"
	next.UserID = "user-1"
	next.DeviceID = "phone-a"
	if err := subs.Save(ctx, next); err != nil {
		t.Fatal(err)
	}
	got, err := subs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	endpoints := map[string]Subscription{}
	for _, sub := range got {
		endpoints[sub.Endpoint] = sub
	}
	if _, ok := endpoints[older.Endpoint]; ok {
		t.Fatalf("older endpoint kept: %#v", got)
	}
	if endpoints[next.Endpoint].Origin != "stockbot.rhionin.com" || endpoints[next.Endpoint].DeviceID != "phone-a" {
		t.Fatalf("new row = %#v", endpoints[next.Endpoint])
	}
	for _, keep := range []string{otherDevice.Endpoint, otherUser.Endpoint, live.Endpoint} {
		if _, ok := endpoints[keep]; !ok {
			t.Fatalf("missing %s in %#v", keep, got)
		}
	}
	if endpoints[live.Endpoint].Origin != "" {
		t.Fatalf("blank origin was rewritten: %#v", endpoints[live.Endpoint])
	}
}

func TestResaveStampsOriginOnTheLiveRow(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	live := sampleSub("https://push.example/live")
	if _, err := subs.db.ExecContext(ctx, `
		INSERT INTO push_subscriptions (endpoint, p256dh, auth)
		VALUES (?, ?, ?)`, live.Endpoint, live.P256dh, live.Auth); err != nil {
		t.Fatal(err)
	}
	live.Origin = "https://stockbot.rhionin.com"
	live.UserID = "user-1"
	live.DeviceID = "phone-a"
	live.Auth = "auth-rotated"
	if err := subs.Save(ctx, live); err != nil {
		t.Fatal(err)
	}
	got, err := subs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Endpoint != live.Endpoint || got[0].Origin != "stockbot.rhionin.com" || got[0].Auth != "auth-rotated" || got[0].UserID != "user-1" || got[0].DeviceID != "phone-a" {
		t.Fatalf("live row = %#v", got)
	}
}

func TestKeyRefreshKeepsOriginUserAndDevice(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	first := sampleSub("https://push.example/device")
	first.Origin = "stockbot.rhionin.com"
	first.UserID = "user-1"
	first.DeviceID = "phone-a"
	if err := subs.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	refreshed := sampleSub(first.Endpoint)
	refreshed.Auth = "auth-rotated"
	if err := subs.Save(ctx, refreshed); err != nil {
		t.Fatal(err)
	}
	got, err := subs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Auth != "auth-rotated" || got[0].Origin != "stockbot.rhionin.com" || got[0].UserID != "user-1" || got[0].DeviceID != "phone-a" {
		t.Fatalf("stored = %#v", got)
	}
}

func TestSubscriptionRejectsABadDeviceID(t *testing.T) {
	sub := sampleSub("https://push.example/device")
	sub.DeviceID = "phone a"
	if sub.Problem() == "" {
		t.Fatal("accepted a device id with a space")
	}
	sub.DeviceID = strings.Repeat("d", 101)
	if sub.Problem() == "" {
		t.Fatal("accepted a long device id")
	}
}

func TestSubscriptionRejectsANonPushURL(t *testing.T) {
	for _, endpoint := range []string{"http://push.example/s", "https://user:pass@push.example/s", "", "not a url"} {
		if sampleSub(endpoint).Problem() == "" {
			t.Fatalf("accepted %q", endpoint)
		}
	}
}

func TestSenderDropsGoneSubscriptions(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	keep := sampleSub("https://push.example/keep")
	gone := sampleSub("https://push.example/gone")
	missing := sampleSub("https://push.example/missing")
	for _, sub := range []Subscription{keep, gone, missing} {
		if err := subs.Save(ctx, sub); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	sender := NewSender(subs, Config{
		Post: func(_ context.Context, sub Subscription, payload []byte) (int, error) {
			calls = append(calls, sub.Endpoint)
			if len(payload) == 0 {
				t.Fatal("empty payload")
			}
			switch sub.Endpoint {
			case gone.Endpoint:
				return http.StatusGone, nil
			case missing.Endpoint:
				return http.StatusNotFound, nil
			default:
				return http.StatusCreated, nil
			}
		},
	})
	sender.Notify(ctx, Notice{Title: "Scan failed", Body: "Try again.", Tag: "pantry-scan-failure-1", URL: "/"})
	if len(calls) != 3 {
		t.Fatalf("calls = %#v", calls)
	}
	left, err := subs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Endpoint != keep.Endpoint {
		t.Fatalf("remaining = %#v", left)
	}
	calls = nil
	sender.Notify(ctx, Notice{Title: "Unrecognized product", Body: "Match it.", Tag: "pantry-scan-unrecognized-1", URL: "/?scanAlert=stock_out"})
	if len(calls) != 1 || calls[0] != keep.Endpoint {
		t.Fatalf("second calls = %#v", calls)
	}
}

func TestSenderSendsOnlyTheCurrentHostAndDropsThePreviousOne(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	current := sampleSub("https://push.example/current")
	current.Origin = "stockbot.rhionin.com"
	legacy := sampleSub("https://push.example/legacy")
	legacy.Origin = "pantry.rhionin.com"
	live := sampleSub("https://push.example/live")
	lan := sampleSub("https://push.example/lan")
	lan.Origin = "192.168.1.203"
	// The live row is what an upgrade finds: no origin column value.
	if _, err := subs.db.ExecContext(ctx, `
		INSERT INTO push_subscriptions (endpoint, p256dh, auth)
		VALUES (?, ?, ?)`, live.Endpoint, live.P256dh, live.Auth); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []Subscription{current, legacy, lan} {
		if err := subs.Save(ctx, sub); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	sender := NewSender(subs, Config{
		PublicHost: "stockbot.rhionin.com",
		LegacyHost: "pantry.rhionin.com",
		Post: func(_ context.Context, sub Subscription, _ []byte) (int, error) {
			calls = append(calls, sub.Endpoint)
			return http.StatusCreated, nil
		},
	})
	sender.Notify(ctx, Notice{Title: "Scan failed", URL: "/"})
	if len(calls) != 2 || !containsAll(calls, current.Endpoint, live.Endpoint) {
		t.Fatalf("calls = %#v", calls)
	}
	left, err := subs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	endpoints := map[string]Subscription{}
	for _, sub := range left {
		endpoints[sub.Endpoint] = sub
	}
	if _, ok := endpoints[legacy.Endpoint]; ok {
		t.Fatalf("previous host kept: %#v", left)
	}
	if _, ok := endpoints[live.Endpoint]; !ok || endpoints[live.Endpoint].Origin != "" {
		t.Fatalf("live row = %#v", left)
	}
	if _, ok := endpoints[lan.Endpoint]; !ok {
		t.Fatalf("other host was deleted: %#v", left)
	}
	if _, ok := endpoints[current.Endpoint]; !ok {
		t.Fatalf("current host missing: %#v", left)
	}
}

func TestSenderWithoutAPublicHostStillDropsThePreviousHost(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	lan := sampleSub("https://push.example/lan")
	lan.Origin = "pantry.local"
	legacy := sampleSub("https://push.example/legacy")
	legacy.Origin = "pantry.rhionin.com"
	for _, sub := range []Subscription{lan, legacy} {
		if err := subs.Save(ctx, sub); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	sender := NewSender(subs, Config{
		LegacyHost: "https://pantry.rhionin.com",
		Post: func(_ context.Context, sub Subscription, _ []byte) (int, error) {
			calls = append(calls, sub.Endpoint)
			return http.StatusCreated, nil
		},
	})
	sender.Notify(ctx, Notice{Title: "Scan failed", URL: "/"})
	// A legacy value that includes a scheme is ignored, so both are sent.
	if len(calls) != 2 {
		t.Fatalf("calls = %#v", calls)
	}
	sender = NewSender(subs, Config{
		LegacyHost: "pantry.rhionin.com",
		Post: func(_ context.Context, sub Subscription, _ []byte) (int, error) {
			calls = append(calls, sub.Endpoint)
			return http.StatusCreated, nil
		},
	})
	calls = nil
	sender.Notify(ctx, Notice{Title: "Scan failed", URL: "/"})
	if len(calls) != 1 || calls[0] != lan.Endpoint {
		t.Fatalf("calls = %#v", calls)
	}
	left, err := subs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Endpoint != lan.Endpoint {
		t.Fatalf("remaining = %#v", left)
	}
}

func TestDeliverAllDedupesByEndpointAndDropsThePreviousHost(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	first := sampleSub("https://push.example/same")
	first.Auth = "auth-old"
	first.Origin = "pantry.rhionin.com"
	second := sampleSub("https://push.example/same")
	second.Auth = "auth-new"
	second.Origin = "stockbot.rhionin.com"
	other := sampleSub("https://push.example/other")
	other.Origin = "stockbot.rhionin.com"
	legacy := sampleSub("https://push.example/legacy")
	legacy.Origin = "pantry.rhionin.com"
	var calls []Subscription
	sender := NewSender(subs, Config{
		PublicHost: "stockbot.rhionin.com",
		LegacyHost: "pantry.rhionin.com",
		Post: func(_ context.Context, sub Subscription, _ []byte) (int, error) {
			calls = append(calls, sub)
			return http.StatusCreated, nil
		},
	})
	sender.deliverAll(ctx, Notice{Title: "Scan failed", URL: "/"}, []Subscription{first, second, other, legacy})
	if len(calls) != 2 {
		t.Fatalf("calls = %#v", calls)
	}
	if calls[0].Endpoint != second.Endpoint || calls[0].Auth != "auth-new" || calls[1].Endpoint != other.Endpoint {
		t.Fatalf("calls = %#v", calls)
	}
	left, err := subs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("dropped rows were not stored, list = %#v", left)
	}
	// The legacy endpoint in the slice is deleted even when it was never saved.
	// Saving it first proves the delete.
	if err := subs.Save(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	sender.deliverAll(ctx, Notice{Title: "Scan failed", URL: "/"}, []Subscription{legacy})
	left, err = subs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("legacy row kept: %#v", left)
	}
}

func containsAll(got []string, want ...string) bool {
	seen := map[string]bool{}
	for _, value := range got {
		seen[value] = true
	}
	for _, value := range want {
		if !seen[value] {
			return false
		}
	}
	return true
}

func TestSenderKeepsASubscriptionWhenThePushServiceFails(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	sub := sampleSub("https://push.example/busy")
	if err := subs.Save(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sender := NewSender(subs, Config{
		Post: func(context.Context, Subscription, []byte) (int, error) {
			return http.StatusInternalServerError, nil
		},
	})
	sender.Notify(ctx, Notice{Title: "Scan failed", URL: "/"})
	left, err := subs.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Fatalf("a 500 removed the browser: %#v", left)
	}
}

func TestNotifyLogsHostAndStatus(t *testing.T) {
	subs := newTestSubscriptions(t)
	ctx := context.Background()
	ok := sampleSub("https://updates.push.services.mozilla.com/wpush/v2/DO-NOT-LOG-TOKEN")
	ok.P256dh = "p256dh-DO-NOT-LOG"
	ok.Auth = "auth-DO-NOT-LOG"
	gone := sampleSub("https://fcm.googleapis.com/fcm/send/DO-NOT-LOG-TOKEN")
	gone.P256dh = "p256dh-DO-NOT-LOG"
	gone.Auth = "auth-DO-NOT-LOG"
	for _, sub := range []Subscription{ok, gone} {
		if err := subs.Save(ctx, sub); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	sender := NewSender(subs, Config{
		Post: func(_ context.Context, sub Subscription, _ []byte) (int, error) {
			if sub.Endpoint == gone.Endpoint {
				return http.StatusGone, nil
			}
			return http.StatusCreated, nil
		},
	})
	sender.Notify(ctx, Notice{Title: "Scan failed", Body: "Try again.", URL: "/"})

	logged := buf.String()
	for _, want := range []string{
		"scan alerts: push to updates.push.services.mozilla.com returned 201",
		"scan alerts: push to fcm.googleapis.com returned 410",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("log missing %q\n%s", want, logged)
		}
	}
	for _, secret := range []string{"DO-NOT-LOG-TOKEN", "p256dh-DO-NOT-LOG", "auth-DO-NOT-LOG", "https://"} {
		if strings.Contains(logged, secret) {
			t.Errorf("log included %q\n%s", secret, logged)
		}
	}
}
