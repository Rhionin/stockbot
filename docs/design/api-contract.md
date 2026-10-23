# Hosted API contract

Status: draft for review by Frontend Builder and Architect. Issue [#338](https://github.com/Rhionin/stockbot/issues/338).

This is the cross-stack contract for hosted, multi-household Stockbot. It covers the endpoints in design §6, the push rules in design §3.1, and the export document in design §7. It is docs and TypeScript types only. It does not change the running server, and it does not change the Pi.

Source of truth: [`docs/design/multi-tenant-saas.md`](multi-tenant-saas.md) at `d5cc44a` on `cursor/multi-tenant-saas-design-c887` (PR #224). That commit records CJ's decision: Aurora DSQL, and AppSync Events push on every visible screen, with the 2 s / 10 s polling kept only while the client is disconnected.

The copies attached with the T2 request are older than that commit. The uploaded design still recommends polling and has no decision log. The uploaded §3.1 still says the push choice is pending. This contract follows the branch. Where this document marks a rule **proposed**, the design does not state it; those items are repeated under [Open questions](#open-questions).

Issue #338 matches the T2 scope and acceptance criteria in the ticket list. There is no scope difference to resolve.

The Pi keeps SQLite, the SSE stream `GET /api/events`, the `pantry_session` cookie, and the `userId` parameter. None of §3.1 applies to it. `GET /api/config` tells each client which transport to use.

JSON field names are camelCase, matching the API the web client already calls. Design prose uses snake_case for the same ideas (`household_id`, `channel_key`). The camelCase choice is a working assumption; see [Open questions](#open-questions).

## Tenancy

tenant-rule: Nothing in this contract accepts a client-supplied tenant other than the X-Household-Id header, and that header is valid only when it names a household the signed-in user belongs to.

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

A household has at least one owner. The check runs in the same transaction as the demotion, removal, or account deletion. A write that would leave zero owners returns `last_owner`. If optimistic-concurrency retries are exhausted first, the response is `busy`.

Cognito owns sign-up, email verification, and password reset. This API does not take passwords. A session exists only after the email is verified. The post-confirmation trigger creates the `users` row. The household is created by `POST /api/households` after that.

## Endpoint index

endpoint-count: 36

Paths the design names are unmarked. Paths added so Frontend can call a complete flow are marked **proposed** in the detail sections (login redirect, household list, pairing completion, export and import job polls, `GET /api/config`, and the stock and list changes-since reads).

```text
endpoint-index
GET /api/auth/login
GET /api/auth/callback
POST /api/auth/logout
GET /api/auth/session
POST /api/households
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
POST /api/import
GET /api/import/{id}
DELETE /api/me
POST /api/live/ticket
GET /api/config
```

Routes the current server already serves, and that this contract does not redefine, keep today's JSON. On the hosted API they drop `userId` and require `X-Household-Id`. That includes history, restock, groups, providers, settings, and the shopping-list mutations. Their field list stays the one the handlers use today.

## Auth and session

### GET /api/auth/login

Web only. **Proposed** path: design §6 names callback, logout, and session.

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

**Proposed:** the server also calls Cognito `AdminUserGlobalSignOut` for that user, so a refresh token cannot mint a new session. Expo deletes its secure-store tokens after this returns.

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
| Request | `{ "name": string }` |
| Response | `201` `{ "id": string, "name": string, "role": "owner" }` |
| Errors | `unauthorized`, `validation` |

`name` is required, trimmed, and 1–80 characters. **Proposed** length. The restock-mode question (#296) is not this body. After create, the client calls the existing restock setting route with `X-Household-Id`. Whether sign-up must send that choice on this request is open.

### GET /api/households

**Proposed** path. Design §6 says create, get, rename, delete, and restore. The switcher (T20) needs the list. `GET /api/auth/session` already returns `memberships`. This route is the same list without the user object, so a household switcher does not depend on the session route's other fields.

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

Rename. **Proposed** role: owner. Design §5 does not say who may rename.

| | |
|---|---|
| Auth | Session |
| Role | Owner **proposed** |
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

What the caller must type is open. **Proposed:** the current household name, compared case-sensitively after trim.

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
| Role | Owner **proposed** (the same role that deletes) |
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

Remove that member. This rotates `channel_key`.

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required, equal to `{id}` |
| Request | Empty body |
| Response | `204` |
| Errors | `unauthorized`, `forbidden_role`, `not_found`, `last_owner`, `busy` |

Removing the last owner returns `last_owner`. **Proposed:** an owner who is not the last owner may remove themselves.

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

When `email` is present the server sends that URL through SES. The address is not a tenant.

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

The list does not include `code` or `url`. **Proposed:** the raw code is not recoverable after create.

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
| Errors | `unauthorized`, `validation`, `invite_expired`, `not_found`, `busy` |

**Proposed** mapping: a code that is past `expiresAt`, or already used, is `invite_expired`. A code that was revoked, or that was never issued, is `not_found`.

The new membership is `member`. The client then sends that `householdId` as `X-Household-Id`. Accepting does not switch any other household.

## Devices

Pairing code or QR, 10-minute expiry, one household per device. Owners revoke. The token is returned once and stored hashed.

### POST /api/devices/pair

Starts pairing from a signed-in client. **Proposed** role: member. Design §5 says owners revoke, and does not say who may start a code.

| | |
|---|---|
| Auth | Session |
| Role | Member **proposed** |
| X-Household-Id | Required |
| Request | Empty body |
| Response | `201` `{ "code": string, "qrPayload": string, "expiresAt": string }` |
| Errors | `unauthorized`, `validation`, `not_found`, `busy` |

`expiresAt` is 10 minutes from creation. `qrPayload` is the same code, for the QR. It does not contain a household id the device is expected to send back.

### POST /api/devices/pair/complete

**Proposed** path. The device calls this with the code and receives the bearer token. Design §6 names `/api/devices/pair` and does not name the device's half of the handshake.

| | |
|---|---|
| Auth | None. The code is the credential. |
| Role | None |
| X-Household-Id | Omit. The code is bound to the household that created it. |
| Request | `{ "code": string, "label"?: string }` |
| Response | `201` `{ "deviceId": string, "token": string }` |
| Errors | `validation`, `not_found`, `invite_expired` is not used here |

An unknown, revoked, or already used code is `not_found`. A code past its 10 minutes is `not_found` as well. **Proposed:** pairing does not reuse `invite_expired`, so a device cannot tell "expired" from "wrong". `label` is an optional display name, **proposed**.

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

Revoke. Owners only. This rotates `channel_key`.

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
  heartbeat?: boolean;        // proposed
}

interface DeviceScanResponse {
  id: string;
  status: 'accepted' | 'duplicate';
  cursor: string;             // scans cursor after the commit. Empty when this call was a heartbeat only.
}
```

Idempotency: a second request with the same device and the same `id` does not create a second scan. It returns `200` with `status: "duplicate"` and the original result. **Proposed:** if the barcode or `unitCount` differs from the stored request, the response is `conflict` and the stored scan is unchanged.

Heartbeat: every accepted scan updates that device's `lastSeenAt`. **Proposed:** `heartbeat: true` with no barcode updates `lastSeenAt` and does not enqueue a scan. A body with neither `barcode` nor `heartbeat: true` is `validation`.

The offline ring of 500 scans is device firmware. It is not a request field. Each stored scan is posted with its own `id`.

A committed scan publishes one `scans` hint after the final commit, on this device's household channel only.

## Scans and changes-since

Hosted scan routes are the current scan routes without `userId`. The household is `X-Household-Id`. Role for every scan route below: member. Auth: session.

Cursors are opaque strings, at most 128 ASCII characters, with no item names and no barcodes. The client sends the cursor back as `since` and does not parse it. The server may build it from `(household_id, updated_at)`. An omitted `since` is a full read of that screen's current rows. A `since` equal to the current cursor is `304` with an empty body. A `since` the server does not recognize is a full read, `200`, not an error.

When something changed, the `200` body is the full current set for that screen, not a patch. Rows that disappeared are absent. The client replaces its list.

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
| Request | `{ "barcode": string, "direction"?: "stock_in" \| "stock_out", "unitCount"?: number, "expiresAt"?: string }` |
| Response | `201` `HostedScanEntry` |
| Errors | `unauthorized`, `validation`, `not_found`, `busy`, `no_space` |

`barcode` is required. An omitted `direction` still takes the household's scanner mode, as it does today.

### GET /api/scans

| | |
|---|---|
| X-Household-Id | Required |
| Request | Query `since` (cursor) and `status` (`pending`, `flagged`, `committed`, `cancelled`, `qr`). `userId` is not a query parameter. |
| Response | `200` `{ "cursor": string, "scans": HostedScanEntry[] }`, or `304` when `since` is current |
| Errors | `unauthorized`, `validation`, `not_found` |

An omitted `status` returns the queue the Scan screen shows today (pending, flagged, and qr). **Proposed:** that default stays. `since` still applies on top of the status filter, and the cursor is for that filter.

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

A batch commit stays `200`. Per-item failures are `failedIds`, `failedReasons`, and `failedCodes`, parallel arrays, as today. A committed retry stays in `committedIds` and is not a `not_pending` failure. See the open question on `busy` inside a batch.

### POST /api/scans/batch-commit

Today's body, unchanged: `scanEntryIds`, optional `direction`, `unitCount`, `expiresAt`, and `commit`.

| | |
|---|---|
| X-Household-Id | Required. Every id must be in that household. |
| Response | `200` today's batch body (`updatedCount`, `committedIds`, `failedIds`, `failedReasons`, `failedCodes`, `stockOutCounts`) |
| Errors | `unauthorized`, `validation`, `not_found`, `busy`, `no_space` |

An id from another household is `not_found` for the whole request. **Proposed:** the request does not partially commit a mix of households, and it does not report the foreign id as a per-item failure.

### GET /api/inventory

Changes-since for hint type `stock`. The items are today's inventory JSON with `userId` removed.

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required |
| Request | Query `since`, and today's `q` |
| Response | `200` `{ "cursor": string, "items": InventoryItem[] }`, or `304` |
| Errors | `unauthorized`, `validation`, `not_found` |

### GET /api/shopping-list

Changes-since for hint type `list`. Entries are today's shopping-list JSON with `userId` removed.

| | |
|---|---|
| Auth | Session |
| Role | Member |
| X-Household-Id | Required |
| Request | Query `since` |
| Response | `200` `{ "cursor": string, "items": ShoppingListEntry[] }`, or `304` |
| Errors | `unauthorized`, `validation`, `not_found` |

Mutations of inventory and the shopping list stay on their current paths, with `userId` removed. After the final commit they publish `stock` or `list`.

## Export and import

The canonical file is JSON, `schemaVersion` 1. CSV is a spreadsheet view of the same export, not an import format. The hosted app imports the JSON only. The export is not a SQLite file.

A household id inside the file is not a tenant. Import writes into the household named by `X-Household-Id`. `legacyUserId` on a Pi export is recorded so the job can map `user-1` onto that household. Any other legacy user id is `validation`.

### Document, schema version 1

The normative TypeScript is `ExportDocumentV1` in `frontend/src/api/contract.ts`. The envelope is:

```ts
interface ExportDocumentV1 {
  schemaVersion: 1;
  importId: string;     // UUID minted at export. Idempotency key with the destination household.
  exportedAt: string;    // RFC3339
  source: 'hosted' | 'pi';
  householdName: string; // display only
  products: ExportProductV1[];
  barcodeOverrides: ExportBarcodeOverrideV1[];
  items: ExportItemV1[];
  itemInstances: ExportItemInstanceV1[];
  scanEntries: HostedScanEntry[];
  shoppingListItems: ExportShoppingListItemV1[];
  stockMoves: ExportStockMoveV1[];
  groups: ExportGroupV1[];
  categories: ExportCategoryV1[];
  settings: ExportSettingsV1;
}
```

`schemaVersion` other than `1` is `validation`. A top-level key not in this envelope is `validation`. Ids are preserved. Identity columns are `GENERATED BY DEFAULT`, so the import can keep them (design §2.1, §7). Nested fields are the ones on those TypeScript types. `settings` may be an empty object in version 1; its keys are an open question.

Collections and the identity they preserve:

| Key | Preserves | Notes |
|---|---|---|
| `products` | product id | Snapshot of name and the current product fields the household displayed. The global catalog is not dumped. |
| `barcodeOverrides` | barcode | Household overrides only, not global barcodes. |
| `items` | item id, product id | `userId` is absent. |
| `itemInstances` | instance id, item id | |
| `scanEntries` | scan id | Same shape as `HostedScanEntry`. |
| `shoppingListItems` | shopping-list id, item id | |
| `stockMoves` | move id | |
| `groups` | group id and member product ids | |
| `categories` | category id, item links, targets | |
| `settings` | the household settings row | Includes the restock-mode choice and the supply default. |

Not in version 1: memberships, invites, device tokens, push subscription secrets, and provider tokens (Kroger and any other OAuth ciphertext). After import, people re-invite, re-pair, and reconnect a store. Whether those belong in a later version is open.

CSV, **proposed:** two objects beside the JSON, `items.csv` (name, barcode, on-hand count, expires) and `shopping-list.csv` (name, quantity). The JSON remains the file the importer reads.

Equality for the round-trip test is equality of the version 1 collections after import, compared by id.

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

**Proposed** path, the poll for the job `POST /api/export` created. `{id}` is the job id, and the job must belong to `X-Household-Id`.

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

### POST /api/import

Owner. The destination household must be empty. Writes run in transactions of at most 1,000 rows. Idempotent per `(household_id, importId)`.

| | |
|---|---|
| Auth | Session |
| Role | Owner |
| X-Household-Id | Required. This is the destination. |
| Request | The `ExportDocumentV1` JSON body |
| Response | `202` `{ "jobId": string, "importId": string, "status": "pending" }` |
| Errors | `unauthorized`, `forbidden_role`, `validation`, `household_not_empty`, `not_found`, `busy`, `no_space` |

**Proposed:** empty means the household has no items, scan entries, shopping-list rows, groups, categories, or devices. The owner membership and the settings row created with the household are allowed.

A second POST with the same `importId` and the same household returns `202` with the original job and does not write again. A different `importId` when the household is no longer empty is `household_not_empty`.

**Proposed:** import is asynchronous, same job shape as export, because a Pi database does not fit one Lambda request under the 1,000-row cap. Design §7 calls import a job and calls export async. It does not say the HTTP call itself returns before the write finishes.

### GET /api/import/{id}

**Proposed** poll. Same auth as POST. `{id}` is the job id.

```ts
interface ImportJob {
  jobId: string;
  importId: string;
  status: 'pending' | 'ready' | 'failed';
}
```

`ready` means the version 1 collections match the file. Counts are not required in the body. **Proposed:** the body stays free of item names.

The Pi command `stockbot export` writes this same JSON locally. Uploading it is `POST /api/import`. There is no hosted route that reads the Pi's disk.

## Account deletion

### DELETE /api/me

Deletes the signed-in user: memberships, then the Cognito user (`AdminDeleteUser`). It does not delete a household that still has another owner.

| | |
|---|---|
| Auth | Session |
| Role | Signed in |
| X-Household-Id | Omit. The check covers every household the user owns. |
| Request | `{ "confirmation": string }` **proposed** |
| Response | `204` |
| Errors | `unauthorized`, `validation`, `confirmation_mismatch`, `last_owner`, `busy` |

The user cannot delete the account while they are the last owner of any household. They transfer ownership or delete that household first. The code is `last_owner`.

**Proposed** confirmation string: the account email, trimmed, case-insensitive. Design §9 states the last-owner rule and does not say the account delete is typed. Household delete is the one the design calls a typed confirmation.

## Push

Decided transport: AppSync Events on every visible screen. Polling is the fallback while the socket is disconnected. The Pi does not use this section.

### GET /api/config

**Proposed** as a hosted route. Design §3.1 gives it as the example the client uses to pick a transport.

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
  | { live: 'push'; appsync?: { httpUrl: string; realtimeUrl: string } };
```

The Pi returns `{ "live": "sse" }`. Hosted returns `{ "live": "push" }`. `poll` is reserved for a deployment that has no push. `appsync` is **proposed**: the design does not say how the client learns the AppSync domain. The client cannot open the socket without it.

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

- Both segments are at most 50 characters (AppSync's limit). A household id is a UUID (36). **Proposed:** `channel_key` is 32 URL-safe characters.
- `channel_key` lives in `household_settings` and rotates when a member is removed or a device is revoked.
- Subscribe is allowed only when the channel string equals the ticket's channel. No wildcards and no prefix match.
- Clients cannot publish. Publish is IAM, from the API and jobs roles, after the final successful commit.
- A failed publish does not fail the write. The client catches up on resync.

### Hint

```ts
interface LiveHint {
  type: 'scans' | 'stock' | 'list';
  cursor: string;
}
```

The JSON is under 1 KB. It has no item names, barcodes, quantities, or emails. A client that receives a hint refetches the matching changes-since route. The refetch is what enforces tenancy.

Publish covers scan ingest, confirm, edit, stock changes, list changes, and `stockbot-jobs` writes that change those three. Job completion for export and import is not one of these types; see open questions.

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
- On every connect, including a reconnect: `POST /api/live/ticket`, then one changes-since fetch, then subscribe.
- The server sends `ka` about every 60 s. If none arrives within 300 s, the client closes the socket.
- While the socket is disconnected, and the screen is still visible: poll every 2 s on the Scan Queue and every 10 s on any other visible screen. Do not poll while the socket is up.
- Reconnect backs off exponentially with jitter. An expired ticket is refreshed with a new `POST /api/live/ticket` and is not shown as an error.
- The subprotocols are `aws-appsync-event-ws` and `header-<base64url>`. The header carries the ticket for the Lambda authorizer. The exact header JSON key is open.

## Error model

Every error body is JSON:

```json
{ "error": "<plain sentence>", "code": "<stable code>" }
```

`error` is the sentence the household sees. `code` is what the client branches on. Logs may carry detail. The response does not.

A scan commit may also include `members` or `suggestions`, as today's 409 bodies do. Other codes do not.

generic-sentence: Something went wrong. Try again.

That sentence is `clientmsg.ServerError` on master. PR #320 (open, not merged) calls the same sentence `clientmsg.Generic` and uses it for every 5xx that is not `busy` or `no_space`. A 5xx body then has no `code`, except a commit or stock-out that already uses `internal`.

`busy` and `no_space` use the sentences PR #320 locks. On the hosted API, `busy` is also the response when an optimistic-concurrency conflict retries until the attempt budget is exhausted (design §6, T12). `no_space` is the response when the write cannot be stored. Neither body includes `Retry-After`.

PR #320 keeps a batch commit at `200`. A lock or a full disk on one of its items stays inside that body as `internal` and the generic sentence. Whether a hosted batch does the same for `busy` and `no_space` is open.

### Error code registry

The TypeScript union `HostedErrorCode` matches this table. A sentence marked **proposed, PM to confirm** is not in the product today.

| code | HTTP | sentence | notes |
|---|---|---|---|
| `busy` | 503 | Stockbot is busy right now. Try again in a moment. | Existing sentence. PR #320, not merged. Hosted also uses it when OCC retries are exhausted. |
| `no_space` | 503 | Stockbot is out of storage space, so this wasn't saved. | Existing sentence. PR #320, not merged. |
| `unauthorized` | 401 | Sign in to continue. | proposed, PM to confirm |
| `forbidden_role` | 403 | Only an owner can do that. | proposed, PM to confirm |
| `not_found` | 404 | That isn't available. | proposed, PM to confirm. Also the response for cross-household access. |
| `conflict` | 409 | That change conflicts with one that just landed. Refresh and try again. | proposed, PM to confirm |
| `invite_expired` | 409 | This invite has expired. Ask for a new one. | proposed, PM to confirm |
| `validation` | 400 | Check that and try again. | proposed, PM to confirm |
| `not_pending` | 400 | This scan was already handled. | Existing scan-queue sentence. Unchanged. |
| `product_unresolved` | 400 | Choose which item this is first. | Existing scan-queue sentence. Unchanged. |
| `none_on_hand` | 409 | Nothing on hand to take out. | Existing scan-queue sentence. Unchanged. |
| `instance_gone` | 409 | That one was already taken out. Pick another. | Existing scan-queue sentence. Unchanged. |
| `no_item` | 409 | Which item is this? | Existing scan-queue sentence. Unchanged. |
| `link_target_gone` | 409 | That item isn't available anymore. Pick another. | Existing scan-queue sentence. Unchanged. |
| `scan_not_found` | 404 | This scan is no longer in the queue. | Existing scan-queue sentence. Unchanged. |
| `direction_unset` | 400 | Choose stock in or stock out first. | Existing scan-queue sentence. Unchanged. |
| `internal` | 500 | Something went wrong. Try again. | Existing sentence, `clientmsg.ServerError`. Code kept on commit and stock-out. |
| `last_owner` | 409 | You're the last owner. Transfer ownership or delete the household first. | proposed, PM to confirm. Not a code design §6 names. |
| `household_not_empty` | 409 | This household already has items, so it can't take an import. | proposed, PM to confirm. Not a code design §6 names. |
| `confirmation_mismatch` | 400 | The confirmation doesn't match. | proposed, PM to confirm. Not a code design §6 names. |

## Open questions

These are the points the design leaves open. The working assumption used above is stated first so Frontend can mock against it.

1. **JSON casing.** This contract uses camelCase, like the current API. Design prose says `household_id` and `channel_key`. Confirm the wire format before T13.
2. **Session cookie name.** Not specified. It must not be the Pi's `pantry_session`.
3. **Who may rename a household.** Assumed owner.
4. **Who may start pairing.** Assumed any member. Revoke stays owner-only, which the design does say.
5. **Who may restore.** Assumed owner.
6. **An owner removing themselves** when another owner remains. Assumed allowed.
7. **Confirmation strings.** Household delete assumes the household name. Account delete assumes the email, and assumes a typed body even though §9 only requires that for household delete.
8. **Extra error codes.** `last_owner`, `household_not_empty`, and `confirmation_mismatch` are not in the §6 list. They exist so those three cases can each have a sentence. If PM wants one sentence per named code only, they fold into `conflict` or `validation` and the sentences here change.
9. **`conflict` vs those codes.** A same-second edit that is not "last owner" and not "household not empty" uses `conflict`. Confirm that split.
10. **Invite outcomes.** Expired or already used → `invite_expired`. Revoked or unknown → `not_found`. Confirm, including the HTTP status `409` for `invite_expired`.
11. **Pairing completion path** `POST /api/devices/pair/complete`, the optional `label`, and using `not_found` for an expired pairing code instead of `invite_expired`.
12. **Heartbeat shape.** Assumed a `heartbeat: true` body on `POST /api/device/scans`, plus `lastSeenAt` on every accepted scan. The design says the scan route has a heartbeat and does not show a body.
13. **Idempotent scan with a different barcode.** Assumed `conflict`. The design only says the key is `(device_id, id)`.
14. **Device scan response** does not include a household id. Confirm the device never needs to display or send one.
15. **Empty household.** Assumed: no items, scans, shopping-list rows, groups, categories, or devices. The owner row and the initial settings row are allowed.
16. **Import HTTP shape.** Assumed async `202` plus `GET /api/import/{id}`, by analogy with export. §7 says the import is a job. It does not say the POST waits.
17. **Export and import poll paths** are not named in §6. Nor is `GET /api/auth/login`, `GET /api/households`, or `GET /api/config`.
18. **CSV columns.** Assumed `items.csv` and `shopping-list.csv` only.
19. **What version 1 leaves out:** memberships, invites, device tokens, push subscriptions, provider tokens. Confirm those stay out. `settings` may be `{}`; the key list inside it is not in the design.
20. **Product snapshot.** Assumed the export copies the products the household references, not the shared catalog. Ties to the open catalog question in design §13.
21. **AppSync URLs** on `GET /api/config`. The client needs the realtime domain. The design doesn't say where it is published.
22. **Hint types** are only `scans`, `stock`, and `list`. History, restock, and export/import job completion have no hint. Assumed those screens refetch on the type they already show, and that job polls are ordinary GET polls.
23. **Batch commit and `busy` / `no_space`.** A single request uses `503`. PR #320 keeps a batch at `200` and reports a per-item lock as `internal`. Hosted OCC inside one batch item is not specified.
24. **Unrecognized changes-since cursor.** Assumed a full `200`, not `validation`.
25. **Default scan status filter** when `status` is omitted. Assumed today's queue (pending, flagged, qr).
26. **Foreign id inside a batch.** Assumed the whole request is `not_found`.
27. **Invite URL host.** The hosted domain is still TODO(CJ) in design §13. The server builds the URL. The client does not send a host.
28. **AppSync header JSON key** that carries the live ticket (`authorization` vs another name).
29. **Reconnect backoff numbers.** The design says exponential backoff with jitter and does not give the delays. The 2 s / 10 s polls and the 300 s `ka` timeout are specified; the backoff schedule is not.
30. **Sign-up restock question.** Assumed it stays on the existing restock settings route after `POST /api/households`, not in the create body.
31. **Pi auth.** Design §13 still asks whether the Pi gets this auth or keeps the shared password. This contract does not change the Pi either way.
32. **Grocy.** Design §13 is open. No Grocy route is added here. A Grocy token, if it stays, is a device-class token for one household and is not a client-supplied tenant.
