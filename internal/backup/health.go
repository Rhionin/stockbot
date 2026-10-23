package backup

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HealthPinger reports a one-line result. A failure ping goes to the URL's
// /fail path. The URL itself is not logged.
type HealthPinger interface {
	Ping(ctx context.Context, rawURL, body string, failed bool) error
}

// HTTPPinger posts to a healthchecks.io-style URL.
type HTTPPinger struct {
	Client *http.Client
}

func (p HTTPPinger) Ping(ctx context.Context, rawURL, body string, failed bool) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errClass(ExitPreflight, "preflight", "the healthchecks address is not a URL")
	}
	if failed {
		u.Path = strings.TrimRight(u.Path, "/") + "/fail"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(body))
	if err != nil {
		return errClass(ExitPreflight, "preflight", "the healthchecks address is not a URL")
	}
	req.Header.Set("Content-Type", "text/plain")
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return errClass(ExitPreflight, "preflight", "the healthchecks ping failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return errClass(ExitPreflight, "preflight", "the healthchecks ping was not accepted")
	}
	return nil
}
