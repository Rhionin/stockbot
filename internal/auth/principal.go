package auth

import "context"

// Principal is who this request is allowed to act as.
// Kind is account or device.
type Principal struct {
	Kind            string
	UserID          string
	Email           string
	Name            string
	Operator        bool
	Role            string
	HouseholdID     string
	StoredHousehold string
	SessionID       string
	DeviceID        string
	FromCookie      bool
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok && p.Kind != ""
}
