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
	tsBytes, err := os.ReadFile(filepath.Join(root, "shared", "contract.ts"))
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
		"invite-role: The role is set when the invite is created.",
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
		"The saved-origin fallback applies only when `GET /api/config` omits `entry` (an older server). If `appBaseUrl` is present, a saved origin is LAN when it is http: or its host is not `appBaseUrl`'s host. If `appBaseUrl` is also absent, an http: origin is LAN and an https: origin is public.",
		"set by the listener that served the request",
		"The pantry server opens two listeners. `:8080` is the LAN entry, published to the host as today. `:8081` is the public entry. It is not published to the host and is reachable only on the Docker network, where the reverse proxy forwards to `pantry:8081`. Everything on `:8081` is public. Everything on `:8080` is LAN. Stage 1 removes `X-Pantry-Entry`: it is never honored anywhere. `X-Pantry-Client-IP` is read only on `:8081`, as the rate-limit key. On `:8080` the key is always `RemoteAddr`. `X-Forwarded-For` is never trusted. If `APP_BASE_URL` is set, the server refuses to start unless the public listener (`:8081`) is enabled. There is no fail-open path. A server without `APP_BASE_URL` needs no `:8081` listener, so dev and test servers can run `:8080` only, and the refusal to start applies only when `APP_BASE_URL` is set. On a hairpin or userland-proxy path, the LAN `RemoteAddr` can be the Docker bridge gateway, which would make the 10-streams-per-IP LAN cap effectively household-wide. That is accepted for stage 1.",
		"On the LAN: the LAN principal while the household password is on.",
		"on the LAN after retirement, unauthorized even with no cookie or bearer; Bearer sbs_ on the LAN is always unauthorized.",
		"`unauthorized`, `validation`, `rate_limited`, `busy`. On the LAN, `404` `not_found`",
		"`validation`, `reset_expired`, `not_found`, `rate_limited`, `busy`. On the LAN, `404` `not_found`",
		"The first setup creates the operator as owner of household 1 and sets no session.",
		"Recover resets the existing operator's password. It never creates an account, never changes the email, and never changes roles.",
		"The POST body is `{ \"setupCode\": string, \"email\": string, \"newPassword\": string }`.",
		"An unknown or non-operator email gets the same `404` `not_found` as a bad code, so the endpoint never reveals which emails exist.",
		"set the new argon2id hash; revoke all of that account's sessions (web and Expo `sbs_`); delete the push subscriptions tied to those sessions and close their SSE streams; invalidate any outstanding reset tokens for that account; set the code row's `used` to true.",
		"Device tokens (`sbd_`) and the household's other members are untouched.",
		"No session is set. A successful POST sends the user to sign-in at `APP_BASE_URL`.",
		"`POST /setup`, in both first-setup and recover mode, uses the same per-IP limit as `POST /api/auth/login`, keyed on `RemoteAddr` on `:8080`, sharing that route's per-IP bucket of 5 failures per 15 minutes. The 6th failing `POST /setup` from one `RemoteAddr` on `:8080` within 15 minutes gets `429` `rate_limited` with `Retry-After`. The rate limit is checked before the code, so a limited request gets `429` and doesn't count again. A failed attempt is any `POST /setup` with a well-formed body that doesn't succeed: a bad or expired code, or a right code with an unknown or non-operator email. All return the same `404` `not_found` and count toward the limit. A malformed body gets `400` `validation` and doesn't count. The code is used only on success. The recover code also expires after 24 h. Recover mode and GET /setup behave exactly as the `/setup` sentence says. `POST /setup` failures count only toward the per-IP bucket, not login's per-email bucket.",
		"The Pi user can read it without sudo, and only the server writes or deletes it.",
		"`/opt/pantry/setup/`",
		"mode `2750` (setgid)",
		"bind-mounted read-write at `/run/setup`",
		"The issue order is: write the temp file inside `/run/setup` and chmod it to `0640` explicitly, then `BEGIN IMMEDIATE`, insert the row (marking any row it replaces as used), rename the temp file to `code`, and `COMMIT`.",
		"The rename happens inside the transaction, before `COMMIT`, so a concurrent check can't see a committed row before its file exists",
		"If the temp-file write fails, no row is committed.",
		"deletes the temp file",
		"Only an unused, unexpired code whose hash matches is accepted.",
		"Deleting the file is best-effort after that commit and can never make a code reusable.",
		"At startup, the server deletes the file only if no unused, unexpired row exists.",
		"`issued_at` is RFC 3339 UTC.",
		"A successful `POST /setup` sets `used` to true in the same transaction as the account or password write.",
		"Only automatic issuing reuses an unused, unexpired code whose display file is present and matches.",
		"An `issued_at` more than 24 h in the future counts as expired.",
		"A code is expired when `now - issued_at >= 24 h`.",
		"If the clock reads earlier than `issued_at` by less than 24 h (the Pi booted before NTP synced), the code counts as valid and is not reissued.",
		"A clock jump forward can at worst expire it early, which just means a new code is issued.",
		"The \"no account exists\" check runs inside the account-creating transaction, so racing first-setup POSTs cannot create two operators.",
		"That `404` counts toward the per-IP failure bucket, the same as any `/setup` failure.",
		"`docker exec pantry stockbot-server admin setup-code` with no flag issues a fresh setup-mode code when no account exists, marking any unused code as used.",
		"`docker exec pantry stockbot-server admin setup-code --recover` requires an existing operator and issues a recover-mode code the same way.",
		"reused and never replaced",
		"While no account exists, a missing `code` file or one that doesn't match the unused, unexpired row makes the server mark that row used and issue a fresh code by that issue order.",
		"Recover-mode rows never reissue automatically, so a lost recover file needs another `--recover`.",
		"`code_hash` is argon2id with the account-password parameters, not SHA-256 and not HMAC.",
		"The no-flag `admin setup-code` with an existing account exits nonzero and changes nothing.",
		"On `SQLITE_BUSY` it exits nonzero without touching the file.",
		"A full disk shows `setupCode` `\"no_space\"`, not `\"unwritable\"`.",
		"If `/setup` hits `busy` or `no_space`, it returns the existing `busy` or `no_space` codes, which don't count toward the per-IP bucket.",
		"If `COMMIT` fails after the rename, setup mode self-heals, and the recover-mode admin command exits nonzero so the operator reruns it.",
		"Serialization is in the database (`BEGIN IMMEDIATE`), so it also covers the `docker exec` process.",
		"https://github.com/Rhionin/stockbot/pull/397#issuecomment-6375160842",
		"https://github.com/Rhionin/stockbot/pull/397#issuecomment-6375169537",
		"https://github.com/Rhionin/stockbot/pull/397#issuecomment-6374601821",
		"Recover mode is the stored mode.",
		"This pair is the manual reissue path.",
		"`/health` carries a warning field `setupCode` `\"unwritable\"` only while no account exists.",
		"`docker exec pantry stockbot-server admin setup-code --recover`",
		"https://github.com/Rhionin/stockbot/pull/394#issuecomment-6374556040",
		"Limits: 5 failures per 15 minutes per client IP, and 10 failures per 15 minutes per normalised email. Over the limit is `429` `rate_limited` with `Retry-After`. The body is still the registry sentence and does not say which field was wrong. `POST /setup` failures count only toward the per-IP bucket, not login's per-email bucket.",
		"When `APP_BASE_URL` is unset, a successful setup or recover POST returns success with no redirect, and the page says the account is ready but sign-in isn't set up on this server (placeholder copy owned by PM), and on the LAN after the first account exists, outside recover mode, GET /setup is `not_found`.",
		"When `appBaseUrl` is absent (an older server), Expo only signs in against an https: saved origin and never sends credentials to an http: origin.",
		"Every `rate_limited` response sends `Retry-After` in seconds, `POST /setup` included.",
		"The code is single-use with a 24 h expiry, and the setup rate limits defined above apply. An expired code returns `not_found`.",
		"pairing complete, stage-1 `POST /api/households`, and `POST /setup`.",
		"`POST /setup` (first setup and recover) uses the same per-IP limit as login, keyed on `RemoteAddr` on `:8080`.",
		"(f) The 6th failing `POST /setup` from one `RemoteAddr` on `:8080` within 15 minutes gets `429` `rate_limited` with `Retry-After`, sharing `POST /api/auth/login`'s per-IP bucket (5 failures per 15 minutes). The rate limit is checked before the code, so a limited request gets `429` and doesn't count again.",
		"The server's listener change and both Caddyfile changes (every handle to `pantry:8081`, `X-Pantry-Entry` dropped) must ship in the same release. If the server stops reading `X-Pantry-Entry` while Caddy still targets `:8080`, all internet traffic is LAN, and the startup check can't catch it.",
		"That same release updates every current `X-Pantry-Entry` user: `deploy/Caddyfile`, `deploy/Caddyfile.tunnel`, `deploy/setup.sh` (it probes with the header, around lines 546 and 554), `check-publish-modes.sh` (around lines 256–257), `setup_apply_test.sh` (around lines 313 and 766), `deploy/README.md` (around line 16), `cmd/server/README.md` (around line 172), `cmd/server/public_compose_test.go` (around line 111 it asserts `deploy/Caddyfile` contains `X-Pantry-Entry public` and `reverse_proxy pantry:8080`, and around line 321 the same header in the tunnel file; this test must change in the same release as the Caddyfile change, or CI fails), `cmd/server/main.go` (around line 386), `internal/server/server.go` (around line 295), `internal/server/handler_sync_test.go` (around line 77), `internal/server/handler_push_test.go` (around lines 293, 301, 316, and 331), `internal/server/sentry_test.go` (around line 720), `internal/server/sentry_tunnel_test.go` (around lines 671, 681, 691, 701, and 743), `docs/design/multi-household-staged.md`, and `docs/design/multi-tenant-saas.md`.",
		"During rollout, a `:8080` request carrying `X-Pantry-Entry` is ignored, never refused, and the server logs it at info.",
		"(g) Both `deploy/Caddyfile` and `deploy/Caddyfile.tunnel` point every handle at `pantry:8081`, and neither contains `pantry:8080`.",
		"It is an infra deploy step owned by the network engineer, paired with S15/S5.",
		"(a) On `:8080`, `X-Pantry-Entry: public` on `POST /api/auth/login` is ignored, so `404` `not_found`, and `GET /api/config` returns `entry` `\"lan\"`.",
		"(b) On `:8080`, 11 `POST /api/devices/pair/complete` attempts, each with a different `X-Pantry-Client-IP` and `X-Forwarded-For`, are keyed on `RemoteAddr`, so the attempt over the limit gets `429` `rate_limited`.",
		"(c) On `:8081`, `X-Pantry-Client-IP` sets the key and `X-Forwarded-For` is ignored.",
		"(d) On `:8081`, a request with no `X-Pantry-Entry` (`GET /api/telemetry`, `POST /api/telemetry/client`, and `GET /api/build`) is public.",
		"(e) Startup with `APP_BASE_URL` set and the public listener disabled fails.",
		"On routers without NAT hairpin, the certificate (non-tunnel) path can hang from home Wi-Fi (see the comments in `deploy/Caddyfile`).",
		"The client-facing type marks `entry` and `appBaseUrl` optional, because an older server omits them and the client falls back. The Go server always sends `entry`. It sends `appBaseUrl` only when `APP_BASE_URL` is set and omits the field otherwise (never an empty string), so a LAN-only server follows the absent-`appBaseUrl` rule.",
		"`appBaseUrl` is omitted when `APP_BASE_URL` is unset.",
		"The hosted push arm types `entry` as `'public'`.",
		"Hosted `LiveConfig` sends `entry` `\"public\"` only.",
		"clientmsg.Generic",
		"Required in stage 3 only. The Pi takes the household from the job row.",
		"Trim only the ASCII whitespace characters space, tab, LF, VT, FF, and CR.",
		"typed `*`, `~`, `$`, and `=` are allowed",
		"The LAN listener never offers personal sign-in, whether or not the household password is on.",
		"invite accept with `account`, and `POST /api/households` return `404` `not_found`",
		"`Authorization: Bearer sbs_…` on `:8080` returns `401` `unauthorized` and is never honored.",
		"Session cookies are never set and never honored on the LAN.",
		"lan-client: Expo must use `appBaseUrl` for personal sign-in. LAN URLs are unsupported for people after retirement. Clients never show Sign in on a LAN URL.",
		`"appBaseUrl"?: string`,
		"Both entries omit it when `APP_BASE_URL` is unset.",
		"omitting `appBaseUrl` when `APP_BASE_URL` is unset.",
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
		"Dev builds that sign in must set `APP_BASE_URL`, and servers without it run LAN only.",
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
	for _, phrase := range []string{
		"appBaseUrl?: string",
		"entry?: 'lan' | 'public'",
		"entry?: 'public'",
	} {
		if !strings.Contains(ts, phrase) {
			t.Fatalf("contract.ts missing %q", phrase)
		}
	}
	if strings.Contains(ts, "appBaseUrl: string") {
		t.Fatal("client LiveConfig still requires appBaseUrl")
	}
	for _, gone := range []string{
		"On the LAN it stays open, with no session.",
		"Setup sets it when the person has exactly one household",
		"signs them in, and sets `activeHouseholdId`",
		"`POST /api/invites/accept`, and `POST /setup`",
		"Sign-in is required",
		"On success, and on a GET after the first account exists",
		"differs from `appBaseUrl`",
		"one matching no public hostname",
		"public is only the `X-Pantry-Entry` header",
		"the setup rate limits apply",
		"The Go server always sends both fields.",
		"may refuse",
		"each server start writes",
		"updater writes",
		"treated as consumed",
		"a valid code always exists",
		"mutex",
		"the only way to reissue",
		"The code file is consumed only on success",
		"Nothing else changes it",
		"The hash is SHA-256 of the random code",
		"Anyone who can delete files in `/opt/pantry/setup` already controls the Pi",
		"If the commit fails, the server deletes the temp file",
		"by 24 h or less",
		"now minus `issued_at` over 24 h",
		"commit the database row, then rename",
		"under 24 h old whose hash matches",
		"no longer read",
		"Both entries return it.",
		`"appBaseUrl": string`,
		"from this classification",
		"trusted proxy",
		"trusted-proxy",
		"Docker bridge subnet",
		"acceptable alternative",
		"separate upstream port",
		"Today LAN and public share `:8080`",
		"The port binding is owned by infra.",
		"invalidated",
		"fresh code from the updater",
		"fresh one is issued",
		"removed entirely",
		"Dev and test servers must set `APP_BASE_URL`, or dev builds would point at prod.",
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
