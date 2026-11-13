# 664 — Shopping-list group member facts

Contract for product facts on `ShoppingListEntry.group.members`, so the
"This trip, buy" picker can show thumbnails and one differentiator.

- **Issue:** [#664](https://github.com/Rhionin/stockbot/issues/664)
- **Architecture verdict:** [GO with required changes](https://github.com/Rhionin/stockbot/issues/664#issuecomment-6845954417)
- **Evidence:** master [`cf42666c`](https://github.com/Rhionin/stockbot/commit/cf42666ca51dead08b3a10191089d6064bb0eb3a) (`cf42666ca51dead08b3a10191089d6064bb0eb3a`). File:line citations below are on that SHA.
- This document is the frozen field contract and the **source of truth for builders**. Implementation starts after this docs-only PR merges.

## Non-goals

- No `variety` field (no stored source independent of `name`).
- No `storeProduct` field (no name or size on `store_availability`; multi-store availability stays on the entry per `docs/design/api-contract.md` and #509).
- No string `size` field (use `netAmount` / `netUnit` / `packCount`).
- No Pi image proxy, no store or Kroger images, no live provider calls or `store_checks` writes from list reads.
- No mobile UI (the same contract applies when a native list lands).
- No change to the swap URL or to the existing member fields `itemId`, `productId`, `name`.

## Shape

`ShoppingListGroupMember` is the JSON object on every `group.members` array, including `GET /api/shopping-list` and every response that already calls `fillGroupMembers` (swap, get-by-id, updates). Today's member is only `itemId`, `productId`, and `name` (`shared/types.ts:362-366`; `ShoppingListGroupMemberResponse` in `internal/server/handler_shopping_list.go:10-13`). Those three stay required and unchanged. The fields below are additive.

```ts
interface ShoppingListGroupMember {
  itemId: string;
  productId: string;
  name: string;
  imageUrl?: string;          // SafeImageURL(products.image_url); omit if empty or unsafe
  brand?: string;             // household_settings product_brand:{id} via BrandSQL; omit if empty
  netAmount?: number;         // DisplayNetSize(products.net_base_value, net_dimension)
  netUnit?: 'oz' | 'fl oz';   // omit together with netAmount when unknown
  packCount?: number;         // products.pack_count; omit unless stored and > 0
  onHand: number;             // unremoved item_instances; always present, including 0
  lastStockedAt?: string;     // RFC3339; MAX(stock_in_events.at); omit if never stocked
  barcodes?: string[];        // household barcodes for the product; omit if empty
}
```

### Semantics and sources

- **`imageUrl`:** Open Food Facts thumbnail already stored on the catalog product (`products.image_url`, migration `003_add_product_image_url.sql`). Run through `SafeImageURL` (`internal/product/imageurl.go:13-30`). The JSON name must be `imageUrl` so `scrubImageURLs` applies (`internal/server/image_scrub.go:46-48`). Clients render with existing `ProductPhoto` (`frontend/src/components/inventory/ProductPhoto.tsx:14-41`), 48×48 via `.shelf-photo` at 3rem (`frontend/src/components/inventory/shelf.css:202-205`), and its `onError` fallback. Remote HTTPS URL, not proxied through the Pi. CSP already allows `img-src 'self' https: data:` (`deploy/Caddyfile:24`, `deploy/Caddyfile.tunnel:32`). No store or Kroger image (none on this SHA; do not invent).
- **`brand`:** the household-typed brand label from `product.BrandSQL` — `household_settings` key `product_brand:{id}` (`internal/product/override.go:54-68`). Same source as product search. Not `memberBrand()` (`internal/group/member_facts.go:18-22`), which is derived from the display name and would fail the "only if not already in the name" rule. The client still skips this differentiator when the brand is already in `name`. Omit if empty.
- **`netAmount` / `netUnit` / `packCount`:** `products.net_base_value` and `net_dimension` via `product.DisplayNetSize` (`internal/product/netsize.go:272-283`), same as `suggestionMembers` (`internal/group/groups.go:1698-1701`). `netUnit` is `'oz' | 'fl oz'`, matching `ProductSummary.netUnit` (`shared/types.ts:29-30`). `packCount` comes from `products.pack_count` and is omitted unless stored and greater than 0 (`groups.go:1704-1706`). The client formats with existing `formatMemberSize` (`frontend/src/components/groups/copy.ts:71-76`). Do not add a string `size` field (`ProductWriteInput.size` is a write-side string).
- **`onHand`:** count of unremoved `item_instances` joined to household `items`, same correlated subquery as `loadMembers` (`internal/group/groups.go:1316-1318`). Always present, including 0. Matches `GroupMember.onHand` (`internal/group/rule.go:30`, `json:"onHand"` with no omitempty; `shared/types.ts:472`). A Go `omitempty` on an `int` is wrong: 0 must appear so the client can show "0 on hand". A pre-this-change backend that omits the field means "unknown", not zero.
- **`lastStockedAt`:** last stock-in for that product in the household, `MAX(stock_in_events.at)` household-scoped, same as `loadMembers` (`internal/group/groups.go:1324`). Parse with `parseDBTime` (`internal/group/groups.go:1534-1558`) and serialize as an optional RFC3339 `*string`, same treatment as `purchasedAt` (`internal/server/handler_shopping_list.go:63-65`). Omit if never stocked. The client formats "Bought Oct 3" (with year when not this year) per the UX issue.
- **`barcodes`:** full codes from the `barcodes` table, `WHERE household_id = ? AND product_id IN (...)`, same batch as `attachBarcodes` (`internal/group/groups.go:1735-1738`; household column from migrations `022b_household_columns.sql` and `024_household_keys.sql`). Omit if empty. The client may show the last four digits of one code. Never include another household's rows.

**Omit from this contract:**

- **`variety`.** There is no stored variety column. `memberVariety` (`internal/group/member_facts.go:27-36`) extracts tokens from the display name, so it is always "already written in the name" and the UX differentiator would never show it. Do not add a dead field. The client chain skips variety until a stored source exists (a separate follow-up, if wanted).
- **`storeProduct`.** `store_availability` (`internal/app/migrations/030_multi_store.sql:59-74`) has UPC, status, stock level, and prices — not name or size. Multi-store already places `availability` (price and stock) on the **entry**, not on group members (`docs/design/api-contract.md:1780-1789`, `Availability` at `:1668-1672`). Adding `{name, size}` here would invent a shape that collides with #509 / #481 and has no source on this SHA. Size fallback waits until a check-cache field exists.

## Fill rules

- Extend `fillGroupMembers` (`internal/shopping/shopping.go:580-629`). It is already called from `ListManualItems` (`:285`), `ListUnpurchased` (`:321`), and `getByID` (`:344`), so GET, swap, and single-item responses stay consistent. One household-scoped query for catalog columns, `BrandSQL`, and the same on-hand and last-stocked correlated subqueries `loadMembers` already uses (`internal/group/groups.go:1314-1335`). One additional batched query for barcodes (`attachBarcodes` pattern: `WHERE household_id = ? AND product_id IN (...)`). No per-member queries. No N+1.
- `DisplayNameSQL` stays as today (`internal/product/override.go:19-26`): one household placeholder per use (SELECT and ORDER BY). `BrandSQL` adds one more household placeholder (`override.go:61-68`). Bind counts must stay correct.
- Every join that carries household data is scoped by `household_id` (group members, items, instances, settings, barcodes, `stock_in_events`, display-name overrides). Tests must show another household's brand, barcode, or stock is not leaked. Products catalog (`image_url`, net size) is the shared catalog, same as suggestions and inventory.
- `GET /api/shopping-list` (`internal/server/handler_shopping_list_get.go:33-47`) and other `fillGroupMembers` callers must not call Kroger, must not write or start `store_checks`, and must not trigger a provider refresh. Today's GET is a local snapshot: pantry, list, ledger, `LoadLineStoreContext` SQL (`internal/store/linestore.go:20-53`), and a credentials `providerGate` (`internal/server/shopping_line_store.go:62-87`). Keep it that way.

## Coordination

- **#509** (grouped shopping-list / zero-stores restyle): when #509 restyles the grouped row, it should consume this member contract and the #664 radio+thumbnail+differentiator treatment, not a name-only `NativeSelect` (`frontend/src/components/shopping/ShoppingListPage.tsx:450-457`). Do not block #509 on `storeProduct`. Entry-level `store` / `availability` stay on the #481 / #509 contract (`docs/design/api-contract.md:1770-1789`). #545 (one Kroger `provider_connections` row per household) is not a store-product source. This ticket does not add store fields on members.
- **#632** (quantity save blanks the table via `loadShoppingList`): clients must refresh the list after swap without the full-page `loading` path. `loadShoppingList` sets `loading` and unmounts the table (`ShoppingListPage.tsx:123-132`). Swap already uses `refresh()` (`:261-266`) and stays `POST /api/shopping-list/items/{id}/swap` (`frontend/src/api/client.ts:530-534`). The new radio list must not call `loadShoppingList`. The server GET stays a local read.

## Backwards compatibility

Additive. Older clients ignore unknown fields. Mobile has no shopping-list consumer on this SHA. New `onHand` is required only once the backend implements this contract; a client talking to a pre-change backend should treat a missing `onHand` as "skip that differentiator", never as 0.

Payload is URL strings and a few barcodes on a household-scale list — fine for the Pi. Do not proxy thumbnails, and do not embed image bytes.

## Implementation split

Builders start only after this document merges.

- **T1 — Backend.** Extend `GroupChoice` (`internal/shopping/shopping.go:24-29`), `ShoppingListGroupMemberResponse` (`internal/server/handler_shopping_list.go:10-13`), `shared/types.ts` `ShoppingListGroupMember` (`:362-366`), and document the shape in `docs/design/api-contract.md` under `GET /api/shopping-list` (`:1266-1276`). Implement in `fillGroupMembers`. Tests: `onHand: 0` is present; missing image, brand, net size, or stock is omitted; two household barcodes; other-household brand, barcode, or stock is not leaked; `imageUrl` is scrubbed; GET (and a swap response) performs no provider HTTP and does not write `store_checks`. CI green.
- **T2 — Frontend (web).** Depends on T1. Replace `NativeSelect` with the inline radio list per the UX issue; `ProductPhoto` 48×48; at most one differentiator, only when thumbnails collide and names collide (case-fold); priority: `formatMemberSize`, brand (if not already in the name), last bought, `n on hand` (including 0), barcode tail last 4, else the fallback sentence. No variety, no `storeProduct` fallback. Swap stays `POST /api/shopping-list/items/{id}/swap` then `refresh()`, never `loadShoppingList`; the table must not unmount. Contract tests for the new member fields. Mobile is out of scope. New user-visible strings still need CJ approval via Manager / UX as the issue states.

Until T1 lands, inventory `item.product.imageUrl` / net size (`ProductSummary`, `shared/types.ts:14-32`) can temporarily fill thumbnails if Frontend starts in parallel; that is not the permanent path.
