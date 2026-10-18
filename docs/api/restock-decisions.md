# Restock decisions API (#296)

Status: contract only. Backend for [issue #296](https://github.com/Rhionin/stockbot/issues/296). No frontend or mobile changes in this phase.

This document is the contract for the household setting, the waiting list, and the Restock / Don't restock answers. It sits on the restock targets shipped in #231 / PR #240. Names follow that PR's `/api/restock/*` routes. Semantics win if a name has to move.

Multi-tenant households (#224) are not on master. Every new row is stored under the household id #240 already uses, so a later #224 change is a different id in the same column, not a new key.

## How #240 works today

One household. `restock.HouseholdID` is `"user-1"`, the same id as `items.user_id` and `product_groups.user_id`. Handlers do not take a household id from the client.

A target is `{ "kind": "months" | "amount", "value": number }`. `value` 0 means don't restock. The most specific place wins: item, then group, then the higher of the item's categories, then the household default (3 months until someone changes it).

| Place | Where it is stored | Key |
|---|---|---|
| Item | `item_targets` | `(household_id, item_id)` and `item_id` is `items.id` |
| Group | `product_groups.window_months` or `quantity_*` | `product_groups.id`. Null means the group sets nothing. `window_months` 0 is don't restock. |
| Category | `category_targets` | `(household_id, category_id)` |
| Household default | `app_settings` keys `supply_target_kind` and `supply_target_value` | One global pair. This table has no household column. |

Wipe (PR #240, migration `020_shelve_wiped_items.sql`) does not delete the item row. It sets `items.shelved = 1` and leaves `item_targets` in place. The next stock-in clears `shelved`. Deleting the item row deletes its target, via the `delete_item_target_with_item` trigger. Inventory and the planner both skip shelved rows.

`GET /api/restock` returns `restock.Snapshot`: `household`, `householdLabel`, `appliesTo`, `itemCount`, `items` (`ItemView`), `categories`, `notRestocked`, `suggestionCount`. `ItemView` is the resolved target for one unshelved item, including `target`, `level` (`item` | `group` | `category` | `household`), `levelName`, `sourceLabel`, `valueLabel`, `quietLine`, `rowSource`, and `chain`.

`POST /api/restock/categories/{id}/preview` and `POST /api/restock/groups/{id}/preview` return `restock.Preview`:

```json
{
  "title": "Stop restocking Snacks?",
  "body": "Snacks's target becomes 0. These items leave the shopping plan. They stay in inventory, and you can still add them to the list by hand.",
  "stop": [{ "itemId": "item-tc", "name": "Tortilla chips", "changes": true, "before": "2 mo", "after": "0" }],
  "keep": [{ "itemId": "item-pb", "name": "Peanut butter", "changes": false, "after": "Keep 2", "reason": "Set on this item" }],
  "confirm": "Stop restocking 1"
}
```

`stop` is "Will change". `keep` is "Won't change". The screen draws those labels. The API keeps the field names `stop` and `keep`.

The planner reads the same resolution. A resolved value of 0 is not bought. A member with its own item target leaves the group's pooled line. A purchased shopping row stays until it is stocked in. Changing a target does not reopen it, and undoing that change does not put it back. Manual shopping rows are kept even when the target is 0. There is no restock SSE event today. `GET /api/events` already fans out `scan`, `inventory`, `scanner_mode`, `scan_processing`, and `scan_processing_failed`.

Errors are JSON. A handler error with a code is `{ "error": "...", "code": "..." }`. A 5xx is always `{ "error": "Something went wrong. Try again." }` with no code. The server log keeps the detail. Malformed JSON stays the existing wrapper sentence, `invalid request body: …`, with no code. A body over 1 MiB stays `request body is too large`, with no code.

Sentences in this contract are the whole `error` string: short, plain, and ending with a period. Unknown values are rejected. They are never coerced to `auto` or to Restock.

## Concepts

### Policy

`newItemPolicy` is one household setting.

| API value | Meaning while something is waiting |
|---|---|
| `auto` | Nothing waits. The usual target applies. This is the default, and it is today's behavior. |
| `ask_restock` | It waits, and it uses its usual target, so it can be on the shopping list. |
| `ask_hold` | It waits, and it is treated as don't restock until someone answers. |

"Usual target" is the #240 resolution, ignoring the wait: item, then group, then the higher category, then the household default.

Switching modes:

- `auto` to either Ask mode leaves products and groups the household already has as they are. Only something created after the change can wait.
- `ask_restock` and `ask_hold` keep the same waiting rows. Only the plan changes (usual target, or 0).
- Either Ask mode back to `auto` answers every waiting subject as Restock. The request has to say so. See `PUT /api/restock/new-item-policy`.

### What is new

A row in `new_item_decisions` means the household has had that item or group. No row means it is new.

- A new item row is the first time this household has an `items` row for that product, or the first time after that row was deleted. The decision row is inserted in the same database transaction as the item. That covers stock-in (including scan commit), a manual add, a group member that has to create the item, and any import that inserts an item. A scan entry by itself does not insert an item and does not create a decision row. The scan card's chip is not a backend field.
- A new group row is inserted in the same transaction as `product_groups`, including accepting a suggestion that creates a group.
- Not new: a re-scan, a stock-in after a wipe (the item id is still there), a rename, or adding an item the household already has into a group.
- Created while the policy is `auto`: the row is stored as `auto`. It is not waiting, and a later switch to an Ask mode does not make it waiting.

### Decision states

`state` is `auto` | `waiting` | `restock` | `dont_restock`.

| State | Waiting? | How it got there |
|---|---|---|
| `auto` | no | Created or backfilled while the policy was `auto`. |
| `waiting` | yes | Created under an Ask mode, and nobody has answered. |
| `restock` | no | Answered Restock, or a group that was born with nothing to ask. No target is written by the Restock answer itself. |
| `dont_restock` | no | Answered Don't restock. The target at that level is 0. |

`decided_at` is null only while `waiting`. Any other state sets it.

### Answers

Restock clears `waiting` and writes no target. The item or group keeps following the next level, and a later change there still reaches it. If a target was already stored at that level, Restock leaves it. A waiting subject normally has none.

Don't restock clears `waiting` and writes 0 at that level. An item gets `item_targets` kind `months`, value 0, the same row `POST /api/restock/items/{id}/dont-restock` writes today. A group gets `window_months` 0 and its quantity columns cleared.

Any explicit target write on that item or that group also clears `waiting`. See "Existing routes that answer". A category target and the household default do not answer anybody.

The usual target being 0 does not make `decision: "restock"` fail. The API still writes no target and clears the wait, so the subject stays at 0. The screen opens the target picker in that case and saves with `PUT /api/restock/items/{id}`, which is the write that both sets a target and answers.

### Groups

A waiting group is one decision. Its waiting members are not separate decisions.

- The group is waiting only when at least one member is waiting and has no item target. A new group whose members are all already decided starts decided: state `restock`, no group target written. Holding it would turn off items the household already accepted.
- Created under `auto`: state `auto`, even if the create body also sets a target.
- Created under an Ask mode with an explicit target in the same request: that target is the answer. Value 0 is `dont_restock`. Any other saved target, including clearing it, is `restock`. Waiting members with no item target are answered with the group. Members that have an item target are left alone, and so are members that were already answered.
- Answering the group answers every waiting member that has no item target, and writes no item target on them. Their state becomes the group's new state. Members with an item target stay as they are. Members already out of `waiting` stay as they are.
- A waiting item added to a decided group is answered by that group. Its state becomes the group's state (`auto`, `restock`, or `dont_restock`). No item target is written. It then follows the group.
- A waiting item added to a waiting group stays waiting and is covered by the group.
- A decided item added to a waiting group stays decided and is not part of the group's answer.
- An item removed from a group that already answered it is not asked again. Its state stays answered. No item target is written, so after it leaves the group it follows category or household.
- An item removed from a group that is still waiting stays waiting and is its own row again. If that was the group's last waiting member, the group becomes decided (`restock`, no target written).
- Deleting a waiting group drops the group's decision row. Members that were still waiting stay waiting and show up on their own. Members the group had already answered stay answered.
- Removing the last member deletes the group today. That is the same as deleting the group.
- There is no group-merge route on master. Moving members with `fromGroupId` uses the join rules above. A later merge route must leave the surviving group waiting only when both groups were waiting.

`newMembers` lists the unshelved waiting members with no item target, ordered by name, then `itemId`. Shelved members are left off that list until a stock-in puts them back. A wiped product that was still waiting leaves the list with the wipe. Stocking it in again shows it as waiting, because nobody answered.

A waiting group counts only when `newMembers` is non-empty. A group whose waiting members are all shelved does not count and does not appear as a decision row until one of them is unshelved.

### Resolution

The planner and every screen use one resolution.

- Policy `auto`, or state not `waiting`: today's `restock.Resolve`. No new level, no new target.
- Policy `ask_restock` and state `waiting`: today's `restock.Resolve`. The row is waiting, and the plan is the usual one.
- Policy `ask_hold` and the item's own state is `waiting`: `target` is `{ "kind": "months", "value": 0 }`, `level` is `waiting`, `sourceLabel` and `valueLabel` are `Not restocked yet`. The stored item target is not consulted.
- Policy `ask_hold` and the item is a waiting member of a waiting group, with no item target: the same held resolution. The group is the decision subject.
- A member with its own item target always resolves at item level, including while its group is waiting.

`level` `waiting` is new, next to `item`, `group`, `category`, and `household`. Copy never uses the word `waiting` for a person. The source label is `Not restocked yet`.

Under `ask_hold`, `chain` is still the usual four cells, and no cell has `win: true`. Under `ask_restock`, `chain` is the usual chain, including the winning cell. `quietLine` is empty while the resolved level is `waiting`, so the row does not also say "Not restocked · …". `rowSource` while held is `Not restocked yet`. `setLabel` stays `Set for this item` while the item has no item target.

`decision.usualTarget` and `decision.usualSourceLabel` describe the resolution you would get if this subject were not waiting. `usualSourceLabel` is today's `SourceLabel` for that resolution: `Set on this item`, `From Salad kits group`, `From Snacks & sweets`, or `Household default`.

### Count

`needsDecision` counts:

1. Each waiting group that has a non-empty `newMembers`, once.
2. Each unshelved item whose own state is `waiting` and that is not a member of a group counted in 1.

Shelved items are not in the count. Members of a counted group are not in the count.

`notRestocked` stays a count of unshelved items whose resolved value is 0, except those whose resolved level is `waiting`. Under `ask_hold`, a waiting item is in Needs a decision only. Under `ask_restock`, a waiting item whose usual target is already 0 is in both lists. That is the overlap the issue allows.

Under `auto`, nothing is waiting, the hold never applies, and the planner's shopping lines are unchanged from master. The snapshot JSON is not byte-for-byte the same, because it gains `newItemPolicy` (`auto`) and `needsDecision` (`0`).

### Undo

Undo puts back the waiting state and the target that was stored before the answer. It does not invent a target. "No target before" means the target row is gone again.

The shopping list is refreshed the same way a target write refreshes it today. Undo does not insert, reopen, or delete a shopping row itself. A purchased row stays. A purchased row that was already bought is not put back on the list as a new plan. An unpurchased planned line comes back only when the restored target would plan one, which is the same rule as undoing Don't restock today.

Undo of a group answer restores the group and every member that answer changed, and no one else.

## Storage

Migration `internal/app/migrations/021_new_item_decisions.sql`. It runs in the normal migration transaction. Existing items and groups are backfilled as `auto`, including shelved items, so nobody who already has a product becomes waiting.

`subject_key` matches item targets after the wipe fix. For an item it is `items.id`. For a group it is `product_groups.id`. A wipe keeps the item row, so the decision stays and reattaches on the next stock-in. Deleting the item or the group deletes the decision row, the same way deleting an item deletes its target. A later recreate is new.

The policy is not an `app_settings` key. That table has no household column. The policy lives in a household-keyed table beside the other #240 household rows. The household default target stays in `app_settings`.

```sql
-- Household choice for products and groups created from now on.
-- household_id is the same id item_targets uses (today 'user-1').
CREATE TABLE household_restock_settings (
    household_id    TEXT PRIMARY KEY,
    new_item_policy TEXT NOT NULL DEFAULT 'auto'
        CHECK (new_item_policy IN ('auto', 'ask_restock', 'ask_hold'))
);

INSERT INTO household_restock_settings (household_id, new_item_policy)
SELECT 'user-1', 'auto'
WHERE NOT EXISTS (
    SELECT 1 FROM household_restock_settings WHERE household_id = 'user-1'
);

-- One row for everything this household has had. No row means it is new.
-- subject_key is items.id or product_groups.id, same survival rules as item_targets.
CREATE TABLE new_item_decisions (
    household_id TEXT NOT NULL,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('item', 'group')),
    subject_key  TEXT NOT NULL,
    state        TEXT NOT NULL CHECK (state IN ('auto', 'waiting', 'restock', 'dont_restock')),
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    decided_at   DATETIME,
    PRIMARY KEY (household_id, subject_type, subject_key)
);

INSERT INTO new_item_decisions (household_id, subject_type, subject_key, state, created_at, decided_at)
SELECT user_id, 'item', id, 'auto', created_at, created_at FROM items;

INSERT INTO new_item_decisions (household_id, subject_type, subject_key, state, created_at, decided_at)
SELECT user_id, 'group', id, 'auto', created_at, created_at FROM product_groups;

CREATE TRIGGER delete_item_decision_with_item
AFTER DELETE ON items
FOR EACH ROW
BEGIN
    DELETE FROM new_item_decisions
    WHERE household_id = OLD.user_id AND subject_type = 'item' AND subject_key = OLD.id;
END;

CREATE TRIGGER delete_group_decision_with_group
AFTER DELETE ON product_groups
FOR EACH ROW
BEGIN
    DELETE FROM new_item_decisions
    WHERE household_id = OLD.user_id AND subject_type = 'group' AND subject_key = OLD.id;
END;

-- Server-side undo. The token is opaque. payload is not a client contract.
CREATE TABLE new_item_decision_undos (
    token        TEXT PRIMARY KEY,
    household_id TEXT NOT NULL,
    payload      TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    used_at      DATETIME
);
```

The undo payload records, for each row the answer changed, the prior `state`, the prior `decided_at`, and the prior target at that level (item kind and value, or null; group window, quantity, and dimension, or null). Clients never read this table. They send the token back.

There is no time limit. A token lasts until it is used, or until a later write makes it stale. A token is single-use.

## Shared types

`Target` is unchanged:

```json
{ "kind": "months", "value": 2 }
```

`Decision` is present only on a decision subject (an item or group with `needsDecision: true`):

```json
{
  "usualTarget": { "kind": "months", "value": 2 },
  "usualSourceLabel": "From Snacks & sweets",
  "newMembers": [{ "itemId": "item-sm", "name": "Spring mix kit" }]
}
```

`newMembers` is present on a group only, and only when it is non-empty. It is omitted on an item.

A subject in a request or a skipped list:

```json
{ "type": "item", "id": "item-tc" }
```

`type` is `item` or `group`.

## GET /api/restock

The existing snapshot, plus the policy and the count. Household-scoped by `restock.HouseholdID`. A missing settings row is read as `auto` (the migration inserts the row).

### Response `200`

```json
{
  "household": { "kind": "months", "value": 3 },
  "householdLabel": "3 months",
  "appliesTo": 12,
  "itemCount": 20,
  "newItemPolicy": "ask_hold",
  "needsDecision": 6,
  "notRestocked": 4,
  "suggestionCount": 0,
  "items": [
    {
      "itemId": "item-tc",
      "productId": "prod-tc",
      "name": "Tortilla chips",
      "unit": "bag",
      "target": { "kind": "months", "value": 0 },
      "level": "waiting",
      "sourceLabel": "Not restocked yet",
      "valueLabel": "Not restocked yet",
      "quietLine": "",
      "rowSource": "Not restocked yet",
      "chain": [
        { "key": "item", "label": "Item", "value": "—", "win": false, "struck": false },
        { "key": "group", "label": "Group", "value": "—", "win": false, "struck": false },
        { "key": "category", "label": "Category", "value": "2 mo", "win": false, "struck": false },
        { "key": "household", "label": "Default", "value": "3 mo", "win": false, "struck": false }
      ],
      "categories": [{ "id": "cat-snacks", "name": "Snacks & sweets" }],
      "setLabel": "Set for this item",
      "needsDecision": true,
      "decision": {
        "usualTarget": { "kind": "months", "value": 2 },
        "usualSourceLabel": "From Snacks & sweets"
      }
    }
  ],
  "categories": []
}
```

Fields not shown on `ItemView` stay as they are today. `needsDecision` is always present on every item. `decision` is omitted when `needsDecision` is false. A member covered by a waiting group has `needsDecision: false` and no `decision`, and under `ask_hold` its `target` / `level` / `sourceLabel` still show the hold.

The same item under `ask_restock` keeps today's `target`, `level`, and `sourceLabel` (for this example, 2 months at category, source `From Snacks & sweets`) and still has `needsDecision: true` with the same `decision` object.

### Errors

| Status | Code | Sentence |
|---|---|---|
| 500 | *(none)* | Something went wrong. Try again. |

### Idempotency and concurrency

Read-only. A repeated call returns the current snapshot. It does not lock the policy. A decision committed before the read is visible. One committed during the read is either fully in or fully out. The count and the item fields come from the same read.

## PUT /api/restock/new-item-policy

Sets the household policy. Household-scoped. Products and groups the household already has stay as they are, except the explicit Restock of waiting rows when leaving an Ask mode for `auto`.

### Request

```json
{ "policy": "ask_hold" }
```

Leaving an Ask mode for `auto` while something is waiting also requires:

```json
{ "policy": "auto", "answerWaiting": "restock" }
```

`answerWaiting` is only legal as the string `restock`. Any other value, including `dont_restock`, is rejected.

### Response `200`

The same snapshot as `GET /api/restock`, after the change.

Switching to `auto` with `answerWaiting: "restock"` answers every current decision subject as Restock, then saves `auto`. Groups are answered first, so their waiting members are answered with the group and do not also get an item target. The snapshot then has `newItemPolicy` `auto` and `needsDecision` 0.

### Errors

| Status | Code | When | Sentence |
|---|---|---|---|
| 400 | `invalid_policy` | `policy` is missing, null, empty, or not one of the three values | Choose what happens with new products. |
| 400 | `invalid_answer` | `answerWaiting` is present and is not `restock` | The waiting items can only be answered as restock. |
| 400 | `invalid_answer` | `answerWaiting` is present and `policy` is not `auto` | That answer is only used when switching back to restocking automatically. |
| 409 | `answer_waiting` | Current policy is an Ask mode, `policy` is `auto`, at least one subject is waiting, and `answerWaiting` is absent | See below. |
| 500 | *(none)* | Unexpected failure | Something went wrong. Try again. |

Malformed JSON and an oversized body use the shared sentences above, with no code.

The 409 body adds `waiting`, the same number as `needsDecision`. The sentence uses "item" / "items" for that number, groups included, matching the confirm copy.

One waiting:

```json
{
  "error": "1 item is waiting. It'll be restocked with its usual target.",
  "code": "answer_waiting",
  "waiting": 1
}
```

More than one:

```json
{
  "error": "6 items are waiting. They'll be restocked with their usual target.",
  "code": "answer_waiting",
  "waiting": 6
}
```

Nothing is written on a 400 or a 409.

### Idempotency and concurrency

The check and the write are one transaction.

- Saving the policy the household already has returns 200 and does not answer anyone again. If the policy is already `auto`, a body that also has `answerWaiting: "restock"` is still 200 and changes nothing. A bad `answerWaiting` is still 400.
- Switching to `auto` when nothing is waiting succeeds without `answerWaiting`.
- A retry after a successful switch to `auto` sees `auto` and waiting 0, so it does not require `answerWaiting` and does not undo the answers.
- Two concurrent saves serialize. The second sees the first commit. Only one of them can observe the waiting rows and answer them. The 409 cannot be skipped by a race.

## GET /api/inventory

The existing list. Each row keeps its shape, and gains the decision fields. `q` still searches name and category.

`needsDecision` on the item is true only when that item is itself a decision subject: its state is `waiting`, it is unshelved, and it is not a member of a waiting group that counts. `decision` is omitted otherwise.

The nested `group`, when present, gains the group's `needsDecision` and, when that is true, the group's `decision` (including `newMembers`).

### Query `needsDecision`

- Omitted: the full unshelved list, with the new fields.
- `true`: only the item rows whose own `needsDecision` is true. Members of a waiting group are not in this list. Waiting groups are not inventory rows. They come from `GET /api/groups`.
- Any other value, including `false`, `1`, or `yes`: 400. It is not coerced.

With no `q`, the filter matches the count:

```
snapshot.needsDecision
  = number of rows from GET /api/inventory?needsDecision=true
  + number of groups from GET /api/groups with needsDecision true
```

`q` narrows the item rows further. It does not change the snapshot count.

### Response `200`

An array, the same as today. One waiting item under `ask_hold`:

```json
[
  {
    "item": {
      "id": "item-tc",
      "userId": "user-1",
      "productId": "prod-tc",
      "product": { "id": "prod-tc", "name": "Tortilla chips", "category": "Snacks", "unitOfMeasure": "bag" },
      "targetQuantity": null,
      "createdAt": "2026-10-18T00:00:00Z",
      "replenishmentMode": "target"
    },
    "instanceCount": 3,
    "nearExpiryCount": 0,
    "expiredCount": 0,
    "needsAttention": false,
    "needsDecision": true,
    "decision": {
      "usualTarget": { "kind": "months", "value": 2 },
      "usualSourceLabel": "From Snacks & sweets"
    }
  }
]
```

A member of a waiting group, on the unfiltered list, looks like today's row plus:

```json
{
  "needsDecision": false,
  "group": {
    "id": "group-sk",
    "name": "Salad kits",
    "needsDecision": true,
    "decision": {
      "usualTarget": { "kind": "months", "value": 3 },
      "usualSourceLabel": "Household default",
      "newMembers": [
        { "itemId": "item-sm", "name": "Spring mix kit" },
        { "itemId": "item-ck", "name": "Caesar kit" }
      ]
    }
  }
}
```

The other group fields (`rule`, `members`, `onHand`, and the rest) are unchanged. `?needsDecision=true` omits this member.

### Errors

| Status | Code | Sentence |
|---|---|---|
| 400 | `invalid_needs_decision` | needsDecision has to be true. |
| 500 | *(none)* | Something went wrong. Try again. |

### Idempotency and concurrency

Read-only. Same visibility rule as `GET /api/restock`: a committed answer is fully in or fully out. The query does not reserve the rows. A later `POST /api/restock/decisions` may skip a row that left `waiting` after this read.

## GET /api/groups and GET /api/groups/{id}

The existing group objects, plus `needsDecision` and `decision` on every group. `needsDecision` is always present. `decision` is omitted when it is false.

`GET /api/groups/{id}` is the object inside `updated` when a decision returns a group (plus today's `usage` field when the handler already adds it).

No new query parameter. Clients find the decision groups by `needsDecision: true` on this list.

A waiting group on the list:

```json
{
  "id": "group-sk",
  "name": "Salad kits",
  "rule": "same_as_ran_out",
  "ruleConfirmed": false,
  "onHand": 4,
  "memberCount": 3,
  "runningLow": false,
  "members": [
    { "productId": "prod-sm", "name": "Spring mix kit", "onHand": 2 }
  ],
  "needsDecision": true,
  "decision": {
    "usualTarget": { "kind": "months", "value": 3 },
    "usualSourceLabel": "Household default",
    "newMembers": [
      { "itemId": "item-sm", "name": "Spring mix kit" },
      { "itemId": "item-ck", "name": "Caesar kit" }
    ]
  }
}
```

Existing member fields stay as they are today. `needsDecision` and `decision` are the only additions.

### Errors

Unchanged from those routes today, plus the shared 500 sentence.

## POST /api/restock/decisions

Answers one or more waiting subjects. Household-scoped. An id that is not this household's item or group is 404, including an id that belongs to some other `user_id`. The sentence does not say whether the id exists elsewhere.

### Request

```json
{
  "subjects": [
    { "type": "item", "id": "item-tc" },
    { "type": "group", "id": "group-sk" }
  ],
  "decision": "restock"
}
```

`decision` is `restock` or `dont_restock`.

Duplicate subjects in one body are one subject. The first copy is kept. That is not an error.

### Response `200`

```json
{
  "updated": [
    {
      "type": "item",
      "item": {
        "itemId": "item-tc",
        "productId": "prod-tc",
        "name": "Tortilla chips",
        "unit": "bag",
        "target": { "kind": "months", "value": 2 },
        "level": "category",
        "levelName": "Snacks & sweets",
        "sourceLabel": "From Snacks & sweets",
        "valueLabel": "2 months",
        "needsDecision": false,
        "categories": [{ "id": "cat-snacks", "name": "Snacks & sweets" }],
        "setLabel": "Set for this item"
      }
    },
    {
      "type": "group",
      "group": {
        "id": "group-sk",
        "name": "Salad kits",
        "needsDecision": false
      }
    }
  ],
  "skipped": [
    { "type": "item", "id": "item-ba", "reason": "not_waiting" }
  ],
  "undoToken": "4f1c0b2e-7a31-4d0e-9c5a-1b2d3e4f5a6b"
}
```

`updated` lists only subjects this call changed, in request order. A group answer that also changed members lists the group and those members. Each item value is the `ItemView` after the write. Each group value is the group object after the write. Examples below omit unchanged fields. The wrapper exists so a client can tell them apart. The issue's `[ItemView | GroupView]` union is this wrapper.

`skipped` lists subjects that are in the household and are not waiting. The only `reason` is `not_waiting`. They are not an error. Subjects this call did change are not skipped.

`undoToken` is present only when `updated` is non-empty. It is an opaque string. When every subject was skipped, the body is `{ "updated": [], "skipped": [ ... ] }` with no `undoToken`.

Restock writes no target. Don't restock writes 0 at that level, as in Answers. Answering a group follows the group rules in Concepts.

After the decision transaction commits, the shopping list is refreshed the same way `PUT /api/restock/items/{id}` refreshes it. Don't restock drops an unpurchased planned line. A purchased row stays. A manual row stays.

### Errors

| Status | Code | When | Sentence |
|---|---|---|---|
| 400 | `invalid_decision` | `decision` is missing or not `restock` or `dont_restock` | Choose restock or don't restock. |
| 400 | `invalid_decision` | `subjects` is missing or empty | Choose at least one product or group. |
| 400 | `invalid_subject` | A `type` is not `item` or `group`, or an `id` is missing or empty | Choose a product or a group. |
| 404 | `not_in_household` | An item id is not this household's item | That item isn't in the household. |
| 404 | `not_in_household` | A group id is not this household's group | That group isn't in the household. |
| 404 | `not_in_household` | More than one id is missing | One of those isn't in the household. |
| 500 | *(none)* | Unexpected failure | Something went wrong. Try again. |

A 400 or 404 writes nothing and does not refresh the shopping list. Not-waiting is not a 404.

### Idempotency and concurrency

Not idempotent. A second identical call finds the subjects already answered, returns them in `skipped`, and does not mint a token.

The apply is one transaction. Each subject is updated only if its state is still `waiting` (a group also updates only those members that are still `waiting` and have no item target). Two concurrent answers for the same subject: one commits, the other reports `not_waiting`. There is no 409 for that race.

A preview is not a lock and is not sent back. The POST applies to the state at commit time, not to the state the preview showed. That is the stale-preview rule.

## POST /api/restock/decisions/preview

Same request body as `POST /api/restock/decisions`. Read-only. Does not answer, does not write a target, does not mint an undo token, does not refresh the shopping list.

The response is the #240 `Preview` plus one field on each line, `answers` (boolean). `answers` is true when this decision would clear that item's waiting state. A group subject is expanded to its member items, the same way `POST /api/restock/groups/{id}/preview` expands a group. `stop` and `keep` still compare the resolved shopping target before and after. A held item whose target is already 0 stays in `keep` for Don't restock, and `answers` is still true.

`confirm` counts request subjects that are still waiting. A group counts as one, not as its members. The button can say 6 when only some of those 6 change the shopping target.

### Copy

A single group and `dont_restock` uses the #240 group preview of `{ "kind": "months", "value": 0 }` for `title`, `body`, `stop`, `keep`, and the per-line strings. `confirm` is still the waiting-subject count: `Stop restocking 1` when the group is waiting, even if `stop` is empty. `answers` is added on each line.

Everything else:

| Request | Title | Body | Confirm |
|---|---|---|---|
| One item, `restock` | Restock Tortilla chips? | Tortilla chips keeps its usual target. Nothing is saved on it, so a later change to that target still reaches it. | Restock N |
| One item, `dont_restock` | Stop restocking Tortilla chips? | Tortilla chips leaves the shopping plan. It stays in inventory, and you can still add it to the list by hand. | Stop restocking N |
| One group, `restock` | Restock Salad kits? | Salad kits keeps its usual target. Nothing is saved on the group, so a later change to that target still reaches it. | Restock N |
| Several subjects, `restock` | Restock these? | They keep their usual target. Nothing is saved on them, so a later change to that target still reaches them. | Restock N |
| Several subjects, `dont_restock` | Stop restocking these? | These items leave the shopping plan. They stay in inventory, and you can still add them to the list by hand. | Stop restocking N |

N is the number of subjects in the body that are still waiting. The name in a one-subject title is the item or group name.

A line for a subject that is not waiting is in `keep` when the target would not change, with `answers: false`. Its `reason` is today's won't-change reason when one applies, otherwise `Already decided.`

### Response `200`

```json
{
  "title": "Restock these?",
  "body": "They keep their usual target. Nothing is saved on them, so a later change to that target still reaches them.",
  "stop": [
    {
      "itemId": "item-tc",
      "name": "Tortilla chips",
      "changes": true,
      "before": "0",
      "after": "2 mo",
      "answers": true
    }
  ],
  "keep": [
    {
      "itemId": "item-ba",
      "name": "Bananas",
      "changes": false,
      "after": "0",
      "reason": "From Produce",
      "answers": true
    }
  ],
  "confirm": "Restock 2"
}
```

### Errors

The same 400 and 404 table as `POST /api/restock/decisions`. A not-waiting subject is not an error. It appears in `keep`.

### Idempotency and concurrency

Read-only and safe to repeat. It shows the state at read time. A decision made after the preview is not reflected. The following POST does not check that this preview is still current.

## POST /api/restock/decisions/undo

Restores exactly the rows recorded for that token: waiting state, `decided_at`, and the target at that level. Then refreshes the shopping list the same way a target write does. Purchased rows are left alone. Undo does not put a bought line back.

### Request

```json
{ "undoToken": "4f1c0b2e-7a31-4d0e-9c5a-1b2d3e4f5a6b" }
```

### Response `200`

The same `updated` array as `POST /api/restock/decisions`, listing the restored item and group views. Those views are waiting again where they were waiting before. There is no `undoToken` and no `skipped`. Undo of undo is not supported.

```json
{
  "updated": [
    {
      "type": "item",
      "item": {
        "itemId": "item-tc",
        "name": "Tortilla chips",
        "target": { "kind": "months", "value": 0 },
        "level": "waiting",
        "sourceLabel": "Not restocked yet",
        "needsDecision": true,
        "decision": {
          "usualTarget": { "kind": "months", "value": 2 },
          "usualSourceLabel": "From Snacks & sweets"
        }
      }
    }
  ]
}
```

### Errors

| Status | Code | When | Sentence |
|---|---|---|---|
| 400 | `invalid_undo` | `undoToken` is missing or empty | That undo isn't available. |
| 404 | `unknown_undo` | The token is unknown, or it belongs to another household | That undo isn't available. |
| 409 | `stale_undo` | The token was already used | That undo was already used. |
| 409 | `stale_undo` | A recorded row was deleted, or its state or target is no longer what this answer wrote | That change is already out of date. |
| 500 | *(none)* | Unexpected failure | Something went wrong. Try again. |

A 409 does not consume the token when the reason is a mismatch, so a retry still returns 409 rather than 404. An already-used token stays used.

### Idempotency and concurrency

Not idempotent. The second use of a token that succeeded is 409 `That undo was already used.`

The check and the restore are one transaction. Undo applies only when every recorded row still has the state and the target this answer wrote. A target write or a second answer that landed after the token was minted makes the token stale, the transaction writes nothing, and the client gets 409. Two concurrent undos of the same token: one commits and marks it used, the other gets 409.

## Existing routes that answer

These routes keep their current request and response bodies. When the write clears a `waiting` state, they also perform the answer below. They do not return `undoToken`. The row-menu undo that puts the previous target back does not restore `waiting`. Exact restore is only `POST /api/restock/decisions/undo`.

| Route | While the subject is waiting |
|---|---|
| `PUT /api/restock/items/{itemId}` value 0 | State `dont_restock`. The target write is the 0. |
| `PUT /api/restock/items/{itemId}` value above 0 | State `restock`. The target write is that target. |
| `DELETE /api/restock/items/{itemId}` | State `restock`. The target row is removed, which is what this route already does. |
| `POST /api/restock/items/{itemId}/dont-restock` | State `dont_restock`. Response stays `{ "previous", "title", "detail" }`. |
| `PUT /api/groups/{id}/target` saved value 0 | The group is answered `dont_restock`, and its waiting members with no item target are answered with it. |
| `PUT /api/groups/{id}/target` any other saved target, including `clear: true` | The group is answered `restock`, and those same members are answered with it. |

A write on a subject that is not waiting does not change `state`. Category targets, category membership, and `PUT /api/restock/household` do not answer.

Creating an item or a group follows What is new and Groups. Deleting a group follows Groups. Wipe does not delete decision rows. Delete of an item row does, via the trigger. There is no public delete-item route today. The trigger is what makes a later delete behave.

## SSE

`GET /api/events` gains one event type, `restock_decisions`. The frame is the existing one: `id`, `event`, `data`. There is no replay. A client that reconnects misses what it missed, then refetches.

Published after the decision transaction commits, including when the later shopping refresh fails and the HTTP call returns 500. Not published when nothing about the policy or the waiting set changed.

### Payload

```json
{
  "newItemPolicy": "ask_hold",
  "needsDecision": 5,
  "reason": "decision",
  "subjects": [{ "type": "item", "id": "item-tc" }]
}
```

| `reason` | When | `subjects` |
|---|---|---|
| `policy` | A PUT changed the policy | Subjects that PUT answered, or `[]` when only the plan changed (`ask_restock` ↔ `ask_hold`) |
| `decision` | An answer or an undo changed state | The subjects whose state changed |
| `waiting` | A new row entered `waiting`, or a covered member became its own row | The subjects that are newly their own decision |

`needsDecision` and `newItemPolicy` are the values after the commit. The payload is a hint. Clients refetch `GET /api/restock` and the lists. It does not include a household id. The stream is still the one household. #224 should scope the stream, not add a second event name.

A repeated PUT of the same policy does not fire. A POST that only skips does not fire.

## Shopping list

No new shopping route. Banners read `newItemPolicy` and `needsDecision` from the snapshot.

Under `ask_hold`, a waiting subject resolves to 0, so the planner does not buy it. Under `ask_restock`, it resolves as usual, so it can be on the list and it can be bought. Manual rows work in both modes, including while the resolved target is 0. Don't restock removes the unpurchased planned line through the existing refresh. A purchased row stays until stock-in. Undo uses that same refresh and does not bring a purchased line back.

The planner treats resolved level `waiting` and value 0 like don't restock. A held solo item is not planned. Members of a held waiting group are not planned as a pooled buy. A member with its own item target is planned on its own, unchanged from #240.

## Household scope

Every read and write in this contract filters `household_id = restock.HouseholdID` (`user-1`). Item lookup also requires `items.user_id` to be that id. Group lookup requires `product_groups.user_id` to be that id. A row for any other id is invisible: it is not in the snapshot, the inventory filter, or the count, and answering it is 404 `not_in_household`.

Clients cannot pass a household id. When #224 lands, the same columns take the authenticated household id. Do not store the policy only in `app_settings`.

## Acceptance map

| Backend acceptance bullet | Where it is settled |
|---|---|
| Policy stored per household, default `auto`, GET/PUT scoped | Migration, GET and PUT `/api/restock` |
| Planner unchanged under `auto` | Resolution, Shopping list |
| New item waits; re-scan and post-wipe stock-in do not | What is new, Storage |
| New group waits only if a member is waiting | Groups |
| `ask_hold` resolves to 0 at level `waiting`; `ask_restock` resolves normally; members follow the group | Resolution |
| Restock writes no target; Don't restock writes 0; a target write answers | Answers, Existing routes |
| Answering a group answers waiting members with no item target | Groups |
| A waiting item that joins a decided group stops waiting | Groups |
| Undo restores waiting and the prior target, and not a purchased line | Undo, POST undo |
| Delete removes the decision row; wipe keeps it | Storage |
| Leaving an Ask mode requires `answerWaiting` and answers Restock | PUT policy |
| Count and filter match; groups once; covered members excluded | Count, GET inventory |
| Invalid policy or decision is 400 and not coerced | Each route's error table |
| Households cannot see or answer each other | Household scope |
| SSE on every policy change and every answer | SSE |
| Migration leaves nothing waiting | Storage backfill |

## Open questions

None of the behavior above is blocked on these. The sentences are the contract until they change. They are called out because the issue does not say the words.

1. **Preview sentences other than the single-group Don't restock case.** That one case reuses the #240 preview strings. The Restock titles and bodies, and the bulk Don't restock title and body, are written in the preview section. They are not in the issue.
2. **Undo tokens do not expire by time.** The issue does not say how long Undo stays available. The contract keeps a token until it is used or until the subject changes.
