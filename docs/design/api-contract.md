# Hosted API contract

Status: draft for review by Frontend Builder and Architect. Issue [#338](https://github.com/Rhionin/stockbot/issues/338).

This is the cross-stack contract for hosted, multi-household Stockbot. It covers the endpoints in design §6, the push rules in design §3.1, and the export document in design §7. It is docs and TypeScript types only. It does not change the running server, and it does not change the Pi.

Source of truth: [`docs/design/multi-tenant-saas.md`](multi-tenant-saas.md) at `d5cc44a` on `cursor/multi-tenant-saas-design-c887` (PR #224). That commit records CJ's decision: Aurora DSQL, and AppSync Events push on every visible screen, with the 2 s / 10 s polling kept only while the client is disconnected.

The copies attached with the T2 request are older than that commit. The uploaded design still recommends polling and has no decision log. The uploaded §3.1 still says the push choice is pending. This contract follows the branch. Rules PM confirmed are in [Decided by PM](#decided-by-pm). Rules the Architect decided on review of this contract are in [Decided by Architect](#decided-by-architect). What is still open is under [Open questions](#open-questions).

Issue #338 matches the T2 scope and acceptance criteria in the ticket list. There is no scope difference to resolve.

The Pi keeps SQLite, the SSE stream `GET /api/events`, the `pantry_session` cookie, and the `userId` parameter. None of §3.1 applies to it. `GET /api/config` tells each client which transport to use.

JSON field names are camelCase, matching the API the web client already calls. Design prose uses snake_case for the same ideas (`household_id`, `channel_key`). PM confirmed camelCase in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445).

## Tenancy

tenant-rule: Nothing in this contract accepts a client-supplied tenant other than the X-Household-Id header, and that header is valid only when it names a household the signed-in user belongs to, or, for device calls, the household bound to the device token; nothing else selects a tenant.

The signed-in user comes from the server session. Web sends the BFF cookie. Expo sends `Authorization: Bearer` with the Cognito access token. A paired device sends its own bearer token, and the household is the one stored for that device. The client does not send a household id for a device call.

`userId` is removed from every hosted request: query, body, and path, except the path segment on the member routes below, which names the member being changed and is not a tenant. userId-removed: If a client still sends userId, the server ignores it and does not treat it as the tenant.

These are not tenants, and the server does not select a household from them:

- a `householdId` field in a body, including an imported file
- the `{id}` of a household path, unless it equals the validated `X-Household-Id`
- the `{userId}` of a member path
- `device_id` in a scan body (the device id comes from the device token)
- the `household_id` claim inside a live ticket (the server minted it; the client does not present it to `/api`)

A missing `X-Household-Id` on a route that requires it is `validation`. An unknown id, a household the caller does not belong to, or a path `{id}` that does not equal the header is `not_found`. Cross-household access is `not_found`, never `forbidden_role` and never `unauthorized`, so the response does not reveal that the other household exists (design §4).

`forbidden_role` is only for a caller who is a member of the named household and whose role cannot perform the action.

### Soft-deleted households

Inside the 30-day restore window, GET, restore, and export still work. That is `GET /api/households`, `GET /api/households/{id}`, `POST /api/households/{id}/restore`, `POST /api/export`, and `GET /api/export/{id}`. `GET /api/auth/session` still lists the membership so the owner can find it.

Every other route returns `not_found`. A device token bound to a soft-deleted household returns `unauthorized` instead.

Proposed: restore does not revive device tokens. The owner re-pairs each device after restore.

## Auth and roles

| Caller | How they authenticate | Household |
|---|---|---|
| Web | HttpOnly, Secure, SameSite=Lax session cookie. The browser does not hold a Cognito token. | `X-Household-Id` |
| Expo | `Authorization: Bearer` Cognito access token, from the system browser PKCE flow. Tokens stay in the device secure store. | `X-Household-Id` |
| Device | `Authorization: Bearer` device token, returned once at pairing. | Bound to the token. One household per device. |

Web cookie calls that change state also require an `Origin` equal to the hosted origin (design §5). Expo bearer calls do not use that cookie.

The cookie's name is not specified here. It replaces `pantry_session` on the hosted origin. `pantry_session` stays the Pi cookie.

Roles, from design §5:

| Role | Can |
|---|---|
| member | Scan, confirm, edit, manage the shopping list, create and revoke invites, list members, list devices. |
| owner | Everything a member can do, plus change roles, remove members, revoke devices, export, import, and delete or restore the household. |

A household has at least one owner. The check locks the owner rows with `SELECT ... FOR UPDATE` in the same transaction as the demotion, removal, or account deletion. A write that would leave zero owners returns `last_owner`. A soft-deleted household does not count for `last_owner` on `DELETE /api/me`. If the serialization-error retry budget is exhausted first, the response is `busy`. T13 adds a concurrent cross-demotion test for this lock.

The server retries a transaction that fails with a serialization error, from the start, inside a bounded attempt budget. Calls that leave the database run only after that transaction has committed: the SES invite email, Cognito `AdminUserGlobalSignOut` on logout, Cognito `AdminDeleteUser` on account deletion, and EventBridge Scheduler create. `DELETE /api/me` commits the database step first, then calls `AdminDeleteUser` from a job that retries on its own. A failure of that job does not restore the memberships.

Cognito owns sign-up, email verification, and password reset. This API does not take passwords. A session exists only after the email is verified. The post-confirmation trigger creates the `users` row. The household is created by `POST /api/households` after that.

## Endpoint index

endpoint-count: 39

Paths the design names are unmarked. Paths added so Frontend can call a complete flow (login redirect, household list, pairing completion, export and import job polls, the presigned import upload, `GET /api/config`, and the stock and list changes-since reads) are part of this contract. PM confirmed the earlier extras in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445). The upload route is the Architect's import-size decision.

```text
endpoint-index
GET /api/auth/login
GET /api/auth/callback
POST /api/auth/logout
GET /api/auth/session
POST /api/households
GET /api/household/settings
PATCH /api/household/settings
GET /api/households
GET /api/households/{id}
PATCH /api/households/{id}
DELETE /api/households/{id}
POST /api/households/{id}/restore
GET /api/households/{id}/members
PATCH /api/households/{id}/members/{userId}
DELETE /api/households/{id}/members/{userId}
POST /api/invites
GET /api/invites
POST /api/invites/{id}/revoke
POST /api/invites/accept
POST /api/devices/pair
POST /api/devices/pair/complete
GET /api/devices
DELETE /api/devices/{id}
POST /api/device/scans
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
POST /api/live/ticket
GET /api/config
```

Routes the current server already serves, and that this contract does not redefine, keep today's JSON. On the hosted API they drop `userId` and require `X-Household-Id`. That includes history, restock, groups, providers, settings, and the shopping-list mutations. Their field list stays the one the handlers use today.

## Auth and session

### GET /api/auth/login

Web only. Design §6 names callback, logout, and session. This redirect is the step that starts that callback.

| | |
|---|---|
| Auth | None |
| Role | None |
| X-Household-Id | Omit |
| Request | Query `returnTo`, a path that starts with `/` and does not start with `//` |
| Response | `302` to the Cognito managed-login authorize URL, with PKCE |
| Errors | `validation` when `returnTo` is not a same-origin path |

Expo does not call this. Expo runs PKCE in the system browser and stores the tokens itself.

### GET /api/auth/callback

Web only. The Lambda exchanges the Cognito code and sets the session cookie.

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

After the session row is deleted and the transaction commits, the server calls Cognito `AdminUserGlobalSignOut` for that user, so a refresh token cannot mint a new session. That call is not inside the transaction. Expo deletes its secure-store tokens after this returns.

### GET /api/auth/session

| | |
|---|---|
| Auth | Session (cookie or bearer) |
| Role | Signed in |
| X-Household-Id | Omit |
| Request | None |
| Response | `200` `AuthSession` |
| Errors | `unauthorized` |

```ts
interface AuthSession {
  user: { id: string; email: string };
  memberships: { householdId: string; name: string; role: 'owner' | 'member' }[];
}
```

`memberships` is every household this user belongs to, including one that is soft-deleted and still inside the 30-day restore window. It is the switcher list. It is not a tenant the client can override: a later call still has to send `X-Household-Id`, and the server checks membership again.

## Households, members, and roles

### POST /api/households

Creates a household and makes the caller an owner. This is the step after email verification in the sign-up flow.

| | |
|---|---|
| Auth | Session |
| Role | Signed in |
| X-Household-Id | Omit |
| Request | `{ "name": string, "restockMode"?: "auto" \| "ask_restock" \| "ask_hold" }` |
| Response | `201` `{ "id": string, "name": string, "role": "owner" }` |
| Errors | `unauthorized`, `validation` |

`name` is required, trimmed, and 1–80 characters. The 1–80 length is not in the design. `restockMode` is optional: `auto`, `ask_restock`, or `ask_hold`, the three values in [#296](https://github.com/Rhionin/stockbot/issues/296). Omitted means `auto`, which is today's behavior. Master has no route for that setting. `PUT /api/restock/household` sets a numeric household target. It is not this mode.

### GET /api/household/settings

### PATCH /api/household/settings

Proposed. These two routes are the household restock mode from #296. They supersede the earlier assumption that sign-up would call an existing restock route.

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required |
| GET response | `200` `{ "restockMode": "auto" \| "ask_restock" \| "ask_hold" }` |
| PATCH request | `{ "restockMode": "auto" \| "ask_restock" \| "ask_hold" }` |
| PATCH response | `200` the same object |
| Errors | `unauthorized`, `validation`, `not_found`, `busy` |

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
| Errors | `unauthorized`, `forbidden_role`, `validation`, `not_found`, `busy` |

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

Remove that member. The server publishes a `rekey` hint on the current channel, then rotates `channel_key`.

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required, equal to `{id}` |
| Request | Empty body |
| Response | `204` |
| Errors | `unauthorized`, `forbidden_role`, `not_found`, `last_owner`, `busy` |

Removing the last owner returns `last_owner`. An owner who is not the last owner may remove themselves.

## Invites

An invite is a link or a code, expires in 7 days, is single use, and any member may revoke it (design §5). The hash is what the server stores. The raw code is returned once, from create.

The household is the caller's `X-Household-Id` on create, list, and revoke. Accept looks the household up from the code. The body has no household id.

### POST /api/invites

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required |
| Request | `{ "email"?: string }` |
| Response | `201` `InviteCreated` |
| Errors | `unauthorized`, `validation`, `not_found`, `busy` |

```ts
interface InviteCreated {
  id: string;
  code: string;       // shown once
  url: string;        // hosted origin + code, origin from server config
  email: string | null;
  expiresAt: string;  // created plus 7 days
}
```

The code has at least 40 bits of entropy. When `email` is present, the server sends that URL through SES only after the invite row commits. The address is not a tenant. A failure to send does not roll back the invite.

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
  createdBy: string; // user id
  expiresAt: string;
  status: 'open' | 'revoked' | 'used' | 'expired';
}
```

The list does not include `code` or `url`. The raw code is not recoverable after create.

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

### POST /api/invites/accept

The caller is signed in (they have just signed up, or they already have an account). The code is in the body so it does not land in an access log as a path.

| | |
|---|---|
| Auth | Session |
| Role | Signed in |
| X-Household-Id | Omit. The invite names the household. |
| Request | `{ "code": string }` |
| Response | `200` `{ "householdId": string, "name": string, "role": "member" }` |
| Errors | `unauthorized`, `validation`, `invite_expired`, `not_found`, `busy`, `rate_limited` |

A code that is past `expiresAt`, or already used, is `invite_expired`. A code that was revoked, or that was never issued, is `not_found`. Decided by PM in [the review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445).

Accept is attempt-limited. Too many tries returns `rate_limited`.

The new membership is `member`. The client then sends that `householdId` as `X-Household-Id`. Accepting does not switch any other household.

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
| Response | `201` `{ "code": string, "qrPayload": string, "expiresAt": string }` |
| Errors | `unauthorized`, `validation`, `not_found`, `busy` |

`expiresAt` is 10 minutes from creation. `qrPayload` is the same code, for the QR. It does not contain a household id the device is expected to send back. The code has at least 40 bits of entropy.

### POST /api/devices/pair/complete

The device calls this with the code and receives the bearer token. Design §6 names `/api/devices/pair` and does not name the device's half of the handshake. PM confirmed this path.

| | |
|---|---|
| Auth | None. The code is the credential. |
| Role | None |
| X-Household-Id | Omit. The code is bound to the household that created it. |
| Request | `{ "code": string, "label"?: string }` |
| Response | `201` `{ "deviceId": string, "token": string }` |
| Errors | `validation`, `not_found`, `rate_limited`. `invite_expired` is not used here |

An unknown, revoked, already used, or expired code is `not_found`. Pairing does not reuse `invite_expired`. `label` is an optional display name. Complete is attempt-limited. Too many tries returns `rate_limited`.

The response does not include a household id. The token is bound to that household on the server. `token` is shown once.

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
}
```

The token and the token hash are not in this response.

### DELETE /api/devices/{id}

Revoke. Owners only. The server publishes a `rekey` hint on the current channel, then rotates `channel_key`.

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required. `{id}` must be a device of that household. |
| Response | `204` |
| Errors | `unauthorized`, `forbidden_role`, `not_found`, `busy` |

A device in another household is `not_found`.

### POST /api/device/scans

The relay and any other paired scanner call this. The relay is never a push subscriber.

| | |
|---|---|
| Auth | Device bearer |
| Role | Device |
| X-Household-Id | Omit. If the client sends it and it does not equal the device's household, `not_found`. |
| Request | `DeviceScanRequest` |
| Response | `200` `DeviceScanResponse` |
| Errors | `unauthorized`, `validation`, `not_found`, `conflict`, `busy`, `no_space` |

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

Idempotency: a second request with the same device and the same `id` does not create a second scan. It returns `200` with `status: "duplicate"` and the original result. If the barcode or `unitCount` differs from the stored request, the response is `conflict` and the stored scan is unchanged.

Heartbeat: every accepted scan updates that device's `lastSeenAt`. `heartbeat: true` with no barcode updates `lastSeenAt` and does not enqueue a scan. A body with neither `barcode` nor `heartbeat: true` is `validation`.

The offline ring of 500 scans is device firmware. It is not a request field. Each stored scan is posted with its own `id`.

A committed scan publishes one `scans` hint after the final commit, on this device's household channel only.

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

A hosted file has `schemaVersion` 1. Any other `schemaVersion` is `validation`. A top-level key the importer does not know is not a failure. The job ignores it and lists the key in `warnings`. Item, instance, scan, shopping-list, stock-move, group, and category ids are preserved. Identity columns are `GENERATED BY DEFAULT`, so the import can keep them (design §2.1, §7). Before inserting, the job advances those identity sequences above the file's max ids. Product ids in the file are not inserted into the shared catalog. Nested fields are the ones on those TypeScript types.

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

Owner. Async. `stockbot-jobs` writes the JSON and the CSV to the exports bucket (SSE-KMS, 7-day expiry) and a later GET returns presigned URLs.

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required |
| Request | Empty body |
| Response | `202` `{ "jobId": string, "status": "pending" }` |
| Errors | `unauthorized`, `forbidden_role`, `not_found`, `busy`, `no_space` |

### GET /api/export/{id}

The poll for the job `POST /api/export` created. `{id}` is the job id, and the job must belong to `X-Household-Id`.

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required |
| Response | `200` `ExportJob` |
| Errors | `unauthorized`, `forbidden_role`, `not_found` |

```ts
interface ExportJob {
  jobId: string;
  status: 'pending' | 'ready' | 'failed';
  jsonUrl: string | null;
  csvUrl: string | null;
  expiresAt: string | null; // URL expiry, within the 7-day object life
}
```

`failed` is a job status, not an error code. The body has no item names. A job for another household is `not_found`.

### POST /api/household/import/uploads

Owner. First step of import. Returns a presigned S3 PUT URL. The object key is under `imports/`. The documented max file size is 32 MiB (`33554432` bytes).

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required |
| Request | `{ "contentLength": number }` |
| Response | `201` `{ "uploadUrl": string, "key": string, "maxBytes": 33554432 }` |
| Errors | `unauthorized`, `forbidden_role`, `validation`, `not_found`, `payload_too_large`, `busy` |

`contentLength` above `maxBytes` is `413` `payload_too_large`, and no URL is issued. The presigned PUT is limited to that same size. The client PUTs the JSON to `uploadUrl`, then starts the job.

Infrastructure: the imports bucket needs CORS that allows `PUT` from the hosted origin. Network Engineer owns that bucket policy. This contract does not create the bucket.

### POST /api/import

Owner. Starts the job for an object already uploaded. The destination household must be empty. Writes run in transactions of at most 1,000 rows. Idempotent per `(household_id, importId)`.

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

Same auth as POST. `{id}` is the job id.

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

Decided transport: AppSync Events on every visible screen. Polling is the fallback while the socket is disconnected. The Pi does not use this section.

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
  | { live: 'sse' | 'poll' }
  | { live: 'push'; appsync: { httpUrl: string; realtimeUrl: string } };
```

The Pi returns `{ "live": "sse" }`. Hosted returns `{ "live": "push", "appsync": { "httpUrl": string, "realtimeUrl": string } }`. `appsync` is required when `live` is `push`. `poll` is reserved for a deployment that has no push. `httpUrl` is the AppSync HTTP domain. `realtimeUrl` is the websocket domain.

### POST /api/live/ticket

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
  | { type: 'rekey' };
```

The JSON is under 1 KB. It has no item names, barcodes, quantities, or emails. A client that receives `scans`, `stock`, or `list` refetches the matching changes-since route. The refetch is what enforces tenancy. A client that receives `rekey` does not refetch those lists. It loads `GET /api/config` and subscribes again, as the channel section says.

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
{ "error": "<plain sentence>", "code": "<stable code>" }
```

`error` is the sentence the household sees. `code` is what the client branches on. Logs may carry detail. The response does not.

A scan commit may also include `members` or `suggestions`, as today's 409 bodies do. Other codes do not.

generic-sentence: Something went wrong. Try again.

That sentence is `clientmsg.ServerError` on master. On the Pi, `writeStatusError` returns a generic 5xx as `{"error":"Something went wrong. Try again."}` with no `code`, except a commit or stock-out that already sets `code` to `internal`. The Pi stays that way. The hosted API requires the code on every 5xx that is not `busy` or `no_space`:

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
| `busy` | 503 | Stockbot is busy right now. Try again in a moment. | Existing sentence. PR #320, not merged. Hosted also uses it when OCC retries are exhausted. A batch commit keeps HTTP 200 and puts this code on `failedCodes` (lands with #330). |
| `no_space` | 503 | Stockbot is out of storage space, so this wasn't saved. | Existing sentence. PR #320, not merged. A batch commit keeps HTTP 200 and puts this code on `failedCodes` (lands with #330). |
| `unauthorized` | 401 | Sign in to continue. | Approved by PM. |
| `forbidden_role` | 403 | Only an owner can do that. | Approved by PM. |
| `not_found` | 404 | Stockbot couldn't find that. It may have been removed. | Rewritten by PM. Also the response for cross-household access. |
| `conflict` | 409 | Someone else just changed this. Refresh and try again. | Rewritten by PM. |
| `invite_expired` | 409 | This invite has expired or was already used. Ask for a new one. | Rewritten by PM. |
| `validation` | 400 | Check what you entered and try again. | Rewritten by PM. Clients show a field-specific message instead when they have one. |
| `not_pending` | 400 | This scan was already handled. | Existing scan-queue sentence. Unchanged. |
| `product_unresolved` | 400 | Choose which item this is first. | Existing scan-queue sentence. Unchanged. |
| `none_on_hand` | 409 | Nothing on hand to take out. | Existing scan-queue sentence. Unchanged. |
| `instance_gone` | 409 | That one was already taken out. Pick another. | Existing scan-queue sentence. Unchanged. |
| `no_item` | 409 | Which item is this? | Existing scan-queue sentence. Unchanged. |
| `link_target_gone` | 409 | That item isn't available anymore. Pick another. | Existing scan-queue sentence. Unchanged. |
| `scan_not_found` | 404 | This scan is no longer in the queue. | Existing scan-queue sentence. Unchanged. |
| `direction_unset` | 400 | Choose stock in or stock out first. | Existing scan-queue sentence. Unchanged. |
| `internal` | 500 | Something went wrong. Try again. | Existing sentence, `clientmsg.ServerError`. The Pi omits `code` on a generic 5xx and keeps it on commit and stock-out. Hosted sends `code` `internal` on every such 5xx. |
| `last_owner` | 409 | You're the only owner. Make someone else an owner first, or delete the household. | Rewritten by PM. Not a code design §6 names. Architect confirmed it stays a stable code. |
| `household_not_empty` | 409 | You can only import into an empty household. This one already has items. | Rewritten by PM. Not a code design §6 names. Architect confirmed it stays a stable code. |
| `confirmation_mismatch` | 400 | That doesn't match. Check the spelling and try again. | Rewritten by PM. Not a code design §6 names. Architect confirmed it stays a stable code. |
| `rate_limited` | 429 | Too many tries. Wait a moment and try again. | Attempt limit on `POST /api/invites/accept` and `POST /api/devices/pair/complete`. |
| `payload_too_large` | 413 | That file is too large to import. | Import object over the documented max of 32 MiB. |

## Decided by PM

Numbers are the ones in [PM's review comment](https://github.com/Rhionin/stockbot/pull/364#issuecomment-6357031445). PM confirmed every working assumption except the invite host. Where the comment rewrote a rule, that rewrite is the contract.

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
22. **Hint types.** `scans`, `stock`, and `list`. Decided by PM. Frontend Builder added `rekey` for a channel rotation.
23. **Unrecognized changes-since cursor.** A full `200`. Decided by PM. (The earlier batch question that used this number was already closed with #330.) Architect confirmed this, including a cursor from another household or scope. An unchanged cursor is `200` with `changed: false` and `Cache-Control: no-store`, not `304`.
24. **Default scan list** when `status` is omitted: pending, flagged, and qr. Decided by PM. PM's comment calls this #25, which was its number before the #330 edit.
25. **Foreign id inside a batch.** The whole request is `not_found`. Decided by PM. The Architect added the single scoped SELECT and the identical response for a missing id.
29. **Sign-up restock question.** PM said to call the existing restock route after create. PM's comment calls this #30. Master has no route for the #296 mode. Frontend Builder replaced that assumption: optional `restockMode` on `POST /api/households`, and proposed `GET`/`PATCH /api/household/settings`.
31. **Grocy.** Out of scope. Decided by PM. PM's comment calls this #32.

## Decided by Architect

Numbers are the open-question numbers from the `e584250` contract, which is the text the Architect reviewed. Where one of these refines a PM ruling, the text in the body is this decision.

8. **Stable extra codes.** `last_owner` stays 409, `household_not_empty` stays 409, and `confirmation_mismatch` stays 400.
15. **Empty household.** Zero rows in every household-owned table that version 1 import writes: items, item instances, scans, shopping list, stock moves, groups, categories, and barcode overrides. Memberships, invites, devices, and the settings row are allowed, and import overwrites settings. The check runs on POST and again at job start. The job advances the identity sequences above the file's max ids before inserting.
19. **Export exclusions.** Export excludes memberships, invites, device tokens, push subscriptions, provider ciphertext, and `usage_daily`. Settings exclude `channel_key`. `ExportSettingsV1` is an allowlist.
20. **Referenced products.** Export only products the household references, and each product carries its barcodes. Import matches by barcode or creates a product and remaps. It does not insert into the shared catalog under the file's ids. Item, scan, and move ids stay preserved. The round-trip test compares products by barcode set plus name, not by id.
24. **Unknown or expired cursor.** A full `200`.
26. **Foreign id in a batch.** The whole request is `not_found`. Every id is validated in one scoped SELECT before any write. A foreign id and a missing id get the same response, and the response never says which id failed.
28. **AppSync header.** The connection subprotocol is `header-<base64url>` plus `aws-appsync-event-ws`. The base64url payload is `{"Authorization":"<ticket>","host":"<AppSync HTTP domain>"}`, with no `=` padding. `host` is the HTTP domain. Each subscribe message carries `"authorization":{"Authorization":"<ticket>"}`, with no `Bearer` prefix. `LiveConfig.appsync` is required when `live` is `push`. Protocol: https://docs.aws.amazon.com/appsync/latest/eventapi/event-api-websocket-protocol.html

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
12. **Presigned import.** The imports bucket needs CORS for the hosted origin. Network Engineer owns that. Job start checks that a file `importId` matches the request.

## Open questions

1. **Invite URL host** (PM's #27). Still open. It waits on CJ's hosted domain. The server builds the URL. The client does not send a host.
2. **Session cookie name.** Still open for the Architect. It must not be the Pi's `pantry_session`. PM's comment did not name it.
3. **Reconnect backoff numbers.** Still unspecified. The 2 s / 10 s polls and the 300 s `ka` timeout stand. PM's comment did not set the delays.
4. **Pi auth** (question 31 on the reviewed contract). This contract does not change the Pi. Which auth the Pi keeps is PM's decision. It is not a T2 blocker.
