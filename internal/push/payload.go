package push

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Rhionin/stockbot/internal/app"
)

// maxPushPlaintext is the largest JSON document Notify sends. With the
// record overhead the encrypted body stays under maxPushBody.
const maxPushPlaintext = 2500

// wireNotice is what the service worker shows. Names from the batch are
// folded into Body; the phone never receives the full list.
type wireNotice struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag,omitempty"`
	URL   string `json:"url"`
}

// pushPayload builds the compact document for one alert: a short title, a
// body with the count and the first item names that fit, and a url.
func pushPayload(notice Notice) ([]byte, error) {
	return pushPayloadFor(notice, "")
}

func pushPayloadFor(notice Notice, appName string) ([]byte, error) {
	appName = notificationTitle(appName)
	wire := wireNotice{
		Title: compactTitle(notice.Title, appName),
		Body:  compactBody(notice),
		Tag:   strings.TrimSpace(notice.Tag),
		URL:   alertURL(notice.URL),
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	if len(payload) <= maxPushPlaintext {
		return payload, nil
	}
	wire.Body = truncateToFit(wire)
	payload, err = json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	if len(payload) <= maxPushPlaintext {
		return payload, nil
	}
	// The title, tag, or url alone overflowed. Keep a short shell the
	// service worker can still open.
	wire.Tag = ""
	wire.Body = ""
	wire.Title = truncateRunes(wire.Title, 80)
	wire.URL = truncateRunes(wire.URL, 160)
	payload, err = json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	if len(payload) <= maxPushPlaintext {
		return payload, nil
	}
	return json.Marshal(wireNotice{Title: appName, URL: "/"})
}

func notificationTitle(appName string) string {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return app.DefaultAppName
	}
	return appName
}

func compactTitle(title, appName string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return appName
	}
	const maxTitleBytes = 120
	if len(title) <= maxTitleBytes {
		return title
	}
	return truncateRunes(title, maxTitleBytes)
}

func alertURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/"
	}
	return raw
}

func compactBody(notice Notice) string {
	names := listedNames(notice.Names)
	if len(names) == 0 {
		return notice.Body
	}
	count := notice.Count
	if count < len(names) {
		count = len(names)
	}
	lead := "1 scan is waiting"
	if count != 1 {
		lead = strconv.Itoa(count) + " scans are waiting"
	}
	return lead + ": " + strings.Join(names, ", ")
}

func listedNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	listed := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			listed = append(listed, name)
		}
	}
	return listed
}

// truncateToFit shortens the body until the JSON fits. The cut is on a rune
// boundary and ends with ….
func truncateToFit(wire wireNotice) string {
	body := wire.Body
	lo, hi := 0, len(body)
	best := ""
	for lo <= hi {
		mid := (lo + hi) / 2
		candidate := body
		if mid < len(body) {
			candidate = truncateRunes(body, mid)
		}
		trial := wire
		trial.Body = candidate
		payload, err := json.Marshal(trial)
		if err == nil && len(payload) <= maxPushPlaintext {
			best = candidate
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
}

// truncateRunes shortens s to at most maxBytes. When s must be cut, the cut
// falls on a rune boundary and the result ends with ….
func truncateRunes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes && utf8.ValidString(s) {
		return s
	}
	const ellipsis = "…"
	if maxBytes < len(ellipsis) {
		return validPrefix(s, maxBytes)
	}
	prefix := strings.TrimRight(validPrefix(s, maxBytes-len(ellipsis)), " ,")
	if prefix == "" {
		return ellipsis
	}
	return prefix + ellipsis
}

func validPrefix(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			break
		}
		if i+size > maxBytes {
			break
		}
		i += size
	}
	return s[:i]
}
