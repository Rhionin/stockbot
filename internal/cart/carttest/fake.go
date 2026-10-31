// Package carttest provides test infrastructure for cart providers.
// It follows the net/http/httptest pattern: a non-test package that can be
// imported by both internal/cart and internal/cart/kroger tests.
package carttest

import (
	"context"
	"sync"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
)

// AddOutcome queues one ServerPush.Add result.
type AddOutcome struct {
	Result cart.ProvisionResult
	Err    error
}

// Script queues per-request dispositions, per-line results for per_line,
// identity lookups, token-exchange outcomes, and optional Add outcomes.
type Script struct {
	mu              sync.Mutex
	dispositions    []cart.ResultDisposition
	lineResults     map[cart.ProductIdentity]bool
	identityLookups map[string]cart.ProductIdentity
	tokenExchanges  []TokenExchangeResult
	addOutcomes     []AddOutcome
}

// TokenExchangeResult records the result of a token exchange.
type TokenExchangeResult struct {
	TokenSet cart.TokenSet
	Err      error
}

// NewScript creates a new empty script.
func NewScript() *Script {
	return &Script{
		lineResults:     make(map[cart.ProductIdentity]bool),
		identityLookups: make(map[string]cart.ProductIdentity),
	}
}

// WithDispositions sets the dispositions for successive Add calls.
func (s *Script) WithDispositions(disps ...cart.ResultDisposition) *Script {
	s.dispositions = disps
	return s
}

// WithLineResults sets the per-line results for per_line confirmation.
func (s *Script) WithLineResults(results map[cart.ProductIdentity]bool) *Script {
	s.lineResults = results
	return s
}

// WithIdentityLookup records what identity should be returned for a barcode.
func (s *Script) WithIdentityLookup(barcode string, identity cart.ProductIdentity) *Script {
	s.identityLookups[barcode] = identity
	return s
}

// WithTokenExchangeSuccess records a successful token exchange result.
func (s *Script) WithTokenExchangeSuccess() *Script {
	s.tokenExchanges = append(s.tokenExchanges, TokenExchangeResult{
		TokenSet: cart.TokenSet{
			AccessToken:  "test-access-token",
			RefreshToken: "test-refresh-token",
			ExpiresIn:    3600 * time.Second,
			ReceiptAt:    time.Now(),
		},
		Err: nil,
	})
	return s
}

// WithTokenExchangeError records an error token exchange result.
func (s *Script) WithTokenExchangeError(err error) *Script {
	s.tokenExchanges = append(s.tokenExchanges, TokenExchangeResult{
		TokenSet: cart.TokenSet{},
		Err:      err,
	})
	return s
}

// WithAddOutcomes sets explicit Add results in order. When empty, Add uses dispositions as before.
func (s *Script) WithAddOutcomes(outcomes ...AddOutcome) *Script {
	s.addOutcomes = append(s.addOutcomes, outcomes...)
	return s
}

// fakeCore holds the state every fake provider shares: its declared identity,
// the controlling Script, and the recorded call log. The behavior mixins below
// embed it so a composite provider records into one shared log.
type fakeCore struct {
	id          cart.ProviderID
	displayName string
	caps        cart.Capabilities
	script      *Script

	callsMu sync.Mutex
	calls   []string
}

func (f *fakeCore) ID() cart.ProviderID             { return f.id }
func (f *fakeCore) DisplayName() string             { return f.displayName }
func (f *fakeCore) Capabilities() cart.Capabilities { return f.caps }

func (f *fakeCore) recordCall(name string) {
	f.callsMu.Lock()
	defer f.callsMu.Unlock()
	f.calls = append(f.calls, name)
}

// Calls returns a copy of the method invocations recorded so far, in order.
func (f *fakeCore) Calls() []string {
	f.callsMu.Lock()
	defer f.callsMu.Unlock()
	result := make([]string, len(f.calls))
	copy(result, f.calls)
	return result
}

// The behavior mixins below each embed *fakeCore. A composite provider is one
// struct that embeds *fakeCore plus exactly the mixins its capabilities
// require, so its method set satisfies exactly the corresponding narrow cart
// interfaces and no others. NewFake selects the composite for a caps profile.

// Each mixin holds a named *fakeCore rather than embedding it, so a composite
// can embed several mixins without ambiguous promotion of the core methods.

type oauthFlow struct{ core *fakeCore }

func (f oauthFlow) AuthorizationScope() string { return "test:scope" }

func (f oauthFlow) AuthorizationURL(state string) (string, error) {
	return "https://example.com/auth?" + state, nil
}

func (f oauthFlow) ExchangeCode(ctx context.Context, code string) (cart.TokenSet, error) {
	f.core.recordCall("ExchangeCode")
	return f.core.nextTokenExchange()
}

func (f oauthFlow) RefreshAccessToken(ctx context.Context, refreshToken string) (cart.TokenSet, error) {
	f.core.recordCall("RefreshAccessToken")
	return f.core.nextTokenExchange()
}

func (f *fakeCore) nextTokenExchange() (cart.TokenSet, error) {
	f.script.mu.Lock()
	defer f.script.mu.Unlock()
	if len(f.script.tokenExchanges) == 0 {
		return cart.TokenSet{}, nil
	}
	result := f.script.tokenExchanges[0]
	f.script.tokenExchanges = f.script.tokenExchanges[1:]
	return result.TokenSet, result.Err
}

type staticCredential struct{ core *fakeCore }

func (f staticCredential) Credential() cart.Credential {
	f.core.recordCall("Credential")
	return cart.NewBearerCredential("test-api-key")
}

type serverPush struct{ core *fakeCore }

func (f serverPush) Add(ctx context.Context, cred cart.Credential, req cart.ProvisionRequest) (cart.ProvisionResult, error) {
	f.core.recordCall("Add")
	f.core.script.mu.Lock()
	defer f.core.script.mu.Unlock()

	if len(f.core.script.addOutcomes) > 0 {
		outcome := f.core.script.addOutcomes[0]
		f.core.script.addOutcomes = f.core.script.addOutcomes[1:]
		return outcome.Result, outcome.Err
	}

	disposition := cart.DispositionAccepted
	if len(f.core.script.dispositions) > 0 {
		disposition = f.core.script.dispositions[0]
		f.core.script.dispositions = f.core.script.dispositions[1:]
	}

	return cart.ProvisionResult{
		Disposition: disposition,
		PerLine:     f.core.script.lineResults,
		Status:      200,
	}, nil
}

type handoffBuilder struct{ core *fakeCore }

func (f handoffBuilder) BuildHandoff(req cart.ProvisionRequest) (cart.HandoffArtifact, error) {
	f.core.recordCall("BuildHandoff")
	return cart.HandoffArtifact{
		URL:  "https://example.com/handoff",
		Data: map[string]string{"key": "value"},
	}, nil
}

type derivedIdentity struct{ core *fakeCore }

func (f derivedIdentity) DeriveIdentity(barcode string) (cart.ProductIdentity, bool) {
	f.core.recordCall("DeriveIdentity")
	f.core.script.mu.Lock()
	defer f.core.script.mu.Unlock()
	identity, ok := f.core.script.identityLookups[barcode]
	return identity, ok
}

type lookedUpIdentity struct{ core *fakeCore }

func (f lookedUpIdentity) LookUpIdentity(ctx context.Context, cred cart.Credential, barcode string) (cart.ProductIdentity, bool, error) {
	f.core.recordCall("LookUpIdentity")
	f.core.script.mu.Lock()
	defer f.core.script.mu.Unlock()
	identity, ok := f.core.script.identityLookups[barcode]
	return identity, ok, nil
}

type lineUpdater struct{ core *fakeCore }

func (f lineUpdater) UpdateLine(ctx context.Context, cred cart.Credential, line cart.ProvisionLine) (cart.ProvisionResult, error) {
	f.core.recordCall("UpdateLine")
	return cart.ProvisionResult{Disposition: cart.DispositionAccepted, Status: 200}, nil
}

type lineRemover struct{ core *fakeCore }

func (f lineRemover) RemoveLine(ctx context.Context, cred cart.Credential, identity cart.ProductIdentity) (cart.ProvisionResult, error) {
	f.core.recordCall("RemoveLine")
	return cart.ProvisionResult{Disposition: cart.DispositionAccepted, Status: 200}, nil
}

// Calls exposes the recorded invocations on any composite NewFake returns.
type Calls interface {
	Calls() []string
}

// NewFake returns a provider declaring exactly caps and satisfying the narrow
// cart interfaces those capabilities require. The returned value always
// satisfies cart.Provider and the Calls interface. Its auth dimension selects
// the auth interface: OAuthFlow for oauth2, StaticCredential for api_key,
// neither for none. Its delivery dimension selects ServerPush or HandoffBuilder,
// and its identity dimension selects DerivedIdentity or LookedUpIdentity. The
// mutation interfaces LineUpdater and LineRemover are always present; the engine
// dispatches to them only when the declared mutation capability warrants, so
// their presence on a lower-mutation fake is inert.
//
// The concrete composite is one of the twelve auth-by-delivery-by-identity
// shapes below, chosen so that the method set matches the auth, delivery, and
// identity dimensions exactly rather than by embedding nil interfaces.
func NewFake(caps cart.Capabilities, script *Script) cart.Provider {
	if script == nil {
		script = NewScript()
	}
	core := &fakeCore{
		id:          cart.ProviderID("test-" + string(caps.Auth) + "-" + string(caps.Delivery)),
		displayName: "Test Provider",
		caps:        caps,
		script:      script,
	}
	return buildComposite(core)
}

// mutations carries the mutation mixins every composite embeds.
type mutations struct {
	lineUpdater
	lineRemover
}

func newMutations(core *fakeCore) mutations {
	return mutations{lineUpdater{core}, lineRemover{core}}
}

// The twelve composites cover auth (none, oauth2, api_key) by delivery
// (server_push, client_handoff) by identity (derived, looked_up). Each embeds
// *fakeCore for the Provider and Calls methods, its delivery and identity
// mixins, its mutation mixins, and, when auth requires it, an auth mixin.

type pushDerived struct {
	*fakeCore
	serverPush
	derivedIdentity
	mutations
}
type pushLookedUp struct {
	*fakeCore
	serverPush
	lookedUpIdentity
	mutations
}
type handoffDerived struct {
	*fakeCore
	handoffBuilder
	derivedIdentity
	mutations
}
type handoffLookedUp struct {
	*fakeCore
	handoffBuilder
	lookedUpIdentity
	mutations
}

type oauthPushDerived struct {
	pushDerived
	oauthFlow
}
type oauthPushLookedUp struct {
	pushLookedUp
	oauthFlow
}
type oauthHandoffDerived struct {
	handoffDerived
	oauthFlow
}
type oauthHandoffLookedUp struct {
	handoffLookedUp
	oauthFlow
}

type apiKeyPushDerived struct {
	pushDerived
	staticCredential
}
type apiKeyPushLookedUp struct {
	pushLookedUp
	staticCredential
}
type apiKeyHandoffDerived struct {
	handoffDerived
	staticCredential
}
type apiKeyHandoffLookedUp struct {
	handoffLookedUp
	staticCredential
}

func buildComposite(core *fakeCore) cart.Provider {
	base := func() any {
		push := core.caps.Delivery == cart.DeliveryServerPush
		derived := core.caps.Identity == cart.IdentityDerived
		switch {
		case push && derived:
			return pushDerived{core, serverPush{core}, derivedIdentity{core}, newMutations(core)}
		case push && !derived:
			return pushLookedUp{core, serverPush{core}, lookedUpIdentity{core}, newMutations(core)}
		case !push && derived:
			return handoffDerived{core, handoffBuilder{core}, derivedIdentity{core}, newMutations(core)}
		default:
			return handoffLookedUp{core, handoffBuilder{core}, lookedUpIdentity{core}, newMutations(core)}
		}
	}()

	switch core.caps.Auth {
	case cart.AuthOAuth2:
		switch b := base.(type) {
		case pushDerived:
			return oauthPushDerived{b, oauthFlow{core}}
		case pushLookedUp:
			return oauthPushLookedUp{b, oauthFlow{core}}
		case handoffDerived:
			return oauthHandoffDerived{b, oauthFlow{core}}
		default:
			return oauthHandoffLookedUp{b.(handoffLookedUp), oauthFlow{core}}
		}
	case cart.AuthAPIKey:
		switch b := base.(type) {
		case pushDerived:
			return apiKeyPushDerived{b, staticCredential{core}}
		case pushLookedUp:
			return apiKeyPushLookedUp{b, staticCredential{core}}
		case handoffDerived:
			return apiKeyHandoffDerived{b, staticCredential{core}}
		default:
			return apiKeyHandoffLookedUp{b.(handoffLookedUp), staticCredential{core}}
		}
	default:
		return base.(cart.Provider)
	}
}
