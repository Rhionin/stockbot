# Hosted API contract

Status: stage-1 amendment (S1) for review. Issue [#374](https://github.com/Rhionin/stockbot/issues/374), which absorbs [#338](https://github.com/Rhionin/stockbot/issues/338).

This is the cross-stack contract for hosted, multi-household Stockbot. It covers the endpoints in design §6, the push rules in design §3.1, and the export document in design §7. It is docs and TypeScript types only. It does not change the running server.

S1 amends this document for stage 1 on the Pi. It does not replace it. Invites, devices, device scans, the error registry, and export version 1 keep the shapes below. Auth routes gain local email and password sign-in. Fields added for stage 1 are called out in place. Stage 3 material stays: Cognito login and callback, SES, AppSync, presigned import upload, DSQL identity columns, and optimistic-concurrency retries. Pi-only additions say what stage 3 returns.

Source of truth: [`docs/design/multi-tenant-saas.md`](multi-tenant-saas.md) at `d5cc44a` on `cursor/multi-tenant-saas-design-c887` (PR #224). That commit records CJ's decision: Aurora DSQL, and AppSync Events push on every visible screen, with the 2 s / 10 s polling kept only while the client is disconnected.

The copies attached with the T2 request are older than that commit. The uploaded design still recommends polling and has no decision log. The uploaded §3.1 still says the push choice is pending. This contract follows the branch. Rules PM confirmed are in [Decided by PM](#decided-by-pm). Rules the Architect decided on review of this contract are in [Decided by Architect](#decided-by-architect). What is still open is under [Open questions](#open-questions).

Issue #338 matches the T2 scope and acceptance criteria in the ticket list. There is no scope difference to resolve.

Stage 3 push is AppSync. The Pi keeps SQLite and the SSE stream `GET /api/events`. Stage 1 on that Pi uses the local auth in [Stage 1 auth](#stage-1-auth). `pantry_session` exists only during household 1's changeover, then it is retired. `GET /api/config` tells each client which transport to use, and it carries `minClientVersion.expo`.

JSON field names are camelCase, matching the API the web client already calls. Design prose uses snake_case for the same ideas (`household_id`, `channel_key`). PM confirmed camelCase in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445).

## Tenancy

tenant-rule: Nothing in this contract accepts a client-supplied tenant other than the X-Household-Id header, and that header is valid only when it names a household the signed-in user belongs to, or, for device calls, the household bound to the device token, except the householdId body on POST /api/auth/session/household, which sets the session's active household after a membership check; nothing else selects a tenant.

The signed-in user comes from the server session. Web sends the BFF cookie. Stage 3 Expo sends `Authorization: Bearer` with the Cognito access token, and that client still sends `X-Household-Id` on each data request. Stage 1 Expo sends `Authorization: Bearer sbs_…`, an opaque session token. A paired device sends its own bearer token, and the household is the one stored for that device. The client does not send a household id for a device call. A device route reads that token's household. If the call also sends `X-Household-Id` and the header names a different household, the response is `404` `not_found`.

`X-Household-Id` is optional on session routes. When it is omitted, the session's `activeHouseholdId` is the household for that request. At login and at reset, `activeHouseholdId` is the last-used household if the person is still a member, else their only membership, else null. Invite accept sets it to the household on the invite. After the person is removed, only that person's sessions whose active household was the removed one have `activeHouseholdId` set to null, and a request that still sends the old header is `404` `not_found`. The header accepts any household the person belongs to, including one that differs from the stored id. The stored id is only the default. A route table that marks `X-Household-Id` required means the call needs a household. The header may be omitted when `activeHouseholdId` is set. No header and no active household is `400` `validation`. A household the caller does not belong to, or an unknown household, is `404` `not_found`. `GET /api/events` and a same-origin export download use the session and do not require the header. The download's household is the one stored on the export job.

activeHouseholdId order, for a personal session: a present `X-Household-Id` is the household for that request, and it must be one of the caller's memberships. If the header is omitted, the server uses the `activeHouseholdId` stored on the session, which must still be a membership. The header wins when both are present. Sending `X-Household-Id` does not change the stored `activeHouseholdId`. The next request that omits the header uses the stored id again. The stored id is not a client-supplied tenant on the data request. `POST /api/auth/session/household` is what writes it, and its `householdId` body is the one exception to the tenant rule. A header that is not a membership is `404` `not_found`. A missing header with no stored `activeHouseholdId` is `400` `validation`.

`userId` is removed from every hosted request: query, body, and path, except the path segment on the member routes below, which names the member being changed and is not a tenant. userId-removed: If a client still sends userId, the server ignores it and does not treat it as the tenant.

These are not tenants, and the server does not select a household from them:

- a `householdId` field in a body, including an imported file, other than `POST /api/auth/session/household`
- the `{id}` of a household path, unless it equals the household selected for this request (the validated header, or the session's `activeHouseholdId` when the header is omitted)
- the `{userId}` of a member path
- `device_id` in a scan body (the device id comes from the device token)
- the `household_id` claim inside a live ticket (the server minted it; the client does not present it to `/api`)

A missing `X-Household-Id` is `400` `validation` only when the session has no `activeHouseholdId`. An unknown id, a household the caller does not belong to, or a path `{id}` that does not equal the household selected for this request is `404` `not_found`. Cross-household access is `not_found`, never `forbidden_role` and never `unauthorized`, so the response does not reveal that the other household exists (design §4).

`forbidden_role` is only for a caller who is a member of the named household and whose role cannot perform the action.

### Soft-deleted households

Inside the 30-day restore window, GET, restore, and export still work. That is `GET /api/households`, `GET /api/households/{id}`, `POST /api/households/{id}/restore`, `POST /api/export`, and `GET /api/export/{id}`. `GET /api/auth/session` still lists the membership so the owner can find it.

Every other route returns `not_found`. A device token bound to a soft-deleted household returns `unauthorized` instead.

Proposed: restore does not revive device tokens. The owner re-pairs each device after restore.

## Auth and roles

| Caller | How they authenticate | Household |
|---|---|---|
| Web | HttpOnly, Secure, SameSite=Lax session cookie. The browser does not hold a Cognito token. | `X-Household-Id` |
| Expo, stage 1 | `Authorization: Bearer sbs_…` opaque session token, from `POST /api/auth/login`. Tokens stay in the device secure store. | Same as web |
| Expo, stage 3 | `Authorization: Bearer` Cognito access token, from the system browser PKCE flow. Tokens stay in the device secure store. | `X-Household-Id` |
| Device | `Authorization: Bearer sbd_…` device token, returned once at pairing and stored hashed. | Bound to the token. One household per device. |
| Household password | Only during household 1's changeover. `{ "householdPassword": string }` on login, or the existing `pantry_session` cookie. Member of household 1. Ignores `X-Household-Id`. | Household 1 |

Cookie auth and the `Origin` check apply to the public entry. Web cookie calls that change state also require an `Origin` equal to `APP_BASE_URL` (design §5). Bearer calls do not use that cookie. `APP_BASE_URL` is per-stage config. The server reads the variable. On the Pi the default is `https://stockbot.rhionin.com`. A rename is a config change. Invite and reset links use that value. Stage 3 production is `https://app.stockbot.rhionin.com`.

Session cookie, decided by CJ on 2026-10-23 (§11.3): the name is `__Host-stockbot_session` (`SESSION_COOKIE_NAME`). Attributes are `HttpOnly; Secure; SameSite=Lax; Path=/`. It is host-only, so it has no `Domain`. Expo does not use the cookie. Expo sends `Authorization: Bearer sbs_…`. The token is 32 random bytes, base64url, prefixed `sbs_`. Only the SHA-256 is stored. Lifetime is a 30-day sliding window and a 180-day absolute cap. At the cap the response is `unauthorized`. Logout revokes this session. Password reset revokes all of that user's sessions, then signs them in. Removing a member ends that member's access on the next request. Only that person's sessions whose active household was the removed one have `activeHouseholdId` set to null, and the old header is `not_found`. The client shows the switcher when `activeHouseholdId` is null. `pantry_session` is the household-password cookie during the changeover only. Retirement replaces its signing key, so every `pantry_session` fails at once. Personal sessions do not use that key.

Roles, from design §5:

| Role | Can |
|---|---|
| member | Scan, confirm, edit, manage the shopping list, create and revoke invites, list members, list devices. |
| owner | Everything a member can do, plus change roles, remove members, revoke devices, export, import, and delete or restore the household. |

A household has at least one owner. The check runs in the same transaction as the demotion, removal, or account deletion. On the Pi that transaction is `BEGIN IMMEDIATE`. SQLite has no `FOR UPDATE`. Stage 3 (DSQL) locks the owner rows with `SELECT ... FOR UPDATE`. A write that would leave zero owners returns `last_owner`. A soft-deleted household does not count for `last_owner` on `DELETE /api/me`. If the serialization-error retry budget is exhausted first, the response is `busy`. T13 adds a concurrent cross-demotion test for this lock.

The server retries a transaction that fails with a serialization error, from the start, inside a bounded attempt budget. Calls that leave the database run only after that transaction has committed: the SES invite email, Cognito `AdminUserGlobalSignOut` on logout, Cognito `AdminDeleteUser` on account deletion, and EventBridge Scheduler create. `DELETE /api/me` commits the database step first, then calls `AdminDeleteUser` from a job that retries on its own. A failure of that job does not restore the memberships.

Cognito, in stage 3, owns hosted sign-up, email verification, and Cognito's own forgot-password. Stage 1 does take passwords, on the routes in [Stage 1 auth](#stage-1-auth). A stage-3 session exists only after the email is verified. The post-confirmation trigger creates the `users` row. The household is created by `POST /api/households` after that. Stage 1 uses the same route and requires a `start_household` `inviteCode`. Later stages may omit that code without a new route.

### Stage 1 auth

CJ decided on 2026-10-23 that everyone, household 1 included, uses a personal email and password. On the Pi this is the local identity provider. Sessions are opaque and stored on this server, so the same user id can link to a Cognito `sub` in stage 3. The shared household password signs in to household 1 as member only while it is on. An owner retires it with `POST /api/household/household-password/retire`. It can be restored for 7 days, then the hash is deleted. Device tokens do not depend on a person's session, password, or `pantry_session`.

The version gate runs before auth on every `/api` route except `GET /api/config`, `GET /api/build`, and the telemetry routes (`GET /api/telemetry`, `POST /api/telemetry/client`). `GET /api/config` and `GET /api/build` never return `426`. Telemetry stays exempt from the gate. `GET /api/config` includes `minClientVersion.expo`. The header name is `X-Stockbot-Client` (`CLIENT_HEADER`). An Expo request sends `expo/<semver>` (`EXPO_CLIENT_PREFIX`), where `<semver>` is `expo.version` from `app.json`. Today's value is `1.0.0`, three numeric parts. The server compares that dotted number numerically, part by part. A missing part compares as 0, so `1.0` equals `1.0.0`. A pre-release suffix is ignored. A new Expo build sends `X-Stockbot-Client` on every request, including `GET /api/events`. Below the minimum, or a value that starts with `expo/` and is not a semver, the response is `426` `upgrade_required`. The body is `{"error":"Update Stockbot to keep using it.","code":"upgrade_required"}`. A missing header means the client is not Expo, so there is no gate. `web/<sha>` is web and is not gated. Any `X-Stockbot-Client` value that does not start with `expo/` is web too, the same as a missing header, so there is no version gate. The `426` `upgrade_required` gate runs before auth, rate limits, and busy handling, including when nobody is signed in. A request that fails the gate is `426` and is not counted as a login attempt.

Both of these rules apply:

1. A missing `X-Stockbot-Client` header means web, so there is no version gate.
2. The exception: once the household password is retired, a request with `Authorization: Basic` and no `X-Stockbot-Client` returns `426` `upgrade_required`, not `401`. The same `426` applies when that Basic request carries a value that does not start with `expo/`. A value that starts with `expo/` uses the version gate, not this exception. Old Expo builds then show the update sentence and do not loop on sign-in. While the household password is still on, that Basic request with no header signs in as a member of household 1.

Pi-only additions, outside the stage-3 AWS behavior: `householdPasswordEnabled` and `householdPasswordRestoreUntil` on `GET /api/household/settings`, and `POST /api/household/household-password/retire` and `POST /api/household/household-password/restore`. Stage 3 returns `householdPasswordEnabled: false` and `not_found` for those two routes. Households other than household 1 also return `householdPasswordEnabled: false` and `not_found` for the routes, including in stage 1. The settings body never includes the password or its hash.

LAN `:8080`, while the household password is on: people get the LAN principal. That is household 1, member, no sign-in, and `X-Household-Id` is ignored, so the LAN cannot return another household's data. `GET /api/auth/session` on that listener returns this principal: `kind` `lan`, `user` null, `operator` false, `activeHouseholdId` household 1, and one membership with role `member`. While the household password is on, the LAN principal stays household 1, member, as §3.7 says. The LAN listener never offers personal sign-in, whether or not the household password is on. On `:8080`, `POST /api/auth/login`, `POST /api/auth/reset`, invite accept with `account`, and `POST /api/households` return `404` `not_found`. Those routes do not exist on the LAN. `Authorization: Bearer sbs_…` on `:8080` returns `401` `unauthorized` and is never honored. There is no LAN cookie in stage 1. Session cookies are never set and never honored on the LAN. A plain-HTTP LAN origin cannot hold `__Host-stockbot_session`. `LAN_PEOPLE=signin` is a documented future option and is not stage 1. Owner and operator routes need a personal session on the public entry. Device routes use device tokens. After the password is retired, a browser document GET on the LAN is `302` to `APP_BASE_URL`, and other people-facing `/api/*` on the LAN return `401`. These keep working on the LAN: `/api/device/*`, `POST /api/devices/pair/complete` (the code is the credential, and its rate limits still apply; hubs pair against `http://pi:8080`), `GET /api/build`, `GET /api/config` (a LAN-URL phone learns `appBaseUrl` here), `GET /health`, and `POST /api/telemetry/client`. `GET /api/telemetry` follows the `401` rule after retirement. `GET /api/auth/session` on the LAN after retirement returns `401` `unauthorized`, under the same rule. The four routes above stay `404` `not_found`. LAN URLs are unsupported for people after retirement.

The pantry server opens two listeners. `:8080` is the LAN entry, published to the host as today. `:8081` is the public entry. It is not published to the host and is reachable only on the Docker network, where the reverse proxy forwards to `pantry:8081`. Everything on `:8081` is public. Everything on `:8080` is LAN. Stage 1 removes `X-Pantry-Entry`: it is never honored anywhere. `X-Pantry-Client-IP` is read only on `:8081`, as the rate-limit key. On `:8080` the key is always `RemoteAddr`. `X-Forwarded-For` is never trusted. If `APP_BASE_URL` is set, the server refuses to start unless the public listener (`:8081`) is enabled. There is no fail-open path. A server without `APP_BASE_URL` needs no `:8081` listener, so dev and test servers can run `:8080` only, and the refusal to start applies only when `APP_BASE_URL` is set. On a hairpin or userland-proxy path, the LAN `RemoteAddr` can be the Docker bridge gateway, which would make the 10-streams-per-IP LAN cap effectively household-wide. That is accepted for stage 1.

lan-client: Expo must use `appBaseUrl` for personal sign-in. LAN URLs are unsupported for people after retirement. Clients never show Sign in on a LAN URL. While the household password is on, Expo on a LAN URL may keep using the shared household-1 access, and should offer to move to `appBaseUrl`. After retirement, the LAN `401` means moving to `appBaseUrl`. `GET /api/auth/session` on the LAN after retirement returns `401` `unauthorized`, under the same rule, and this rule then sends the client to `appBaseUrl`.

LAN detection: clients fetch `entry` from `GET /api/config` first. `GET /api/config` returns `entry` `"lan"` or `"public"`, set by the listener that served the request. Clients use `entry` as the exact signal. The saved-origin fallback applies only when `GET /api/config` omits `entry` (an older server). If `appBaseUrl` is present, a saved origin is LAN when it is http: or its host is not `appBaseUrl`'s host. If `appBaseUrl` is also absent, an http: origin is LAN and an https: origin is public. When `appBaseUrl` is absent (an older server), Expo only signs in against an https: saved origin and never sends credentials to an http: origin. Expo sends sign-in and its `sbs_` token only to `appBaseUrl`, never to a LAN origin. Dev builds that sign in must set `APP_BASE_URL`, and servers without it run LAN only.

`GET /api/telemetry` is operator-only on the public entry. Nothing public may reveal household activity, including process-wide counts. On that entry, a caller who is not the operator, including a household-password or device principal, gets `not_found`. On the LAN, `GET /api/telemetry` is open only while the household password is on. After retirement it follows the LAN `401` rule, and the operator reads it on the public entry. The LAN response never includes the changeover counts. PM's ruling is the public entry only. `POST /api/telemetry/client` stays a public write on both entries and stays open on the LAN. Its response has no household activity and no process-wide counts. The changeover counts (household-password sessions and Basic requests in the last 7 days) are part of the operator-only GET on the public entry. `GET /api/config` and `GET /health` stay open on the LAN. Both telemetry routes stay exempt from the version gate.

These routes do not select a household from the client. A row that names a session still does not take `X-Household-Id`. None of them return a household id, name, item, or count, except `GET /api/telemetry` on the public entry, which is operator-only. On the LAN, `GET /api/telemetry` is open only while the household password is on, and that response never includes the changeover counts.

| Path | Who | What |
|---|---|---|
| `GET /api/build` | No session | `{ "commit": string, "committedAt"?: string, "builtAt"?: string, "version": string, "subject"?: string }`. Stays available on the LAN after retirement. Never `426`. |
| `GET /api/telemetry` | Operator on the public entry. Open on the LAN while the household password is on | On the public entry, process-wide counts for the operator. Anyone else on that entry gets `not_found`. On the LAN, `GET /api/telemetry` is open only while the household password is on. After retirement it follows the LAN `401` rule. The LAN response never includes the changeover counts. Exempt from the version gate. |
| `POST /api/telemetry/client` | No session | Public write. Stays open on the LAN. The response has no household activity and no process-wide counts. Exempt from the version gate. |
| `POST /api/sentry-tunnel` | No session | Forwards an allowlisted client envelope. The response has no household data. |
| `POST /api/deploy-hook` | No session | Accepted only when the HMAC signature and timestamp check. The response has no household data. |
| `GET /brand/logo.png` | No session | The brand mark. |
| `GET /terms` | No session | The terms page. |
| `GET /privacy` | No session | The privacy page. |
| `GET /health` | No session on the LAN. A session on the public entry | `{"status":"ok"}` and an optional `scanner` object. While no account exists and the setup code file is unwritable, the body also has `setupCode` `"unwritable"`. A full disk sets `setupCode` `"no_space"`, not `"unwritable"`. No household id, name, or item count. It stays open on the LAN after retirement. It is not on the public no-password list. |
| `GET /api/sync/snapshot` | Sync token | Pi only. Not a hosted route. Household 1, read-only. |
| `GET /setup` and `POST /setup` | No session | The setup code is 40 bits. On the LAN, while no personal account exists, the page asks for that code, then name, email, and password. The first setup creates the operator as owner of household 1 and sets no session. On the public entry, GET and POST return `not_found`. On the LAN, after the first account exists, GET sends the user to sign-in at `APP_BASE_URL` and POST returns `not_found`, except in recover mode, where GET serves the page and a successful POST sends the user to sign-in at `APP_BASE_URL`. When `APP_BASE_URL` is unset, a successful setup or recover POST returns success with no redirect, and the page says the account is ready but sign-in isn't set up on this server (placeholder copy owned by PM), and on the LAN after the first account exists, outside recover mode, GET /setup is `not_found`. |
| `GET /api/config` | No session | `LiveConfig`, including `appBaseUrl` and `entry` (`"lan"` or `"public"`). `appBaseUrl` is omitted when `APP_BASE_URL` is unset. Both entries. Stays open on the LAN after retirement, so a LAN-URL phone can learn `appBaseUrl`. Never `426`. |
| `POST /api/auth/login` | No session on the public entry | Email and password, or `{ "householdPassword" }` while that password is on. On the LAN this route is `404` `not_found`. |
| `GET /api/auth/session` | Optional session | No cookie and no bearer is a signed-out session. On the LAN, while the household password is on, this is the LAN principal. After retirement it returns `401` `unauthorized`, and the lan-client rule sends the client to `appBaseUrl`. `Bearer sbs_…` on the LAN is `401` `unauthorized`. |
| `POST /api/auth/logout` | Session on the public entry | Revokes that session and clears the cookie. On the LAN a session cookie is not honored, and `Bearer sbs_…` is `401` `unauthorized`. |
| `POST /api/auth/session/household` | Personal session on the public entry | Sets `activeHouseholdId`. The LAN has no personal session. |
| `POST /api/auth/reset` | No session on the public entry | The reset code is the credential. On the LAN this route is `404` `not_found`. |
| `GET /api/auth/login` and `GET /api/auth/callback` | No session | Stage 3 web PKCE. Stage 1 sign-in is `POST /api/auth/login` on the public entry. |
| `POST /api/invites/preview` | No session | `{ "code" }`. The body has no household id. |
| `POST /api/invites/accept` | No session when `account` is sent. Otherwise a session on the public entry | On the LAN, accept with `account` is `404` `not_found`. |
| `POST /api/devices/pair/complete` | No session. The code is the credential | Returns the device token. Omit `X-Household-Id`. The body has no household id. Keeps working on the LAN, where hubs pair against `http://pi:8080`. Its rate limits still apply. |

`POST /setup`, in both first-setup and recover mode, uses the same per-IP limit as `POST /api/auth/login`, keyed on `RemoteAddr` on `:8080`, sharing that route's per-IP bucket of 5 failures per 15 minutes. The 6th failing `POST /setup` from one `RemoteAddr` on `:8080` within 15 minutes gets `429` `rate_limited` with `Retry-After`. The rate limit is checked before the code, so a limited request gets `429` and doesn't count again. A failed attempt is any `POST /setup` with a well-formed body that doesn't succeed: a bad or expired code, or a right code with an unknown or non-operator email. All return the same `404` `not_found` and count toward the limit. A malformed body gets `400` `validation` and doesn't count. The code is used only on success. The recover code also expires after 24 h. Recover mode and GET /setup behave exactly as the `/setup` sentence says. `POST /setup` failures count only toward the per-IP bucket, not login's per-email bucket.

The server owns the whole setup-code lifecycle. The updater has no role in setup codes. The governing ruling is [Architect's amended comment on this pull request](https://github.com/Rhionin/stockbot/pull/397#issuecomment-6375160842) together with [the addendum](https://github.com/Rhionin/stockbot/pull/397#issuecomment-6375169537). Earlier history is [the previous ruling](https://github.com/Rhionin/stockbot/pull/397#issuecomment-6374601821). The audit trail is [this comment](https://github.com/Rhionin/stockbot/pull/394#issuecomment-6374556040). The host directory `/opt/pantry/setup/` is owned by uid 65532, with its group set to the Pi user's group, mode `2750` (setgid). It is bind-mounted read-write at `/run/setup`. The file inherits the Pi user's group. The Pi user can read it without sudo, and only the server writes or deletes it. The file is only a display copy for the Pi user. `code_hash` is argon2id with the account-password parameters, not SHA-256 and not HMAC. A key in `/opt/pantry/setup` would sit next to the display copy, and argon2id makes offline guessing of a ~40-bit code impractical with no key to manage. Verification happens only on rate-limited `POST /setup`, at startup, and on the GET `/setup` self-heal check. The new migration goes to CJ through Stockbot Manager before it ships.

The issue order is: write the temp file inside `/run/setup` and chmod it to `0640` explicitly, then `BEGIN IMMEDIATE`, insert the row (marking any row it replaces as used), rename the temp file to `code`, and `COMMIT`. The rename happens inside the transaction, before `COMMIT`, so a concurrent check can't see a committed row before its file exists, and the rename stays on the same filesystem. The self-heal check runs at startup and on GET `/setup` under the same `BEGIN IMMEDIATE`. If `COMMIT` fails after the rename, setup mode self-heals, and the recover-mode admin command exits nonzero so the operator reruns it.

If the temp-file write fails, no row is committed. If the temp-file write or the rename fails, the server rolls back and deletes the temp file, `/setup` returns `404` `not_found`, and `/health` shows `setupCode` `"unwritable"`. The admin command exits nonzero. Once it is writable, the next check issues normally.

The database is the source of truth. The row is `code_hash`, `issued_at` (RFC 3339 UTC), the mode (`setup` or `recover`), and `used` `false`. Recover mode is the stored mode. `POST /setup` checks the submitted code against `code_hash`. Only an unused, unexpired code whose hash matches is accepted. A successful `POST /setup` sets `used` to true in the same transaction as the account or password write. Deleting the file is best-effort after that commit and can never make a code reusable.

Automatic issuing reuses a valid code, so a valid code is reused and never replaced, except for setup-mode self-heal. While no account exists, a missing `code` file or one that doesn't match the unused, unexpired row makes the server mark that row used and issue a fresh code by that issue order. The server checks this at startup and on GET `/setup`, under the same `BEGIN IMMEDIATE`. Recover-mode rows never reissue automatically, so a lost recover file needs another `--recover`. Only automatic issuing reuses an unused, unexpired code whose display file is present and matches.

Serialization is in the database (`BEGIN IMMEDIATE`), so it also covers the `docker exec` process. A request that finds an unused, unexpired row reuses it and never replaces it, when the display file is present and matches, so a code the Pi user just read stays valid. The "no account exists" check runs inside the account-creating transaction, so racing first-setup POSTs cannot create two operators.

At startup and on each `/setup` request, if no account exists and there is no unused, unexpired code with a matching display file, the server generates a fresh one, with the same alphabet and entropy as today, by that issue order. Expiry is checked at startup and on each `/setup` request, so any `/setup` request finds a valid code.

`issued_at` is RFC 3339 UTC. A code is expired when `now - issued_at >= 24 h`. An `issued_at` more than 24 h in the future counts as expired. If the clock reads earlier than `issued_at` by less than 24 h (the Pi booted before NTP synced), the code counts as valid and is not reissued. A clock jump forward can at worst expire it early, which just means a new code is issued.

The file holds the code, the issued-at timestamp, the mode (`setup` or `recover`), and the human instruction line from the staged plan: `Stockbot setup code: XXXX-XXXX (open /setup to finish setup; expires in 24 h)`. The code and the file contents are never logged. The server logs only that the code was written to the configured host path, plus the expiry.

At startup, the server deletes the file only if no unused, unexpired row exists.

`docker exec pantry stockbot-server admin setup-code` with no flag issues a fresh setup-mode code when no account exists, marking any unused code as used. The no-flag `admin setup-code` with an existing account exits nonzero and changes nothing. `docker exec pantry stockbot-server admin setup-code --recover` requires an existing operator and issues a recover-mode code the same way. Both are subcommands of the existing `/usr/local/bin/stockbot-server` binary, with no alias. This pair is the manual reissue path. They use the same issue order and the same `BEGIN IMMEDIATE` serialization. The admin command opens the database through the same `OpenSQLite` path (`busy_timeout`, immediate txlock). On `SQLITE_BUSY` it exits nonzero without touching the file.

If the server cannot write `code` (a missing mount or permissions), `/setup` returns `404` `not_found` as today, the server logs one error naming the path, and `/health` carries a warning field `setupCode` `"unwritable"` only while no account exists. That `404` counts toward the per-IP failure bucket, the same as any `/setup` failure. If `/setup` hits `busy` or `no_space`, it returns the existing `busy` or `no_space` codes, which don't count toward the per-IP bucket. A full disk shows `setupCode` `"no_space"`, not `"unwritable"`.

Recover resets the existing operator's password. It never creates an account, never changes the email, and never changes roles. The POST body is `{ "setupCode": string, "email": string, "newPassword": string }`. The email must belong to an account with the operator role. An unknown or non-operator email gets the same `404` `not_found` as a bad code, so the endpoint never reveals which emails exist. On success, in one transaction: set the new argon2id hash; revoke all of that account's sessions (web and Expo `sbs_`); delete the push subscriptions tied to those sessions and close their SSE streams; invalidate any outstanding reset tokens for that account; set the code row's `used` to true. Deleting the display file is best-effort after that commit and can never make the code reusable. Device tokens (`sbd_`) and the household's other members are untouched. No session is set. A successful POST sends the user to sign-in at `APP_BASE_URL`. The code is single-use with a 24 h expiry, and the setup rate limits defined above apply. An expired code returns `not_found`.

## Endpoint index

endpoint-count: 63

Paths the design names are unmarked. Paths added so Frontend can call a complete flow (login redirect, household list, pairing completion, export and import job polls, the presigned import upload, `GET /api/config`, and the stock and list changes-since reads) are part of this contract. PM confirmed the earlier extras in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445). The upload route is the Architect's import-size decision.

```text
endpoint-index
GET /api/auth/login
POST /api/auth/login
GET /api/auth/callback
POST /api/auth/logout
GET /api/auth/session
POST /api/auth/session/household
POST /api/auth/reset
POST /api/households
GET /api/household/settings
PATCH /api/household/settings
POST /api/household/household-password/retire
POST /api/household/household-password/restore
GET /api/households
GET /api/households/{id}
PATCH /api/households/{id}
DELETE /api/households/{id}
POST /api/households/{id}/restore
GET /api/households/{id}/members
PATCH /api/households/{id}/members/{userId}
DELETE /api/households/{id}/members/{userId}
POST /api/households/{id}/members/{userId}/reset-link
POST /api/invites
GET /api/invites
POST /api/invites/{id}/revoke
POST /api/invites/preview
POST /api/invites/accept
POST /api/operator/household-invites
GET /api/operator/households
POST /api/devices/pair
GET /api/devices/pair/{pairingId}
POST /api/devices/pair/complete
GET /api/devices
PATCH /api/devices/{id}
DELETE /api/devices/{id}
POST /api/device/scans
POST /api/device/heartbeat
POST /api/device/token/rotate
GET /api/device/firmware/{releaseId}
GET /api/operator/firmware
POST /api/operator/firmware/{id}/promote
POST /api/operator/firmware/{id}/yank
POST /api/operator/firmware/{id}/retry
POST /api/scans
GET /api/scans
PATCH /api/scans/{id}
POST /api/scans/{id}/commit
POST /api/scans/batch-commit
GET /api/inventory
GET /api/shopping-list
POST /api/export
GET /api/export/{id}
POST /api/household/import/uploads
POST /api/import
GET /api/import/{id}
DELETE /api/me
GET /api/events
POST /api/live/ticket
GET /api/config
GET /api/build
GET /api/telemetry
POST /api/telemetry/client
POST /api/sentry-tunnel
POST /api/deploy-hook
```

Routes the current server already serves, and that this contract does not redefine, keep today's JSON. On the hosted API they drop `userId`. The household is the validated `X-Household-Id` or, when that header is omitted, the session's `activeHouseholdId`. That includes history, restock, groups, providers, settings, and the shopping-list mutations. Their field list stays the one the handlers use today.

## Auth and session

### GET /api/auth/login

Stage 3. Web only. Design §6 names callback, logout, and session. This redirect is the step that starts that callback. Stage 1 sign-in is `POST /api/auth/login`.

| | |
|---|---|
| Auth | None |
| Role | None |
| X-Household-Id | Omit |
| Request | Query `returnTo`, a path that starts with `/` and does not start with `//` |
| Response | `302` to the Cognito managed-login authorize URL, with PKCE |
| Errors | `validation` when `returnTo` is not a same-origin path |

Expo does not call this. Expo runs PKCE in the system browser and stores the tokens itself.

### POST /api/auth/login

Stage 1. Email and password. The household-password form exists only while household 1's household password is on.

| | |
|---|---|
| Auth | None |
| Role | None |
| X-Household-Id | Omit |
| Request | `{ "email": string, "password": string }` or, during the changeover, `{ "householdPassword": string }` |
| Response | `200` `LoginResponse`. Web sets `__Host-stockbot_session`. Expo, when `X-Stockbot-Client` starts with `expo/`, also receives `token` (`sbs_…`). |
| Errors | `unauthorized`, `validation`, `rate_limited`, `busy`. On the LAN, `404` `not_found` |

Email is trimmed and compared case-insensitively. Password length is 10 through 256 bytes. Shorter or longer is `validation`. The client may show "Use at least 10 characters." on the field. An unknown email and a wrong password return the same `401` `unauthorized` body. The body does not say which one failed. expo-401: Expo clears its stored `sbs_` token on any `401`, except the sign-in routes `POST /api/auth/login`, `POST /api/auth/reset`, and `POST /api/invites/accept`. A `401` on those routes is a failed sign-in and does not clear the token. `{ "householdPassword" }` signs in as a member of household 1 with no user id. After the password is retired, that form is the same `401` `unauthorized` body. Sending both forms, or neither, is `validation`. At login, `activeHouseholdId` is the last-used household if the person is still a member, else their only membership, else null. Reset uses the same rule. A wait of more than 2 seconds for the argon2id semaphore is `busy`. Reset and invite accept use that same semaphore.

Limits: 5 failures per 15 minutes per client IP, and 10 failures per 15 minutes per normalised email. Over the limit is `429` `rate_limited` with `Retry-After`. The body is still the registry sentence and does not say which field was wrong. `POST /setup` failures count only toward the per-IP bucket, not login's per-email bucket.

### GET /api/auth/callback

Stage 3. Web only. The Lambda exchanges the Cognito code and sets the session cookie.

| | |
|---|---|
| Auth | None. The query carries `code` and `state` from Cognito. |
| Role | None |
| X-Household-Id | Omit |
| Request | Cognito's redirect query |
| Response | `302` to the `returnTo` path from login, with `Set-Cookie` |
| Errors | `unauthorized` when the code is missing, expired, or fails to exchange |

### POST /api/auth/logout

| | |
|---|---|
| Auth | Session (cookie or bearer) |
| Role | Signed in |
| X-Household-Id | Omit |
| Request | Empty body |
| Response | `204` empty. The web cookie is cleared. |
| Errors | `unauthorized` |

Stage 1 revokes this session row and clears the cookie. Stage 3, after that row is deleted and the transaction commits, calls Cognito `AdminUserGlobalSignOut` for that user, so a refresh token cannot mint a new session. That call is not inside the transaction. Expo deletes its secure-store tokens after this returns.

### GET /api/auth/session

| | |
|---|---|
| Auth | Optional. A missing cookie and a missing bearer is a signed-out session, not an error. On the LAN: the LAN principal while the household password is on. |
| Role | None |
| X-Household-Id | Omit |
| Request | None |
| Response | `200` `AuthSession` |
| Errors | `unauthorized` when a cookie or bearer is present and is expired, revoked, or unknown; on the LAN after retirement, unauthorized even with no cookie or bearer; Bearer sbs_ on the LAN is always unauthorized. |

```ts
interface AuthSession {
  user: { id: string; email: string; name: string } | null;
  kind: 'signed_out' | 'account';
  memberships: { householdId: string; name: string; role: 'owner' | 'member'; deletedAt: string | null }[];
  households: { id: string; name: string; role: 'owner' | 'member'; deletedAt: string | null }[];
  activeHouseholdId: string | null;
  operator: boolean;
}
```

`user` is null when nobody is signed in. Then `kind` is `signed_out`, `memberships` and `households` are empty, `activeHouseholdId` is null, and `operator` is false. An account session has `kind` `account`.

`memberships` is the #364 list. `households` is the same list with `id` in place of `householdId`, and it is the switcher list. Both include `deletedAt`, so the switcher can offer Restore while the household is soft-deleted and still inside the 30-day window. Neither list is a tenant the client can override. A later call follows the activeHouseholdId order above. `name` on `user` is the person's display name. `operator` is a flag, not a household role, and it does not grant another household's data.

### POST /api/auth/session/household

Sets the session's `activeHouseholdId`. The header on a later request may name any household the person belongs to. The stored id is the default when the header is omitted. After this returns, the client refetches every scope and closes and reopens `GET /api/events`.

| | |
|---|---|
| Auth | Session (a personal account, not a household-password session) |
| Role | Signed in |
| X-Household-Id | Omit |
| Request | `{ "householdId": string }` |
| Response | `200` `AuthSession` |
| Errors | `unauthorized`, `not_found` |

A household the user does not belong to, including one that does not exist, is `not_found`.

### POST /api/auth/reset

Sets a new password from a reset link. The link is single use and expires 24 hours after it was issued.

| | |
|---|---|
| Auth | None. The code is the credential. |
| Role | None |
| X-Household-Id | Omit |
| Request | `{ "code": string, "password": string }` |
| Response | `200` `LoginResponse`. This signs the person in and revokes their other sessions. Web sets `__Host-stockbot_session`. Expo, when `X-Stockbot-Client` starts with `expo/`, also receives `token` (`sbs_…`). |
| Errors | `validation`, `reset_expired`, `not_found`, `rate_limited`, `busy`. On the LAN, `404` `not_found` |

Password rules match login. A code past its expiry, or already used, is `reset_expired`, sentence "This reset link has expired or was already used. Ask for a new one." A revoked or unknown code is `not_found`. Invite links do not use `reset_expired`. Limits match invite accept: 10 per 15 minutes per IP, and 5 attempts per code. The server builds the link that created this code as `<APP_BASE_URL>/reset/<code>`. Caddy and the app redact that code from logs. The path is not a fragment.

## Households, members, and roles

### POST /api/households

Creates a household and makes the caller an owner. This is the step after email verification in the sign-up flow.

| | |
|---|---|
| Auth | Session |
| Role | Signed in |
| X-Household-Id | Omit |
| Request | `{ "name": string, "restockMode"?: "auto" \| "ask_restock" \| "ask_hold", "inviteCode"?: string }` |
| Response | `201` `{ "id": string, "name": string, "role": "owner" }` |
| Errors | `unauthorized`, `validation`, `invite_required`, `invite_expired`, `not_found`, `rate_limited` |

`name` is required, trimmed, and 1–80 characters. The 1–80 length is not in the design. `restockMode` is optional: `auto`, `ask_restock`, or `ask_hold`, the three values in [#296](https://github.com/Rhionin/stockbot/issues/296). Omitted means `auto`, which is today's behavior. Master has no route for that setting. `PUT /api/restock/household` sets a numeric household target. It is not this mode.

`inviteCode` is optional on the route so a later stage can make it optional without changing the path. In stage 1 it is required, and it must be an open `start_household` invite. A missing `inviteCode` is `403` `invite_required`. An expired or already used code is `invite_expired`. A revoked or unknown code is `not_found`, the same split as invite accept. A code for joining an existing household is `not_found` on this route. The server consumes the code in the same transaction that creates the household and the owner membership. A failed create does not consume the code. When two requests use the same invite, the loser gets `invite_expired`. Invite-accept limits apply: 10 per 15 minutes per IP, and 5 attempts per code. Over the limit is `rate_limited`. When this household is the person's only one, the session's `activeHouseholdId` is set to it. household-start: A person who is already signed in starts another household here, with `inviteCode`. A person with no session starts one by `POST /api/invites/accept` with `account` and `household` on a `start_household` invite. A `join_household` invite is always that accept route. `household` is omitted on a join, and `account` is sent only when they have no account yet.

### GET /api/household/settings

### PATCH /api/household/settings

Proposed. These two routes are the household restock mode from #296. They supersede the earlier assumption that sign-up would call an existing restock route.

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required |
| GET response | `200` `HouseholdSettings` |
| PATCH request | `{ "restockMode"?: "auto" \| "ask_restock" \| "ask_hold", "fwAutoUpdate"?: boolean }` |
| PATCH response | `200` `HouseholdSettings` |
| Errors | `unauthorized`, `validation`, `not_found`, `forbidden_role`, `busy` |

```ts
interface HouseholdSettings {
  restockMode: 'auto' | 'ask_restock' | 'ask_hold';
  fwAutoUpdate: boolean; // default true; owner only to change
  householdPasswordEnabled: boolean;
  householdPasswordRestoreUntil?: string; // RFC3339, only while a retired password can be restored
}
```

`restockMode` is the #364 field. `householdPasswordEnabled` and `householdPasswordRestoreUntil` are the stage-1 additions. `householdPasswordEnabled` is true only while household 1's shared password can still sign in. After retire it is false, and `householdPasswordRestoreUntil` is set for 7 days. A household that never had a shared password, stage 3, and every household other than household 1 return `householdPasswordEnabled: false` and omit `householdPasswordRestoreUntil`.

password-switch: The household password switch returns only `householdPasswordEnabled` and, while the password can still be restored, `householdPasswordRestoreUntil`. It never returns the password or its hash. Only an owner can change the switch. A member gets `forbidden_role`. True means the shared password is on. False with `householdPasswordRestoreUntil` means it is retired and can still be restored. False without that timestamp means it is retired, or this household has no shared password.

PATCH accepts `restockMode` and `fwAutoUpdate` only. Only an owner can change `fwAutoUpdate`, and a member who sends it gets `forbidden_role` (see [Household firmware setting](#household-firmware-setting)). A body that includes `householdPasswordEnabled`, `householdPasswordRestoreUntil`, the password, or a hash is `validation`. Changing the switch is the retire and restore routes. Login's `{ "householdPassword": string }` is the sign-in secret for that request. It is not this field, and no response returns it.

### POST /api/household/household-password/retire

### POST /api/household/household-password/restore

Owner actions for household 1 during stage 1. Retire sets `householdPasswordEnabled` to false, replaces the session key, and revokes sessions that came from that password. In that same transaction it deletes household-password push subscriptions, the rows with `household_id = 1` and `user_id` null. After commit, `LiveHub.Kick` closes those SSE streams. Personal sessions stay. Restore, before `householdPasswordRestoreUntil`, sets `householdPasswordEnabled` back to true. The new session key stays, so old `pantry_session` cookies stay dead. A member of household 1 who is not an owner gets `forbidden_role`. The response is `HouseholdSettings` and does not include the password or its hash.

| | |
|---|---|
| Auth | Session (personal account) |
| Role | Owner of household 1 |
| X-Household-Id | Required, and it must be household 1 |
| Request | Empty body |
| Response | `200` `HouseholdSettings` |
| Errors | `unauthorized`, `forbidden_role`, `not_found`, `busy` |

Any other household, and every caller in stage 3, gets `not_found`. Restore after the 7 days is `not_found`.

### GET /api/households

Design §6 says create, get, rename, delete, and restore. The switcher (T20) needs the list. `GET /api/auth/session` already returns `memberships`. This route is the same list without the user object, so a household switcher does not depend on the session route's other fields.

| | |
|---|---|
| Auth | Session |
| Role | Signed in |
| X-Household-Id | Omit |
| Response | `200` `{ "households": AuthSession["memberships"] }` |
| Errors | `unauthorized` |

### GET /api/households/{id}

| | |
|---|---|
| Auth | Session |
| Role | Member of `{id}` |
| X-Household-Id | Required, and equal to `{id}` |
| Response | `200` `Household` |
| Errors | `unauthorized`, `validation`, `not_found` |

```ts
interface Household {
  id: string;
  name: string;
  role: 'owner' | 'member';
  deletedAt: string | null; // RFC3339 while soft-deleted, otherwise null
  purgeAt: string | null;   // deletedAt plus 30 days, otherwise null
}
```

### PATCH /api/households/{id}

Rename. Owner only. Decided by PM in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445).

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required, equal to `{id}` |
| Request | `{ "name": string }` |
| Response | `200` `Household` |
| Errors | `unauthorized`, `forbidden_owner`, `invalid_input`, `household_not_found`, `busy` |

A blank or whitespace-only name, or a name longer than 80 characters, is `invalid_input`. A member who is not an owner gets `forbidden_owner`. A caller who is not a member of `{id}` gets `household_not_found` at the service layer; cross-household access at the HTTP layer may still be `not_found` when `X-Household-Id` does not match `{id}`.

### DELETE /api/households/{id}

Soft-delete. Devices and invites for this household stop working. The row stays restorable for 30 days (design §9).

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required, equal to `{id}` |
| Request | `{ "confirmation": string }` |
| Response | `200` `Household` plus the verbatim sentence below |
| Errors | `unauthorized`, `forbidden_role`, `validation`, `confirmation_mismatch`, `not_found`, `busy` |

The caller types the current household name. The server trims it and compares it case-insensitively. A mismatch is `confirmation_mismatch`. Decided by PM in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445).

The success body includes this sentence, verbatim from design §9, as `message`:

> Your household will be permanently deleted after 30 days. Backup copies are cleared within two weeks after that.

```ts
interface HouseholdDeleted extends Household {
  message: 'Your household will be permanently deleted after 30 days. Backup copies are cleared within two weeks after that.';
}
```

Deleting a household rotates `channel_key`, so current subscribers receive nothing further.

### POST /api/households/{id}/restore

| | |
|---|---|
| Auth | Session |
| Role | Owner. That includes anyone who was an owner when the household was deleted. |
| X-Household-Id | Required, equal to `{id}` |
| Request | Empty body |
| Response | `200` `Household` with `deletedAt: null` |
| Errors | `unauthorized`, `forbidden_role`, `not_found`, `busy` |

Restore succeeds only while `deletedAt` is set and the 30 days have not elapsed. After the hard delete, or for a household the caller cannot see, the response is `not_found`.

### GET /api/households/{id}/members

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required, equal to `{id}` |
| Response | `200` `{ "members": Member[] }` |
| Errors | `unauthorized`, `validation`, `not_found` |

```ts
interface Member {
  userId: string;
  email: string;
  role: 'owner' | 'member';
}
```

`userId` here is the member's id in a response. It is not accepted back as a tenant.

### PATCH /api/households/{id}/members/{userId}

Change that member's role. `{userId}` is the target member.

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required, equal to `{id}` |
| Request | `{ "role": "owner" | "member" }` |
| Response | `200` `Member` |
| Errors | `unauthorized`, `forbidden_role`, `validation`, `not_found`, `last_owner`, `busy` |

Demoting the last owner returns `last_owner` and changes nothing.

### DELETE /api/households/{id}/members/{userId}

Remove that member. On the Pi the member's event streams close. Stage 3 publishes a `rekey` hint on the current channel, then rotates `channel_key`.

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required, equal to `{id}` |
| Request | Empty body |
| Response | `204` |
| Errors | `unauthorized`, `forbidden_role`, `not_found`, `last_owner`, `busy` |

Removing the last owner returns `last_owner`. An owner who is not the last owner may remove themselves. Removal ends that person's access immediately. Only that person's sessions whose active household was the removed one have `activeHouseholdId` set to null, and the old header is `not_found`. Their other memberships stay. On the Pi this closes that member's `GET /api/events` streams. Stage 3 publishes a `rekey` hint on the current channel, then rotates `channel_key`. `rekey` is not sent on the Pi stream.

### POST /api/households/{id}/members/{userId}/reset-link

Owner, for a member of this household. The operator can make one for anyone, including an owner, by calling it with that household's id. The link is single use and expires in 24 hours. Stage 1 does not email it. The owner copies `url`.

| | |
|---|---|
| Auth | Session |
| Role | Owner of `{id}`, or operator |
| X-Household-Id | Required, equal to `{id}` |
| Request | Empty body |
| Response | `201` `{ "url": string, "expiresAt": string }` |
| Errors | `unauthorized`, `forbidden_role`, `not_found`, `busy` |

`url` is `<APP_BASE_URL>/reset/<code>`. A member who is not an owner and not the operator gets `forbidden_role`. A user who is not in this household is `not_found`. Using the link is `POST /api/auth/reset`, which revokes that user's other sessions.

## Invites

An invite is a link or a code, expires in 7 days, is single use, and any member may revoke it (design §5). The hash is what the server stores. The raw code is returned once, from create.

The household is the caller's `X-Household-Id` on create, list, and revoke. Accept looks the household up from the code. The body has no household id.

### POST /api/invites

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required |
| Request | `{ "email"?: string, "role"?: "member" \| "owner" }` |
| Response | `201` `InviteCreated` |
| Errors | `unauthorized`, `forbidden_role`, `validation`, `not_found`, `busy` |

```ts
interface InviteCreated {
  id: string;
  code: string;       // shown once
  url: string;        // <APP_BASE_URL>/invite/<code>
  email: string | null;
  role: 'member' | 'owner'; // the role stored at create
  expiresAt: string;  // created plus 7 days
}
```

invite-role: The role is set when the invite is created. Only an owner can create an owner invite. A member who sends `role` `owner` gets `forbidden_role`. Omitting `role` stores `member`. Accept echoes that role and does not accept a role in the body. The person accepting cannot choose or raise it.

Invite codes are 48 bits. The server builds `url` as `<APP_BASE_URL>/invite/<code>`. The client does not send a host. Stage 1 ignores `email`: it does not store it and it does not send mail, so `email` in the response is null. Stage 3, when `email` is present, sends that URL through SES only after the invite row commits. SES sends from `stockbot.rhionin.com`, and the MAIL FROM domain is `mail.stockbot.rhionin.com`. The address is not a tenant. A failure to send does not roll back the invite.

### GET /api/invites

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required |
| Response | `200` `{ "invites": InviteSummary[] }` |
| Errors | `unauthorized`, `validation`, `not_found` |

```ts
interface InviteSummary {
  id: string;
  email: string | null;
  role: 'member' | 'owner';
  createdBy: string; // user id
  expiresAt: string;
  status: 'open' | 'revoked' | 'used' | 'expired';
}
```

The list does not include `code` or `url`. The raw code is not recoverable after create. `status` stays `open`, `revoked`, `used`, or `expired`. Clients label `revoked` as Cancelled.

### POST /api/invites/{id}/revoke

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required. `{id}` is the invite row, and it must belong to that household. |
| Request | Empty body |
| Response | `204` |
| Errors | `unauthorized`, `not_found`, `busy` |

A wrong household, an unknown id, or an invite that is already used returns `not_found`.

### POST /api/invites/preview

The code is in the body so it does not land in an access log as a path. No session required.

| | |
|---|---|
| Auth | None |
| Role | None |
| X-Household-Id | Omit |
| Request | `{ "code": string }` |
| Response | `200` `{ "kind": "start_household" \| "join_household", "role": "member" \| "owner", "householdName"?: string, "invitedBy"?: string, "expiresAt": string }` |
| Errors | `validation`, `invite_expired`, `not_found`, `rate_limited` |

`join_household` includes `householdName`. `start_household` omits it. `invitedBy` is a display name. A code past `expiresAt`, or already used, is `invite_expired`. The sentence is "This invite has expired or was already used. Ask for a new one." A revoked or never issued code is `not_found`. Limits: 10 per 15 minutes per IP, and 5 attempts per code.

### POST /api/invites/accept

The code is in the body so it does not land in an access log as a path. The #364 body is `{ "code": string }` from a signed-in caller. Stage 1 also accepts an account, and a household name when the invite starts a household.

| | |
|---|---|
| Auth | Session, or none when `account` is present |
| Role | Signed in, or none when creating the account |
| X-Household-Id | Omit. The invite names the household. |
| Request | `{ "code": string, "account"?: { "name": string, "email": string, "password": string }, "household"?: { "name": string, "restockMode"?: "auto" \| "ask_restock" \| "ask_hold" } }` |
| Response | `200` `LoginResponse` with `householdId`, `name`, and `role`. Accept with `account` creates the session: web sets `__Host-stockbot_session`, and Expo, when `X-Stockbot-Client` starts with `expo/`, also receives `token` (`sbs_…`). Accept returns `token` only when `account` is sent. |
| Errors | `unauthorized`, `validation`, `invite_expired`, `not_found`, `account_exists`, `busy`, `rate_limited` |

A code that is past `expiresAt`, or already used, is `invite_expired`. The sentence is "This invite has expired or was already used. Ask for a new one." When two requests use the same invite, the loser gets `invite_expired`. A code that was revoked, or that was never issued, is `not_found`. Decided by PM in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445).

Accept is attempt-limited. Too many tries returns `rate_limited`. The same limits as preview apply, and login's password rules apply to `account.password`. When `account.email` already belongs to a user, the response is `409` `account_exists` and no membership is created. That person signs in and accepts with `{ "code" }` only. Login does not return `account_exists`. A signed-in person sends no `account`. Sending a session and `account` together is `validation`. A signed-in `{ "code" }` accept does not return a token and does not rotate one. Expo keeps the token it has. The accept body has no `role`. Sending `role` is `validation`, including a value that matches the invite. Accept cannot raise a `member` invite to `owner` or lower an `owner` invite to `member`.

`role` on the accept response is the role stored when the invite was created. It is not chosen from `kind` at accept time. `join_household` is `member` unless an owner stored `owner`. `household` is omitted. A signed-in caller sends `{ "code" }` only. A new person sends `account` and no session. `start_household` creates the household, makes the person `owner`, and requires `household.name`. `restockMode` is optional and defaults to `auto`. The operator is the only one who can create that invite. Accepting sets this session's `activeHouseholdId` to that household. It does not remove any other membership. Invite links are `<APP_BASE_URL>/invite/<code>`. Caddy and the app redact that code from logs.

### POST /api/operator/household-invites

Operator only. Creates a `start_household` invite whose stored role is `owner`. Same code rules as `POST /api/invites`: single use, 7 days, 48 bits, stored hashed. The body does not take a role. Sending `role` is `validation`.

| | |
|---|---|
| Auth | Session |
| Role | Operator. This is not a household role. |
| X-Household-Id | Omit |
| Request | Empty body. `email`, if sent, is ignored in stage 1. |
| Response | `201` `InviteCreated` |
| Errors | `unauthorized`, `not_found`, `busy` |

A signed-in caller who is not the operator gets `not_found`. Stage 3 may also email the link through SES. The accept path is `POST /api/invites/accept`.

### GET /api/operator/households

Operator only. Lists every household in the product, including soft-deleted households and households the operator does not belong to. Ordered by primary first, then `createdAt`, then `id`.

| | |
|---|---|
| Auth | Session |
| Role | Operator. This is not a household role. |
| X-Household-Id | Omit |
| Request | None |
| Response | `200` `{ "households": OperatorHouseholdSummary[] }` |
| Errors | `unauthorized`, `forbidden_operator`, `busy` |

A signed-in caller who is not the operator gets `403` `forbidden_operator`.

```ts
interface OperatorHouseholdSummary {
  id: string;
  name: string;
  isPrimary: boolean;
  restockMode: string;
  createdAt: string; // RFC3339
  deletedAt?: string;
  purgeAfter?: string;
  members: { userId: string; email: string; name: string; role: 'owner' | 'member'; createdAt: string }[];
  memberCount: number;
  productCount: number;
  itemsOnHand: number;
  pendingScans: number;
  lastActivityAt?: string;
}
```

## Devices

Pairing code or QR, 10-minute expiry, one household per device. Owners revoke. The token is returned once and stored hashed.

### POST /api/devices/pair

Starts pairing from a signed-in client. Any member can start a code. Only an owner can revoke. Decided by PM in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445).

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required |
| Request | Empty body |
| Response | `201` `PairingCode` |
| Errors | `unauthorized`, `validation`, `not_found`, `busy` |

`expiresAt` is 10 minutes from creation. `qrPayload` is the same code, for the QR. It does not contain a household id the device is expected to send back. Pairing codes are 40 bits.

```ts
interface PairingCode {
  code: string;          // #364 field. SBP-XXXX-XXXX-C
  qrPayload: string;     // #364 field. Same text as code.
  expiresAt: string;
  pairingId: string;     // poll id for GET /api/devices/pair/{pairingId}
  barcodeValue: string;  // same text as code, drawn as Code 128, and the typed-code fallback
  qrValue: string;       // same text as qrPayload, drawn as QR
}
```

pairing-collision: A pairing code cannot collide with a product barcode. The grammar is `SBP-XXXX-XXXX-C`: a `SBP-` prefix, 8 Crockford base32 characters (40 bits) in two groups of four, and a check character. Crockford's payload alphabet is `0123456789ABCDEFGHJKMNPQRSTVWXYZ`. U is a check symbol only, not a payload character. O, I, and L are aliases: O maps to 0, and I or L maps to 1. The check character is that 40-bit value modulo 37, written with `0123456789ABCDEFGHJKMNPQRSTVWXYZ*~$=U`. The symbols `*`, `~`, `$`, and `=` stay in that alphabet. The screen pattern is `^SBP-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z*~$=U]$`.

Before that compare, the client and the server normalize a scan the same way. Trim only the ASCII whitespace characters space, tab, LF, VT, FF, and CR. `U+0085` and `U+FEFF` are not trimmed. A value that still contains a non-ASCII character is rejected. Then uppercase ASCII `a` through `z` only. Never use Unicode case folding, so `ſ` does not become `S`. Then map O to 0 and I or L to 1. The shared vectors are `testdata/pairing-codes.json`.

A product barcode is ASCII digits of length 1 through 14 after that same trim. That includes 6- and 7-digit UPC-E, and GTIN-8, GTIN-12, GTIN-13, and GTIN-14. The server's scan classifier uses this rule: digits only and at most 14 characters is a product, not a QR payload. A pairing code contains letters and hyphens, so it is not one.

The client and the server reject a scan that matches the pattern, including a bad check character. It is never created as a product. `looksLikePairingCode` in `frontend/src/api/contract.ts` is that grammar check after normalization. A code with the right pattern and a bad check character matches `looksLikePairingCode` and fails `isPairingCode`, and the scan is still rejected. `POST /api/devices/pair/complete` also checks the check character. A pattern match with a bad check is `not_found` there. `isPairingCode` alone would let that scan through as a product.

### GET /api/devices/pair/{pairingId}

The signed-in client polls this while the hub scans the code. The token is not in this response. The hub receives the token from `POST /api/devices/pair/complete`.

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required. The pairing must belong to that household. |
| Response | `200` `{ "pairingId": string, "status": "pending" \| "complete" \| "expired", "expiresAt": string, "deviceId"?: string }` |
| Errors | `unauthorized`, `validation`, `not_found` |

`deviceId` is present when `status` is `complete`. An unknown pairing, or one for another household, is `not_found`.

### POST /api/devices/pair/complete

The device calls this with the code and receives the bearer token. Design §6 names `/api/devices/pair` and does not name the device's half of the handshake. PM confirmed this path.

| | |
|---|---|
| Auth | None. The code is the credential. |
| Role | None |
| X-Household-Id | Omit. The code is bound to the household that created it. |
| Request | `{ "code": string, "label"?: string, "hub"?: { "kind": "pi_hub" \| "box_hub" \| "esp32_scanner", "version": string, "bootId": string } }` |
| Response | `201` `{ "deviceId": string, "token": string, "householdName"?: string, "config"?: DeviceHubConfig, "heartbeatSeconds"?: 60 }` |
| Errors | `validation`, `not_found`, `rate_limited`. `invite_expired` is not used here |

An unknown, revoked, already used, or expired code is `not_found`. A code that matches the pattern and fails the check character is `not_found` too. Pairing does not reuse `invite_expired`. `label` is an optional display name. `code` and `label` are the #364 fields. `hub` is the stage-1 addition a scan hub sends. Complete is attempt-limited: 10 per 10 minutes per IP, and 5 attempts per code. Too many tries returns `rate_limited`. This route keeps working on the LAN. Hubs pair against `http://pi:8080`. The code is the credential, and the rate limits above still apply.

The response does not include a household id. The token is bound to that household on the server. `householdName` is the paired household's display name for scanner UI. `token` is shown once. The prefix is `sbd_` plus 32 random bytes in base32. Only the SHA-256 is stored. Logs show an 8-character prefix. `config` and `heartbeatSeconds` are present when `hub` was sent. `box_hub` is reserved for later hardware. Stage 1 hubs send `pi_hub`. **`esp32_scanner`** is the handheld ESP32 barcode scanner (Ticket 1): pairing accepts the same `hub` object with `kind: "esp32_scanner"`. Default `config` on complete is `defaultDirection: "stock_out"`, `inactivityRevertSeconds: 300`, `commitMode: "queue"`, `controlBarcodes: { stockIn: "STOCK-IN", stockOut: "STOCK-OUT" }` (underscore variants `STOCK_IN` / `STOCK_OUT` are recognized on ingest), and `configVersion` from the device row.

### GET /api/devices

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required |
| Response | `200` `{ "devices": Device[] }` |
| Errors | `unauthorized`, `validation`, `not_found` |

```ts
interface Device {
  id: string;
  label: string | null;
  createdAt: string;
  lastSeenAt: string | null;
  revokedAt: string | null;
  kind: 'pi_hub' | 'box_hub' | 'direct' | 'esp32_scanner';
  online: boolean;
  queued?: number;
  defaultDirection?: 'stock_in' | 'stock_out';
  inactivityRevertSeconds?: number;
  commitMode?: 'queue' | 'immediate';
  batteryPct?: number | null;
  charging?: boolean | null;
  // esp32_scanner only: the DeviceFirmware fields in Firmware updates
}
```

`defaultDirection`, `inactivityRevertSeconds`, and `commitMode` reflect the stored scanner settings (`stock_out`, `300`, and `queue` by default). `batteryPct` (0–100) and `charging` come from the latest heartbeat when reported; omit or null when unknown.

`id`, `label`, `createdAt`, `lastSeenAt`, and `revokedAt` are the #364 fields. `kind` and `online` are the stage-1 additions. `lastSeenAt` in this response is the in-memory value. The database row is written only when online status flips, or every 5 minutes. `online` is true when that in-memory time is within 300 seconds of now. The server decides, so web and mobile agree. `direct` is the scanner on this server. It belongs to household 1, and it cannot be paired, renamed, revoked, or moved: those routes return `not_found` for it. The token and the token hash are not in this response.

### PATCH /api/devices/{id}

Rename. Any member. The #364 list fields are otherwise unchanged.

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required. `{id}` must be a device of that household. |
| Request | `{ "label"?: string, "defaultDirection"?: "stock_in" | "stock_out", "inactivityRevertSeconds"?: number (30..3600), "commitMode"?: "queue" | "immediate", "fwChannel"?: "stable" | "beta", "fwPinnedReleaseId"?: string | null }` |
| Response | `200` `Device` |
| Errors | `unauthorized`, `validation`, `not_found`, `forbidden_operator`, `busy` |

`label` is trimmed, 1–80 characters. The direct scanner is `not_found`. `fwChannel` and `fwPinnedReleaseId` are operator only. See [Firmware fields on devices](#firmware-fields-on-devices).

### DELETE /api/devices/{id}

Revoke. Owners only. The server publishes a `rekey` hint on the current channel, then rotates `channel_key`.

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required. `{id}` must be a device of that household. |
| Response | `204` |
| Errors | `unauthorized`, `forbidden_role`, `not_found`, `busy` |

A device in another household is `not_found`. The direct scanner is `not_found`. After revoke, that device's next call is `401` `device_revoked`. Revoke rotates `channel_key` after the `rekey` hint. A revoked device wipes its token and keeps its unsent queue, as [Device error handling](#device-error-handling) says. The confirm uses the last `queued` count from heartbeat.

### POST /api/device/scans

The relay and any other paired scanner call this. The relay is never a push subscriber.

| | |
|---|---|
| Auth | Device bearer |
| Role | Device |
| X-Household-Id | Omit. If the client sends it and it does not equal the device's household, `not_found`. |
| Request | `DeviceScanRequest` or `DeviceScanBatchRequest` |
| Response | `200` `DeviceScanResponse` or `DeviceScanBatchResponse` |
| Errors | `unauthorized`, `device_revoked`, `validation`, `not_found`, `conflict`, `rate_limited`, `payload_too_large`, `busy`, `no_space` |

```ts
interface DeviceScanRequest {
  id: string;                 // client-generated. Idempotency key is (device id from the token, id).
  barcode?: string;
  scannedAt?: string;         // RFC3339. Server time when omitted.
  direction?: 'stock_in' | 'stock_out';
  unitCount?: number;
  heartbeat?: boolean;
}

interface DeviceScanResponse {
  id: string;
  status: 'accepted' | 'duplicate';
  cursor: string;             // scans cursor after the commit. Empty when this call was a heartbeat only.
}
```

The #364 body above stays valid. Stage 1 hubs may instead send `DeviceScanBatchRequest` on this same route. Stage 3 accepts that batch too. The single-object body and `DeviceScanResponse` stay as written. A body is a batch when it has a `scans` array. A body that has both `scans` and `id` is `validation`. A batch holds 1 to 50 items.

Idempotency is the same for both forms. The same device and the same `id`, with the same barcode, direction, and `unitCount`, does not create a second scan. The single body returns `200` with `status: "accepted"` the first time and `status: "duplicate"` on that replay. `accepted` on the single body is `created` on a batch item. If the barcode, direction, or `unitCount` differs, the single body is `conflict` and the stored scan is unchanged. A batch item with that difference has `status: "conflict"` and the HTTP status stays 200.

Heartbeat: every accepted scan updates that device's `lastSeenAt`. `heartbeat: true` with no barcode updates `lastSeenAt` and does not enqueue a scan. A body with neither `barcode` nor `heartbeat: true` is `validation`. The heartbeat flag is only on the single body.

The offline ring of up to 500 scans is device firmware. It is not a request field. Each stored scan is posted with its own `id`. On `pi_hub` and `box_hub`, the hub keeps at most 500 unsent scans and drops the oldest when full. On `esp32_scanner`, when the ring is full the device refuses new scans and shows that its memory is full until it reconnects and a flush frees space; it never drops the oldest scans.

A committed scan publishes one `scans` hint after the final commit, on this device's household channel only.

Scan time, for either form: use `scannedAt` when it is present and inside the window from now minus 30 days through now plus 5 minutes. Otherwise use `receivedAt` minus `ageMs` when `ageMs` is present. Otherwise use `receivedAt` and set `timeApprox`. A `scannedAt` that is not a timestamp is not this fallback.

```ts
interface DeviceScanBatchRequest {
  scans: {
    id: string;
    barcode?: string;
    scannedAt?: string;
    direction?: 'stock_in' | 'stock_out';
    unitCount?: number;
    ageMs?: number;
    bootId?: string;
  }[];
  droppedCount?: number;
}

interface DeviceScanBatchResponse {
  results: { id: string; status: 'created' | 'duplicate' | 'conflict' | 'rejected'; code?: 'validation' }[];
  config: DeviceHubConfig;
}
```

`code` is present only when `status` is `rejected`.

These fail the whole request and write nothing: a body over 64 KB (`413` `payload_too_large`), more than 50 items (`validation`), and JSON the server cannot read (`validation`). A barcode longer than 64 characters, a barcode `looksLikePairingCode` matches, or a `scannedAt` that is not a timestamp does not fail the other items. That item is `rejected` with `code` `validation`. On the single body those three are `validation` for the request, because `DeviceScanResponse` has no `rejected` status.

A device may submit 600 scans per minute. Over that limit the response is `429` `rate_limited` with `Retry-After`, and nothing in the request is written.

The batch is one transaction. A per-item `conflict` or `rejected` does not fail the other items and does not change the HTTP status from 200. `duplicate` is the same device and id with the same barcode, direction, and `unitCount`. `created` is a new scan, and it is the batch form of single-body `accepted`. On `pi_hub` and `box_hub`, `droppedCount` above 0 is the number of buffered scans the hub dropped on that flush; those hubs keep at most 500 unsent scans and drop the oldest when full. An `esp32_scanner` never drops scans from the ring and always sends `droppedCount: 0`. An unrecognized device token is `unauthorized`. A revoked token is `device_revoked`.

`DeviceHubConfig` is `{ label: string, controlBarcodes: { stockIn: string, stockOut: string }, idleSeconds: number, defaultDirection: "stock_in" | "stock_out", inactivityRevertSeconds: number, commitMode: "queue" | "immediate", configVersion: number }`. `idleSeconds` matches `inactivityRevertSeconds` (default 300). Control barcodes accept `STOCK-IN`/`STOCK-OUT` and `STOCK_IN`/`STOCK_OUT`.

**1D product barcodes only (hardware scans).** After trimming ASCII whitespace, each batch item barcode must be 1–14 ASCII digits (`0`–`9`). QR URLs (`https://…`), JSON payloads, pairing codes (`SBP-…`), empty strings, strings over 64 characters, and non-digit product strings are **`rejected`** with `code: "validation"`. Hardware ingest never creates `status: "qr"` scan rows.

**Enriched LCD fields** on each accepted batch item (and on the single-scan body when not heartbeat-only) include: `scanStatus` (`queued` | `committed` | `flagged`), `known` (boolean), `productName` (string or null), `barcode`, `direction`, `queuedCount`, `onHand`, `delta` (signed stock change; `0` in queue mode), and `backfilled` (boolean). Idempotency is keyed by `(device id, client scan id)` in `device_scan_receipts`; a **`duplicate`** replay returns the cached LCD fields without double-queuing or double-committing. A replay with a different barcode, direction, or `unitCount` is **`conflict`**.

**`commitMode: "queue"`** (default): known products create or merge an open **`pending`** queue row and return `scanStatus: "queued"`, `delta: 0`, and current `onHand`. Unknown barcodes create or merge **`flagged`** rows with `scanStatus: "flagged"`, `known: false`, `productName: null`, `onHand: 0`.

**`commitMode: "immediate"`** (known products only): stock-in commits in one step (`scanStatus: "committed"`, positive `delta`, `queuedCount: 0`). Stock-out with on-hand decrements immediately; stock-out at **zero on-hand** runs atomic **#294 backfill** (`backfilled: true`, `delta` negative, `onHand` 0 after). Unknown barcodes still **`flagged`** in the queue even in immediate mode.

### POST /api/device/heartbeat

The hub reports liveness. A flush also counts as contact. This does not remove `heartbeat: true` on `POST /api/device/scans`.

| | |
|---|---|
| Auth | Device bearer |
| Role | Device |
| X-Household-Id | Omit |
| Request | `{ "version": string, "bootId": string, "uptimeS": number, "queued": number, "droppedCount"?: number, "clockSynced": boolean, "batteryPct"?: number, "charging"?: boolean, "rollback"?: boolean, "fwTarget"?: string, "otaState"?: string, "otaReleaseId"?: string, "otaError"?: string, "otaProgressPct"?: number }` |
| Response | `200` `{ "serverTime": string, "config": DeviceHubConfig, "heartbeatSeconds": 60, "rotateToken": boolean, "update"?: FirmwareUpdate }` |
| Errors | `unauthorized`, `device_revoked`, `validation`, `rate_limited`, `busy` |

`heartbeatSeconds` is 60. `online` flips when contact is newer or older than 300 seconds. A change publishes a `devices` hint. `queued` is how many scans the device has not sent yet (0–500 on `esp32_scanner`). On `pi_hub` and `box_hub`, optional `droppedCount` on heartbeat reports scans dropped from the offline buffer since the previous heartbeat. An `esp32_scanner` always sends `droppedCount: 0`. Optional **`batteryPct`** (integer 0–100) and **`charging`** (boolean) update the device row when present; out-of-range `batteryPct` is `validation`. The response **`config`** is the full `DeviceHubConfig` after applying any pending settings (`label`, `controlBarcodes`, `defaultDirection`, `inactivityRevertSeconds`, `commitMode`, `configVersion`). The firmware fields (`rollback`, `fwTarget`, `otaState`, `otaReleaseId`, `otaError`, `otaProgressPct`) and the `update` object are defined in [Firmware updates](#firmware-updates).

### POST /api/device/token/rotate

The hub calls this about every 30 days, or when heartbeat returns `rotateToken: true`.

| | |
|---|---|
| Auth | Device bearer |
| Role | Device |
| X-Household-Id | Omit |
| Request | Empty body |
| Response | `200` `{ "token": string }` |
| Errors | `unauthorized`, `device_revoked`, `rate_limited`, `busy` |

`token` is shown once and stored hashed, same format as pairing. The previous token stays valid until the new token is used, or for 24 hours, whichever comes first.

### Device error handling

The hub branches on the HTTP status and the `code`. It does not branch on the sentence.

Any `401`, whether `unauthorized` or `device_revoked`, means stop, wipe the token, return to pairing mode, and keep the unsent queue.

A `429`, a `5xx`, `busy`, `no_space`, and a network error are retried. The backoff is capped at 30 seconds. A `429` waits at least the `Retry-After` value.

Any other `4xx` is not retried.

## Firmware updates

Decided by CJ on 2026-10-29 (#452). The design is [firmware-ota.md](firmware-ota.md), and the decision record is [ADR-001](adr-001-firmware-ota.md). This section applies to `kind: "esp32_scanner"` devices only. Pi hub updates are unchanged. Every field here is language independent, and the C++ and Rust scanner firmware implement it the same way.

### Heartbeat firmware fields

These fields extend `POST /api/device/heartbeat`. All of them are optional, so older firmware keeps working.

| Request field | Type | Meaning |
|---|---|---|
| `version` | string | Existing field. The running firmware, semver 2.0.0 with no leading `v`. It is stored in the existing `hub_version` column and shown as `fwVersion`. |
| `rollback` | boolean | Existing field. It is `true` on the first heartbeat after the bootloader or the app rolled back to the previous image. |
| `fwTarget` | string | The compiled-in target id: `sticks3_u173`, `sticks3_u173_uart`, `atoms3lite_u173_bench`, or `atoms3lite_symcode_hid`. The server offers no update until a device has reported one. |
| `otaState` | string | `idle`, `waiting`, `downloading`, `verifying`, `pending_verify`, `succeeded`, `failed`, or `rolled_back`. `succeeded`, `failed`, and `rolled_back` are each sent on one heartbeat, then the state goes back to `idle`. |
| `otaReleaseId` | string | The release that `otaState` refers to. Omitted while `idle`. |
| `otaError` | string | Sent only with `failed` or `rolled_back`. See the values below. |
| `otaProgressPct` | number | An integer from 0 to 100, sent only while `downloading`. |

`otaError` is one of `wrong_target`, `unknown_key`, `bad_signature`, `too_large`, `download_failed`, `http_status`, `bad_size`, `bad_sha256`, `flash_write_failed`, `image_invalid`, `checkin_timeout`, or `boot_failed`. These are device report values, not API error codes. The server stores any other value as `other` and still answers `200`, so newer firmware can add values. An unknown `otaState` or `fwTarget` is stored as sent, and the server offers no update for an unknown target. An out-of-range `otaProgressPct` is ignored.

The heartbeat response key `update` keeps its reserved name. Its full shape is:

```ts
interface FirmwareUpdate {
  releaseId: string; // fwr_<ulid>
  version: string;   // semver
  target: string;    // equals the device's fwTarget
  size: number;      // bytes of firmware.bin
  sha256: string;    // 64 lowercase hex
  keyId: string;     // fw-YYYY-N
  sig: string;       // base64 Ed25519 over the canonical manifest
  url: string;       // "/api/device/firmware/{releaseId}", relative to the device's server URL
}
```

- `update` is present only when the server's resolution picks a release whose version differs from the reported `version`. The rules are in [firmware-ota.md §5](firmware-ota.md#resolution).
- The device rebuilds the canonical manifest `stockbot-fw-manifest-v1` from these fields and verifies `sig` before it downloads anything. The byte form is in [firmware-ota.md §4](firmware-ota.md#canonical-signed-bytes). An `update` that fails that check is reported as `failed` with the matching `otaError`, and it is never installed.
- An `update` never changes `config` or `configVersion`.

mark-valid-rule: A new firmware image is valid only after it gets an HTTP 200 from POST /api/device/heartbeat, authenticated with its stored device token and reporting the new version with otaState pending_verify; with no such 200 within 10 minutes of boot, it rolls back.

### GET /api/device/firmware/{releaseId}

This downloads one firmware image. The `url` in `update` points here.

| | |
|---|---|
| Auth | Device bearer |
| Role | Device |
| X-Household-Id | Omit |
| Request | No body. An optional `Range: bytes=<start>-` resumes a download. |
| Response | `200` or `206` `application/octet-stream`, the raw `firmware.bin`, with `Content-Length`, `ETag: "<sha256>"`, and `Accept-Ranges: bytes` |
| Errors | `unauthorized`, `device_revoked`, `not_found`, `rate_limited` |

- The image is served only when the release's target equals the device's last reported `fwTarget`, the release is not yanked, and the release is one the server offered this device. Any other release id is `404` `not_found`.
- The server sends `416` with no body for a range it cannot satisfy. The device then starts again from byte 0.
- A device can start 10 downloads per hour. The 11th gets `429` `rate_limited` with `Retry-After`. A ranged resume counts as a download.
- Firmware releases are global, not household data. The bearer is still required, and a token from one household can download only releases for its own device's target.
- The device handles errors as described in [Device error handling](#device-error-handling). Any `401` still wipes the token. A `404` ends this attempt with `otaError` `http_status`.

### Firmware fields on devices

`GET /api/devices` adds these fields to `Device` for `esp32_scanner` devices. Other kinds omit them.

```ts
interface DeviceFirmware {
  fwVersion: string | null;          // last reported version
  fwTarget: string | null;           // last reported fwTarget
  fwChannel: 'stable' | 'beta';      // default 'stable'
  fwPinnedReleaseId: string | null;
  otaState: string | null;           // last reported otaState
  otaError: string | null;           // last failed or rolled_back reason, kept until the next succeeded
  otaUpdatedAt: string | null;       // RFC3339, when the server stored the last otaState change
  fwUpdateAvailable: string | null;  // the version the next heartbeat would offer, or null
}
```

`PATCH /api/devices/{id}` also accepts `"fwChannel"?: "stable" | "beta"` and `"fwPinnedReleaseId"?: string | null`.

- Only an operator can change these two fields. A household member or owner who sends either one gets `403` `forbidden_operator`, and nothing in the request is applied.
- A pin must name a release that is not yanked and whose target equals the device's `fwTarget`. Otherwise the response is `validation` with `field` `fwPinnedReleaseId`. `null` clears the pin.
- A pin to a version lower than the running one is a downgrade. It is allowed and audited.
- These changes publish a `devices` hint. They do not change `configVersion`.
- Every change writes an `audit_log` row.

### Household firmware setting

`HouseholdSettings` adds `fwAutoUpdate: boolean`, and `PATCH /api/household/settings` accepts `{ "fwAutoUpdate": boolean }` alongside `restockMode`.

- It is stored as the `household_settings` key `fw_auto_update` with the value `"1"` or `"0"`. A missing row means `true`.
- Only an owner can change it. A member gets `forbidden_role`.
- When it is `false`, no `update` is offered to that household's scanners, except for a pinned device.

### Operator firmware routes

| | |
|---|---|
| Auth | Session |
| Role | Operator. Others get `403` `forbidden_operator`. |
| X-Household-Id | Omit |
| Errors | `unauthorized`, `forbidden_operator`, `not_found`, `validation`, `busy` |

`GET /api/operator/firmware` returns `200` `{ "releases": FirmwareRelease[] }`, newest version first:

```ts
interface FirmwareRelease {
  id: string;            // fwr_<ulid>
  target: string;
  version: string;
  size: number;
  sha256: string;
  keyId: string;
  channel: 'beta' | 'stable';
  createdAt: string;     // RFC3339
  promotedAt: string | null;
  yankedAt: string | null;
  canary: 'none' | 'pending' | 'healthy' | 'failed';
  devices: { total: number; succeeded: number; failed: number; rolledBack: number };
}
```

- `canary` is `healthy` once a beta-channel device that is not revoked has reported `succeeded` for a release of this version. It is `failed` when a beta device reported `failed` or `rolled_back` for this version and none succeeded, `pending` while a beta device is offered it, and `none` when no beta device has this target.
- `POST /api/operator/firmware/{id}/promote` takes an empty body.
  - It moves every release with the same version as `{id}` to `stable`, and returns `200` `{ "version": string, "promoted": string[], "uncoveredTargets": string[] }`.
  - `promoted` lists release ids. `uncoveredTargets` lists targets in this version with no healthy beta device.
  - Until the version is healthy, the response is `validation` with `field` `canary`.
  - Promoting an already stable version returns `200` and changes nothing.
- `POST /api/operator/firmware/{id}/yank` takes an empty body.
  - It sets `yankedAt` on every release with that version and returns `200` `{ "version": string, "yanked": string[] }`.
  - A yanked release is never offered or downloaded. Devices on it are offered the highest remaining release on their channel, which may be lower. Each such downgrade offer is audited.
  - Yanking an already yanked version returns `200` and changes nothing.
- `POST /api/operator/firmware/{id}/retry` takes `{ "deviceId": string }`.
  - It clears the rollback suppression for that device and release, so the release can be offered again. It returns `200` `Device`.
  - An unknown device, or a device that did not roll back from `{id}`, is `not_found`.
- Promote, yank, and retry each write an `audit_log` row.

### Firmware schema (PROPOSED)

The schema is PROPOSED. CJ approves migration `029_firmware_ota.sql` at T2 PR time, and this contract does not add it. It is additive only:

- a new `firmware_releases` table;
- new `devices` columns `fw_target`, `fw_channel`, `fw_pinned_release_id`, `ota_state`, `ota_release_id`, `ota_last_error`, `ota_updated_at`, and `ota_retry_release_id`.

The full SQL is in [firmware-ota.md §12](firmware-ota.md#12-schema-proposed-migration-029_firmware_otasql).

## Scans and changes-since

Hosted scan routes are the current scan routes without `userId`. The household is `X-Household-Id`. Role for every scan route below: member. Auth: session.

cursor-invariant: any committed write that changes a scope's result set (including deletes and rows leaving a filter) changes that scope's cursor; a cursor from another household or scope is unrecognized and gets a full 200.

The cursor is an opaque string, at most 128 ASCII characters, with no item names and no barcodes. The client sends it back as `since` and does not parse it. The server encodes household, scope, and version. The version is a counter per `(household, scope)`, bumped in the same transaction as the write. It is per scope, not per status filter. The scopes are `scans`, `stock`, and `list`.

An omitted `since` is a full read of that scope's current rows, `200`, with `changed: true` and the collection. A `since` equal to the current cursor is `200` `{ "cursor": string, "changed": false }` and `Cache-Control: no-store`. The collection is omitted. There is no `304`: a client fetch can turn a `304` into a cached `200`. A `since` the server does not recognize, including one from another household or another scope, is a full read, `200` with `changed: true`, not an error.

When `changed` is true, the body is the full current set for that scope, not a patch. Rows that disappeared are absent. The client replaces its list. Changes-since applies to the scan queue (pending, flagged, and qr), to inventory, and to the shopping list. Committed and cancelled scans are not this cursor.

| Hint `type` | Refetch |
|---|---|
| `scans` | `GET /api/scans?since=<cursor>` |
| `stock` | `GET /api/inventory?since=<cursor>` |
| `list` | `GET /api/shopping-list?since=<cursor>` |
| `devices` | `GET /api/devices` |

### POST /api/scans

Phone camera or a keyboard wedge in the browser. Same body as today's create, without `userId`.

| | |
|---|---|
| X-Household-Id | Required |
| Request | `{ "id"?: string, "barcode": string, "direction"?: "stock_in" \| "stock_out", "unitCount"?: number, "expiresAt"?: string }` |
| Response | `201` `HostedScanEntry`. A replay of the same `id` is `200` with the original entry. |
| Errors | `unauthorized`, `validation`, `not_found`, `conflict`, `busy`, `no_space` |

`barcode` is required. `id`, when present, is a client UUID. It stays optional: the Pi web app on plain HTTP has no `crypto.randomUUID`. The server dedupes it on `(household, user, id)`, the same way a device scan dedupes on `(device, id)`. A replay of the same body returns the original result and does not create a second scan. A replay whose body differs is `409` `conflict`, and the stored scan is unchanged. An omitted `direction` still takes the household's scanner mode, as it does today. `unitCount`, when present, is an integer from 1 through 999. Anything else is `validation`.

### GET /api/scans

| | |
|---|---|
| X-Household-Id | Required |
| Request | Query `since` (scope cursor) for the queue, or `status` plus `limit` and `before` for history. `status` is `pending`, `flagged`, `committed`, `cancelled`, or `qr`. `userId` is not a query parameter. |
| Response | Queue: `200` `ScanChanges`. Unchanged sends `Cache-Control: no-store`. History: `200` `ScanHistoryPage`. |
| Errors | `unauthorized`, `validation`, `not_found` |

An omitted `status` returns the queue the Scan screen shows today: pending, flagged, and qr. Decided by PM in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445). `since` applies to that queue. The cursor is the `scans` scope cursor, not a cursor per status.

`committed` and `cancelled` are history. `since` does not apply to that paging. They are paginated with `limit` (default 50, maximum 100) and `before` (the previous page's `nextBefore`). The page is newest first. The body returns `nextBefore: string | null`, null on the last page. `since` on a history status, or `before` on a queue status, is `validation`.

```ts
interface HostedScanEntry {
  id: string;
  barcode: string;
  scannedAt: string;
  direction: 'stock_in' | 'stock_out' | null;
  unitCount: number;
  expiresAt: string | null;
  status: 'pending' | 'flagged' | 'committed' | 'cancelled' | 'qr';
  productId: string | null;
  product: ProductSummary | null;
  groupHint?: { groupId?: string; name: string };
  committedAt: string | null;
  createdAt: string;
}
```

There is no `userId` on the entry.

### PATCH /api/scans/{id}

Today's update body: `direction`, `unitCount`, `expiresAt`, `productId`, `status`.

| | |
|---|---|
| X-Household-Id | Required. The scan must belong to that household. |
| Response | `200` `HostedScanEntry` |
| Errors | `unauthorized`, `validation`, `not_found`, `busy` |

A scan in another household is `not_found`. Today's handler returns an uncoded 404, `scan entry not found`. Hosted uses `not_found` and the registry sentence. Commit still uses `scan_not_found` when the scan is gone from this household's queue.

### POST /api/scans/{id}/commit

Today's body: `{ "instanceId"?: string, "linkProductId"?: string }`.

| | |
|---|---|
| X-Household-Id | Required |
| Response | `200` `HostedScanEntry` plus `removedCount` and `requestedCount` on a stock-out, as today |
| Errors | The scan-queue codes in the registry, plus `unauthorized`, `not_found` for a foreign household, `busy`, `no_space` |

Status codes that already exist, unchanged:

| Code | HTTP | When |
|---|---|---|
| `not_pending` | 400 | The scan was already committed or cancelled. Sentence: `This scan was already handled.` |
| `product_unresolved` | 400 | The scan has no product yet. Sentence: `Choose which item this is first.` |
| `direction_unset` | 400 | No stock direction. |
| `scan_not_found` | 404 | The scan is not in this household's queue. |
| `no_item` | 409 | A single stock-out whose barcode is not linked. Body may include `suggestions`, as today. |
| `none_on_hand` | 409 | Stock-out and the shelf is empty. |
| `instance_gone` | 409 | The chosen unit is already gone. Body may include `members`, as today. |
| `link_target_gone` | 409 | `linkProductId` does not name a live item. |

A committed retry stays in `committedIds` and is not a `not_pending` failure. Batch `busy` and `no_space` are specified on `POST /api/scans/batch-commit`.

### POST /api/scans/batch-commit

Today's body: `scanEntryIds`, optional `direction`, `unitCount`, `expiresAt`, and `commit`. `scanEntryIds` holds at most 100 ids. Over that cap is `validation`, and nothing is written. `unitCount`, when present, is an integer from 1 through 999. Over that cap is `validation` too.

| | |
|---|---|
| X-Household-Id | Required. Every id must be in that household. |
| Response | `200` today's batch body (`updatedCount`, `committedIds`, `failedIds`, `failedReasons`, `failedCodes`, `stockOutCounts`) |
| Errors | `unauthorized`, `validation`, `not_found` |

The HTTP status stays `200` when an item hits `busy` or `no_space`. This lands with [#330](https://github.com/Rhionin/stockbot/issues/330) (in progress). PM's ruling on that issue:

- On the first `busy` or `no_space` result, the batch stops. Items after that one are not tried.
- The failed item and every untried item get that same code and its sentence in `failedCodes` and `failedReasons`. `failedIds` lists them in request order. They stay pending. A flagged scan stays flagged: the status is left unchanged, so a retry does not stock the same unit twice.
- Items confirmed before the stop stay confirmed and stay in `committedIds`.
- Other per-item failures (`none_on_hand`, `no_item`, `not_pending`, and the rest) do not stop the batch. They stay parallel entries, as they do today.

A single commit or stock-out still uses HTTP 503 for `busy` and `no_space`. Only the batch puts those codes on the `200` body.

A foreign id fails the whole request as `not_found`. The server validates every id in one scoped SELECT before any write. A foreign id and an id that does not exist get the same response. The response never says which id failed. The request does not partially commit, and it does not report the id as a per-item failure.

### GET /api/inventory

Changes-since for hint type `stock`. The items are today's inventory JSON with `userId` removed.

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required |
| Request | Query `since`, and today's `q` |
| Response | `200` `InventoryChanges`. `items` is present only when `changed` is true. An unchanged body sends `Cache-Control: no-store`. |
| Errors | `unauthorized`, `validation`, `not_found` |

### GET /api/shopping-list

Changes-since for hint type `list`. Entries are today's shopping-list JSON with `userId` removed.

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required |
| Request | Query `since` |
| Response | `200` `ShoppingListChanges`. `items` is present only when `changed` is true. An unchanged body sends `Cache-Control: no-store`. |
| Errors | `unauthorized`, `validation`, `not_found` |

Mutations of inventory and the shopping list stay on their current paths, with `userId` removed. After the final commit, a write that touches several scopes publishes one hint per scope. Publish batches at most 5 events, which is AppSync's limit per publish call. These three scopes fit in one batch.

## Export and import

The canonical file is JSON, `schemaVersion` 1. CSV is a spreadsheet view of the same export, not an import format. The hosted app imports the JSON only. The export is not a SQLite file.

A household id inside the file is not a tenant. Import writes into the household named by `X-Household-Id`. `legacyUserId` is optional. When it is present it is recorded so the job can map that Pi user onto the destination household. When it is absent, the Pi's single user is `user-1`. Any other legacy user id is `validation`.

### Document, schema version 1

The normative TypeScript is `ExportDocumentV1` in `frontend/src/api/contract.ts`. The envelope is:

```ts
interface ExportDocumentV1 {
  schemaVersion: 1;
  importId: string;     // UUID minted at export. Idempotency key with the destination household.
  exportedAt: string;    // RFC3339
  source: 'hosted' | 'pi';
  householdName: string; // display only
  legacyUserId?: string; // optional. Absent on a Pi snapshot means user-1.
  products: ExportProductV1[];
  barcodeOverrides: ExportBarcodeOverrideV1[];
  items: ExportItemV1[];
  itemInstances: ExportItemInstanceV1[];
  scanEntries: ExportScanEntryV1[];
  shoppingListItems: ExportShoppingListItemV1[];
  stockMoves: ExportStockMoveV1[];
  groups: ExportGroupV1[];
  categories: ExportCategoryV1[];
  settings: ExportSettingsV1;
}
```

A hosted file has `schemaVersion` 1. Any other `schemaVersion` is `validation`. A top-level key the importer does not know is not a failure. The job ignores it and lists the key in `warnings`. Item, instance, scan, shopping-list, stock-move, group, and category ids are preserved. Stage 1 (Pi, SQLite) keeps UUIDv7 text ids preserved; no sequences. Stage 3 identity columns are `GENERATED BY DEFAULT`, so the import can keep them (design §2.1, §7). Before inserting, the stage-3 job advances those identity sequences above the file's max ids. Product ids in the file are not inserted into the shared catalog. Nested fields are the ones on those TypeScript types.

`ExportSettingsV1` is an allowlist: `restockMode` and `supplyDefault`. It is not an open set of keys. `channel_key` is never in the file. Import overwrites those two settings and leaves the destination household's `channel_key` alone.

Collections and the identity they preserve:

| Key | Preserves | Notes |
|---|---|---|
| `products` | nothing in the shared catalog | Only products the household references. Each `ExportProductV1` carries that product's barcodes. Import matches by barcode or creates a product and remaps. It does not insert under the file's product id. |
| `barcodeOverrides` | barcode | Household overrides only, not global barcodes. |
| `items` | item id | `userId` is absent. `productId` is remapped. Each item carries `category`, `group`, and `stockTarget` so `items.csv` can be written from this document. |
| `itemInstances` | instance id, item id | |
| `scanEntries` | scan id | Flat `ExportScanEntryV1` with `productId`. No nested product. `productId` is remapped. |
| `shoppingListItems` | shopping-list id, item id | |
| `stockMoves` | move id | `productId` is remapped. |
| `groups` | group id | Member product ids are remapped. |
| `categories` | category id, item links, targets | |
| `settings` | the allowlisted settings | `restockMode` and `supplyDefault` only. |

Not in version 1: memberships, invites, device tokens, push subscriptions, provider ciphertext (Kroger and any other OAuth ciphertext), `usage_daily`, and `channel_key`. After import, people re-invite, re-pair, and reconnect a store.

CSV is two objects beside the JSON. The hosted app does not import the CSV. Decided by PM in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445).

| File | Columns |
|---|---|
| `items.csv` | name, barcode, on-hand count, expires, category, group, stock target |
| `shopping-list.csv` | name, quantity |

`stock target` is the item's `stockTarget`. `category` and `group` are the item's `category` and `group`. An empty value is an empty cell. The JSON remains the file the importer reads.

The round-trip test compares products by barcode set plus name, not by id. Item, instance, scan, shopping-list, move, group, and category rows are compared by their preserved ids. A `productId` on those rows is compared after the import's remap.

### POST /api/export

Owner. On the Pi the export is synchronous. `POST` returns `202` with `status` `ready`. Stage 3 stays async: `stockbot-jobs` writes the JSON and the CSV to the exports bucket (SSE-KMS, 7-day expiry) and `POST` returns `status` `pending`.

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required |
| Request | Empty body |
| Response | `202` `{ "jobId": string, "status": "pending" \| "ready" }` |
| Errors | `unauthorized`, `forbidden_role`, `not_found`, `busy`, `no_space` |

The Pi status is `ready`. The stage-3 status is `pending`. Stage 1 on the Pi serves export and household delete (S8). They are not hosted-only. `DELETE /api/households/{id}` is that delete.

### GET /api/export/{id}

The poll for the job `POST /api/export` created. `{id}` is the job id. On the Pi the household is the one stored on the job row, so this poll does not require `X-Household-Id`. In stage 3 the header is required, and the job must belong to that household.

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required in stage 3 only. The Pi takes the household from the job row. |
| Response | `200` `ExportJob` |
| Errors | `unauthorized`, `forbidden_role`, `not_found` |

```ts
interface ExportJob {
  jobId: string;
  status: 'pending' | 'ready' | 'failed';
  jsonUrl: string | null;
  csvUrl: string | null;
  expiresAt: string | null; // URL expiry. Pi: 15 minutes. Stage 3: within the 7-day object life
}
```

`failed` is a job status, not an error code. The body has no item names. A job for another household is `not_found`.

On the Pi, `jsonUrl` and `csvUrl` are same-origin paths. Each one needs the owner's session (cookie or bearer). The household is taken from the job row, so the download does not require `X-Household-Id`. The URLs expire 15 minutes after they are issued. Stage 3 leaves those fields null until the job is `ready`, and then they are presigned URLs.

### POST /api/household/import/uploads

Stage 3; Pi returns `not_found`. Owner. First step of import. Returns a presigned S3 PUT URL. The object key is under `imports/`. The documented max file size is 32 MiB (`33554432` bytes).

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required |
| Request | `{ "contentLength": number }` |
| Response | `201` `{ "uploadUrl": string, "key": string, "maxBytes": 33554432 }` |
| Errors | `unauthorized`, `forbidden_role`, `validation`, `not_found`, `payload_too_large`, `busy` |

`contentLength` above `maxBytes` is `413` `payload_too_large`, and no URL is issued. The presigned PUT is limited to that same size. The client PUTs the JSON to `uploadUrl`, then starts the job.

### Infrastructure dependencies

Network Engineer owns these. CJ approved the production names on 2026-10-22.

- `APP_BASE_URL` is per stage. Production is `https://app.stockbot.rhionin.com`. A later dev stage gets its own host. The server reads the variable.
- Invite links are `<APP_BASE_URL>/invite/<code>`.
- SES sends from `stockbot.rhionin.com`. The MAIL FROM domain is `mail.stockbot.rhionin.com`.
- The imports bucket needs CORS that allows `PUT` from the origin in `APP_BASE_URL`. This contract does not create the bucket.

### POST /api/import

Stage 3; Pi returns `not_found`. Owner. Starts the job for an object already uploaded. The destination household must be empty. Writes run in transactions of at most 1,000 rows. Idempotent per `(household_id, importId)`.

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required. This is the destination. |
| Request | `{ "importId": string, "key": string }` |
| Response | `202` `{ "jobId": string, "importId": string, "status": "pending" }` |
| Errors | `unauthorized`, `forbidden_role`, `validation`, `household_not_empty`, `not_found`, `payload_too_large`, `busy`, `no_space` |

Empty means zero rows in every household-owned table that version 1 import writes: items, item instances, scans, shopping list, stock moves, groups, categories, and barcode overrides. Memberships, invites, devices, and the settings row are allowed. The check runs on this POST and again when the job starts. Import overwrites the allowlisted settings.

The stored object over `maxBytes` is `payload_too_large`. A `key` outside `imports/`, or one this household did not upload, is `not_found`.

When the uploaded file contains `importId`, it must equal the `importId` in this request. A mismatch is `validation`, and no job starts. A Pi snapshot has no `importId`. The request's `importId` is then the idempotency key for that file.

A POST while the same `importId` is `pending` or `running` returns `202` with that job and does not start a second writer. A POST of a `ready` job returns that job and does not write again. Re-POSTing a `failed` job with the same `importId` resumes it: the job inserts only the rows that are missing by preserved id, and it rematches products by barcode. A different `importId` when the household is no longer empty is `household_not_empty`.

Import is asynchronous, because a Pi database does not fit one Lambda request under the 1,000-row cap. Design §7 calls import a job and calls export async. The file is not a field of this request.

Job states:

| Status | Meaning |
|---|---|
| `pending` | Accepted. No rows written yet, or a resume is waiting to run. |
| `running` | Inserting the rows that are still missing. |
| `ready` | Finished. The version 1 collections match the file under the remap rules. |
| `failed` | Stopped before `ready`. The same `importId` can be posted again to resume. |

### GET /api/import/{id}

Stage 3; Pi returns `not_found`. Same auth as POST. `{id}` is the job id.

```ts
interface ImportJob {
  jobId: string;
  importId: string;
  status: 'pending' | 'running' | 'ready' | 'failed';
  warnings: string[]; // unknown keys the job ignored. Empty when there were none.
}
```

`ready` means the version 1 collections match the file under the remap rules. Counts are not required in the body. The body stays free of item names.

Master does not implement `stockbot export`. The JSON the Pi actually serves for household data is `GET /api/sync/snapshot` (`syncsnap.Snapshot`). That route stays on the Pi. It is not a hosted route. The client uploads that JSON with `POST /api/household/import/uploads`, then starts the job with `POST /api/import`. There is no hosted route that reads the Pi's disk.

A snapshot has no `schemaVersion` and no `importId`. Missing `schemaVersion` is this Pi shape, not `validation`. Its top-level keys are `generatedAt`, `products`, `groups`, `instances`, `removals`, `moves`, and `shoppingList`. `legacyUserId` may be added and is optional. Any other top-level key is a warning.

How that snapshot differs from `ExportDocumentV1`:

| Pi snapshot | Hosted document |
|---|---|
| `products` carry `barcodes`, plus `netBaseValue`, `netDimension`, `packCount`, `targetQuantity`, `groupId`, and `noRestock` | `products` carry `barcodes`. Target, group, and restock live on items, groups, and settings |
| `groups` use `memberIds`, `rule`, `pinnedProductId`, `windowMonths`, `quantityBase`, and `dimension` | `groups` use `productIds` |
| `instances` point at `productId`. Removed units are `removals` | `itemInstances` point at `itemId` and keep `removedAt` on the same row |
| `moves` omit `scanEntryId` and `createdAt` | `stockMoves` keep both |
| `shoppingList` points at `productId` and omits purchased lines | `shoppingListItems` point at `itemId` |
| `generatedAt` | `exportedAt` |

The importer maps the snapshot into the version 1 rows. A field the mapping does not store is dropped. That is not a validation failure. A top-level key outside the snapshot or the hosted envelope is listed in `warnings` and is not written. The synthetic fixture `frontend/src/api/fixtures/pi-sync-snapshot.json` is that snapshot shape with made-up rows. The contract tests accept it, including with `legacyUserId` omitted.

## Account deletion

### DELETE /api/me

Deletes the signed-in user. The database step commits first: memberships are removed, and the user row is removed. `AdminDeleteUser` then runs from a job that retries on its own. It does not delete a household that still has another owner. A soft-deleted household does not count for `last_owner` here.

| | |
|---|---|
| Auth | Session |
| Role | Signed in |
| X-Household-Id | Omit. The check covers every household the user owns. |
| Request | `{ "confirmation": string }` |
| Response | `204` |
| Errors | `unauthorized`, `validation`, `confirmation_mismatch`, `last_owner`, `busy` |

The user cannot delete the account while they are the last owner of any household. They transfer ownership or delete that household first. The code is `last_owner`.

The caller types the account email. The server trims it and compares it case-insensitively. A mismatch is `confirmation_mismatch`. Decided by PM in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445).

## Push

Decided transport for stage 3: AppSync Events on every visible screen. Polling is the fallback while the socket is disconnected. Stage 1 on the Pi uses `GET /api/events` instead of the AppSync routes below.

### GET /api/events

Stage 1, Pi only. Stage 3 does not serve this stream and returns `not_found`.

| | |
|---|---|
| Auth | Session cookie or `Bearer sbs_…`. A sync token is also accepted, and it stays household 1 and read-only. |
| Role | Member of the active household. The sync token is not a member route. |
| X-Household-Id | Optional. Omitted, the household is the session's `activeHouseholdId`. A present header follows the activeHouseholdId order. The sync token ignores the header and stays household 1. |
| Response | `200` `text/event-stream` |
| Errors | `unauthorized`, `validation`, `not_found`, `rate_limited` |

The stream is scoped to the session's `activeHouseholdId`. `EventSource` cannot send `X-Household-Id`, so a cookie client uses the stored id. A bearer client may send the header, and a membership header wins. A session with no active household and no header is `400` `validation`. After `POST /api/auth/session/household` the client refetches every scope and closes and reopens this stream.

The frame is `id`, `event`, and `data`. Stage-1 hints use the event name `hint`. The data is a `LiveHint`:

| event | data |
|---|---|
| `hint` | `{"type":"scans","cursor":"<cursor>"}` |
| `hint` | `{"type":"stock","cursor":"<cursor>"}` |
| `hint` | `{"type":"list","cursor":"<cursor>"}` |
| `hint` | `{"type":"devices"}` |

`rekey` never appears on the Pi stream. Stage 3 publishes `rekey` on AppSync before rotating `channel_key`. Household 1 also keeps the event names it already receives: `scan`, `inventory`, `scanner_mode`, `scan_processing`, and `scan_processing_failed`, each with the JSON it carries today. A principal may hold at most 10 streams. The 11th stream is `429` `rate_limited`, with `Retry-After` in seconds. While the household password is on, all LAN clients share the one LAN principal, so the cap for that principal is per source IP: 10 per IP, and the 11th from that IP is `429` `rate_limited`, with `Retry-After` in seconds. This route did not name that response before, so this contract picks `rate_limited`. `EventSource` cannot read status codes. When the stream errors, the client refetches `GET /api/auth/session`. Removing a member closes that member's streams for the household. Retiring the household password closes the household-password streams after commit. After that retirement, `GET /api/events` on the LAN is `401`. Sign in at `appBaseUrl`.

### GET /api/config

Design §3.1 gives this as the example the client uses to pick a transport. PM confirmed the route.

| | |
|---|---|
| Auth | None |
| Role | None |
| X-Household-Id | Omit. The body is not household data. |
| Response | `200` `LiveConfig` |
| Errors | None |

```ts
type LiveConfig =
  | { live: 'sse' | 'poll'; minClientVersion: { expo: string }; householdPasswordSignIn: boolean; appBaseUrl?: string; entry?: 'lan' | 'public' }
  | { live: 'push'; appsync: { httpUrl: string; realtimeUrl: string }; minClientVersion: { expo: string }; householdPasswordSignIn: boolean; appBaseUrl?: string; entry?: 'public' };
```

`householdPasswordSignIn` is true on the Pi while household 1's shared password can still sign in, and false otherwise. The login screen reads it from this route before anyone signs in. Stage 3 returns false. `appBaseUrl` is `APP_BASE_URL` when that is set. Both entries omit it when `APP_BASE_URL` is unset. `entry` is `"lan"` or `"public"`, set by the listener that served the request. Clients use `entry` as the exact signal. The client-facing type marks `entry` and `appBaseUrl` optional, because an older server omits them and the client falls back. The Go server always sends `entry`. It sends `appBaseUrl` only when `APP_BASE_URL` is set and omits the field otherwise (never an empty string), so a LAN-only server follows the absent-`appBaseUrl` rule. The hosted push arm types `entry` as `'public'`. Hosted `LiveConfig` sends `entry` `"public"` only.

The Pi returns `{ "live": "sse", "minClientVersion": { "expo": string }, "householdPasswordSignIn": boolean, "appBaseUrl"?: string, "entry": "lan" | "public" }`, omitting `appBaseUrl` when `APP_BASE_URL` is unset. Hosted returns `{ "live": "push", "appsync": { "httpUrl": string, "realtimeUrl": string }, "minClientVersion": { "expo": string }, "householdPasswordSignIn": boolean, "appBaseUrl"?: string, "entry": "public" }`, with the same omission. `appsync` is required when `live` is `push`. `poll` is reserved for a deployment that has no push. `httpUrl` is the AppSync HTTP domain. `realtimeUrl` is the websocket domain. `minClientVersion.expo` is the oldest Expo build this server accepts. The version gate in [Stage 1 auth](#stage-1-auth) uses it. A missing `X-Stockbot-Client` header means web, so there is no version gate. The exception is `Authorization: Basic` with no `X-Stockbot-Client` after the household password is retired: that request returns `426` `upgrade_required`, not `401`. An `X-Stockbot-Client: expo/<semver>` below the minimum also returns `426` `upgrade_required`. A value that starts with `expo/` and is not a semver is the same `426`. The gate does not run on this route, on `GET /api/build`, or on telemetry. Stage 3 returns the same `minClientVersion` field.

### POST /api/live/ticket

Stage 3; Pi returns `not_found`.

| | |
|---|---|
| Auth | Session (cookie or bearer). Not a device token. |
| Role | Member |
| X-Household-Id | Required, checked against memberships |
| Request | Empty body. No household id, no channel, no user id. |
| Response | `200` `LiveTicket` |
| Errors | `unauthorized`, `validation`, `not_found`, `busy` |

```ts
interface LiveTicket {
  ticket: string;
  channel: `/hh/${string}/${string}`;
  expiresAt: string; // RFC3339, at most 15 minutes from now
}
```

The ticket is an HMAC over `{household_id, user_id, channel_key, exp}`, key in SSM SecureString. `exp` is at most 15 minutes. The client treats `ticket` as opaque and sends it only to the AppSync authorizer, not back to `/api` as a session.

### Channel

`/hh/<household_id>/<channel_key>`

- Both segments are at most 50 characters (AppSync's limit). A household id is a UUID (36). `channel_key` is 32 lowercase hex characters (`[0-9a-f]`). AppSync channel segments allow only `[A-Za-z0-9-]`, so the key stays inside that set and does not change case. Frontend Builder confirmed the same 32 lowercase hex characters.
- `channel_key` lives in `household_settings`. Removing a member or revoking a device publishes a `rekey` hint on the old channel, then rotates the key. A client that receives `rekey` calls `GET /api/config`, then `POST /api/live/ticket`, then subscribes to the new channel.
- Subscribe is allowed only when the channel string equals the ticket's channel. No wildcards and no prefix match.
- Clients cannot publish. Publish is IAM, from the API and jobs roles, after the final successful commit.
- A failed publish does not fail the write. The client catches up on resync.

### Hint

```ts
type LiveHint =
  | { type: 'scans' | 'stock' | 'list'; cursor: string }
  | { type: 'rekey' }
  | { type: 'devices' };
```

The JSON is under 1 KB. It has no item names, barcodes, quantities, or emails. A client that receives `scans`, `stock`, or `list` refetches the matching changes-since route. The refetch is what enforces tenancy. A client that receives `rekey` does not refetch those lists. It loads `GET /api/config` and subscribes again, as the channel section says. A client that receives `devices` refetches `GET /api/devices`. `devices` has no cursor. `scans`, `stock`, `list`, and `rekey` are unchanged.

Publish covers scan ingest, confirm, edit, stock changes, list changes, and `stockbot-jobs` writes that change those three. A write that touches several scopes publishes one hint per scope, in batches of at most 5 events. Job completion for export and import is not one of these types. The client learns that a job finished by polling `GET /api/export/{id}` or `GET /api/import/{id}`.

### Fallback

```ts
interface LiveFallbackRules {
  scanQueuePollMs: 2000;
  otherVisiblePollMs: 10000;
  connectionTimeoutMs: 300000;
  ticketMaxTtlSec: 900;
}
```

- Connect only while a screen is visible. Web uses the Page Visibility API. Expo uses `AppState` plus focus. Hide or background closes the socket. Nothing runs in the background.
- On every connect, including a reconnect: `POST /api/live/ticket`, then subscribe, then the full fetch on `subscribe_success`.
- The server sends `ka` about every 60 s. If none arrives within 300 s, the client closes the socket.
- While the socket is disconnected, and the screen is still visible: poll every 2 s on the Scan Queue and every 10 s on any other visible screen. Do not poll while the socket is up.
- Reconnect backs off exponentially with jitter. An expired ticket is refreshed with a new `POST /api/live/ticket` and is not shown as an error.
- The client connects with the subprotocols `aws-appsync-event-ws` and `header-<base64url>`. The value is base64url of `{"Authorization":"<ticket>","host":"<AppSync HTTP domain>"}` with no `=` padding and no `Bearer` prefix. `host` is the AppSync HTTP domain, the same host as `LiveConfig.appsync.httpUrl`, not the realtime domain. Each subscribe message carries `"authorization":{"Authorization":"<ticket>"}`, again with no `Bearer` prefix. This is the [AppSync Events WebSocket protocol](https://docs.aws.amazon.com/appsync/latest/eventapi/event-api-websocket-protocol.html).

## Error model

Every error body is JSON:

```json
{ "error": "<plain sentence>", "code": "<stable code>", "field": "<input name>" }
```

`error` is the sentence the household sees. `code` is what the client branches on. `field` is optional and only on `validation`, so a field-specific sentence can sit on one input. Other codes omit it. Logs may carry detail. The response does not.

A scan commit may also include `members` or `suggestions`, as today's 409 bodies do. Other codes do not.

generic-sentence: Something went wrong. Try again.

That sentence is `clientmsg.Generic`. On the Pi, `writeStatusError` returns a generic 5xx as `{"error":"Something went wrong. Try again."}` with no `code`, except a commit or stock-out that already sets `code` to `internal`. The Pi stays that way. The hosted API requires the code on every 5xx that is not `busy` or `no_space`:

```json
{"error":"Something went wrong. Try again.","code":"internal"}
```

`code` stays required on `HostedErrorBody`.

`busy` and `no_space` use the sentences PR #320 locks. On the hosted API, `busy` is also the response when an optimistic-concurrency conflict retries until the attempt budget is exhausted (design §6, T12). `no_space` is the response when the write cannot be stored. Neither body includes `Retry-After`. A single commit or stock-out returns those as HTTP 503.

A batch commit stays HTTP 200. On the first `busy` or `no_space` result the batch stops. The failed item and every untried item get that code and sentence in `failedCodes` and stay pending (a flagged scan stays flagged). Items already confirmed stay confirmed. Other per-item failures do not stop the batch. This lands with #330.

### Error code registry

The TypeScript union `HostedErrorCode` matches this table. PM approved the sentences in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445). For `validation`, a client shows the field-specific message instead whenever it has one.

| code | HTTP | sentence | notes |
|---|---|---|---|
| `busy` | 503 | Stockbot is busy right now. Try again in a moment. | Existing sentence. PR #320, not merged. Hosted also uses it when OCC retries are exhausted. A batch commit keeps HTTP 200 and puts this code on `failedCodes` (lands with #330). On the Pi, also an argon2id semaphore wait over 2 seconds, and a writer wait over 2 seconds. Login, reset, and invite accept return it for that semaphore. |
| `no_space` | 503 | Stockbot is out of storage space, so this wasn't saved. | Existing sentence. PR #320, not merged. A batch commit keeps HTTP 200 and puts this code on `failedCodes` (lands with #330). |
| `unauthorized` | 401 | Sign in to continue. | Approved by PM. |
| `forbidden_role` | 403 | Only an owner can do that. | Approved by PM. |
| `forbidden_owner` | 403 | Only an owner can do that. | `PATCH /api/households/{id}` when the caller is a member but not an owner. |
| `forbidden_operator` | 403 | Only an admin can do this. | Operator-only routes such as `GET /api/operator/households`. |
| `household_not_found` | 404 | Stockbot couldn't find that. It may have been removed. | Same sentence as `not_found`. Used when the household id is unknown to the caller at the service layer. |
| `invalid_input` | 400 | Check what you entered and try again. | Same sentence as `validation`. Used for rename name rules on `PATCH /api/households/{id}`. |
| `not_found` | 404 | Stockbot couldn't find that. It may have been removed. | Rewritten by PM. Also the response for cross-household access. |
| `conflict` | 409 | Someone else just changed this. Refresh and try again. | Rewritten by PM. |
| `invite_expired` | 409 | This invite has expired or was already used. Ask for a new one. | Rewritten by PM. Reset links use `reset_expired` instead. |
| `reset_expired` | 409 | This reset link has expired or was already used. Ask for a new one. | `POST /api/auth/reset` only. A revoked or unknown code stays `not_found`. |
| `account_exists` | 409 | That email already has an account. Sign in, then open the invite again. | `POST /api/invites/accept` when `account.email` is already registered. Login stays `unauthorized` for a bad password. |
| `validation` | 400 | Check what you entered and try again. | Rewritten by PM. Clients show a field-specific message instead when they have one. The body may include `field`. |
| `not_pending` | 400 | This scan was already handled. | Existing scan-queue sentence. Unchanged. |
| `product_unresolved` | 400 | Choose which item this is first. | Existing scan-queue sentence. Unchanged. |
| `none_on_hand` | 409 | Nothing on hand to take out. | Existing scan-queue sentence. Unchanged. |
| `instance_gone` | 409 | That one was already taken out. Pick another. | Existing scan-queue sentence. Unchanged. |
| `no_item` | 409 | Which item is this? | Existing scan-queue sentence. Unchanged. |
| `link_target_gone` | 409 | That item isn't available anymore. Pick another. | Existing scan-queue sentence. Unchanged. |
| `scan_not_found` | 404 | This scan is no longer in the queue. | Existing scan-queue sentence. Unchanged. |
| `direction_unset` | 400 | Choose stock in or stock out first. | Existing scan-queue sentence. Unchanged. |
| `internal` | 500 | Something went wrong. Try again. | Existing sentence, `clientmsg.Generic`. The Pi omits `code` on a generic 5xx and keeps it on commit and stock-out. Hosted sends `code` `internal` on every such 5xx. |
| `last_owner` | 409 | You're the only owner. Make someone else an owner first, or delete the household. | Rewritten by PM. Not a code design §6 names. Architect confirmed it stays a stable code. |
| `household_not_empty` | 409 | You can only import into an empty household. This one already has items. | Rewritten by PM. Not a code design §6 names. Architect confirmed it stays a stable code. |
| `confirmation_mismatch` | 400 | That doesn't match. Check the spelling and try again. | Rewritten by PM. Not a code design §6 names. Architect confirmed it stays a stable code. |
| `rate_limited` | 429 | Too many tries. Wait a moment and try again. | Attempt limit on login, reset, invite preview, invite accept, pairing complete, stage-1 `POST /api/households`, and `POST /setup`. `POST /setup` (first setup and recover) uses the same per-IP limit as login, keyed on `RemoteAddr` on `:8080`. Every `rate_limited` response sends `Retry-After` in seconds, `POST /setup` included. `POST /api/device/scans` uses it at 600 scans per minute per device, with `Retry-After`, and writes nothing. The 11th `GET /api/events` stream is `rate_limited` per principal, and per source IP for the LAN principal, and includes `Retry-After` in seconds, like the other `rate_limited` responses. |
| `payload_too_large` | 413 | That file is too large to import. | Import object over the documented max of 32 MiB. Also a `POST /api/device/scans` body over 64 KB. |
| `invite_required` | 403 | You need an invite to start a household. | Stage 1 `POST /api/households` with no `inviteCode`. Later stages may omit the code. |
| `device_revoked` | 401 | This device was disconnected. Pair it again to keep using it. | A paired device calls after an owner revoked it. An unknown device token stays `unauthorized`. |
| `upgrade_required` | 426 | Update Stockbot to keep using it. | Expo below `minClientVersion.expo`, and a malformed `expo/` value. Also Basic with no `X-Stockbot-Client`, or Basic with a value that does not start with `expo/`, after the household password is retired. A missing header, or any other non-`expo/` value, is web and is not gated. The gate skips `GET /api/config`, `GET /api/build`, and telemetry. |

## Decided by PM

Numbers are the ones in [PM's review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445). PM confirmed every working assumption except the invite host, which CJ later decided. Where the comment rewrote a rule, that rewrite is the contract.

1. **JSON casing.** CamelCase on the wire. Decided by PM.
3. **Rename.** Owner. Decided by PM.
4. **Pairing.** Any member can start pairing. Only an owner can revoke. Decided by PM.
5. **Restore.** Owner, including anyone who was an owner when the household was deleted. Decided by PM.
6. **Owner removing themselves.** Allowed when another owner remains. Decided by PM.
7. **Typed confirmations.** Household delete types the household name. Account delete types the email. Both are compared case-insensitively after trimming. Decided by PM.
8. **Extra error codes.** `last_owner`, `household_not_empty`, and `confirmation_mismatch` stay, with the sentences in the registry. Decided by PM. Architect confirmed they stay stable codes.
9. **`conflict`.** A same-second edit that is not last-owner and not a non-empty import uses `conflict` and PM's sentence. Decided by PM.
10. **Invite errors.** Expired or already used is `invite_expired`. Revoked or never issued is `not_found`. Decided by PM.
11. **Pairing completion.** `POST /api/devices/pair/complete`, optional `label`, and `not_found` for an expired pairing code. Decided by PM.
12. **Heartbeat.** `heartbeat: true` on `POST /api/device/scans`, and `lastSeenAt` on every accepted scan. Decided by PM.
13. **Idempotent scan with a different barcode.** `conflict`. Decided by PM.
14. **Device scan response.** No household id. Decided by PM.
15. **Empty household.** PM's direction was an empty household. The rule in the body is the Architect's: zero rows in every table version 1 import writes, with memberships, invites, devices, and the settings row allowed.
16. **Import HTTP shape.** Async `202` plus `GET /api/import/{id}`. Decided by PM.
17. **Extra paths.** The login redirect, household list, pairing completion, export and import polls, `GET /api/config`, and the stock and list changes-since reads stay in the index. Decided by PM.
18. **CSV.** `items.csv` and `shopping-list.csv` only. `items.csv` also has category, group, and stock target. Decided by PM.
19. **Version 1 leaves out** memberships, invites, device tokens, push subscriptions, and provider tokens. Decided by PM. The Architect added `usage_daily` and `channel_key`, and made settings an allowlist.
20. **Product snapshot.** The export copies the products the household references, not the shared catalog. Decided by PM. The Architect added barcodes, the remap rule, and the round-trip comparison.
22. **Hint types.** `scans`, `stock`, and `list`. Decided by PM. Frontend Builder added `rekey` for a channel rotation. Stage 1 adds `devices` for a device list change. The earlier three and `rekey` are unchanged.
23. **Unrecognized changes-since cursor.** A full `200`. Decided by PM. (The earlier batch question that used this number was already closed with #330.) Architect confirmed this, including a cursor from another household or scope. An unchanged cursor is `200` with `changed: false` and `Cache-Control: no-store`, not `304`.
24. **Default scan list** when `status` is omitted: pending, flagged, and qr. Decided by PM. PM's comment calls this #25, which was its number before the #330 edit.
25. **Foreign id inside a batch.** The whole request is `not_found`. Decided by PM. The Architect added the single scoped SELECT and the identical response for a missing id.
29. **Sign-up restock question.** PM said to call the existing restock route after create. PM's comment calls this #30. Master has no route for the #296 mode. Frontend Builder replaced that assumption: optional `restockMode` on `POST /api/households`, and proposed `GET`/`PATCH /api/household/settings`.
31. **Grocy.** Out of scope. Decided by PM. PM's comment calls this #32.

Stage-1 rulings on the plan-vs-#374 differences, decided by PM:

- **Invite status.** The wire value stays `revoked`. Clients label it Cancelled.
- **Start a household.** `POST /api/households` takes optional `inviteCode`. Stage 1 requires a `start_household` code. Missing is `invite_required`. Expired or used is `invite_expired`. Revoked, unknown, or a join code is `not_found`. The code is consumed in the same transaction that creates the household and the owner. Invite-accept limits apply, so the route also returns `rate_limited`. A later stage may omit the code on this same route. Open question 7 stays closed.
- **Reset link.** `<APP_BASE_URL>/reset/<code>` stands.
- **`device_revoked`.** The sentence is "This device was disconnected. Pair it again to keep using it."
- **`reset_expired`.** An expired or already-used reset code is `reset_expired`. The sentence is "This reset link has expired or was already used. Ask for a new one." A revoked or unknown reset code stays `not_found`.
- **Telemetry.** `GET /api/telemetry` is operator-only on the public entry. Nothing public may reveal household activity, including process-wide counts. On the LAN, `GET /api/telemetry` is open only while the household password is on. After retirement it follows the LAN `401` rule, and the operator reads it on the public entry. The LAN response never includes the changeover counts. The route stays exempt from the version gate. `POST /api/telemetry/client` stays a public write, stays open on the LAN, and returns no such counts.

## Decided by Architect

Numbers are the open-question numbers from the `e584250` contract, which is the text the Architect reviewed. Where one of these refines a PM ruling, the text in the body is this decision.

8. **Stable extra codes.** `last_owner` stays 409, `household_not_empty` stays 409, and `confirmation_mismatch` stays 400.
15. **Empty household.** Zero rows in every household-owned table that version 1 import writes: items, item instances, scans, shopping list, stock moves, groups, categories, and barcode overrides. Memberships, invites, devices, and the settings row are allowed, and import overwrites settings. The check runs on POST and again at job start. Stage 1 keeps UUIDv7 text ids preserved; no sequences. The stage-3 job advances the identity sequences above the file's max ids before inserting.
19. **Export exclusions.** Export excludes memberships, invites, device tokens, push subscriptions, provider ciphertext, and `usage_daily`. Settings exclude `channel_key`. `ExportSettingsV1` is an allowlist.
20. **Referenced products.** Export only products the household references, and each product carries its barcodes. Import matches by barcode or creates a product and remaps. It does not insert into the shared catalog under the file's ids. Item, scan, and move ids stay preserved. The round-trip test compares products by barcode set plus name, not by id.
24. **Unknown or expired cursor.** A full `200`.
26. **Foreign id in a batch.** The whole request is `not_found`. Every id is validated in one scoped SELECT before any write. A foreign id and a missing id get the same response, and the response never says which id failed.
28. **AppSync header.** The connection subprotocol is `header-<base64url>` plus `aws-appsync-event-ws`. The base64url payload is `{"Authorization":"<ticket>","host":"<AppSync HTTP domain>"}`, with no `=` padding. `host` is the HTTP domain. Each subscribe message carries `"authorization":{"Authorization":"<ticket>"}`, with no `Bearer` prefix. `LiveConfig.appsync` is required when `live` is `push`. Protocol: https://docs.aws.amazon.com/appsync/latest/eventapi/event-api-websocket-protocol.html

Stage-1 answers confirmed by the Architect, matching `docs/design/multi-household-staged.md` §3.2 and §3.9 at `e61d611`:

- **activeHouseholdId order.** A present `X-Household-Id` wins. If it is omitted, the session's stored `activeHouseholdId` is the household. Both are checked against memberships.
- **LAN.** As in §3.7. While the household password is on, LAN `:8080` is household 1 as member and ignores `X-Household-Id`. After retirement, browsers redirect to `APP_BASE_URL` and people-facing API calls are `401`. `GET /api/auth/session` on the LAN is `401` `unauthorized`. `/api/device/*`, `POST /api/devices/pair/complete`, `GET /api/build`, `GET /api/config`, `GET /health`, and `POST /api/telemetry/client` keep working.
- **Login.** Success is `LoginResponse`. An invite accept whose `account.email` is already registered is `409` `account_exists`.
- **Reset.** An expired or used reset link is `reset_expired`, not `invite_expired`.
- **Telemetry.** PM's operator-only rule is the public entry only. On the LAN, `GET /api/telemetry` is open only while the household password is on. After retirement it follows the LAN `401` rule, and the operator reads it on the public entry. The LAN response never includes the changeover counts. `POST /api/telemetry/client`, `GET /api/config`, and `GET /health` stay open on the LAN. Both telemetry routes stay exempt from the version gate.
- **Version gate.** A missing `X-Stockbot-Client`, or a value that does not start with `expo/`, means web and is not gated. After retirement, Basic with no `X-Stockbot-Client`, or Basic with a non-`expo/` value, is `426` `upgrade_required`, not `401`. A value that starts with `expo/` uses the version gate.
- **Device tokens.** An unknown or invalid device token is `401` `unauthorized`. An owner-revoked hub is `401` `device_revoked`. Either way the hub stops, wipes its token, and re-pairs. There is no `device_unauthorized`.

Review of this contract at `6de8179`, decided by the Architect:

- **One `unauthorized` code.** A failed login and an unknown device token are both `unauthorized`. `device_revoked` is the other `401`. The hub treats any `401` as [Device error handling](#device-error-handling) describes.
- **One scan route, two bodies.** `POST /api/device/scans` accepts the single body and the batch. A body with `scans` is the batch. Both `scans` and `id` is `validation`. The duplicate fingerprint is barcode, direction, and `unitCount`. Single `accepted` is batch `created`.
- **Tenant exception.** `POST /api/auth/session/household` is the one body that sets the active household. `X-Household-Id` is optional when the session already has `activeHouseholdId`.
- **Household create.** Matches PM, including same-transaction consumption of `inviteCode` and `rate_limited`. Open question 7 stays closed.
- **Pi export.** `POST /api/export` returns `202` with `status` `ready`. Stage 3 stays `pending`.
- **Stage-3 routes on the Pi.** `POST /api/live/ticket`, `POST /api/household/import/uploads`, `POST /api/import`, and `GET /api/import/{id}` return `not_found` on the Pi.

## Decided by Frontend Builder

From the review on PR #364. Where one of these refines an earlier ruling, the text in the body is this decision.

1. **5xx body.** Hosted 5xx that is not `busy` or `no_space` is `{"error":"Something went wrong. Try again.","code":"internal"}`. The Pi's generic 5xx still omits `code`.
2. **`rekey`.** Member removal and device revoke publish `rekey` on the old channel before rotating `channel_key`. The client reloads `GET /api/config` and subscribes again.
3. **`channel_key`.** 32 lowercase hex characters, the same rule as the Architect.
4. **Reconnect.** Subscribe first. The full fetch runs on `subscribe_success`.
5. **Restock mode.** Master has no #296 route. `POST /api/households` takes optional `restockMode`. `GET` and `PATCH /api/household/settings` are proposed.
6. **Pi import.** `legacyUserId` is optional. Unknown top-level keys are warnings, not `validation`. The fixture is the Pi's `GET /api/sync/snapshot` JSON with synthetic rows.
7. **Unchanged poll.** `200` with `changed: false` and `Cache-Control: no-store`. The changes-since types are a union of the changed and unchanged cases.
8. **Batch cap.** More than 100 `scanEntryIds` is `validation`.
9. **Reused scan id.** A different body is `409` `conflict`. `id` stays optional.
10. **History page.** The body returns `nextBefore`. `since` does not apply to committed or cancelled paging.
11. **AppSync header.** base64url without padding. `host` is the HTTP domain.
12. **Presigned import.** The imports bucket needs CORS for the origin in `APP_BASE_URL`. Network Engineer owns that. Job start checks that a file `importId` matches the request.

Review of this contract at `6de8179`, from the client:

- **LAN.** No LAN cookie in stage 1. Session cookies are never set or honored on the LAN. While the household password is on, `GET /api/auth/session` on `:8080` reports the LAN principal, household 1 member. Personal sign-in is never offered there: `POST /api/auth/login`, `POST /api/auth/reset`, invite accept with `account`, and `POST /api/households` are `404` `not_found`. `Bearer sbs_…` on `:8080` is `401` `unauthorized` and is never honored. `LAN_PEOPLE=signin` is not stage 1. Cookie auth and the Origin check are the public entry. The Pi `APP_BASE_URL` defaults to `https://stockbot.rhionin.com`. Expo uses `appBaseUrl` for personal sign-in, and clients never show Sign in on a LAN URL. While the household password is on, Expo on a LAN URL may keep using the shared household-1 access, and should offer to move to `appBaseUrl`. After retirement, `GET /api/auth/session` on the LAN is `401` `unauthorized`, and the LAN `401` means moving to `appBaseUrl`. `GET /api/config` returns `entry` `"lan"` or `"public"`.
- **Sessions from reset and accept.** Both return `LoginResponse` and create a session the same way login does. A session sent with `account` is `validation`. `account_exists` stays the registry sentence.
- **Version header.** `CLIENT_HEADER` is `X-Stockbot-Client`. The value is `expo/<semver>` from `app.json` `expo.version` (`1.0.0`, three parts). A missing part compares as 0. A new Expo build sends the header on every request, including `GET /api/events`. `GET /api/config` and `GET /api/build` never return `426`. Telemetry stays exempt and `GET /api/telemetry` is operator-only on the public entry. On the LAN, `GET /api/telemetry` is open only while the household password is on.
- **Pi events.** Scoped to `activeHouseholdId`. The client reopens the stream after a switch. Hint frames are event `hint` plus the `LiveHint` data, including `devices`. `rekey` is not on the Pi stream.
- **Active household.** Login and reset use last-used, else the only membership, else null. Removal sets `activeHouseholdId` to null only on that person's sessions whose active household was the removed one. The header may name any membership.
- **Switcher.** `households` is the switcher list. Both lists keep `deletedAt`. After a switch the client refetches every scope and reopens the stream.
- **Validation `field`.** Optional on `validation` only.
- **Config.** `householdPasswordSignIn` tells the login screen whether the shared password can still sign in. `appBaseUrl` is `APP_BASE_URL` on both entries. `entry` is `"lan"` or `"public"`, set by the listener that served the request.
- **Pi export and delete.** Both exist in stage 1 (S8).
- **Log redaction.** Reset and invite codes in URL paths are redacted from Caddy and app logs.

## Decided by CJ

27. **Invite URL host.** Decided by CJ, via Network Engineer, on 2026-10-22. This replaces the open domain note in design §13. The hosted app and invite URLs use `APP_BASE_URL`. Production is `https://app.stockbot.rhionin.com`. Invite links are `<APP_BASE_URL>/invite/<code>`. SES sends from `stockbot.rhionin.com`, with MAIL FROM at `mail.stockbot.rhionin.com`. The host is stage config, not a hardcoded string, because dev will get its own host later. The client does not send a host. Stage 1 does not send mail. Those SES domains stay the stage-3 sender.
31. **Pi auth.** Decided by CJ on 2026-10-23. Everyone uses a personal email and password. The shared household password is only for household 1's changeover, then it is retired. Sessions are this server's opaque tokens: cookie `__Host-stockbot_session` or bearer `sbs_`. See [Stage 1 auth](#stage-1-auth).
1. **Session cookie name.** Decided by CJ on 2026-10-23. `__Host-stockbot_session`, or bearer `sbs_` for Expo. See [Auth and roles](#auth-and-roles).
- **Firmware rollout, bench first (#452 Q1, option b).** Decided by CJ on 2026-10-29, relayed by Stockbot Manager. Every scanner firmware release goes to the `beta` channel and installs automatically on CJ's bench scanner. CJ promotes it to `stable` with one click once it reports healthy. Household scanners follow `stable`. See [Operator firmware routes](#operator-firmware-routes).
- **Firmware signing key (#452 Q2, option a).** Decided by CJ on 2026-10-29, relayed by Stockbot Manager. The Ed25519 signing seed is the GitHub Actions secret `FW_SIGNING_KEY_ED25519` in environment `firmware-release`, which only `master` can deploy to. CI signs automatically, and an offline backup of the seed is kept. See [ADR-001](adr-001-firmware-ota.md).

## Open questions

2. **Reconnect backoff numbers.** Still unspecified. The 2 s / 10 s polls and the 300 s `ka` timeout stand. PM's comment did not set the delays. This is stage-3 AppSync reconnect behavior.
3. **Pairing check symbols.** `*`, `~`, `$`, and `=` stay in the check alphabet. Pairing codes can be typed with the USB-keyboard fallback, the same string as `barcodeValue`, so typed `*`, `~`, `$`, and `=` are allowed. Whether a later ruling replaces them with a keyboard-safe alphabet stays open.

## Follow-ups

- **S6 (#379) must run the production classifier in `internal/scan/qr.go` against `testdata/pairing-codes.json`.**
- **S1 public listener.** The server's listener change and both Caddyfile changes (every handle to `pantry:8081`, `X-Pantry-Entry` dropped) must ship in the same release. If the server stops reading `X-Pantry-Entry` while Caddy still targets `:8080`, all internet traffic is LAN, and the startup check can't catch it. That same release updates every current `X-Pantry-Entry` user: `deploy/Caddyfile`, `deploy/Caddyfile.tunnel`, `deploy/setup.sh` (it probes with the header, around lines 546 and 554), `check-publish-modes.sh` (around lines 256–257), `setup_apply_test.sh` (around lines 313 and 766), `deploy/README.md` (around line 16), `cmd/server/README.md` (around line 172), `cmd/server/public_compose_test.go` (around line 111 it asserts `deploy/Caddyfile` contains `X-Pantry-Entry public` and `reverse_proxy pantry:8080`, and around line 321 the same header in the tunnel file; this test must change in the same release as the Caddyfile change, or CI fails), `cmd/server/main.go` (around line 386), `internal/server/server.go` (around line 295), `internal/server/handler_sync_test.go` (around line 77), `internal/server/handler_push_test.go` (around lines 293, 301, 316, and 331), `internal/server/sentry_test.go` (around line 720), `internal/server/sentry_tunnel_test.go` (around lines 671, 681, 691, 701, and 743), `docs/design/multi-household-staged.md`, and `docs/design/multi-tenant-saas.md`. It is an infra deploy step owned by the network engineer, paired with S15/S5. During rollout, a `:8080` request carrying `X-Pantry-Entry` is ignored, never refused, and the server logs it at info. The test plan: (a) On `:8080`, `X-Pantry-Entry: public` on `POST /api/auth/login` is ignored, so `404` `not_found`, and `GET /api/config` returns `entry` `"lan"`. (b) On `:8080`, 11 `POST /api/devices/pair/complete` attempts, each with a different `X-Pantry-Client-IP` and `X-Forwarded-For`, are keyed on `RemoteAddr`, so the attempt over the limit gets `429` `rate_limited`. (c) On `:8081`, `X-Pantry-Client-IP` sets the key and `X-Forwarded-For` is ignored. (d) On `:8081`, a request with no `X-Pantry-Entry` (`GET /api/telemetry`, `POST /api/telemetry/client`, and `GET /api/build`) is public. (e) Startup with `APP_BASE_URL` set and the public listener disabled fails. (f) The 6th failing `POST /setup` from one `RemoteAddr` on `:8080` within 15 minutes gets `429` `rate_limited` with `Retry-After`, sharing `POST /api/auth/login`'s per-IP bucket (5 failures per 15 minutes). The rate limit is checked before the code, so a limited request gets `429` and doesn't count again. (g) Both `deploy/Caddyfile` and `deploy/Caddyfile.tunnel` point every handle at `pantry:8081`, and neither contains `pantry:8080`.
- **LAN setup and NAT hairpin.** After LAN setup the user is sent to `APP_BASE_URL`. On routers without NAT hairpin, the certificate (non-tunnel) path can hang from home Wi-Fi (see the comments in `deploy/Caddyfile`). The setup page or the deploy docs should say so.

PM ruled on the invite label, `inviteCode`, the reset path, and the `device_revoked` sentence. The Architect accepted the two plan-vs-#374 differences that were still open (one `unauthorized` code, and both scan bodies on one route). They are under [Decided by Architect](#decided-by-architect). Open question 7, household create, stays closed on PM's `invite_required` ruling. An expired or used reset code stays `reset_expired`.
