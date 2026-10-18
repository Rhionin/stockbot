# Backfill on stock-out

API contract for issue #294. This is the contract phase 2 implements. It follows the final spec CJ approved (2026-10-17, 10:44 PM MDT) and the addendum that the link response carries on-hand.

A backfill records a stock-in for a product this household already has, then the stock-out, in one commit. An unknown barcode is defined first. Defining it does not stock anything in or out, and it does not backfill.

Omitting `backfill`, or sending `false`, leaves `POST /api/scans/{id}/commit` as it is today.

## Commit a backfill

`POST /api/scans/{id}/commit`

The scan must already be a pending stock-out whose barcode is linked to a household item, and that item must have 0 on hand. The body has one field. There is no `newItemName`, no `itemId`, and no `linkProductId`. If those fields are sent with `backfill: true`, the server ignores them and still uses the scan's linked item.

```json
{ "backfill": true }
```

`200` body:

```json
{ "removed": 2, "backfilled": 2, "itemId": "item-sour-cream" }
```

| Field | Type | Meaning |
|---|---|---|
| `removed` | number | Units taken out. Always the scan's `unitCount`. |
| `backfilled` | number | Units stocked in. Always the same number as `removed`. |
| `itemId` | string | The household item the scan was already linked to. |

Example: a pending stock-out of sour cream, `unitCount` 2, linked to `item-sour-cream`, 0 on hand.

Request:

```http
POST /api/scans/scan-sour-cream/commit
Content-Type: application/json

{ "backfill": true }
```

Response `200`:

```json
{ "removed": 2, "backfilled": 2, "itemId": "item-sour-cream" }
```

What that writes, in one transaction:

- A stock-in of exactly `unitCount`, then a stock-out of exactly `unitCount`. Both are timestamped at the scan's `scannedAt` (the same instant). On hand stays 0.
- Pack size is not applied. A normal stock-in multiplies by `packCount` when that is greater than 1. A backfill does not, so the in and the out stay equal and the shelf stays empty.
- The new units have no expiration. They exist only so the stock-out can consume them.
- The scan becomes `committed`.
- The stock-out counts as usage. The stock-in does not (see [How the stock-in is tagged](#how-the-stock-in-is-tagged)).

`unitCount` below 1 is the same failure as any other commit of a bad count: `500` `internal`, sentence `Something went wrong. Try again.` The real error stays in the server log.

## Errors

Every error body is JSON. Codes the client matches are on `code`. The sentence is `error`.

```json
{ "error": "Choose which item this is first.", "code": "product_unresolved" }
```

A backfill on a scan that is not linked yet is `400`, not the `409` `no_item` a normal stock-out commit returns. "Not linked" means the scan has no product, or the product is not a live item in this household. The client links the barcode or adds the item first, then commits the backfill.

| HTTP | `code` | `error` |
|---|---|---|
| 400 | `product_unresolved` | Choose which item this is first. |
| 400 | `units_on_hand` | You already have some on hand, so there's nothing to add. |
| 400 | `not_pending` | This scan was already handled. |
| 400 | `direction_unset` | Choose stock in or stock out first. |
| 400 | `not_stock_out` | This only works when taking something out. |
| 404 | `scan_not_found` | This scan is no longer in the queue. |
| 409 | `none_on_hand` | Nothing on hand to take out. |
| 500 | `internal` | Something went wrong. Try again. |

`units_on_hand` is the backfill refusal when the linked item already has units on hand. Backfill must not add more. The body also names the item and the current count, so the card can offer a normal stock-out instead:

```json
{
  "error": "You already have some on hand, so there's nothing to add.",
  "code": "units_on_hand",
  "itemId": "item-sour-cream",
  "onHand": 3
}
```

`onHand` is a number, the same count as `LinkSuggestion.onHand`. `0` is sent as `0`, not omitted.

`not_stock_out` is a backfill sent for a scan whose direction is stock-in. The body is `error` and `code` only.

`none_on_hand` here is only a shelved (wiped) item. A live item with 0 on hand is the success path above. A wiped item is not brought back by a backfill.

`500` uses the commit sentence `Something went wrong. Try again.` and `code` `internal`. That is the commit contract. Other 500s from the generic handler use a different sentence and omit `code`; a backfill failure does not.

These requests have no `code`:

| HTTP | `error` |
|---|---|
| 400 | scan entry id is required |
| 400 | request body is too large |
| 400 | invalid request body: … |

An empty body is a normal commit, not a backfill.

## Batch commit

`POST /api/scans/batch-commit` **ignores** `backfill`.

The batch body is still `scanEntryIds`, an optional shared `direction` / `unitCount` / `expiresAt`, and `commit`. A `backfill` field on that body is dropped. Each id is committed or failed exactly as it is today. A stock-out with nothing on hand fails that id with `none_on_hand` and `Nothing on hand to take out.`, and nothing is stocked in.

Ignore, rather than reject the whole batch. The flag is per card. One batch flag cannot mean "backfill every id" without stocking in items the household did not confirm, and it cannot mean "backfill some of them" because the body has no per-id flag. Rejecting the field would turn a batch that happens to send it into a 400 and commit nothing. Ignoring it keeps today's batch result and still never backfills.

## Undo

`DELETE /api/moves/{id}`

The move id is either history row from the backfill: the stock-in (`source` `backfill`) or the stock-out recorded with it. Undoing either one removes both.

Both means:

- the stock-in move and the stock-out move
- the units those two moves created and consumed
- the usage row for the stock-out

The item stays. The product stays. The barcode link stays. On hand stays 0, because the pair had put the units on the shelf and taken them off. Undo does not delete an item that was created earlier with **Add as a new item**. That was a separate step. The household deletes that item from the item's menu.

The pair is the exception to the usual stock-in undo rule. A normal stock-in whose units were already used returns `409` and the sentence `1 of these was already used. Undo that use first.` A backfill's units are used by its own stock-out, so that conflict does not apply to this pair. The server removes the stock-out and the stock-in together.

`200` is the item's history view after the pair is gone (`GET /api/items/{itemId}/history` shape). The commit response does not include move ids. The client takes them from that history: the `in` row whose `source` is `backfill`, and the `out` row with the same `at`.

## Add as a new item

Stock-in already creates a product from a name and links the barcode without stocking anything. A stock-out **Add as a new item** uses that same sequence. The name is the one the household confirms (prefilled from the lookup, or typed when the lookup has no name).

### 1. Create the product

`POST /api/products`

```json
{ "name": "Sour cream", "category": "Dairy", "unitOfMeasure": "tub" }
```

`201` is the stored product. The id is what the next two calls use.

```json
{
  "id": "prod-sour-cream",
  "name": "Sour cream",
  "category": "Dairy",
  "unitOfMeasure": "tub",
  "createdAt": "2026-10-18T04:40:00Z",
  "source": "user"
}
```

`name` is required. `400` `name is required` when it is missing. Optional size and contribution fields are the same ones stock-in already sends (`netAmount`, `netUnit`, `packCount`, `contribute`, `contributeTo`, `barcode`). This call writes a product row only. It does not write an item, a barcode link, a stock move, a stock-in event, or a usage row.

### 2. Link the barcode

`POST /api/products/overrides`

```json
{ "barcode": "011110000000", "productId": "prod-sour-cream" }
```

`201`:

```json
{ "barcode": "011110000000", "productId": "prod-sour-cream", "source": "user_override" }
```

This writes the household barcode override and nothing else. No stock events.

### 3. Point the scan at that product

`PATCH /api/scans/{id}`

```json
{ "productId": "prod-sour-cream", "status": "pending" }
```

On a **flagged** stock-in (the path used today), this resolves the scan: it writes the same `user_override` again, sets `productId`, and moves the scan from `flagged` to `pending`. It does not create an item and it does not stock anything. `200` is the scan entry.

```json
{
  "id": "scan-sour-cream",
  "status": "pending",
  "productId": "prod-sour-cream",
  "barcode": "011110000000",
  "direction": "stock_in",
  "unitCount": 1
}
```

That stock-in path is unchanged, including that it records no stock events.

### What changes for a stock-out

A stock-out **Which item is this?** card is a **pending** scan, not a flagged one. Today `PATCH` on a pending scan only updates columns. It does not write the barcode override (`ResolveFlaggedEntry` runs only for `flagged`), and it does not create a household item. A later backfill would still see an unlinked scan.

For a stock-out, the same `PATCH` also:

- writes the `user_override` for the scan's barcode and this household, whether the scan was `pending` or `flagged`
- creates the household item for that product if this household does not already have one
- leaves the scan `pending` (it must not commit)
- records no stock events: no instances, no `stock_moves`, no `stock_in_events`, no `consumption_events`

The item is created with nothing on hand. The refreshed scan is the `PATCH` response, and that body carries `onHand` (see below). It is `0` for an item this call just created.

Stock-in flagged resolution does not gain an item row and does not gain `onHand`. Only the stock-out link does.

## On hand after a link

The client has to decide, on the same card, whether to offer **Add it and take it out**. The link response carries the count so the client does not scan again or call another route.

The field is `onHand`, a JSON number, the same name and meaning as `LinkSuggestion.onHand`: how many units of that item are on the shelf. It is an integer count, not a boolean.

### Existing item

The link call stays `POST /api/scans/{id}/commit` with the product the household picked.

```json
{ "linkProductId": "prod-pears" }
```

When the item has units on hand, the stock-out still commits, as it does today. The response is that scan entry, with `onHand` added: the count left after the take-out.

`200`:

```json
{
  "id": "scan-pears",
  "status": "committed",
  "productId": "prod-pears",
  "barcode": "011110828385",
  "direction": "stock_out",
  "unitCount": 1,
  "onHand": 2
}
```

When the item is a live household item with 0 on hand, the response stays `409` `none_on_hand`. The barcode link is kept: the override is stored, the scan's `productId` becomes the linked product, and no stock move is written. The scan stays `pending`. This replaces the #281 rollback, which undid the link.

`409`:

```json
{
  "error": "Nothing on hand to take out.",
  "code": "none_on_hand",
  "itemId": "item-pears",
  "onHand": 0
}
```

`itemId` is the linked item. `onHand` is `0`. A later commit without `backfill` is still `409` `none_on_hand` / `Nothing on hand to take out.` A later commit with `backfill: true` is the success path above.

`linkProductId` for a missing or wiped product stays `409` `link_target_gone` / `That item isn't available anymore. Pick another.` The scan stays pending and unlinked. That error body has no `onHand`.

### Add as a new item

`onHand` is on the `PATCH /api/scans/{id}` response from the stock-out sequence above. A new item has nothing on hand, so it is always `0`.

`200`:

```json
{
  "id": "scan-sour-cream",
  "status": "pending",
  "productId": "prod-sour-cream",
  "barcode": "011110000000",
  "direction": "stock_out",
  "unitCount": 1,
  "onHand": 0
}
```

`POST /api/products` and `POST /api/products/overrides` do not carry `onHand`. The scan response does.

`GET /api/scans` does not grow an `onHand` field. The link response is the one the card reads.

## How the stock-in is tagged

The backfill stock-in is stored as a history move with `source` `backfill`. The stock-out from the same commit is a normal usage move with `source` `scan`.

Pace, replenish, and buy-frequency all read the stock-in event table (`stock_in_events`) and the usage table (`consumption_events`). They do not read the history `source` string.

- The backfill stock-in writes **no** `stock_in_events` row, and its history move is not tracked. It does not move the last-stocked time, and it is not a stock-in in the rate calculation (`derive` / `DeriveAmount`). Replenish and buy-frequency ignore it.
- The stock-out **does** write a `consumption_events` row. Pace (`GET /api/items/{itemId}/history`, which counts usage) and replenish both count that use.
- History still lists the stock-in, because history lists `stock_moves`, not `stock_in_events`.

`GET /api/items/{itemId}/history` and `GET /api/history` return the move with `"source": "backfill"`. They do not return a display sentence.

**Added when taking out** is a client label. The server does not send it. Web and mobile already turn `source` into words (`scan` vs `by hand`, and `Stocked in` vs `Used`). They map an `in` move whose `source` is `backfill` to **Added when taking out**.

## New-item setting

Issue #296 (`newItemPolicy`: `auto`, `ask_restock`, `ask_hold`) is not built. Nothing in this contract reads or writes it.

An item created by **Add as a new item** behaves as `auto`: it does not wait for a restock decision, and the usual target applies immediately. When #296 exists, that same create path marks a new item waiting according to the household's setting. Backfill itself still never creates an item. Only the link step does.

## Household scoping

The household is the signed-in household. Item rows, barcode overrides, scans, and history are keyed by that household's `userId` (the rows the app stores as `user-1` today).

A backfill, a link, and **Add as a new item** read and write only that household:

- the item is the one with this household's `userId` and the linked product
- the barcode override is stored for this household's `userId`
- stock moves and the usage row hang off that item

A scan whose `userId` is another household is `404` `scan_not_found` / `This scan is no longer in the queue.` The handler does not commit it and does not copy its barcode onto this household's item. Another household's item, even for the same product id, is not read or updated.
