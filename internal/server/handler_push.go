package server

import (
	"errors"
	"strings"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/push"
)

// errPushUnavailable is the log detail when these routes have no database.
// Callers see the generic message for a server error.
var errPushUnavailable = errors.New("push subscriptions are not available")

// PushHandler stores browsers that want scan alerts while Pantry is closed.
// The public key is not a secret. The private key never leaves this process.
type PushHandler struct {
	Subscriptions *push.Subscriptions
	Keys          push.Config
}

type vapidKeyResponse struct {
	PublicKey string `json:"publicKey"`
}

type pushSubscriptionBody struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	UserID   string `json:"userId"`
	DeviceID string `json:"deviceId"`
}

type pushEndpointResponse struct {
	Endpoint string `json:"endpoint"`
}

func (h *PushHandler) PublicKey(Request[struct{}, struct{}]) (vapidKeyResponse, error) {
	if h == nil || !h.Keys.Configured() {
		return vapidKeyResponse{}, nil
	}
	return vapidKeyResponse{PublicKey: h.Keys.PublicKey}, nil
}

func (h *PushHandler) Save(req Request[pushSubscriptionBody, struct{}]) (pushEndpointResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return pushEndpointResponse{}, err
	}
	if h == nil || h.Subscriptions == nil {
		return pushEndpointResponse{}, InternalError(errPushUnavailable)
	}
	if !h.Keys.Configured() {
		return pushEndpointResponse{}, BadRequest("Background scan alerts are not set up on this pantry yet.")
	}
	userID := strings.TrimSpace(req.Body.UserID)
	if userID == "" {
		if p, ok := auth.PrincipalFrom(req.Context); ok && p.UserID != "" {
			userID = p.UserID
		}
	}
	sub := push.Subscription{
		Endpoint: strings.TrimSpace(req.Body.Endpoint),
		P256dh:   strings.TrimSpace(req.Body.Keys.P256dh),
		Auth:     strings.TrimSpace(req.Body.Keys.Auth),
		Origin:   push.OriginHost(req.RawRequest),
		UserID:   userID,
		DeviceID: strings.TrimSpace(req.Body.DeviceID),
	}
	if problem := sub.Problem(); problem != "" {
		return pushEndpointResponse{}, BadRequest(problem)
	}
	if h.Keys.RejectsOrigin(sub.Origin) {
		if _, err := h.Subscriptions.DeleteHost(req.Context, scope, sub.Origin); err != nil {
			return pushEndpointResponse{}, InternalError(err)
		}
		return pushEndpointResponse{}, BadRequest("This address no longer sends scan alerts. Open the current site and allow notifications again.")
	}
	if err := h.Subscriptions.Save(req.Context, scope, sub); err != nil {
		return pushEndpointResponse{}, InternalError(err)
	}
	return pushEndpointResponse{Endpoint: sub.Endpoint}, nil
}

func (h *PushHandler) Delete(req Request[pushSubscriptionBody, struct{}]) (NoContent, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return NoContent{}, err
	}
	if h == nil || h.Subscriptions == nil {
		return NoContent{}, InternalError(errPushUnavailable)
	}
	endpoint := strings.TrimSpace(req.Body.Endpoint)
	if endpoint == "" {
		return NoContent{}, BadRequest("Enter a push subscription from this browser.")
	}
	if err := h.Subscriptions.Delete(req.Context, scope, endpoint); err != nil {
		return NoContent{}, InternalError(err)
	}
	return NoContent{}, nil
}
