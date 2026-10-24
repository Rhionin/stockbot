package push

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/events"
	webpush "github.com/SherClockHolmes/webpush-go"
)

// pushTTL is how long a push service should keep an alert for a phone that
// is asleep. A scan the household already confirmed can still be opened.
const pushTTL = 24 * 60 * 60

// Firefox for Android receives Web Push through Mozilla's FCM bridge.
// autopush base64url-encodes the encrypted body and puts it, together with
// the channel id, content coding, and reliability id, into an FCM data map
// whose JSON must not exceed 4096 bytes. A 3070-byte body is already 4094
// bytes once encoded, so the map goes over and autopush answers 413.
//
// webpush-go v1.4.0 pads every request body to exactly RecordSize, so the
// record is sized to the payload: 86-byte header (16 salt, 4 record size,
// 1 key id length, 65 sender key), the plaintext, 1 delimiter byte, and a
// 16-byte tag. No padding is added.
const pushRecordOverhead = 86 + 1 + 16

// maxPushBody keeps the base64url body plus the bridge's other fields well
// under the FCM 4096-byte data limit.
const maxPushBody = 2800

func pushRecordSize(payload []byte) uint32 {
	return uint32(len(payload) + pushRecordOverhead)
}

var pushClient = &http.Client{Timeout: 15 * time.Second}

// Sender delivers a notice to every stored browser and drops subscriptions
// the push service says are gone.
type Sender struct {
	subs *Subscriptions
	cfg  Config
}

// NewSender sends with cfg. A nil Post uses Web Push.
func NewSender(subs *Subscriptions, cfg Config) *Sender {
	return &Sender{subs: subs, cfg: cfg}
}

// Listen follows the same broadcaster the scan queue uses, so a push goes
// out when no browser tab is open to see the event stream. scans is read
// when a batch settles, so a scan already confirmed is not announced again.
// A batch that was still waiting out its quiet period is armed again from
// the due time stored for it, so a restart does not drop that alert.
func Listen(b *events.Broadcaster, sender *Sender, scans scanReader) {
	if b == nil || sender == nil || !sender.cfg.CanSend() {
		return
	}
	msgs, unsub := b.Subscribe()
	go func() {
		defer unsub()
		alerts := newAttention(func(notice Notice) {
			go sender.Notify(context.Background(), notice)
		}, sender.cfg.settle(), realClock{}, scans)
		if sender.subs != nil {
			alerts.schedules = sender.subs
		}
		alerts.rearm(context.Background())
		defer alerts.stop()
		for msg := range msgs {
			alerts.handle(msg)
		}
	}()
}

// Notify pushes one notice to subscriptions for the current public host.
// A row with no origin was stored before origins existed and is included, so
// a live browser is not dropped on upgrade. The previous public host is
// removed. Each endpoint is contacted once. A 404 or 410 removes that row.
func (s *Sender) Notify(ctx context.Context, notice Notice) {
	if s == nil || s.subs == nil || !s.cfg.CanSend() {
		return
	}
	if legacy := s.cfg.legacyHost(); legacy != "" {
		n, err := s.subs.DeleteHost(ctx, legacy)
		if err != nil {
			log.Printf("scan alerts: could not remove a subscription from the previous address: %v", err)
		} else if n > 0 {
			log.Printf("scan alerts: removed subscriptions for %s", legacy)
		}
	}
	subs, err := s.subs.List(ctx)
	if err != nil {
		log.Printf("scan alerts: could not read push subscriptions: %v", err)
		return
	}
	s.deliverAll(ctx, notice, subs)
}

// deliverAll sends notice to the subscriptions selectForSend keeps.
func (s *Sender) deliverAll(ctx context.Context, notice Notice, subs []Subscription) {
	send, drop := selectForSend(subs, s.cfg)
	for _, sub := range drop {
		if err := s.subs.Delete(ctx, sub.Endpoint); err != nil {
			log.Printf("scan alerts: could not remove a subscription from the previous address: %v", err)
		}
	}
	if len(send) == 0 {
		return
	}
	payload, err := pushPayloadFor(notice, s.cfg.AppName)
	if err != nil {
		log.Printf("scan alerts: could not encode a notification")
		return
	}
	for _, sub := range send {
		status, _ := s.deliver(ctx, sub, payload)
		// Host only. The path is a capability and the keys are secrets.
		log.Printf("scan alerts: push to %s returned %d", endpointHost(sub.Endpoint), status)
		if status == http.StatusNotFound || status == http.StatusGone {
			if err := s.subs.Delete(ctx, sub.Endpoint); err != nil {
				log.Printf("scan alerts: could not remove an expired push subscription: %v", err)
			}
		}
	}
}

// selectForSend keeps one subscription per endpoint. The later row wins so a
// refreshed key is what gets sent. The previous public host is returned in
// drop. A blank origin stays: it predates the origin column.
func selectForSend(subs []Subscription, cfg Config) (send, drop []Subscription) {
	chosen := make([]Subscription, 0, len(subs))
	index := make(map[string]int, len(subs))
	for _, sub := range subs {
		endpoint := strings.TrimSpace(sub.Endpoint)
		if endpoint == "" {
			continue
		}
		sub.Endpoint = endpoint
		if at, ok := index[endpoint]; ok {
			chosen[at] = sub
			continue
		}
		index[endpoint] = len(chosen)
		chosen = append(chosen, sub)
	}
	for _, sub := range chosen {
		if cfg.RejectsOrigin(sub.Origin) {
			drop = append(drop, sub)
			continue
		}
		if cfg.DeliversTo(sub.Origin) {
			send = append(send, sub)
		}
	}
	return send, drop
}

func (s *Sender) deliver(ctx context.Context, sub Subscription, payload []byte) (int, error) {
	if s.cfg.Post != nil {
		return s.cfg.Post(ctx, sub, payload)
	}
	return postWebPush(s.cfg, sub, payload)
}

func postWebPush(cfg Config, sub Subscription, payload []byte) (int, error) {
	return deliverWebPush(pushClient, cfg, sub, payload)
}

func deliverWebPush(client webpush.HTTPClient, cfg Config, sub Subscription, payload []byte) (int, error) {
	resp, err := webpush.SendNotification(payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}, &webpush.Options{
		HTTPClient:      client,
		Subscriber:      cfg.subject(),
		VAPIDPublicKey:  cfg.PublicKey,
		VAPIDPrivateKey: cfg.PrivateKey,
		TTL:             pushTTL,
		Urgency:         webpush.UrgencyHigh,
		RecordSize:      pushRecordSize(payload),
	})
	if resp != nil {
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if err != nil {
			return resp.StatusCode, err
		}
		return resp.StatusCode, nil
	}
	if err != nil {
		return 0, err
	}
	return 0, nil
}

func endpointHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "a browser"
	}
	return parsed.Host
}
