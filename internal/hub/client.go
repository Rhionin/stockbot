package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// pairPath is the device half of pairing. api-contract.md names
	// POST /api/devices/pair/complete; the stage-1 server implements that
	// route and returns the bearer token once.
	pairPath      = "/api/devices/pair/complete"
	scansPath     = "/api/device/scans"
	heartbeatPath = "/api/device/heartbeat"
	rotatePath    = "/api/device/token/rotate"

	maxArtifact = 32 << 20
)

// HubConfig is the control-barcode set learned at pairing and on heartbeat.
type HubConfig struct {
	ControlBarcodes struct {
		StockIn  string `json:"stockIn"`
		StockOut string `json:"stockOut"`
	} `json:"controlBarcodes"`
	IdleSeconds   int `json:"idleSeconds"`
	ConfigVersion int `json:"configVersion"`
}

// Release is a signed hub update offered on a heartbeat.
type Release struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Sig     string `json:"sig"`
}

type pairResult struct {
	DeviceID         string    `json:"deviceId"`
	Token            string    `json:"token"`
	Config           HubConfig `json:"config"`
	HeartbeatSeconds int       `json:"heartbeatSeconds"`
}

type scanPayload struct {
	ID        string `json:"id"`
	Barcode   string `json:"barcode,omitempty"`
	ScannedAt string `json:"scannedAt,omitempty"`
	Direction string `json:"direction,omitempty"`
	UnitCount int    `json:"unitCount,omitempty"`
	AgeMs     int64  `json:"ageMs"`
	BootID    string `json:"bootId,omitempty"`
}

type batchRequest struct {
	Scans        []scanPayload `json:"scans"`
	DroppedCount int           `json:"droppedCount,omitempty"`
}

// batchResult is one item from a flush. Accepted is the single-scan spelling
// of created; both leave the queue.
type batchResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
}

type batchResponse struct {
	Results []batchResult `json:"results"`
	Config  HubConfig     `json:"config"`
}

type beatRequest struct {
	Version      string `json:"version"`
	BootID       string `json:"bootId"`
	UptimeS      int    `json:"uptimeS"`
	Queued       int    `json:"queued"`
	DroppedCount int    `json:"droppedCount,omitempty"`
	ClockSynced  bool   `json:"clockSynced"`
	Rollback     bool   `json:"rollback,omitempty"`
}

type beatResponse struct {
	ServerTime       string    `json:"serverTime"`
	Config           HubConfig `json:"config"`
	HeartbeatSeconds int       `json:"heartbeatSeconds"`
	RotateToken      bool      `json:"rotateToken"`
	Update           *Release  `json:"update,omitempty"`
}

// StatusError is a completed HTTP response the hub branches on by status.
type StatusError struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	if e.Code != "" {
		return "the server replied " + strconv.Itoa(e.Status) + " (" + e.Code + ")"
	}
	return "the server replied " + strconv.Itoa(e.Status)
}

// Client speaks the device API. Token values are sent as headers and are
// never included in errors.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) url(path string) string {
	return strings.TrimRight(c.BaseURL, "/") + path
}

// Pair exchanges a pairing code for a device token. name is stored as the
// device label. kind is pi_hub for this binary.
func (c *Client) Pair(ctx context.Context, code, name, kind, version, bootID string) (pairResult, error) {
	if kind == "" {
		kind = "pi_hub"
	}
	body := map[string]any{
		"code":    code,
		"name":    name,
		"label":   name,
		"kind":    kind,
		"version": version,
		"hub": map[string]string{
			"kind":    kind,
			"version": version,
			"bootId":  bootID,
		},
	}
	var out pairResult
	err := c.do(ctx, http.MethodPost, pairPath, "", body, &out)
	return out, err
}

// Scans posts one batch.
func (c *Client) Scans(ctx context.Context, token string, batch batchRequest) (batchResponse, error) {
	var out batchResponse
	err := c.do(ctx, http.MethodPost, scansPath, token, batch, &out)
	return out, err
}

// Heartbeat posts liveness.
func (c *Client) Heartbeat(ctx context.Context, token string, beat beatRequest) (beatResponse, error) {
	var out beatResponse
	err := c.do(ctx, http.MethodPost, heartbeatPath, token, beat, &out)
	return out, err
}

// Rotate asks for a new device token.
func (c *Client) Rotate(ctx context.Context, token string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do(ctx, http.MethodPost, rotatePath, token, map[string]any{}, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("the server did not return a new device token")
	}
	return out.Token, nil
}

// Download fetches an update artifact, bounded so a bad URL cannot fill the disk.
func (c *Client) Download(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{Status: resp.StatusCode, RetryAfter: retryAfterHeader(resp.Header.Get("Retry-After"))}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArtifact+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxArtifact {
		return nil, errors.New("that update is too large")
	}
	return body, nil
}

func (c *Client) do(ctx context.Context, method, path, token string, in, out any) error {
	var buf bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&buf).Encode(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &StatusError{
			Status:     resp.StatusCode,
			Code:       errorCode(body),
			RetryAfter: retryAfterHeader(resp.Header.Get("Retry-After")),
		}
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("the server sent a response that could not be read")
	}
	return nil
}

func errorCode(body []byte) string {
	var parsed struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return parsed.Code
}

func retryAfterHeader(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		d := time.Until(when)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

func retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var status *StatusError
	if errors.As(err, &status) {
		return status.Status == http.StatusTooManyRequests || status.Status >= 500
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

func retryAfterOf(err error) time.Duration {
	var status *StatusError
	if errors.As(err, &status) {
		return status.RetryAfter
	}
	return 0
}

func revoked(err error) bool {
	var status *StatusError
	return errors.As(err, &status) && status.Status == http.StatusUnauthorized
}
