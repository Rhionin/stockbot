package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/syncsnap"
)

// Sentences the server already returns. busy and no_space are the words PR #320
// locks; those constants are not on master yet, so the literals are pinned here.
func existingContractSentences() map[string]string {
	return map[string]string{
		"busy":                          "Stockbot is busy right now. Try again in a moment.",
		"no_space":                      "Stockbot is out of storage space, so this wasn't saved.",
		"unauthorized":                  "Sign in to continue.",
		"forbidden_role":                "Only an owner can do that.",
		"not_found":                     "Stockbot couldn't find that. It may have been removed.",
		"conflict":                      "Someone else just changed this. Refresh and try again.",
		"invite_expired":                "This invite has expired or was already used. Ask for a new one.",
		"validation":                    "Check what you entered and try again.",
		inventory.CodeNotPending:        inventory.NotPendingMessage,
		inventory.CodeProductUnresolved: inventory.ProductUnresolvedMessage,
		inventory.CodeNoneOnHand:        inventory.NoneOnHandMessage,
		inventory.CodeInstanceGone:      inventory.InstanceGoneMessage,
		inventory.CodeNoItem:            inventory.WhichItemMessage,
		inventory.CodeLinkTargetGone:    inventory.LinkTargetGoneMessage,
		inventory.CodeScanNotFound:      inventory.ScanNotFoundMessage,
		inventory.CodeDirectionUnset:    inventory.DirectionUnsetMessage,
		inventory.CodeInternal:          inventory.InternalMessage,
		"last_owner":                    "You're the only owner. Make someone else an owner first, or delete the household.",
		"household_not_empty":           "You can only import into an empty household. This one already has items.",
		"confirmation_mismatch":         "That doesn't match. Check the spelling and try again.",
		"rate_limited":                  "Too many tries. Wait a moment and try again.",
		"payload_too_large":             "That file is too large to import.",
		"invite_required":               "You need an invite to start a household.",
		"reset_expired":                 "This reset link has expired or was already used. Ask for a new one.",
		"account_exists":                "That email already has an account. Sign in, then open the invite again.",
		"device_revoked":                "This device was disconnected. Pair it again to keep using it.",
		"upgrade_required":              "Update Stockbot to keep using it.",
	}
}

type contractRow struct {
	code     string
	sentence string
	notes    string
}

func contractRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func readContract(t *testing.T) (doc, ts string) {
	t.Helper()
	root := contractRepoRoot(t)
	docBytes, err := os.ReadFile(filepath.Join(root, "docs", "design", "api-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	tsBytes, err := os.ReadFile(filepath.Join(root, "frontend", "src", "api", "contract.ts"))
	if err != nil {
		t.Fatal(err)
	}
	return string(docBytes), string(tsBytes)
}

func parseRegistry(doc string) []contractRow {
	start := strings.Index(doc, "### Error code registry")
	end := strings.Index(doc, "## Open questions")
	if start < 0 || end < start {
		return nil
	}
	var rows []contractRow
	for _, line := range strings.Split(doc[start:end], "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 5 {
			continue
		}
		rows = append(rows, contractRow{
			code:     strings.Trim(parts[1], " `"),
			sentence: strings.TrimSpace(parts[3]),
			notes:    strings.TrimSpace(parts[4]),
		})
	}
	return rows
}

func parseHostedErrorCodes(ts string) []string {
	re := regexp.MustCompile(`(?s)export type HostedErrorCode =\s*([^;]+);`)
	found := re.FindStringSubmatch(ts)
	if found == nil {
		return nil
	}
	codeRe := regexp.MustCompile(`'([a-z0-9_]+)'`)
	var codes []string
	for _, match := range codeRe.FindAllStringSubmatch(found[1], -1) {
		codes = append(codes, match[1])
	}
	return codes
}

func parseEndpointIndex(doc string) []string {
	lines := strings.Split(doc, "\n")
	start := -1
	for i, line := range lines {
		if line == "endpoint-index" {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	var paths []string
	for _, line := range lines[start+1:] {
		if strings.HasPrefix(line, "```") {
			break
		}
		if strings.TrimSpace(line) != "" {
			paths = append(paths, strings.TrimSpace(line))
		}
	}
	return paths
}

func TestHostedAPIContract(t *testing.T) {
	doc, ts := readContract(t)
	rows := parseRegistry(doc)
	if len(rows) == 0 {
		t.Fatal("error code registry is empty")
	}

	docCodes := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.code] {
			t.Fatalf("duplicate error code %s", row.code)
		}
		seen[row.code] = true
		docCodes = append(docCodes, row.code)
	}
	tsCodes := parseHostedErrorCodes(ts)
	sort.Strings(docCodes)
	sort.Strings(tsCodes)
	if strings.Join(docCodes, ",") != strings.Join(tsCodes, ",") {
		t.Fatalf("doc codes %v\nts codes %v", docCodes, tsCodes)
	}

	existing := existingContractSentences()
	// Master names this clientmsg.Generic. The branch base still calls it ServerError.
	// Pin the sentence so the test compiles against either tree.
	const genericServerSentence = "Something went wrong. Try again."
	if existing["internal"] != genericServerSentence {
		t.Fatalf("internal sentence %q is not %q", existing["internal"], genericServerSentence)
	}
	if strings.Contains(doc, "proposed, PM to confirm") {
		t.Fatal("a sentence is still marked proposed, PM to confirm")
	}
	if strings.Contains(doc, "TODO(CJ)") {
		t.Fatal("invite host is still TODO(CJ)")
	}
	for _, phrase := range []string{
		"Decided by CJ, via Network Engineer, on 2026-10-22",
		"<APP_BASE_URL>/invite/<code>",
		"https://app.stockbot.rhionin.com",
		"mail.stockbot.rhionin.com",
	} {
		if !strings.Contains(doc, phrase) {
			t.Fatalf("contract missing %q", phrase)
		}
	}
	for _, row := range rows {
		want, ok := existing[row.code]
		if !ok {
			t.Fatalf("code %s has no pinned sentence", row.code)
		}
		if row.sentence != want {
			t.Fatalf("code %s sentence %q, want %q", row.code, row.sentence, want)
		}
	}

	const generic = "generic-sentence: Something went wrong. Try again."
	if !strings.Contains(doc, generic) {
		t.Fatal("generic sentence line missing")
	}
	if !strings.Contains(generic, genericServerSentence) {
		t.Fatalf("generic sentence does not match %q", genericServerSentence)
	}

	const tenant = "tenant-rule: Nothing in this contract accepts a client-supplied tenant other than the X-Household-Id header, and that header is valid only when it names a household the signed-in user belongs to, or, for device calls, the household bound to the device token, except the householdId body on POST /api/auth/session/household, which sets the session's active household after a membership check; nothing else selects a tenant."
	if !strings.Contains(doc, tenant) {
		t.Fatal("tenant rule missing")
	}

	paths := parseEndpointIndex(doc)
	method := regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE) /api/`)
	for _, path := range paths {
		if !method.MatchString(path) {
			t.Fatalf("bad endpoint index line %q", path)
		}
	}
	countLine := regexp.MustCompile(`(?m)^endpoint-count: (\d+)$`).FindStringSubmatch(doc)
	if countLine == nil {
		t.Fatal("endpoint-count missing")
	}
	count, err := strconv.Atoi(countLine[1])
	if err != nil {
		t.Fatal(err)
	}
	if count != len(paths) {
		t.Fatalf("endpoint-count %d, index has %d", count, len(paths))
	}
	for _, phrase := range []string{
		"__Host-stockbot_session",
		"Bearer sbs_",
		"upgrade_required",
		"householdPasswordEnabled: false",
		"invite-role: The role is set when the invite is created.",
		"password-switch: The household password switch returns only `householdPasswordEnabled`",
		"pairing-collision: A pairing code cannot collide with a product barcode.",
		"SBP-XXXX-XXXX-C",
		"POST /api/auth/login",
		"POST /api/device/heartbeat",
		"POST /api/device/token/rotate",
		"GET /api/events",
		"BEGIN IMMEDIATE",
		"UUIDv7 text ids preserved; no sequences",
		"looksLikePairingCode",
		"Stage 3; Pi returns `not_found`",
		"https://stockbot.rhionin.com",
		"LAN_PEOPLE=signin",
		"CLIENT_HEADER",
		"reset_expired",
		"operator-only on the public entry",
		"On the LAN, `GET /api/telemetry` is open only while the household password is on.",
		"After retirement it follows the LAN `401` rule.",
		"The LAN response never includes the changeover counts.",
		"Any `X-Stockbot-Client` value that does not start with `expo/` is web too, the same as a missing header, so there is no version gate.",
		"The same `426` applies when that Basic request carries a value that does not start with `expo/`.",
		"S6 (#379) must run the production classifier in `internal/scan/qr.go` against `testdata/pairing-codes.json`.",
		"POST /api/devices/pair/complete",
		"Invite codes are 48 bits.",
		"Pairing codes are 40 bits.",
		"The setup code is 40 bits.",
		"O, I, and L are aliases: O maps to 0, and I or L maps to 1.",
		"testdata/pairing-codes.json",
		"When two requests use the same invite, the loser gets `invite_expired`.",
		"The `426` `upgrade_required` gate runs before auth, rate limits, and busy handling",
		"On the LAN, after the first account exists, GET sends the user to sign-in at `APP_BASE_URL` and POST returns `not_found`, except in recover mode, where GET serves the page and a successful POST sends the user to sign-in at `APP_BASE_URL`.",
		"No session on the LAN. A session on the public entry",
		"The saved-origin fallback applies only when `entry` is absent from `GET /api/config`: a saved http: origin, or one matching no public hostname, means LAN.",
		"On the LAN: the LAN principal while the household password is on.",
		"on the LAN after retirement, unauthorized even with no cookie or bearer; Bearer sbs_ on the LAN is always unauthorized.",
		"`unauthorized`, `validation`, `rate_limited`, `busy`. On the LAN, `404` `not_found`",
		"`validation`, `reset_expired`, `not_found`, `rate_limited`, `busy`. On the LAN, `404` `not_found`",
		"The first setup creates the operator as owner of household 1 and sets no session.",
		"Recover resets the existing operator's password. It never creates an account, never changes the email, and never changes roles.",
		"The POST body is `{ \"setupCode\": string, \"email\": string, \"newPassword\": string }`.",
		"An unknown or non-operator email gets the same `404` `not_found` as a bad code, so the endpoint never reveals which emails exist.",
		"set the new argon2id hash; revoke all of that account's sessions (web and Expo `sbs_`); delete the push subscriptions tied to those sessions and close their SSE streams; invalidate any outstanding reset tokens for that account; consume the code (delete the file and leave recover mode).",
		"Device tokens (`sbd_`) and the household's other members are untouched.",
		"No session is set. A successful POST sends the user to sign-in at `APP_BASE_URL`.",
		"The code is single-use with a 24 h expiry, and the setup rate limits apply. An expired code returns `not_found`.",
		"`X-Pantry-Entry` and `X-Pantry-Client-IP` are trusted only when the request's `RemoteAddr` is the configured reverse proxy's address (a trusted-proxy setting). Otherwise the request is LAN and those headers are ignored.",
		"A separate upstream port that only the proxy can reach is an acceptable alternative deployment.",
		"LAN rate-limit keys always use `RemoteAddr`, and `X-Forwarded-For` is never trusted.",
		"The port binding is owned by infra.",
		"On routers without NAT hairpin, the certificate (non-tunnel) path can hang from home Wi-Fi (see the comments in `deploy/Caddyfile`).",
		"The client-facing type marks `entry` optional, because an older server omits it and the client falls back. The Go server always sends `entry`.",
		"Hosted `LiveConfig` sends `entry` `\"public\"` only.",
		"householdPasswordEnabled: boolean",
		"clientmsg.Generic",
		`"householdPasswordSignIn": boolean`,
		"Required in stage 3 only. The Pi takes the household from the job row.",
		"Trim only the ASCII whitespace characters space, tab, LF, VT, FF, and CR.",
		"typed `*`, `~`, `$`, and `=` are allowed",
		"The LAN listener never offers personal sign-in, whether or not the household password is on.",
		"invite accept with `account`, and `POST /api/households` return `404` `not_found`",
		"`Authorization: Bearer sbs_…` on `:8080` returns `401` `unauthorized` and is never honored.",
		"Session cookies are never set and never honored on the LAN.",
		"lan-client: Expo must use `appBaseUrl` for personal sign-in. LAN URLs are unsupported for people after retirement. Clients never show Sign in on a LAN URL.",
		`"appBaseUrl": string`,
		"The 11th stream is `429` `rate_limited`, with `Retry-After` in seconds.",
		"When the stream errors, the client refetches `GET /api/auth/session`.",
		"only that person's sessions whose active household was the removed one have `activeHouseholdId` set to null",
		"Accept returns `token` only when `account` is sent.",
		"household-start: A person who is already signed in starts another household here, with `inviteCode`.",
		"A missing part compares as 0, so `1.0` equals `1.0.0`.",
		"A new Expo build sends `X-Stockbot-Client` on every request, including `GET /api/events`.",
		"expo-401: Expo clears its stored `sbs_` token on any `401`, except the sign-in routes `POST /api/auth/login`, `POST /api/auth/reset`, and `POST /api/invites/accept`.",
		"Sign in at `appBaseUrl`.",
		"per source IP: 10 per IP, and the 11th from that IP is `429` `rate_limited`",
		"includes `Retry-After` in seconds, like the other `rate_limited` responses.",
		"Keeps working on the LAN, where hubs pair against `http://pi:8080`.",
		"Clients use `entry` as the exact signal.",
		"Dev and test servers must set `APP_BASE_URL`, or dev builds would point at prod.",
		"While the household password is on, Expo on a LAN URL may keep using the shared household-1 access, and should offer to move to `appBaseUrl`.",
		`"entry": "lan" | "public"`,
	} {
		if !strings.Contains(doc, phrase) {
			t.Fatalf("contract missing %q", phrase)
		}
	}
	if strings.Contains(doc, "clientmsg.ServerError") {
		t.Fatal("generic 5xx sentence still names clientmsg.ServerError")
	}
	for _, gone := range []string{
		"On the LAN it stays open, with no session.",
		"Setup sets it when the person has exactly one household",
		"signs them in, and sets `activeHouseholdId`",
		"`POST /api/invites/accept`, and `POST /setup`",
		"Sign-in is required",
		"On success, and on a GET after the first account exists",
		"differs from `appBaseUrl`",
	} {
		if strings.Contains(doc, gone) {
			t.Fatalf("contract still says %q", gone)
		}
	}
}

func TestPiSyncSnapshotFixture(t *testing.T) {
	root := contractRepoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "frontend", "src", "api", "fixtures", "pi-sync-snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snap syncsnap.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Products) == 0 || snap.Products[0].Name != "Chickpeas" || len(snap.Products[0].Barcodes) != 1 || snap.Products[0].Barcodes[0] != "000111222333" {
		t.Fatalf("fixture is not the Pi snapshot shape: %+v", snap.Products)
	}
	if len(snap.Groups) == 0 || len(snap.Instances) == 0 || len(snap.Moves) == 0 || len(snap.ShoppingList) == 0 || len(snap.Removals) == 0 {
		t.Fatal("fixture is missing a Pi snapshot collection")
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["legacyUserId"]; ok {
		t.Fatal("legacyUserId is optional and the fixture omits it")
	}
	if _, ok := doc["schemaVersion"]; ok {
		t.Fatal("a Pi snapshot has no schemaVersion")
	}
	doc["legacyUserId"] = "user-1"
	again, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var withUser syncsnap.Snapshot
	if err := json.Unmarshal(again, &withUser); err != nil {
		t.Fatal(err)
	}
	if withUser.Products[0].Name != "Chickpeas" {
		t.Fatal("adding legacyUserId rejected the Pi snapshot")
	}
}

type pairingVector struct {
	Input      string  `json:"input"`
	Normalized *string `json:"normalized"`
	LooksLike  bool    `json:"looksLike"`
	Pairing    bool    `json:"pairing"`
	GTIN       bool    `json:"gtin"`
}

const (
	pairingPayloadAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	pairingCheckAlphabet   = pairingPayloadAlphabet + "*~$=U"
)

var (
	pairingGrammar = regexp.MustCompile(`^SBP-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z*~$=U]$`)
	productBarcode = regexp.MustCompile(`^\d{1,14}$`)
)

func trimASCIIWhitespace(s string) string {
	return strings.Trim(s, " \t\n\v\f\r")
}

func normalizePairingCode(input string) (string, bool) {
	trimmed := trimASCIIWhitespace(input)
	var b strings.Builder
	for _, r := range trimmed {
		if r > 127 {
			return "", false
		}
		if r >= 'a' && r <= 'z' {
			r -= 32
		}
		switch r {
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		b.WriteRune(r)
	}
	return b.String(), true
}

func pairingCheckCharacter(payload string) (byte, bool) {
	var value uint64
	for i := 0; i < len(payload); i++ {
		digit := strings.IndexByte(pairingPayloadAlphabet, payload[i])
		if digit < 0 {
			return 0, false
		}
		value = value*32 + uint64(digit)
	}
	return pairingCheckAlphabet[value%37], true
}

func looksLikePairingCode(input string) bool {
	code, ok := normalizePairingCode(input)
	return ok && pairingGrammar.MatchString(code)
}

func isPairingCode(input string) bool {
	code, ok := normalizePairingCode(input)
	if !ok || !pairingGrammar.MatchString(code) {
		return false
	}
	check, ok := pairingCheckCharacter(code[4:8] + code[9:13])
	return ok && code[14] == check
}

func isProductBarcode(input string) bool {
	return productBarcode.MatchString(trimASCIIWhitespace(input))
}

func TestPairingCodeFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(contractRepoRoot(t), "testdata", "pairing-codes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []pairingVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) == 0 {
		t.Fatal("pairing fixture is empty")
	}
	for _, vector := range vectors {
		got, ok := normalizePairingCode(vector.Input)
		switch {
		case vector.Normalized == nil && ok:
			t.Fatalf("%q normalized to %q, want reject", vector.Input, got)
		case vector.Normalized != nil && (!ok || got != *vector.Normalized):
			t.Fatalf("%q normalized to %q ok=%v, want %q", vector.Input, got, ok, *vector.Normalized)
		}
		if looksLikePairingCode(vector.Input) != vector.LooksLike {
			t.Fatalf("%q looksLike %v, want %v", vector.Input, looksLikePairingCode(vector.Input), vector.LooksLike)
		}
		if isPairingCode(vector.Input) != vector.Pairing {
			t.Fatalf("%q pairing %v, want %v", vector.Input, isPairingCode(vector.Input), vector.Pairing)
		}
		if isProductBarcode(vector.Input) != vector.GTIN {
			t.Fatalf("%q gtin %v, want %v", vector.Input, isProductBarcode(vector.Input), vector.GTIN)
		}
		if (isPairingCode(vector.Input) || looksLikePairingCode(vector.Input)) && isProductBarcode(vector.Input) {
			t.Fatalf("%q is both a pairing code and a product barcode", vector.Input)
		}
	}
	sawNBSP := false
	for _, vector := range vectors {
		if !strings.ContainsRune(vector.Input, '\u00a0') {
			continue
		}
		sawNBSP = true
		got, ok := normalizePairingCode(vector.Input)
		if ok {
			t.Fatalf("U+00A0 pad %q normalized to %q", vector.Input, got)
		}
		if vector.Normalized != nil || vector.LooksLike || vector.Pairing || vector.GTIN {
			t.Fatalf("U+00A0 pad %q must be rejected", vector.Input)
		}
	}
	if !sawNBSP {
		t.Fatal("pairing fixture has no U+00A0 row")
	}
}
