package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// maxQuarantined is the number of routes still waiting on per-household
// authorization (#377). The coverage test fails when the quarantine list
// is longer than this, and when it is shorter until this constant is
// lowered to match. When a route leaves quarantine, lower this constant
// to the new length in the same change.
//
// Raising maxQuarantined requires explicit reviewer sign-off. Raising it
// is not an acceptable way to add a route.
const maxQuarantined = 99

const waitsOnHouseholdScope = "waits #377"

type isoCategory string

const (
	catPublic     isoCategory = "public"
	catOps        isoCategory = "ops"
	catIsolation  isoCategory = "isolation"
	catQuarantine isoCategory = "quarantine"
)

// isoKind is the cross-household check a route needs once it leaves
// quarantine. The kinds are the case shapes from the route inventory.
type isoKind string

const (
	kindForeignID        isoKind = "foreign-id"
	kindListLeak         isoKind = "list-leak"
	kindWriteScope       isoKind = "write-scope"
	kindScopedCollection isoKind = "scoped-collection"
)

// allowEntry is one public or ops route that is intentionally outside
// household isolation, with the reason it stays there.
type allowEntry struct {
	pattern string
	reason  string
}

// isolationCase is a quarantine entry or a real isolation case.
// check is nil for quarantine. A real case sets check; the suite runs it.
type isolationCase struct {
	kind   isoKind
	reason string
	check  func(*testing.T, *twoHouseholds)
}

// The case table is keyed by the pattern string the router registers.
// Method routes use "METHOD /path". /api/sentry-tunnel is registered with
// no method, so its key is that pattern.

// publicAllowlist must match isPublicPath and isAuthEndpoint. The coverage
// test fails when either side gains or loses a path.
var publicAllowlist = []allowEntry{
	{pattern: "GET /api/session", reason: "auth endpoint; isAuthEndpoint leaves it open before the household password"},
	{pattern: "POST /api/login", reason: "auth endpoint; isAuthEndpoint leaves it open before the household password"},
	{pattern: "POST /api/logout", reason: "auth endpoint; isAuthEndpoint leaves it open before the household password"},
	{pattern: "GET /api/build", reason: "build identity; isPublicPath leaves it open on the public entry"},
	{pattern: "GET /api/telemetry", reason: "process-wide counters; isPublicPath, and the body must not carry household ids or names"},
	{pattern: "POST /api/telemetry/client", reason: "browser report; isPublicPath leaves it open on the public entry"},
	{pattern: "POST /api/deploy-hook", reason: "deploy hook; isPublicPath, and the handler checks the HMAC itself"},
	{pattern: "/api/sentry-tunnel", reason: "signed-out failure reports; isPublicPath, registered with no method"},
	{pattern: "GET /brand/logo.png", reason: "mark a grocery login stores by URL; isPublicPath"},
	{pattern: "GET /terms", reason: "legal page a grocery login stores by URL; isPublicPath"},
	{pattern: "GET /privacy", reason: "legal page a grocery login stores by URL; isPublicPath"},
}

// opsAllowlist is process liveness. These routes are not household data.
// GET /health still asks for the household password on the public entry.
var opsAllowlist = []allowEntry{
	{pattern: "GET /health", reason: "process liveness with no household data"},
}

// isolationCases holds routes whose cross-household check runs in this
// suite. A new household route lands here with a check. It does not join
// quarantine.
var isolationCases = map[string]isolationCase{
	"GET /api/moves/{id}/preview": {
		kind:   kindForeignID,
		reason: "another household's userId or move id is the same 404 as a missing move, and the body names nothing",
		check:  checkPreviewHidesForeignMove,
	},
	"GET /api/scans/{id}/link-suggestions": {
		kind:   kindForeignID,
		reason: "another household's scan id is the same 404 as a missing scan, and the body names nothing",
		check:  checkLinkSuggestionsHideForeignScan,
	},
}

func checkPreviewHidesForeignMove(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	for _, path := range []string{
		"/api/moves/" + fx.Birch.ProductID + "/preview?userId=" + fx.Birch.ID,
		"/api/moves/seg/preview?userId=" + fx.Birch.ID,
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		fx.principalFor(fx.Alder).Apply(req)
		rec := httptest.NewRecorder()
		fx.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status %d body %s", path, rec.Code, rec.Body.String())
		}
		assertNoLeak(t, rec.Body.Bytes(), fx.Birch)
		assertNoLeak(t, rec.Body.Bytes(), fx.Alder)
	}
}

func checkLinkSuggestionsHideForeignScan(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/scans/"+fx.Birch.ProductID+"/link-suggestions", nil)
	fx.principalFor(fx.Alder).Apply(req)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	assertNoLeak(t, rec.Body.Bytes(), fx.Birch)
	assertNoLeak(t, rec.Body.Bytes(), fx.Alder)
}

// quarantine is every household route that cannot be asserted until
// per-household authorization exists (#377). The list may shrink. It must
// not grow. GET /api/events and GET /api/sync/snapshot stay here: they
// carry household data and are not on an allowlist.
var quarantine = map[string]isolationCase{
	"GET /api/events":         {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/scanner/mode":  {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"GET /api/scanner/config": {kind: kindListLeak, reason: waitsOnHouseholdScope},

	"GET /api/products/lookup":             {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/products":                    {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/products/{id}":               {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/products":                   {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"PUT /api/products/{id}":               {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/products/overrides":         {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/products/{id}/refresh":      {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/products/{id}/contributions": {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/contributions":               {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/settings/contribution":       {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"PUT /api/settings/contribution":       {kind: kindWriteScope, reason: waitsOnHouseholdScope},

	"POST /api/scans":              {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"GET /api/scans":               {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/scans/history":       {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"PATCH /api/scans/{id}":        {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/scans/{id}/commit":  {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/scans/batch-commit": {kind: kindWriteScope, reason: waitsOnHouseholdScope},

	"GET /api/inventory":                           {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/inventory/{itemId}/instances":        {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/inventory/{itemId}/instances":       {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/inventory/{itemId}/stock-out":       {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/inventory/instances/{instanceId}": {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/inventory/wipe":                     {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"GET /api/sync/snapshot":                       {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/history":                             {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/items/{itemId}/history":              {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/groups/{groupId}/history":            {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PATCH /api/moves/{id}":                        {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/moves/{id}":                       {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/settings/supply":                     {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"PUT /api/settings/supply":                     {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"GET /api/products/{id}/supply-override":       {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PUT /api/products/{id}/supply-override":       {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/onboarding/complete":                {kind: kindWriteScope, reason: waitsOnHouseholdScope},

	"GET /api/restock":                                           {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"PUT /api/restock/household":                                 {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"PUT /api/restock/items/{itemId}":                            {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/restock/items/{itemId}":                         {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/restock/items/{itemId}/dont-restock":              {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/restock/items/{itemId}/categories":                {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/restock/items/{itemId}/categories/{categoryId}": {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/restock/categories":                                {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/restock/categories":                               {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"PUT /api/restock/categories/{id}/target":                    {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/restock/categories/{id}/preview":                  {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PATCH /api/restock/categories/{id}":                         {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/restock/categories/{id}/merge":                    {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/restock/categories/{id}":                        {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/restock/groups/{id}/preview":                      {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/restock/suggestions":                               {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/restock/suggestions/apply":                        {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/restock/suggestions/dismiss":                      {kind: kindWriteScope, reason: waitsOnHouseholdScope},

	"GET /api/groups":                                  {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/groups":                                 {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/groups/preview":                         {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"GET /api/groups/{id}":                             {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PATCH /api/groups/{id}":                           {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/groups/{id}":                          {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/groups/{id}/members":                    {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/groups/{id}/members/{productId}":      {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PUT /api/groups/{id}/members/{productId}/restock": {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PUT /api/groups/{id}/rule":                        {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PUT /api/groups/{id}/target":                      {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/group-suggestions":                       {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/group-suggestions/rescan":               {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/group-suggestions/from-scan":            {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/group-suggestions/{id}/accept":          {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/group-suggestions/{id}/dismiss":         {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/group-suggestions/{id}/skip":            {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/settings/group-rule":                     {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"PUT /api/settings/group-rule":                     {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/items/{itemId}/target-quantity":         {kind: kindForeignID, reason: waitsOnHouseholdScope},

	"GET /api/shopping-list":                   {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/shopping-list/fill":             {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/shopping-list/items":            {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"DELETE /api/shopping-list/items/{id}":     {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PATCH /api/shopping-list/items/{id}":      {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/shopping-list/items/{id}/swap":  {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PUT /api/shopping-list/deals":             {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"DELETE /api/shopping-list/deals/{itemId}": {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/shopping-list/export":           {kind: kindWriteScope, reason: waitsOnHouseholdScope},

	"GET /api/providers":                              {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"GET /api/providers/{providerId}/authorize":       {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"GET /api/providers/{providerId}/callback":        {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"DELETE /api/providers/{providerId}/connection":   {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"PUT /api/providers/{providerId}/credentials":     {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"DELETE /api/providers/{providerId}/credentials":  {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"GET /api/providers/{providerId}/ledger":          {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"POST /api/providers/{providerId}/ledger/reset":   {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"POST /api/items/{itemId}/replenishment-mode":     {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PUT /api/shopping-list/items/{id}/adjustment":    {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/shopping-list/items/{id}/adjustment": {kind: kindForeignID, reason: waitsOnHouseholdScope},

	"GET /api/push/vapid-public-key":                        {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/push/subscriptions":                          {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"DELETE /api/push/subscriptions":                        {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/shopping-list/items/{id}/unknown-resolution": {kind: kindForeignID, reason: waitsOnHouseholdScope},
}
