// Package cart provides provider-agnostic provisioning capability types and interfaces.
package cart

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ProviderID names one Grocery_Provider. Lowercase, non-empty.
type ProviderID string

func (p ProviderID) Valid() bool {
	return len(p) > 0
}

// Dimension returns the string "provider_id" for use in error messages.
func (p ProviderID) Dimension() string {
	return "provider_id"
}

// AuthCapability identifies the authentication scheme a provider uses.
type AuthCapability string

const (
	AuthNone   AuthCapability = "none"
	AuthOAuth2 AuthCapability = "oauth2_authorization_code"
	AuthAPIKey AuthCapability = "api_key"
)

func (a AuthCapability) Valid() bool {
	switch a {
	case AuthNone, AuthOAuth2, AuthAPIKey:
		return true
	default:
		return false
	}
}

func (a AuthCapability) Dimension() string {
	return "auth"
}

// DeliveryCapability identifies how a provider receives provisioning requests.
type DeliveryCapability string

const (
	DeliveryServerPush    DeliveryCapability = "server_push"
	DeliveryClientHandoff DeliveryCapability = "client_handoff"
)

func (d DeliveryCapability) Valid() bool {
	switch d {
	case DeliveryServerPush, DeliveryClientHandoff:
		return true
	default:
		return false
	}
}

func (d DeliveryCapability) Dimension() string {
	return "delivery"
}

// ConfirmationCapability identifies how a provider reports results.
type ConfirmationCapability string

const (
	ConfirmPerLine    ConfirmationCapability = "per_line"
	ConfirmPerRequest ConfirmationCapability = "per_request"
	ConfirmNone       ConfirmationCapability = "none"
)

func (c ConfirmationCapability) Valid() bool {
	switch c {
	case ConfirmPerLine, ConfirmPerRequest, ConfirmNone:
		return true
	default:
		return false
	}
}

func (c ConfirmationCapability) Dimension() string {
	return "confirmation"
}

// MutationCapability identifies the mutation operations a provider supports.
type MutationCapability string

const (
	MutateAddOnly      MutationCapability = "add_only"
	MutateAddAndUpdate MutationCapability = "add_and_update"
	MutateFull         MutationCapability = "full"
)

func (m MutationCapability) Valid() bool {
	switch m {
	case MutateAddOnly, MutateAddAndUpdate, MutateFull:
		return true
	default:
		return false
	}
}

func (m MutationCapability) Dimension() string {
	return "mutation"
}

// IdentityCapability identifies how a provider identifies products.
type IdentityCapability string

const (
	IdentityDerived  IdentityCapability = "derived"
	IdentityLookedUp IdentityCapability = "looked_up"
)

func (i IdentityCapability) Valid() bool {
	switch i {
	case IdentityDerived, IdentityLookedUp:
		return true
	default:
		return false
	}
}

func (i IdentityCapability) Dimension() string {
	return "identity"
}

// Capabilities is the declared source of truth for what one provider can do.
type Capabilities struct {
	Auth         AuthCapability
	Delivery     DeliveryCapability
	Confirmation ConfirmationCapability
	Mutation     MutationCapability
	Identity     IdentityCapability
}

// Validate returns an error if any capability dimension is invalid.
func (c Capabilities) Validate() error {
	var errs []string

	if !c.Auth.Valid() {
		errs = append(errs, fmt.Sprintf("auth=%q is invalid", c.Auth))
	}
	if !c.Delivery.Valid() {
		errs = append(errs, fmt.Sprintf("delivery=%q is invalid", c.Delivery))
	}
	if !c.Confirmation.Valid() {
		errs = append(errs, fmt.Sprintf("confirmation=%q is invalid", c.Confirmation))
	}
	if !c.Mutation.Valid() {
		errs = append(errs, fmt.Sprintf("mutation=%q is invalid", c.Mutation))
	}
	if !c.Identity.Valid() {
		errs = append(errs, fmt.Sprintf("identity=%q is invalid", c.Identity))
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid capabilities: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Provider is what every adapter implements, whatever its capabilities.
type Provider interface {
	ID() ProviderID
	DisplayName() string
	Capabilities() Capabilities
}

// OAuthFlow is required exactly when Auth == AuthOAuth2, and forbidden otherwise.
type OAuthFlow interface {
	AuthorizationScope() string
	AuthorizationURL(state string) (string, error)
	ExchangeCode(ctx context.Context, code string) (TokenSet, error)
	RefreshAccessToken(ctx context.Context, refreshToken string) (TokenSet, error)
}

// StaticCredential is required exactly when Auth == AuthAPIKey.
type StaticCredential interface {
	Credential() Credential
}

// ServerPush is required exactly when Delivery == DeliveryServerPush.
type ServerPush interface {
	Add(ctx context.Context, cred Credential, req ProvisionRequest) (ProvisionResult, error)
}

// HandoffBuilder is required exactly when Delivery == DeliveryClientHandoff.
type HandoffBuilder interface {
	BuildHandoff(req ProvisionRequest) (HandoffArtifact, error)
}

// DerivedIdentity is required exactly when Identity == IdentityDerived.
type DerivedIdentity interface {
	DeriveIdentity(barcode string) (ProductIdentity, bool)
}

// LookedUpIdentity is required exactly when Identity == IdentityLookedUp.
type LookedUpIdentity interface {
	LookUpIdentity(ctx context.Context, cred Credential, barcode string) (ProductIdentity, bool, error)
}

// LineUpdater is required when Mutation is add_and_update or full.
type LineUpdater interface {
	UpdateLine(ctx context.Context, cred Credential, line ProvisionLine) (ProvisionResult, error)
}

// LineRemover is required when Mutation is full.
type LineRemover interface {
	RemoveLine(ctx context.Context, cred Credential, identity ProductIdentity) (ProvisionResult, error)
}

// AppCredentials is the non-secret view of one provider's application credentials.
// ClientSecret is never part of this value.
type AppCredentials struct {
	ClientID    string
	RedirectURI string
	Modality    string
	SecretSet   bool
}

// AppCredentialSink is implemented by a provider whose client id, client secret,
// and redirect URI can be saved from the Pantry UI. The secret is accepted on
// apply and never returned.
type AppCredentialSink interface {
	ApplyAppCredentials(clientID, clientSecret, redirectURI, modality string) error
	ClearAppCredentials()
	PublicAppCredentials() AppCredentials
}

// Credential applies one provider's authentication to an outbound request.
type Credential interface {
	Apply(req *http.Request)
	String() string // always redacted
}

// NoCredential is used when Auth == AuthNone.
type NoCredential struct{}

func (n NoCredential) Apply(_ *http.Request) {}
func (n NoCredential) String() string        { return "none" }

// BearerCredential applies a bearer token.
type BearerCredential struct{ token string }

func (b BearerCredential) Apply(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+b.token)
}

func (b BearerCredential) String() string {
	return "bearer(redacted)"
}

// NewBearerCredential creates a new BearerCredential.
// This is provided for packages that need to construct bearer credentials.
func NewBearerCredential(token string) BearerCredential {
	return BearerCredential{token: token}
}

// HeaderCredential applies a custom header.
type HeaderCredential struct{ name, value string }

func (h HeaderCredential) Apply(req *http.Request) {
	req.Header.Set(h.name, h.value)
}

func (h HeaderCredential) String() string {
	return h.name + "=redacted"
}

// ProductIdentity is a provider's own identifier for one pantry product.
type ProductIdentity string

// ResolvedItem is one shopping list entry paired with the identity resolved for
// its product and the quantity that entry contributes.
//
// ItemID is the pantry item (items.id); the ledger, outcomes, and adjustments
// are keyed by it. ProductID is that item's products.id, and barcodes and
// catalog records are keyed by it, so identity resolution must use ProductID,
// not ItemID.
type ResolvedItem struct {
	EntryID   string
	ItemID    string
	ProductID string
	Name      string
	Identity  ProductIdentity
	Quantity  int // 1..999
	// UnresolvedReason is set when identity resolution fails before provisioning.
	UnresolvedReason OutcomeReason
}

// ProvisionLine is one identity/quantity pair submitted to a provider.
type ProvisionLine struct {
	Identity ProductIdentity
	Quantity int               // 1..999, the clamped sum over Accounts
	Options  map[string]string // only keys the provider declares
	Accounts []ResolvedItem    // never empty; len > 1 when entries share an identity
}

// ProvisionRequest carries 1..BatchSize lines to exactly one provider.
type ProvisionRequest struct {
	Provider ProviderID
	Lines    []ProvisionLine
}

// ResultDisposition is the high-level outcome of a provisioning request.
type ResultDisposition string

const (
	DispositionAccepted      ResultDisposition = "accepted"
	DispositionRejected      ResultDisposition = "rejected"
	DispositionIndeterminate ResultDisposition = "indeterminate"
)

// ProvisionResult is what one adapter reports for one request.
type ProvisionResult struct {
	Disposition ResultDisposition        // Accepted | Rejected | Indeterminate
	PerLine     map[ProductIdentity]bool // populated only when Confirmation == per_line
	Status      int                      // provider HTTP status, for the error message
}

// Outcome is the final classification of one entry's provisioning attempt.
type Outcome string

const (
	OutcomeConfirmed Outcome = "confirmed"
	OutcomeFailed    Outcome = "failed"
	OutcomeUnknown   Outcome = "unknown"
)

// OutcomeReason is the "exactly one reason" of Requirement 11.7.
type OutcomeReason string

const (
	// ReasonNoProductIdentity is deprecated; prefer ReasonNoUPC or ReasonNoMatch.
	ReasonNoProductIdentity OutcomeReason = "no_product_identity"
	ReasonNoUPC             OutcomeReason = "no_upc"
	ReasonNoMatch           OutcomeReason = "no_match"
	ReasonUnavailable       OutcomeReason = "unavailable"
	ReasonAPIError          OutcomeReason = "api_error"
	ReasonProviderRejected  OutcomeReason = "provider_rejected"
	ReasonRequestIncomplete OutcomeReason = "request_incomplete"
	ReasonNoPerItemResult   OutcomeReason = "no_per_item_result"
)

// EntryOutcome is the final classification of one entry.
type EntryOutcome struct {
	EntryID  string
	ItemID   string
	Name     string
	Quantity int
	Outcome  Outcome
	Reason   OutcomeReason
}

// HandoffArtifact is created by client_handoff delivery.
type HandoffArtifact struct {
	URL  string
	Data map[string]string
}

// ProvisionReport is what a Provision operation returns.
type ProvisionReport struct {
	Provider  ProviderID
	Confirmed int
	Entries   []EntryOutcome
	Handoff   *HandoffArtifact
}

// LedgerEntry is what Pantry believes it has already requested from one provider.
type LedgerEntry struct {
	Provider  ProviderID
	ItemID    string
	Requested int       // >= 0
	Boundary  time.Time // UTC
}

// ConnectionState is the connection state of one provider.
type ConnectionState string

const (
	StateNotRequired    ConnectionState = "not_required"
	StateConnected      ConnectionState = "connected"
	StateReauthRequired ConnectionState = "reauth_required"
	StateDisconnected   ConnectionState = "disconnected"
)

// Connection is server-side only. No JSON tags: this type is never marshalled.
type Connection struct {
	Provider     ProviderID
	State        ConnectionState
	AccessToken  string
	RefreshToken string
	ExpiresAt    *time.Time
	AuthState    string
	AuthStateAt  *time.Time
}

// TokenSet is returned by OAuthFlow methods.
type TokenSet struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
	ReceiptAt    time.Time
}
