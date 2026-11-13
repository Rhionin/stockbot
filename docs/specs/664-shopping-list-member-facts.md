# 664 — Shopping-list group member facts

Contract for product facts on `ShoppingListEntry.group.members`, so the
"This trip, buy" picker can show thumbnails and one differentiator.

- **Issue:** [#664](https://github.com/Rhionin/stockbot/issues/664)
- **Architecture verdict:** [GO with required changes](https://github.com/Rhionin/stockbot/issues/664#issuecomment-6845954417)
- **Evidence:** master [`cf42666c`](https://github.com/Rhionin/stockbot/commit/cf42666ca51dead08b3a10191089d6064bb0eb3a) (`cf42666ca51dead08b3a10191089d6064bb0eb3a`). File:line citations below are on that SHA.
- This document is the frozen field contract and the **source of truth for builders**. Implementation starts after this docs-only PR merges. The **T1 acceptance tests** section is the binding, automatable test list.

## Non-goals

- No `variety` field (no stored source independent of `name`).
- No `storeProduct` field (no name or size on `store_availability`; multi-store availability stays on the entry per `docs/design/api-contract.md` and #509).
- No string `size` field (use `netAmount` / `netUnit` / `packCount`).
- No Pi image proxy, no store or Kroger images, no live provider calls or `store_checks` writes from list reads.
- No mobile UI (the same contract applies when a native list lands).
- No change to the swap URL or to the existing member fields `itemId`, `productId`, `name`.

## Shape

`ShoppingListGroupMember` is the JSON object on every `group.members` array, including `GET /api/shopping-list` and every response that already calls `fillGroupMembers` (swap, get-by-id, updates). Today's member is only `itemId`, `productId`, and `name` (`shared/types.ts:362-366`; `ShoppingListGroupMemberResponse` in `internal/server/handler_shopping_list.go:10-13`). Those three stay required and unchanged. The fields below are additive.

Go JSON tags must match the existing catalog and suggestion types (`internal/group/groups.go:133-141`; `internal/product/product.go:25`): `imageUrl`, `brand`, `netAmount`, `netUnit`, `packCount`, `lastStockedAt`, and `barcodes` use `omitempty`; `onHand` does **not**. After `scrubImageURLs` (`internal/server/image_scrub.go:46-48`, applied in `writeJSON` at `internal/server/handler_wrapper.go:157-158`) an empty or unsafe `imageUrl` is `""`, and `omitempty` then **omits the key**. The contract is the omitted key, not `"imageUrl":""`. That matches `SafeImageURL` (`internal/product/imageurl.go:13-30`) and the existing drop-private-URL test (`internal/server/handler_security_test.go:57-68`, which uses `json:"imageUrl,omitempty"` and the unsafe fixture `http://127.0.0.1/secret.jpg`).

```ts
interface ShoppingListGroupMember {
  itemId: string;
  productId: string;
  name: string;
  imageUrl?: string;          // SafeImageURL(products.image_url); key absent if empty or unsafe — never ""
  brand?: string;             // household_settings product_brand:{id} via BrandSQL; key absent if empty
  netAmount?: number;         // DisplayNetSize(products.net_base_value, net_dimension)
  netUnit?: 'oz' | 'fl oz';   // both keys absent together when unknown
  packCount?: number;         // products.pack_count; key absent when NULL or 0 (not 0)
  onHand: number;             // unremoved item_instances; always present, including 0
  lastStockedAt?: string;     // RFC3339; MAX(stock_in_events.at); key absent if never stocked
  barcodes?: string[];        // household barcodes, ORDER BY barcode; key absent when none (not [])
}
```

Exact omit / include JSON (keys listed are the complete member object):

- Empty optional fields, zero on hand — keys `imageUrl`, `brand`, `netAmount`, `netUnit`, `packCount`, `lastStockedAt`, and `barcodes` are all **absent**; `onHand` is present and `0`. Not `"barcodes":[]`, not `"packCount":0`, not `"imageUrl":""`, not `"brand":""`.

```json
{"itemId":"item-664-sparse","productId":"prod-664-sparse","name":"664 Sparse Product","onHand":0}
```

- `packCount` is absent when `products.pack_count` is NULL or `0`. It is present only when stored and greater than 0, e.g. `"packCount":2`. Same rule as `suggestionMembers` (`internal/group/groups.go:1704-1706`).
- `netAmount` and `netUnit` are both absent together when `DisplayNetSize` returns not-ok (`internal/product/netsize.go:272-283`: missing, `<= 0`, or unknown dimension). When known, both are present, e.g. `"netAmount":14.5,"netUnit":"oz"` (the existing group-test fixture `14.5*oz` at `internal/server/handler_shopping_group_test.go:14,19`).
- `barcodes` is absent when the household has no codes for that product. When present, it is a JSON array of full code strings in `ORDER BY barcode` ascending (`attachBarcodes`, `internal/group/groups.go:1735-1738`), never an empty array.
- `brand`, `imageUrl`, and `lastStockedAt` are absent when empty, unsafe, or never stocked.

### Semantics and sources

- **`imageUrl`:** Open Food Facts thumbnail already stored on the catalog product (`products.image_url`, migration `003_add_product_image_url.sql`). Run through `SafeImageURL` (`internal/product/imageurl.go:13-30`). The JSON name must be `imageUrl` so `scrubImageURLs` applies (`internal/server/image_scrub.go:46-48`). Clients render with existing `ProductPhoto` (`frontend/src/components/inventory/ProductPhoto.tsx:14-41`), 48×48 via `.shelf-photo` at 3rem (`frontend/src/components/inventory/shelf.css:202-205`), and its `onError` fallback. Remote HTTPS URL, not proxied through the Pi. CSP already allows `img-src 'self' https: data:` (`deploy/Caddyfile:24`, `deploy/Caddyfile.tunnel:32`). No store or Kroger image (none on this SHA; do not invent).
- **`brand`:** the household-typed brand label from `product.BrandSQL` — `household_settings` key `product_brand:{id}` (`internal/product/override.go:54-68`). Same source as product search. Not `memberBrand()` (`internal/group/member_facts.go:18-22`), which is derived from the display name and would fail the "only if not already in the name" rule. The client still skips this differentiator when the brand is already in `name`. Key absent if empty.
- **`netAmount` / `netUnit` / `packCount`:** `products.net_base_value` and `net_dimension` via `product.DisplayNetSize` (`internal/product/netsize.go:272-283`), same as `suggestionMembers` (`internal/group/groups.go:1698-1701`). `netUnit` is `'oz' | 'fl oz'`, matching `ProductSummary.netUnit` (`shared/types.ts:29-30`). The client formats with existing `formatMemberSize` (`frontend/src/components/groups/copy.ts:71-76`). Do not add a string `size` field (`ProductWriteInput.size` is a write-side string).
- **`onHand`:** count of unremoved `item_instances` joined to household `items`, same correlated subquery as `loadMembers` (`internal/group/groups.go:1316-1318`). Always present, including 0. Matches `GroupMember.onHand` (`internal/group/rule.go:30`, `json:"onHand"` with no omitempty; `shared/types.ts:472`). A Go `omitempty` on an `int` is wrong: 0 must appear so the client can show "0 on hand". A pre-this-change backend that omits the field means "unknown", not zero.
- **`lastStockedAt`:** last stock-in for that product in the household, `MAX(stock_in_events.at)` household-scoped, same as `loadMembers` (`internal/group/groups.go:1324`). Parse with `parseDBTime` (`internal/group/groups.go:1534-1558`) and serialize as an optional RFC3339 `*string`, same treatment as `purchasedAt` (`internal/server/handler_shopping_list.go:63-65`). Key absent if never stocked. The client formats "Bought Oct 3" (with year when not this year) per the UX issue.
- **`barcodes`:** full codes from the `barcodes` table, `WHERE household_id = ? AND product_id IN (...)`, same batch as `attachBarcodes` (`internal/group/groups.go:1735-1738`; household column from migrations `022b_household_columns.sql` and `024_household_keys.sql`). Key absent if empty. The client may show the last four digits of one code. Never include another household's rows.

**Omit from this contract:**

- **`variety`.** There is no stored variety column. `memberVariety` (`internal/group/member_facts.go:27-36`) extracts tokens from the display name, so it is always "already written in the name" and the UX differentiator would never show it. Do not add a dead field. The client chain skips variety until a stored source exists (a separate follow-up, if wanted).
- **`storeProduct`.** `store_availability` (`internal/app/migrations/030_multi_store.sql:59-74`) has UPC, status, stock level, and prices — not name or size. Multi-store already places `availability` (price and stock) on the **entry**, not on group members (`docs/design/api-contract.md:1780-1789`, `Availability` at `:1668-1672`). Adding `{name, size}` here would invent a shape that collides with #509 / #481 and has no source on this SHA. Size fallback waits until a check-cache field exists.

## Fill rules

- Extend `fillGroupMembers` (`internal/shopping/shopping.go:580-629`). It is already called from `ListManualItems` (`:285`), `ListUnpurchased` (`:321`), and `getByID` (`:344`), so GET, swap, and single-item responses stay consistent. One household-scoped query for catalog columns, `BrandSQL`, and the same on-hand and last-stocked correlated subqueries `loadMembers` already uses (`internal/group/groups.go:1314-1335`). One additional batched query for barcodes (`attachBarcodes` pattern: `WHERE household_id = ? AND product_id IN (...)`). No per-member queries. No N+1.
- `DisplayNameSQL` stays as today (`internal/product/override.go:19-26`): one household placeholder per use (SELECT and ORDER BY). `BrandSQL` adds one more household placeholder (`override.go:61-68`). Bind counts must stay correct.
- Every join that carries household data is scoped by `household_id` (group members, items, instances, settings, barcodes, `stock_in_events`, display-name overrides). The cross-household leak test in **T1 acceptance tests** is the required proof.
- Member order in `group.members` is `ORDER BY` the household display name (`DisplayNameSQL("p")`), then `i.id` (`internal/shopping/shopping.go:594-601`). That is today's order; T1 must pin it.
- `GET /api/shopping-list` (`internal/server/handler_shopping_list_get.go:33-47`) and other `fillGroupMembers` callers must not call Kroger, must not write or start `store_checks`, and must not trigger a provider refresh. Today's GET is a local snapshot: pantry, list, ledger, `LoadLineStoreContext` SQL (`internal/store/linestore.go:20-53`), and a credentials `providerGate` (`internal/server/shopping_line_store.go:62-87`). Keep it that way. The no-side-effect lock in **T1 acceptance tests** follows AGENTS.md **Negative-path locks (guards and refusals)** (`AGENTS.md:53-58`).

## T1 acceptance tests

This section is the binding test list. Every case is an HTTP-boundary assertion against the implemented contract, on fixtures with the exact values below. Synthetic ids and labels only; no household data. Tests use `app.RunMigrations` (`AGENTS.md:73`). A case is not done if a weaker fixture (missing config, no second household, no unsafe URL) would still pass.

### 1. Cross-household leak

Two households, A and B, share one catalog product `prod-664` (name `664 Contract Product`). Each household has its own pantry item, group, and shopping-list auto line for that product, so `group.members` is filled.

Household A (the caller):

- Brand override `household_settings` key `product_brand:prod-664` = `Alpha Brand` (`BrandSQL`, `internal/product/override.go:54-68`).
- Barcodes on `prod-664` for A: `111111111111` and `999999999999` (insert in any order; query is `ORDER BY barcode` at `internal/group/groups.go:1735-1738`).
- Two unremoved `item_instances` for A's item (`onHand` 2), household-scoped as `loadMembers` (`internal/group/groups.go:1316-1318`).
- One `stock_in_events` row for `prod-664` in A at `2026-01-15T12:00:00Z`.

Household B (must not leak), same `prod-664`:

- Brand override = `Bravo Brand`.
- Barcode `555555555555` (sorts between A's two codes, so a missing `household_id` predicate would insert it in the middle).
- Seven unremoved `item_instances` (`onHand` 7).
- One `stock_in_events` row at `2026-06-01T00:00:00Z`.

`GET /api/shopping-list` (no `since`) as household A, and the `200` body of `POST /api/shopping-list/items/{id}/swap` as A (swap to another of A's own members so the response still includes this product), must contain a member whose JSON includes exactly:

```json
{"itemId":"item-664-a","productId":"prod-664","name":"664 Contract Product","brand":"Alpha Brand","onHand":2,"lastStockedAt":"2026-01-15T12:00:00Z","barcodes":["111111111111","999999999999"]}
```

(`item-664-a` is A's pantry item id. Additional optional keys such as `imageUrl` / net size may appear if the catalog row has them; the keys above are required and exact.)

Assert all of:

- `brand` is `Alpha Brand`, never `Bravo Brand`.
- `barcodes` is exactly `["111111111111","999999999999"]` in that order, and does not contain `555555555555`.
- `onHand` is `2`, never `7` or `9`.
- `lastStockedAt` is `2026-01-15T12:00:00Z`, never `2026-06-01T00:00:00Z`.
- The raw response body does not contain `Bravo Brand`, `555555555555`, or `2026-06-01`.

A fixture that only asserts "B's barcode is not mentioned" without giving B matching brand, barcode, and stock on the same product is not this test.

### 2. Scrubbed `imageUrl` — exact JSON

Use the same unsafe URL as `TestWriteJSONDropsPrivateImageURL` (`internal/server/handler_security_test.go:57-68`) and `TestSafeImageURL` (`internal/product/imageurl_test.go:14`): store `products.image_url = "http://127.0.0.1/secret.jpg"` on `prod-664-img`. `SafeImageURL` returns `""` (`internal/product/imageurl.go:13-30`; loopback case at `imageurl_test.go:14`). `scrubImageURLs` writes that empty string onto the `imageUrl` field (`internal/server/image_scrub.go:46-48`) before `json.Marshal` (`handler_wrapper.go:157-174`). With `json:"imageUrl,omitempty"`, the key is **absent**, not `"imageUrl":""`.

For a sparse member of that product with `onHand` 0 and no other optional facts, the member object is exactly:

```json
{"itemId":"item-664-img","productId":"prod-664-img","name":"664 Image Product","onHand":0}
```

Assert the member has no `imageUrl` key (`imageUrl` path absent), and the raw body does not contain `127.0.0.1` or `secret.jpg`. A test that only checks a safe HTTPS URL is present does not lock the refusal.

Separately, a safe catalog URL `https://images.openfoodfacts.org/thumb.jpg` (kept by `SafeImageURL`, `imageurl_test.go:12`) is present as `"imageUrl":"https://images.openfoodfacts.org/thumb.jpg"`.

### 3. No provider HTTP and no `store_checks` write (negative-path lock)

Follow AGENTS.md **Negative-path locks (guards and refusals)** (`AGENTS.md:53-58`): the fixture is one where a just-in-time provider product check or `store_checks` write **would otherwise succeed** if GET or swap started one. A fixture with no credentials, no linked store, or no HTTP fake is not a lock — it would still pass if the guard were deleted (`AGENTS.md:55,58`).

Required fixture, all present at once:

- The household has a saved Kroger app and a connected `provider_connections` row with tokens (and product-access scope, so a check is allowed).
- A store linked to Kroger (`stores.linked_provider = 'kroger'` with a `location_id`).
- A grouped shopping-list line whose product has a household barcode, so a product lookup would have something to send.
- An `http.RoundTripper` test double is the provider HTTP client (same pattern as `fakeRoundTripper` in `internal/cart/kroger/kroger_test.go:16`), with a call counter starting at 0. Any outbound provider HTTP increments it.
- One existing `store_checks` row for that household and store (`state = 'done'`), so a new write is visible. Record `COUNT(*)` and the row's `state`, `started_at`, `finished_at`, `lease_until`, `checked_count`, and `found_count` before the request.

On `GET /api/shopping-list` and on `POST /api/shopping-list/items/{id}/swap` (a successful same-household swap), assert:

- `SELECT COUNT(*) FROM store_checks` for that household is **identical** before and after (not merely "zero").
- The seeded row's fields listed above are unchanged.
- The provider HTTP fake's call count is **0**.

Sanity: if the implementation started a check or issued a provider request, COUNT or the fake counter would change and the test would fail (`AGENTS.md:56,58`). Apply the same lock to any other `fillGroupMembers` HTTP path under test (swap already returns `GetItemByID`, `shopping.go:344` and `handler_shopping_swap.go:42-50`). Direct DB reads of `store_checks` are the last-resort observable for a table no list endpoint exposes (`AGENTS.md:51`).

### 4. Omitted-empty fields and `onHand: 0`

One GET (or swap) response covers these exact member objects (separate members or separate cases):

- Sparse zero-on-hand member: exactly the JSON in Shape, `{"itemId":"item-664-sparse","productId":"prod-664-sparse","name":"664 Sparse Product","onHand":0}`. `onHand` is present and equal to `0`. Keys `imageUrl`, `brand`, `netAmount`, `netUnit`, `packCount`, `lastStockedAt`, `barcodes` are **absent**.
- `pack_count` NULL and a second product with `pack_count = 0`: `packCount` absent in both. A third product with `pack_count = 2`: `"packCount":2`.
- Unknown net size: both `netAmount` and `netUnit` absent. Known 14.5 oz: both present, `"netAmount":14.5,"netUnit":"oz"`.
- Empty brand, never stocked: `brand` and `lastStockedAt` absent.
- No household barcodes: `barcodes` absent, not `"barcodes":[]`.

### 5. Existing behavior to pin (today's handlers, unchanged)

These are today's responses; T1 must keep them. None of this is a behavior change. There is no `GET /api/shopping-list/items/{id}` route (`internal/server/server.go:663-669`).

- **Empty list.** `GET /api/shopping-list` with no `since`, household has no lines: `200` and body exactly `[]`. `changeView.RawJSON` marshals `Items` alone when `Since` is empty (`internal/server/change_view.go:26-28`); the handler substitutes an empty slice for a nil `items` (`handler_shopping_list_get.go:51-53`). Already locked by `TestShoppingListGet` "empty list when no items" (`handler_shopping_list_get_test.go:14-22`).
- **Not-found swap.** `POST /api/shopping-list/items/missing-line/swap` with body `{"itemId":"item-664-a"}`: `404`, body exactly `{"error":"shopping list item not found","code":"not_found"}`. `SwapAutoLine` returns `ErrItemNotFound` when `getByID` finds nothing in this household (`internal/shopping/shopping.go:22,510-515`); the handler maps it through `NotFound` (`handler_shopping_swap.go:34-35`; `handler_errors.go:89-90`). Same status, message, and code for a foreign household's **line** id (scope is household-only). Already partly locked at `handler_shopping_group_test.go:265-284` (status and `"not found"` substring); T1 asserts the exact JSON. `PATCH` / `DELETE` of a missing id use the same `NotFound` (`handler_shopping_list_item_update.go:34-35`, `handler_shopping_list_item_delete.go:25-26`).
- **Unauthorized — no session.** Under Stage 1 sessions (`WithStage1`, `internal/server/stage1_routes.go:19-26`), `GET /api/shopping-list` and `POST /api/shopping-list/items/{id}/swap` with no session cookie and no bearer token: `401`, body exactly `{"error":"Sign in to continue.","code":"unauthorized"}`. `requiresPrincipal` is true for `/api/shopping-list` (`stage1.go:344-356`); `resolve` writes `unauthorizedSentinel` (`stage1.go:89-91,163-164`); `writeStage1` uses `auth.SentenceUnauthorized` and `auth.CodeUnauthorized` (`stage1.go:393-411`; `internal/auth/errors.go:13,35`). T1 must run this on the Stage 1 stack. The Pi handler harness without Stage 1 does not require a session (`runHandlerTests`, `internal/server/test_runner_test.go:111-114`) and is not this case.
- **Unauthorized — non-member.** Under Stage 1, a signed-in account that is **not** a member of household B, requesting `GET /api/shopping-list` or swap with `X-Household-Id` set to B: `404`, body exactly `{"error":"Stockbot couldn't find that. It may have been removed.","code":"not_found"}`. `bindHousehold` treats `Membership` `ok == false` as `auth.CodeNotFound` / `auth.SentenceNotFound` (`stage1.go:215-222`; `auth/errors.go:19,38`). This is 404, not 403. Today's behavior is correct (cross-household access is `not_found` per `docs/design/api-contract.md`); pin it, do not change it.
- **Swap of another household's `itemId`.** Household A has a grouped line. The swap body names B's pantry item id for the same catalog product (`item-664-b`), which is not a member of A's group under A's `household_id`. `SwapAutoLine` counts `product_group_members` ⋈ `items` with `m.household_id = ?` (`internal/shopping/shopping.go:522-532`); `n == 0` returns `ErrNotGroupMember` (`:501-502`, message `Choose a product in this group.`). The handler returns HTTP 422 with that message and no explicit `ErrorCode` (`handler_shopping_swap.go:36-37`); `writeStatusError` fills the default for 422, `validation` (`handler_wrapper.go:293-294`; `defaultClientCode` at `handler_errors.go:174-177`). Exact response: `422`, body exactly `{"error":"Choose a product in this group.","code":"validation"}`. The line's `item_id` is unchanged (assert with a following GET). This is the same status, code, and message as swapping a same-household product outside the group (`handler_shopping_group_test.go:295-316`). Today's behavior is correct; pin it. (The missing `ErrorCode` on the handler is filled by the 422 default; not a #664 change.)
- **Member order.** `group.members` is ordered by household display name (`DisplayNameSQL("p")`, `internal/product/override.go:19-26`), then pantry `i.id` (`internal/shopping/shopping.go:594-601`). Required test: a group whose display names are `Aspen 664` (`item-664-aspen`) and `Cedar 664` (`item-664-cedar`) returns members in that name order (`Aspen` then `Cedar`). A second group whose two members share the display name `Same 664` and have item ids `item-664-m` and `item-664-z` returns `item-664-m` then `item-664-z` (lexicographic `i.id`). Assert the exact `itemId` array. Barcode order inside a member is pinned in test 1.

## Coordination

- **#509** (grouped shopping-list / zero-stores restyle): when #509 restyles the grouped row, it should consume this member contract and the #664 radio+thumbnail+differentiator treatment, not a name-only `NativeSelect` (`frontend/src/components/shopping/ShoppingListPage.tsx:450-457`). Do not block #509 on `storeProduct`. Entry-level `store` / `availability` stay on the #481 / #509 contract (`docs/design/api-contract.md:1770-1789`). #545 (one Kroger `provider_connections` row per household) is not a store-product source. This ticket does not add store fields on members.
- **#632** (quantity save blanks the table via `loadShoppingList`): clients must refresh the list after swap without the full-page `loading` path. `loadShoppingList` sets `loading` and unmounts the table (`ShoppingListPage.tsx:123-132`). Swap already uses `refresh()` (`:261-266`) and stays `POST /api/shopping-list/items/{id}/swap` (`frontend/src/api/client.ts:530-534`). The new radio list must not call `loadShoppingList`. The server GET stays a local read.

## Backwards compatibility

Additive. Older clients ignore unknown fields. Mobile has no shopping-list consumer on this SHA. New `onHand` is required only once the backend implements this contract; a client talking to a pre-change backend should treat a missing `onHand` as "skip that differentiator", never as 0.

Payload is URL strings and a few barcodes on a household-scale list — fine for the Pi. Do not proxy thumbnails, and do not embed image bytes.

## Implementation split

Builders start only after this document merges.

- **T1 — Backend.** Extend `GroupChoice` (`internal/shopping/shopping.go:24-29`), `ShoppingListGroupMemberResponse` (`internal/server/handler_shopping_list.go:10-13`), `shared/types.ts` `ShoppingListGroupMember` (`:362-366`), and document the shape in `docs/design/api-contract.md` under `GET /api/shopping-list` (`:1266-1276`). Implement in `fillGroupMembers`. **Acceptance is the T1 acceptance tests section above**, in full: cross-household leak with the stated fixture and exact JSON; scrubbed `imageUrl` key-absent JSON; negative-path `store_checks` COUNT and zero provider HTTP fake calls on GET and swap; omitted-empty exact JSON including `onHand: 0`; empty list, not-found, unauthorized (no session and non-member), foreign `itemId` swap, and member order. CI green.
- **T2 — Frontend (web).** Depends on T1. Replace `NativeSelect` with the inline radio list per the UX issue; `ProductPhoto` 48×48; at most one differentiator, only when thumbnails collide and names collide (case-fold); priority: `formatMemberSize`, brand (if not already in the name), last bought, `n on hand` (including 0), barcode tail last 4, else the fallback sentence. No variety, no `storeProduct` fallback. Swap stays `POST /api/shopping-list/items/{id}/swap` then `refresh()`, never `loadShoppingList`; the table must not unmount. Contract tests for the new member fields. Mobile is out of scope. New user-visible strings still need CJ approval via Manager / UX as the issue states.

Until T1 lands, inventory `item.product.imageUrl` / net size (`ProductSummary`, `shared/types.ts:14-32`) can temporarily fill thumbnails if Frontend starts in parallel; that is not the permanent path.
