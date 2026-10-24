# API errors

## 503 busy

A request that waits for the pantry database and runs out of time is HTTP 503.
Any route that reads or writes the database can return it. The failure is
retryable. The response code is `busy` and the message is
`Stockbot is busy right now. Try again in a moment.`

```json
{"error":"Stockbot is busy right now. Try again in a moment.","code":"busy"}
```

That sentence is the only busy sentence. The response does not include a
`Retry-After` header.

## 503 no space

When SQLite cannot save a change because the disk is full, the status is also
503. The code is `no_space` and the message is
`Stockbot is out of storage space, so this wasn't saved.`

```json
{"error":"Stockbot is out of storage space, so this wasn't saved.","code":"no_space"}
```

This response does not include a `Retry-After` header.

Each of these responses also writes one warning log line. The message is
`db_no_space`. The attributes are `event=db_no_space`, `method`, `route`,
`sqlite_code` (the driver result code, not the driver text), and
`household_id` when the server knows the household. The line has no SQL and
no names. Operator alerts for #224 match that line. This does not add an alert.

## Batch commit

`POST /api/scans/batch-commit` stays HTTP 200 when an item cannot be saved.
That is still true when the body includes `direction`, `unitCount`, or
`expiresAt`. `failedIds`, `failedReasons`, and `failedCodes` stay parallel
arrays. Empty fields are omitted: an empty `committedIds`, `failedIds`,
`failedReasons`, `failedCodes`, or `stockOutCounts` is left out of the JSON.

An unknown scan id is `scan_not_found` on a confirm and on an update-only
request. The other ids are updated, and a confirm commits them, as they
would be without the unknown id. When an update-only write cannot start
because the database is locked or full, every id in that request gets
`busy` or `no_space`, including an unknown id. Nothing is read after that
write fails.

`committedIds` means committed, or confirmed as already committed, by this
request. An id this request did not read is not in that list.

A locked database is code `busy` and the busy sentence above. A full disk is
code `no_space` and the no-space sentence above. A full disk writes one
`db_no_space` warning for the whole batch, with the same attributes as the
503 above, not one line per item. A batch that ends `busy` writes one
`db_busy` warning for the whole batch. The message is `db_busy`. The
attributes are `event=db_busy`, `method`, `route`, `sqlite_code`, and
`household_id` when the server knows the household. The line has no SQL and
no names. It is one line for the batch, not one line per item.

The batch stops on the first `busy` or `no_space` result. That item and every
item not yet tried get the same code and the same sentence. This includes an
id that was already committed by something else: the request does not read it
to check. The status of an item this request did not commit stays pending,
flagged, or already committed, so a retry does not commit it twice. On a
confirm, an item not yet tried is not written. A retry lists an id that was
already committed in `committedIds`. Items this request committed before the
stop stay committed and are listed in `committedIds`. On a full disk a field
edit can still persist on the card whose stock commit then failed. On an
update-only request the field write is one transaction before those per-id
reads. If that write succeeds and a later read is `busy` or `no_space`, that
id and the later ids get that code even though their fields were already
saved. Other per-item failures do not stop the batch.

A client shows that sentence once. When some items were committed first, the
line leads with the committed count and "confirmed.", then the sentence. Two
committed items and a lock read:
`2 confirmed. Stockbot is busy right now. Try again in a moment.`

## 503 sign-in not configured

`GET /api/session` and `POST /api/login` return HTTP 503 when the public
hostname requires a password and none is configured. The code is
`auth_not_configured` and the message is `Sign-in isn't set up yet.`

```json
{"error":"Sign-in isn't set up yet.","code":"auth_not_configured"}
```

This response does not include a `Retry-After` header.

## Other 5xx

Every other 5xx uses the generic sentence
`Something went wrong. Try again.` The code is `internal`. That is the
same code an unexpected commit or stock-out failure already uses, and the
same sentence. A lock or a full disk on a single commit or stock-out is
still `busy` or `no_space`. A batch commit stays HTTP 200 and reports
`busy`, `no_space`, or `internal` on each affected item.

`updating`, `provider_unavailable`, and `auth_not_configured` are not this
generic response. Sign-in that is not configured is the 503 above.

The generic sentence does not replace the busy sentence, the no-space
sentence, the updating sentence, the provider sentence, or the sign-in
sentence. A 5xx whose handler set some other message still answers with
this sentence. The detail stays in the server log.

## 4xx

A 4xx stays a 4xx when that 4xx is already decided. The response reports the
client error, including when the cause is a database lock or a full disk.

Some conflicts are decided only after the write transaction starts. Under
write contention those requests can wait up to 2 seconds and then come back
as a busy 503. That covers renaming a group to a name that already exists,
and adding a product that is already in another group.

Creating a group checks the name before the write transaction. A duplicate
name is a 409 while another writer holds a reserved lock. An exclusive lock
blocks that read as well, so the same duplicate check can wait and return a
busy 503.

## Codes on every app-facing error

Every error on a route the web or mobile app calls carries `code`.
The sections below are that contract. Handlers send these codes.
A sentence that already exists stays word for word. A new sentence does not name the app.

The body is JSON, `Content-Type: application/json`:

```json
{"error":"...","code":"..."}
```

Where the hosted contract (#364, `docs/design/api-contract.md` on
`cursor/hosted-api-contract-92f0`) already names a code for the same
meaning, this page uses that name. The sentence stays the one the
server already returns. The HTTP status stays the one the server
already sends. Master does not ship a 429 code, so the sign-in limiter
uses the hosted name `rate_limited`.

`members` and `suggestions` stay on the conflicts that already send them.
A 502, 503, 504, or 530 that this server did not produce still has no
`code`. The client keeps treating that as offline.

`TestAPIJSONErrorsCarryACode` fails when any `/api` route returns an
error with no `code`, other than the exemptions at the end of this page.

## 503 updating

While migrations run, every route except `GET /api/build` is HTTP 503.
The code is `updating` and the message is `Updating. Try again in a minute.`
The body keeps `status` so a deploy health check can still see
`migrating`.

```json
{"status":"migrating","error":"Updating. Try again in a minute.","code":"updating"}
```

This response does not include a `Retry-After` header. `GET /api/build`
stays the build JSON with status 200 (`cmd/server/boot.go`). The same
503 covers `/health`, `/api` routes, and a document load. It is a
server-state notice, shown in the same banner as `busy`. Offline
replaces it, and the next successful request clears it.

The boot gate writes that body (`cmd/server/boot.go`).

## 502 provider unavailable

An upstream that does not answer an action the person just took is HTTP
502. The code is `provider_unavailable`. `provider` is that source's
display name, the same words the sentence uses. It is not an id.

```json
{"error":"Open Food Facts isn't responding. Try again later.","code":"provider_unavailable","provider":"Open Food Facts"}
```

The sentence is `{provider} isn't responding. Try again later.`

This is not a server-state notice and not offline. It shows where the
action was taken, and not in the banner.

| Route | File:line | `provider` |
| --- | --- | --- |
| `POST /api/products/{id}/refresh` | `internal/server/handler_refresh.go:41` | `Open Food Facts` |
| `GET /api/providers/{providerId}/callback` | `internal/server/handler_providers.go:237` | the provider's display name |

A product lookup that fails because an upstream database did not answer
stays 500 `internal` (`internal/server/handler_lookup.go`). It is not a
502. These two routes send the provider body. Any other 502 stays the
generic sentence and code `internal`.

## Sign-in

These sentences stay word for word. They are the sentences the server
already returns. `rate_limited` keeps the `Retry-After` header the
limiter already sends. There is no retry field in the JSON. The sign-in
screen shows the sentence, as it does today.

| Status | Code | Sentence | Where |
| --- | --- | --- | --- |
| 401 | `unauthorized` | `Sign in required.` | A protected `/api` route with no session or a rejected Basic credential (`internal/server/household_auth.go:169`, `:182`, `:208`, `:215`). Also a protected `/api` route when a password is required and none is configured (`:150`), except `GET /api/session` and `POST /api/login`. |
| 404 | `not_found` | `Sign-in is not used on this address.` | `POST /api/login` when this address does not require sign-in (`:221`). |
| 400 | `validation` | `Enter your username and password.` | `POST /api/login` when the body is missing or not JSON (`:234`). This response does not include `field`. |
| 401 | `unauthorized` | `The username or password is incorrect.` | `POST /api/login` when the username or password is wrong (`:244`). |
| 429 | `rate_limited` | `Too many sign-in attempts. Try again later.` | The sign-in limiter on Basic auth, `GET /api/session`, and `POST /api/login` (`:160`, `:199`, `:239`, written at `:370`). |

`GET /api/session` and `POST /api/login` when a password is required and
none is configured are HTTP 503 `auth_not_configured`
(`household_auth.go:193` and `:225`, written at `:357`). The sentence is
`Sign-in isn't set up yet.` The body is the one in
[503 sign-in not configured](#503-sign-in-not-configured). The code string
is `testdata/contract/auth_not_configured.json`. This response does not
include a `Retry-After` header. Unconfigured `POST /api/login` is that
503, not the 401 at `:150`.

## Request parsing

These three apply to every route wrapped by `HandleJSON`. The sentence
stays the parser text it is today. A body or path the server cannot
read is `validation`. A body over the size cap is `payload_too_large`.
The status stays 400. These responses do not include `field`.

| Status | Code | Sentence | File:line |
| --- | --- | --- | --- |
| 400 | `payload_too_large` | `request body is too large` | `internal/server/handler_wrapper.go:65`, `:73` |
| 400 | `validation` | `invalid request body: ` plus the parser text | `:76`, `:81` |
| 400 | `validation` | `invalid path parameters: ` plus the parser text | `:89` |

A handler that checks an empty path id after the parser succeeds is
`validation` as well, in the next section.

## 400 validation

`BadRequest` with no more specific code is HTTP 400 `validation`.
The sentence is the one that handler already returns. These responses
do not include `field`.

| Route | File:line | Sentence |
| --- | --- | --- |
| `GET /api/products/{id}/contributions` | `handler_contribution.go:75` | `product id is required` |
| `GET /api/products/{id}` | `handler_contribution.go:109` | `product id is required` |
| `POST /api/products` | `handler_create.go:83` | `name is required` |
| `POST /api/products`, `PUT /api/products/{id}` | `handler_create.go:129` | `choose an open database to contribute this product to`, `a barcode is required to contribute this product`, or `barcode must be 8 to 14 digits to contribute this product` |
| `PUT /api/products/{id}` | `handler_update.go:20` | `product id is required` |
| `PUT /api/products/{id}` | `handler_update.go:23` | `name is required` |
| `POST /api/products`, `PUT /api/products/{id}` | `handler_update.go:69`, `:72`, `:79` | `Enter a size and a unit together.` `Size has to be greater than zero.` `Choose ounces, fluid ounces, pounds, grams, kilograms, milliliters, or liters.` `Size has to be a weight or a volume.` `Size could not be saved.` `Pack count has to be at least 1.` |
| `PUT` and `DELETE /api/shopping-list/items/{id}/adjustment` | `handler_shopping_list_adjustment.go:38` | `missing id path parameter` |
| same | `:49` | `missing provider` |
| `POST /api/shopping-list/export` | `handler_shopping_list_export.go:86`, `:90`, `:105` | `provider "<id>" is not registered` |
| `PUT /api/settings/supply` | `handler_supply.go:64` | `supply length must be from 1 to 12 months` |
| `GET` and `PUT /api/products/{id}/supply-override` | `handler_supply.go:84`, `:104` | `missing product id` |
| `PUT /api/products/{id}/supply-override` | `handler_supply.go:225`, `:230` | `Send a supply window, a quantity, or clear.` |
| same | `:236`, `:240`, `:246`, `:250` | `supply length must be from 1 to 12 months` or `supply quantity must be from 1 to 999` |
| `POST /api/shopping-list/items/{id}/unknown-resolution` | `handler_unknown_resolution.go:40` | `missing id path parameter` |
| same | `:56` | `missing provider` |
| `GET /api/products/lookup` | `handler_lookup.go:20` | `barcode query parameter is required` |
| `POST /api/products/overrides` | `handler_override.go:26` | `barcode is required` |
| same | `:29` | `productId is required` |
| `GET /api/providers/{providerId}/authorize` | `handler_providers.go:158` | `{Name} does not use an authorization-code connection` |
| `GET /api/providers/{providerId}/callback` | `handler_providers.go:212` | same |
| same | `:222` | `the provider declined the connection` |
| same | `:230` | `invalid or expired authorization state` |
| `DELETE /api/providers/{providerId}/connection` | `handler_providers.go:279` | `provider "<id>" does not require authentication` |
| `POST /api/push/subscriptions` | `handler_push.go:51` | `Background scan alerts are not set up on this pantry yet.` |
| same | `:62` | `Enter a push subscription from this browser.` |
| same | `:68` | `This address no longer sends scan alerts. Open the current site and allow notifications again.` |
| `DELETE /api/push/subscriptions` | `handler_push.go:82` | `Enter a push subscription from this browser.` |
| `POST /api/products/{id}/refresh` | `handler_refresh.go:33` | `product id is required` |
| `PUT /api/restock/household`, `PUT /api/restock/items/{itemId}`, `PUT /api/restock/categories/{id}/target`, `POST /api/restock/categories/{id}/preview`, `POST /api/restock/groups/{id}/preview` | `handler_restock.go:42` | `Choose months of supply or a fixed amount.` |
| same | `:45` | `Enter a target. 0 means don't restock.` |
| `PUT` and `DELETE /api/restock/items/{itemId}`, `POST .../dont-restock` | `handler_restock.go:97`, `:114`, `:128` | `missing item id` |
| `PUT /api/restock/categories/{id}/target` | `handler_restock.go:180` | `missing category id` |
| restock routes that call `restockError` | `handler_restock.go:316` | A sentence that already starts with a capital and is not a not-found sentence below. That includes `Choose months of supply or a fixed amount.` `The target has to be from 0 to 999.` `The target has to be a whole number.` `Months of supply have to be from 0 to 12.` `Choose a different category to merge into.` `Choose at least one item.` `The category needs a name.` |
| `POST /api/scans/batch-commit` | `handler_scan_batch_commit.go:68` | `scanEntryIds is required` |
| same | `:76` | `invalid direction value` |
| `POST /api/scans/{id}/commit` | `handler_scan_commit.go:41` | `scan entry id is required` |
| same | `:75` | `linking a barcode is only available when scanning out` |
| `POST /api/scans` | `handler_scan_create.go:52` | `barcode is required` |
| `GET /api/scans` | `handler_scan_list.go:24` | `invalid status value` |
| `PATCH /api/scans/{id}` | `handler_scan_update.go:32` | `scan entry id is required` |
| same | `:39` | `invalid status value` |
| same | `:47` | `invalid direction value` |
| `POST /api/scanner/mode` | `handler_scanner.go:188` | `mode must be stock_in or stock_out` |
| `POST /api/groups/preview` | `handler_group.go:384` | `Pick one of the rules.` |
| same | `:455` | `The quantity has to be more than zero and at most 999 ounces.` |
| group routes that call `groupErr` | `handler_group.go:518` | The group's invalid sentence: `Choose a window, a quantity, or clearing the target.` `The supply window has to be between 0 and 12 months.` `The quantity has to be more than zero and at most 999 ounces.` `Say whether that amount is weight or volume.` `Pick at least one product.` `Pick one of the rules.` `Star a product that is in this group.` `That product is not in the group you named.` `That product is not in the pantry.` `The group needs a name.` `That product is not on this suggestion.` `Choose a product.` |
| `GET /api/history`, `GET /api/items/{itemId}/history`, `GET /api/groups/{groupId}/history`, `PATCH /api/moves/{id}`, `DELETE /api/moves/{id}` | `handler_history.go:102` | `Quantity has to be at least 1. Undo the move to remove it.` `Quantity has to be 999 or less.` `Choose in, out, or all.` |
| `POST /api/items/{itemId}/replenishment-mode` | `handler_item_replenishment_mode.go:36` | `missing itemId path parameter` |

`POST /api/inventory/wipe` when the typed confirmation does not match is
HTTP 400 `confirmation_mismatch`. The sentence stays
`Type WIPE INVENTORY to confirm wiping the inventory.`
(`handler_inventory_wipe.go:21`).

## 422 validation

A parsed body whose fields are not acceptable is HTTP 422 `validation`.
The status stays 422. The sentence stays the one already returned. No
extra fields, and no `field`.

| Route | File:line | Sentence |
| --- | --- | --- |
| `PUT /api/shopping-list/deals` | `handler_shopping_brand.go:38` | `Choose a brand to mark on sale` |
| same | `:42` | `Add a sale price or a short note` |
| same | `:45` | `Sale price can't be negative` |
| `DELETE /api/shopping-list/deals/{itemId}` | `handler_shopping_brand.go:87` | `Choose a brand to clear` |
| `PUT /api/shopping-list/items/{id}/adjustment` | `handler_shopping_list_adjustment.go:65` | `adjustment must be between 0 and 999, got N` |
| `POST /api/shopping-list/export` | `handler_shopping_list_export.go:178` | `Choose a product on this line.` |
| `POST /api/shopping-list/items` | `handler_shopping_list_item_create.go:27` | `itemId is required` |
| same | `:30` | `quantity must be 1 or greater` |
| `POST /api/shopping-list/items/{id}/swap` | `handler_shopping_swap.go:25` | `Choose a product in this group.` |
| same | `:33` | `That shopping line is not a group.` or `Choose a product in this group.` |
| `POST /api/items/{itemId}/target-quantity` | `handler_suggestion_target_quantity.go:36` | `targetQuantity is required` |
| same | `:40` | `targetQuantity must be 0 or greater` |
| `PUT /api/providers/{providerId}/credentials` | `handler_provider_credentials.go:50` | `Client ID is required.` |
| same | `:53` | `Modality must be PICKUP or DELIVERY.` |
| same | `:57` | `Redirect URI must be an http or https URL.` |
| same | `:71` | `Client secret is required.` |
| same | `:86` | `Those credentials could not be applied.` |
| `PUT` and `DELETE /api/providers/{providerId}/credentials` | `handler_provider_credentials.go:143` | `This provider does not take application credentials.` |
| `POST /api/items/{itemId}/replenishment-mode` | `handler_item_replenishment_mode.go:42` | `mode must be "target" or "replenish"` |

## 404 not found

`NotFound` with no more specific code is HTTP 404 `not_found`. The
sentence is the one that handler already returns. The router and the
sync snapshot use `Not found.`

| Route | File:line | Sentence |
| --- | --- | --- |
| `GET /api/products/{id}/contributions` | `handler_contribution.go:82` | `product not found` |
| `GET /api/products/{id}` | `handler_contribution.go:116` | `product not found` |
| `PUT /api/products/{id}` | `handler_update.go:54` | `product not found` |
| `PUT /api/shopping-list/deals` | `handler_shopping_brand.go:52` | `item not found` |
| `POST /api/shopping-list/items` | `handler_shopping_list_item_create.go:38` | `item not found` |
| `DELETE /api/shopping-list/items/{id}` | `handler_shopping_list_item_delete.go:21` | `shopping list item not found` |
| `PATCH /api/shopping-list/items/{id}` | `handler_shopping_list_item_update.go:30`, `:41` | `shopping list item not found` |
| `POST /api/shopping-list/items/{id}/swap` | `handler_shopping_swap.go:31`, `:43` | `shopping list item not found` |
| `POST /api/items/{itemId}/target-quantity` | `handler_suggestion_target_quantity.go:48`, `:53` | `item not found` |
| `PUT /api/products/{id}/supply-override` | `handler_supply.go:118` | `That product isn't in the household.` |
| `POST /api/shopping-list/items/{id}/unknown-resolution` | `handler_unknown_resolution.go:49` | `entry not found` |
| `PUT` and `DELETE /api/providers/{providerId}/credentials` | `handler_provider_credentials.go:135`, `:139` | `provider is not registered` |
| `GET /api/providers/{providerId}/authorize` | `handler_providers.go:153` | `provider "<id>" not registered` |
| `GET /api/providers/{providerId}/callback` | `handler_providers.go:209` | `provider "<id>" is not registered` |
| `DELETE /api/providers/{providerId}/connection` | `handler_providers.go:272` | `provider "<id>" not registered` |
| `GET /api/providers/{providerId}/ledger` | `handler_providers.go:324` | `provider "<id>" not registered` |
| `POST /api/providers/{providerId}/ledger/reset` | `handler_providers.go:385` | `provider "<id>" not registered` |
| `POST /api/products/{id}/refresh` | `handler_refresh.go:39`, `:49` | `product not found` |
| `POST /api/restock/items/{itemId}/dont-restock` | `handler_restock.go:143` | `That item isn't in the household.` |
| `PUT` and `DELETE /api/restock/items/{itemId}`, `POST` and `DELETE .../categories` | `handler_restock.go:293` | `That item isn't in the household.` |
| restock routes that call `restockError` | `handler_restock.go:313` | `That item isn't in the household.` `That category isn't in the household.` `That group isn't in the household.` |
| `PATCH /api/scans/{id}` | `handler_scan_update.go:59`, `:99` | `scan entry not found` |
| group routes that call `groupErr` | `handler_group.go:520` | `That product is not in this group.` `That group is not here.` `That suggestion is not here.` `That product was not found.` |
| history routes that call `historyHTTP` | `handler_history.go:98` | `That product was not found.` `That group was not found.` `That move is no longer in the history.` |
| `POST /api/inventory/{itemId}/instances` | `handler_inventory_instance_create.go:30` | `item not found` |
| `DELETE /api/inventory/instances/{instanceId}` | `handler_inventory_instance_delete.go:25` | `item instance not found` |
| `POST /api/inventory/{itemId}/stock-out` | `handler_inventory_stock_out.go:25` | `item not found` |
| `GET /api/sync/snapshot` when the sync token is unset | `sync_auth.go:34`, `handler_sync.go:21` | `Not found.` |
| An `/api` path the router does not know | the `/api` mux in `internal/server/server.go` | `Not found.` |

`scan_not_found` on a commit stays `scan_not_found`. It is not this code.

## 409 conflict

`Conflict` with no more specific code is HTTP 409 `conflict`. The
sentence is the one that handler already returns. `in_group`,
`target_decision_required`, and the commit codes stay as they are.

| Route | File:line | Sentence |
| --- | --- | --- |
| `POST /api/shopping-list/export` | `handler_shopping_list_export.go:93`, `:108` | `{Name} is not configured` |
| same | `:113`, `:120` | `{Name} is not connected` |
| same | `:128` | the provision conflict's current reason |
| `PUT /api/providers/{providerId}/credentials` | `handler_provider_credentials.go:39` | `{Name} is disabled` |
| `GET /api/providers/{providerId}/authorize` | `handler_providers.go:161` | `{Name} is not configured` |
| `GET /api/providers/{providerId}/ledger` | `handler_providers.go:336`, `:339` | `provider "<id>" is not connected` |
| `POST /api/providers/{providerId}/ledger/reset` | `handler_providers.go:397`, `:400` | `provider "<id>" is not connected` |
| group routes that call `groupErr` | `handler_group.go:522` | `That suggestion is already settled.` `That product is already in another group.` `A group with that name already exists.` |
| history routes that call `historyHTTP` | `handler_history.go:106` | `This stock in can't be lowered to that quantity.` `This use can't be lowered because those units are no longer recorded.` `This use can't be undone because those units are no longer recorded.` `Only N is on the shelf, so this can't be M.` `Only N are on the shelf, so this can't be M.` `1 of these was already used. Undo that use first.` `N of these were already used. Undo those uses first.` |

`PUT /api/products/{id}/supply-override` when the product is in a group
stays 409 `in_group` with `members.groupId`
(`handler_supply.go:183`). A group target decision stays 409
`target_decision_required` with `members` (`handler_group.go:524`).

## 405 method not allowed

A known `/api` path called with the wrong method is HTTP 405
`method_not_allowed`. The sentence is `Method not allowed.` The body is
JSON. The hosted contract has no 405 code, so this name stays.

## 500 internal

`internal` is every 5xx that is not `busy`, `no_space`, `updating`, or
`provider_unavailable`. The sentence is `Something went wrong. Try again.`
That includes `InternalError`, a plain error from a handler, a commit or
stock-out the server did not expect, and these two:

| Route | File:line | When |
| --- | --- | --- |
| `GET /api/events` | `handler_events.go:48` | The response cannot stream. The body is the JSON above. |
| `GET /api/sync/snapshot` | `handler_sync.go:30` | The snapshot cannot be built. |

A credential store that is missing (`handler_provider_credentials.go:131`)
is this same 500. The client does not see `Credential store is not available.`

## Sync token

The snapshot is what the sync sidecar calls. The web and mobile apps do
not send this token. The sentences stay word for word.

| Status | Code | Sentence | File:line |
| --- | --- | --- | --- |
| 404 | `not_found` | `Not found.` | Token unset. `sync_auth.go:34`, `handler_sync.go:21`. |
| 401 | `unauthorized` | `The sync token is not valid.` | `GET /api/sync/snapshot` with a missing or wrong token. `sync_auth.go:50`, `handler_sync.go:25`. |
| 403 | `sync_read_only` | `This token only reads the sync snapshot.` | Any other route presented with a valid sync token. `sync_auth.go:46`. `GET /api/events` with that token is allowed and is not this error. |

`sync_read_only` stays. The hosted `forbidden_role` means the person is
not an owner, which is a different meaning.

## Telemetry

`POST /api/telemetry/client` rejects a browser report before it stores
one. The sentences stay word for word.

| Status | Code | Sentence | File:line |
| --- | --- | --- | --- |
| 400 | `validation` | `invalid report` | `handler_telemetry.go:33`, `:41` |
| 400 | `payload_too_large` | `report is too large` | `:38` |
| 400 | `validation` | `unknown report kind`, `duration must not be negative`, `status is invalid`, `skipped count is invalid`, or `resource counts are invalid` | `:45` |

These responses do not include `field`. The status stays 400.

`GET /api/telemetry` does not return these.

## Codes that already exist

These stay. This contract does not rename them.

| Code | Status | Extra |
| --- | --- | --- |
| `busy` | 503, or a batch item | |
| `no_space` | 503, or a batch item | |
| `internal` | 500, or a batch item | now also the generic 5xx above |
| `none_on_hand`, `instance_gone`, `not_pending`, `product_unresolved`, `no_item`, `link_target_gone`, `scan_not_found`, `direction_unset` | the commit status that code already uses | `no_item` also sends `suggestions` |
| `in_group` | 409 | `members.groupId` |
| `target_decision_required` | 409 | `members` |

`auth_not_configured` is the 503 in
[503 sign-in not configured](#503-sign-in-not-configured): HTTP 503 on
`GET /api/session` and `POST /api/login` when a password is required and
none is configured. The sentence is `Sign-in isn't set up yet.` The
fixture is `testdata/contract/auth_not_configured.json`.

Shared constants for the codes on this page live in
`frontend/src/api/contract.ts`. The phone imports that module.

## Exemptions

These responses have no `code`.

| Response | Reason |
| --- | --- |
| `POST /api/sentry-tunnel` (`internal/server/sentry_tunnel.go`) | Passthrough. The page does not branch on these bodies. A code would change a tunnel that forwards an upstream status. |
| `POST /api/deploy-hook` (`internal/deployhook/hook.go`) | Not called by the web or mobile app. The issue names this exemption. Bodies stay `method not allowed`, `deploy hook is not configured`, `request is too large`, `could not read the request`, `invalid signature`, and `could not start the update`. |
| Web shell on a non-`/api` path (`internal/webui/webui.go:88`, `:133`, `:168`, `:186`) | Plain text the browser treats as a document or an asset: 405 `Method Not Allowed`, 500 with the generic sentence, and 404 `404 page not found`. |
| `GET /brand/logo.png`, `GET /terms`, `GET /privacy` | The login page loads the logo as an image. These handlers return the file. A wrong method is the router's plain text on a non-`/api` path. A code would replace the image or the page. |
| `GET /health` after boot, and a wrong method on `/health` | `{"status":"ok"}` is not an error. Deploy checks `GET` only. The web and mobile apps do not call `/health`. While migrations run, `/health` is `updating`, which is not this exemption. |
| `GET /api/build` during migration | 200 build JSON, not an error (`cmd/server/boot.go`). |
| A 502, 503, 504, or 530 this server did not produce | A proxy or a failed fetch. No `code`, so the client can still treat it as offline. |
