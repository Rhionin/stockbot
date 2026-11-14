# #571 W3: re-plan on read API contract (docs only)

**Status:** design input for the build PR. **Not build approval.** Do not implement until this contract is merged and UX marks the spec ready (UX spec **revision 4b**, *Stockbot UX Designer: one flow for W3 (automatic list), W4 (restock or send one item) and W7 (stock status at a glance) (#571)* — not on master at contract time).

**Sources (authority order):**

1. Architect [#571](https://github.com/Rhionin/stockbot/issues/571) comments [6743569954](https://github.com/Rhionin/stockbot/issues/571#issuecomment-6743569954) (W3 coalescer), [6744772201](https://github.com/Rhionin/stockbot/issues/571#issuecomment-6744772201) (basis, `list_line_changes`, read-time plan), [6745010424](https://github.com/Rhionin/stockbot/issues/571#issuecomment-6745010424) (`needsAttention` / `status=1` envelope), [6745462489](https://github.com/Rhionin/stockbot/issues/571#issuecomment-6745462489) (rev 2.4, store fields).
2. UX spec revision 4b (*Stockbot UX Designer* flow for W3/W4/W7, #571; W4/W7 called out only where W3 must leave room).
3. CJ copy keys and sentences in `uploads/571-copy-for-cj-rev2.md` (rendered by `shared/listCopy.ts`, not invented in this doc).
4. Master at `ae1650af` or newer: W1 schema in `031_trip_scoped_skips.sql`, W2 read style in `GET /api/shopping-list/send-results`.

W4 (`POST /api/shopping-list/restock`, scoped save) and W7 (`/summary` counts, glance copy) are mostly separate build PRs. This contract defines the **automatic list maintenance** path and the **why this line changed** payload they depend on.

---

## 1. Summary

| Piece | Behavior |
|---|---|
| **Background coalescer** | After stock or setting commits, mark the household dirty; one debounced job runs `Plan` and **scoped save** (no `deleteUnplannedAuto`). Each run upserts affected lines and `list_line_changes`, then bumps the `list` version once (even when quantities are unchanged but `planBasis` moved). |
| **Read path** | `GET /api/shopping-list` (and `/summary`, item status) **wait up to 250 ms** for the writer to finish **one pending coalescer job**, then a **read-only `Plan` on the read pool**. If the job does not finish, respond **`200`** with last-good staged rows, fresh **`planBasis`**, and **`listStale: true`** (never `5xx`). **No provider I/O** on read. |
| **Send path** | `POST /api/shopping-list/export` drains the same pending job first; on writer contention, wait up to the **2 s** `busy_timeout`, then **`409`** — never send from a stale list. |
| **Explanation** | Latest automatic change per line lives in `list_line_changes` (schema from W1). The API exposes it as `lastChange` on entries and off-list rows. Copy uses effect + cause + optional `reason` (taken off only) via `shared/listCopy.ts`. |
| **Compatibility** | Without `?status=1`, the list body stays today's **bare array** of **`ShoppingListEntryW3`** when `since` is absent. On a **W3 server**, each row always includes **`sent`** (**`0`** when none), **`canSendToCart`** (boolean), and **`storeId`** (**`string \| null`**); optional JSON keys (`planBasis`, legacy `basis`, `lastChange`, `touched`, `lineKey` on default GET) may be omitted when empty (§4.1). With `?since=` and **`status=1`**, `changed: true` returns the **full §4.2 envelope** (not `items`-only). Without `status=1`, `changed: true` stays `{ cursor, changed, items }` as today. Pre-W3 servers: §8. |

---

## 2. When re-plan runs

### 2.1 Background coalescer (writes staged lines)

**Triggers** (same household, coalesced per burst):

| Event | Affected keys | Typical `cause` |
|---|---|---|
| Stock-out commit (single or batch) | Items (and owning group lines) in the burst | `taken_out` |
| Stock-in commit | Same | `put_away` |
| Restock target / replenishment change | Item or category keys `refreshPlan` already touches | `target_changed` |
| Group target / membership change | Group line + members per planner | `group_changed` |
| **Start a new cart** / mass stock-in **trip advance** | Lines that change at boundary | `new_trip` |
| Opening **This scan is complete** | Setup-gated lines | `setup_finished` |
| **W4** `POST /api/shopping-list/restock` (scoped plan) | One item or group key | `restocked_here` |
| **Full** `POST /api/shopping-list/fill` | All keys the full plan touches | `list_updated` |
| Process **startup** + lost burst after restart | All keys the catch-up plan touches | `list_updated` (`actor` null) |
| **Day 30** after setup start (rate first eligible) | Lines that flip replace → rate | `usage_learned` |

**Not triggers:** `GET /api/shopping-list`, live hint handling, other read-only routes. (Export/send **do not** enqueue the coalescer; they **drain** one pending job before sending — §3.5.)

**Rules:**

- Do **not** call `Plan` inside the scan-commit transaction (writer pool is single-connection).
- Debounce: reset a per-household timer on each trigger in a burst; **one** `Plan` per burst (Architect W3).
- Save path: **scoped save only** — upsert changed auto lines; delete untouched unpurchased auto rows **only for affected keys** when buy goes to 0; never `deleteUnplannedAuto`; **touched** and **manual** rows unchanged.
- Each completed run: bump `list` version **once** and publish one `list` hint (even if only `planBasis` would change on read).

**Trip advance (`trip_seq` +1)** — **one writer transaction** (Start a new cart or mass stock-in close):

1. Run **`Plan` on the read pool** first (inputs for boundary upserts and `list_line_changes` payloads).
2. Open **one short writer transaction** that, in order:
   - Advances `trip_seq` and writes `trip_closed_at` / `trip_closed_cause` (W1).
   - **`DELETE` prior-trip `list_line_changes`:** `DELETE … WHERE trip_seq < new_seq` **before** any inserts for the new trip.
   - Applies boundary upserts (lines, skips, adjustments, `attention_marks`, and `list_line_changes` for the new trip, including `cause: new_trip` from skip expiry).
3. **Commit once.** Every new `list_line_changes` row uses the **new** `trip_seq`.

**Anti-pattern (forbidden):** `AdvanceSeq` (or equivalent) **must not commit** in the scan/mass stock-in transaction while `new_trip` rows are written later by the coalescer in a second commit. A crash or a read-pool snapshot between those commits could observe an advanced `trip_seq` with **empty or mixed** change history (the PK allows only one row per `line_key`). Trip advance boundary work is **not** split across the scan commit and a follow-up job.

### 2.2 Read path (drain on writer; plan on read pool)

Before building the response, the handler:

1. **Drain (writer)** — If a coalescer job is queued or running for this household, wait up to **250 ms** on the **writer** for that **one** job to finish (not a second plan, not full fill). If it does not finish: continue with **last-good staged rows** and set **`listStale: true`** (Architect §11 Resolved (a)); **never `5xx`** for drain timeout.
2. **Plan (read-only)** — Run `Plan` on the **read pool** (`supply.Open(readDB)`).
3. **Assemble** — Merge staged rows, read-time plan output, ledger, adjustments, skips, `list_line_changes`, `cart_line_results`, store context; compute legacy **`basis`** with the **same logic as master today** (shape unchanged — **T28** locks byte-compatible JSON on default GET), **`planBasis`** from read-time plan (additive), **`sent`**, `quantity` (`toGet`), `lastChange`, and (when `status=1`) `needsAttention` / `offList`.

Re-reading the list **does not** write `list_line_changes` again unless a drain in step 1 actually applied a new material change.

---

## 3. Endpoints

### 3.1 `GET /api/shopping-list` (primary W3 surface)

| | |
|---|---|
| Auth | Session |
| Role | Household **member** |
| `X-Household-Id` | Required on hosted API |
| Query | `since` (optional, unchanged), `provider` (optional, unchanged), **`status`** (optional, see §4) |

**Processing order:** §2.2 (drain → read-only plan → assemble).

**Response encoding** ([change_view.go](https://github.com/Rhionin/stockbot/blob/master/internal/server/change_view.go) — **W3 extends** it for `status=1` and the full-envelope `since` case):

| `since` | Body |
|---|---|
| absent, no `status=1` | Bare `ShoppingListEntry[]` (today's clients) |
| absent, `status=1` | Full §4.2 envelope including `listStale` |
| present, `changed: false` | `{ "cursor", "changed": false }` only + `Cache-Control: no-store` (even with `status=1`) |
| present, `changed: true`, no `status=1` | `{ "cursor", "changed": true, "items": [...] }` |
| present, `changed: true`, `status=1` | **Full §4.2 envelope** with updated `cursor` (not `items`-only delta) |

**Errors:** `401 unauthorized`, `404 not_found` (non-member / unknown household), `400 validation` (malformed `since`). No `502` from providers on this route. Drain timeout → **`200`** + `listStale: true`, not `5xx`.

### 3.2 `GET /api/shopping-list/summary` (W7 hook; W3 read path)

Same §2.2 processing, **counts only** (no per-row adjustment N+1). Response shape **`ShoppingListSummaryW3`** (§4.2). Full field list is owned with W7; W3 requires the **same drain + read-only plan** so counts cannot disagree with the list.

### 3.3 Routes that must **not** gain an implicit full fill

Inventory GET and other reads keep today's behavior unless listed above. #570 "compare plan to row" diagnostics stay **read-only** and must not call scoped save (Architect W3).

### 3.4 Unchanged mutation routes (context)

| Route | W3 interaction |
|---|---|
| `POST /api/shopping-list/fill` | Full fill + `deleteUnplannedAuto` (manual "Update the list"); writes `list_updated` changes |
| `POST /api/shopping-list/items/{itemId}/undo-remove` | Scoped replan (W1); may write `restocked_here` or `list_updated` |
| `POST /api/shopping-list/restock` | W4 scoped replan; `restocked_here` |
| Scan commit routes | Enqueue coalescer; do not block on plan in commit txn |

### 3.5 `POST /api/shopping-list/export` (send / scoped send)

**Locked** — full wire table and `list_stale` copy live in [api-contract.md](./api-contract.md) (*Error code registry*).

Before computing export lines or calling the provider:

1. **Drain (writer)** — Finish **one** pending coalescer job for this household on the **writer** (no read-path **250 ms** cap; no `listStale` on this route).
2. **Writer lock** — If the writer is still busy (coalescer, trip advance, another send), wait up to the connection **`busy_timeout` (2 s)**.
3. **Failure** — If the list cannot be made current within that wait: **`HTTP 409`**, body `{ "error": "…", "code": "list_stale" }` (proposed sentence in api-contract), **zero provider calls** — never send from a stale list.

**Never** send from rows that could still be updated by a pending coalescer job. W4 `itemIds` scoped sends use the same drain-and-wait rules.

---

## 4. JSON shapes

### 4.1 `ShoppingListEntryW3` (list `items[]`)

Canonical W3 list row: **`ShoppingListEntryW3`** extends today's [ShoppingListEntryResponse](https://github.com/Rhionin/stockbot/blob/master/internal/server/handler_shopping_list.go) / `ShoppingListEntry` in [shared/types.ts](https://github.com/Rhionin/stockbot/blob/master/shared/types.ts). **`ShoppingListEntryStatus`** (§4.2) adds `status=1`-only fields. Field rules:

| Field | Type | Omitempty | Nullability | Meaning |
|---|---|---|---|---|
| `basis` | `ShoppingListBasis` (legacy) | yes | as today | **Unchanged** from master (`mode`, `instanceCount`, …). Web `ShoppingListPage` reads `basis.instanceCount`; W3 **must not** repurpose this object. |
| `planBasis` | `ShoppingListPlanBasis` | yes | absent when not computed | Read-time planner numbers for CJ copy §E.1 (W3 only). |
| `sent` | `number` | **no** | n/a | Always emitted, **`0`** when none. Ledger `requested` since last stock-in for this line's cart store. |
| `touched` | `boolean` | yes | n/a | Person edited quantity; coalescer must not overwrite quantity |
| `storeId` | `string \| null` | **no** | always key | Line's effective store; **`null`** when no store (rev 2.4 G8). |
| `canSendToCart` | `boolean` | **no** | n/a | **Always emitted.** Authoritative for cart vs in-person UX for that line. |
| `lastChange` | `ListLineChange` | yes | `null` when no row | Latest `list_line_changes` for this line key |
| `needsAttention` | `NeedsAttention` \| `null` | **no** (`status=1` only) | `null` when not flagged | **Always present** on each row when `status=1`; key never omitted (Architect §11 Resolved (a)); shape §4.7 |
| `lineKey` | `string` | yes (default GET); **no** when `status=1` | n/a | Planner key `item:<itemId>` \| `group:<groupId>`; matches `attentionGroups.rowKeys` and `offList.id` |

**Unchanged:** `id`, `itemId`, `quantity` (= **`toGet`** in UX copy), `source`, `note`, `purchasedAt`, `replenishmentMode`, `provider` (only when line is cart-eligible), `computedQuantity`, `adjustment`, `group`.

**Legacy `ShoppingListBasis`** — same as `shared/types.ts` today (handler may omit it until populated; shape is fixed):

```ts
interface ShoppingListBasis {
  mode: ReplenishmentMode;
  targetQuantity?: number;
  instanceCount: number;
  consumedUnits: number;
  requested: number;
}
```

**`ShoppingListPlanBasis`** (read-time plan; no DB column):

```ts
interface ShoppingListPlanBasis {
  onHand: number;
  onHandAmount?: number;
  unit?: 'g' | 'ml';
  target: ShoppingListPlanTarget;
  ratePerMonth?: number;
  replacing?: number;
  short: number;
}

type ShoppingListPlanTarget =
  | { kind: 'amount'; value: number }
  | { kind: 'months'; value: number; par: number }
  | { kind: 'group_amount'; goal: number; unit: 'g' | 'ml' }
  | { kind: 'none' };
```

```ts
/** Default GET `items[]` and base for status=1 rows — same name in api-contract.md. */
interface ShoppingListEntryW3 extends ShoppingListEntry {
  planBasis?: ShoppingListPlanBasis;
  sent: number;
  storeId: string | null;
  canSendToCart: boolean;
  touched?: boolean;
  lastChange?: ListLineChange | null;
  lineKey?: string;
}
```

### 4.2 `GET /api/shopping-list?status=1` envelope

When `status=1`, the first-load body (no `since`) is an object so the web list can render Needs attention, Short now, and Not on the list without client-side rules:

```ts
interface ShoppingListStatusResponse {
  cursor: string;
  listStale: boolean;
  items: ShoppingListEntryStatus[];
  offList: OffListRow[];
  stores: ShoppingListStoreInfo[];
  attentionGroups: AttentionGroup[];
  storeDisabled: { providerId: string } | null;
  lastTripEndedAt: string | null;
  lastTripEndedBy: 'new_cart' | 'put_away' | null;
}

/** status=1 list row: W3 fields plus required lineKey and needsAttention. */
interface ShoppingListEntryStatus extends ShoppingListEntryW3 {
  lineKey: string;
  needsAttention: NeedsAttention | null;
}

interface AttentionGroup {
  reason: string;
  /** Machine code only — clients map through `listCopy.ts` / CJ copy; never show `detail` raw on screen (UX §11 Resolved (c)). */
  detail?: string;
  count: number;
  /** Count {k} of rows in this group still flagged after the last trip — CJ heading suffix: `· {k} still not fixed after your last shopping trip.` Not a boolean and not a row list. */
  stuck: number;
  /** Each value equals `items[].lineKey` or `offList[].id` (see row-key invariant below). */
  rowKeys: string[];
}
```

- **`listStale`:** `true` when §2.2 drain did not finish within 250 ms; staged quantities may lag until a later GET or hint refresh.
- **`offList`:** Rows with **no** matching `shopping_list_items` row, including §5 "Not on the list" **and** **flagged rows with no staged entry** (`not_planned`, connection/product issues where the planner never wrote a line). Same `lastChange` / `needsAttention` / `planBasis` rules where applicable.

**`OffListRow`** — stable identity is the planner **`line_key`**, not a shopping-list row id (Architect §11 Resolved (c)):

```ts
/** id is always line_key (`item:<itemId>` or `group:<groupId>`), never "". */
interface OffListRow {
  id: string;
  itemId?: string;
  groupId?: string;
  productId?: string;
  name: string;
  offListReason:
    | 'removed'
    | 'taken_off'
    | 'opening'
    | 'zero_for_trip'
    | 'not_on_list'
    | string;
  quantity?: number;
  basis?: ShoppingListBasis;
  planBasis?: ShoppingListPlanBasis;
  sent: number;
  storeId: string | null;
  canSendToCart: boolean;
  lastChange?: ListLineChange | null;
  needsAttention: NeedsAttention | null;
}
```

- Parse `itemId` / `groupId` from `id` for clients that key on pantry ids. There is no `shopping_list_items` row for many off-list shapes; **`id` is not `""`**.
- **Row-key invariant (required):** For every `attentionGroups[].rowKeys[]` value `k`, exactly one row exists in **`items[]`** with `lineKey === k` **or** in **`offList[]`** with `id === k`. Flagged lines without a staged entry **must** appear in `offList` so Needs attention groups always resolve. `count` equals `rowKeys.length`.
- **`stores`:** `{ id, name, kind: 'physical' | 'online', canSendToCart }[]`.
- **`attentionGroups`:** `AttentionGroup[]` in server priority order (rev 2.4 G1); per-group **`stuck`** is the **count** `k` for the CJ heading suffix **`· {k} still not fixed after your last shopping trip.`**, not a per-row list.

```ts
interface ShoppingListSummaryW3 {
  shortNow: number;
  needsAttention: number;
  notOnList: number;
  cursor: string;
  listStale: boolean;
  attentionGroups?: AttentionGroup[];
  lastTripEndedAt?: string | null;
  lastTripEndedBy?: 'new_cart' | 'put_away' | null;
  storeDisabled?: { providerId: string } | null;
}
```
- With `since` + `status=1`, `changed: true` returns the **full** `ShoppingListStatusResponse` shape (Architect §11 Resolved (b)); `changed: false` stays `{ cursor, changed: false }` only.

**Backward compatibility:** Clients that omit `status` never see this envelope; they receive the legacy array (or `items` inside the changes wrapper only).

### 4.3 `ListLineChange` (why this line changed)

Wire name **`lastChange`** on the entry. One row per `line_key` in DB; upserted only when an automatic write changes the line.

```ts
interface ListLineChange {
  effect: ListLineEffect;
  reason?: ListTakenOffReason;
  cause: ListLineCause;
  fromQty?: number;
  toQty?: number;
  n?: number;
  actorUserId?: string | null;
  actorDisplayName?: string | null;
  at: string;
  tripSeq: number;
}
```

| Field | Type | When set |
|---|---|---|
| `effect` | enum | Always |
| `reason` | enum | Only when `effect === 'taken_off'` |
| `cause` | enum | Always |
| `fromQty` / `toQty` | int | `amount_up` / `amount_down` |
| `n` | int | Burst quantity for stock move causes |
| `actorUserId` | string \| null | Same actor on all moves in burst; else null |
| `actorDisplayName` | string \| null | Resolved for copy `{who}`; omit name if user gone |
| `at` | RFC3339 UTC | Change instant (same txn as line write) |
| `tripSeq` | int | Household `trip_seq` when written |

**Client marker (UX §11 Resolved (b)):** Uses **both** values. The device stores **last-viewed list time as a UTC instant**; the marker shows when `lastChange.at` is after that instant. **`{when}`** in copy comes from the server **`lastChange.at`** only (via `listCopy.ts`). A `new_trip` change row stays on the server for the trip (for copy and off-list); it does **not** keep the marker visible for the whole trip once the person has seen it. Server holds no per-device state.

### 4.4 `ListLineEffect` (closed enum)

| Code | Meaning | Copy pattern (keys in `listCopy.ts`) |
|---|---|---|
| `added` | Line appeared on list | "Added" + cause clause |
| `amount_up` | Buy quantity increased | "Changed from {from} to {to}" |
| `amount_down` | Buy quantity decreased | same |
| `taken_off` | Line removed from staged list this trip | "Taken off." + `reason` sentence |

### 4.5 `ListLineCause` (closed enum)

| Code | Trigger (summary) | Clause templates (CJ copy §E.2) |
|---|---|---|
| `taken_out` | Coalesced stock-out | after {n} was/were taken out / after {who} took {n} out |
| `put_away` | Coalesced stock-in | after {n} was/were put away |
| `target_changed` | Target change incl. Don't restock | after {who} changed the restock target |
| `group_changed` | Group change | after the {group} group changed |
| `new_trip` | `trip_seq` advanced | because a new trip started |
| `setup_finished` | Opening complete | because setup is finished |
| `restocked_here` | W4 restock / W1 undo restore | when {who} tapped Restock this |
| `list_updated` | Full fill or startup catch-up | when {who} updated the list / when the list was updated |
| `usage_learned` | Day-30 rate eligibility | now that there's a month of use to go on |

New triggers add a **cause** code and copy in `listCopy.ts`, not a new screen.

### 4.6 `ListTakenOffReason` (closed enum; #570 `list.reason`)

Used only with `effect: 'taken_off'`. Sentences from CJ copy §E.2 / §E.3:

| `reason` | Meaning |
|---|---|
| `on_hand_meets_par` | On hand meets count or group-size target |
| `target_zero` | Item or group Don't restock |
| `nothing_to_replace` | No target and nothing to replace since last stock-in |
| Unknown / legacy | Client uses generic "Taken off the list." |

Do **not** use a separate `taken_off_enough` effect; use `taken_off` + `reason`.

### 4.7 `NeedsAttention` (hook for W6/W7; server computed on read)

When `status=1`, **every** list and off-list row includes the key; each flagged row sets:

```ts
interface NeedsAttention {
  reason: NeedsAttentionReason;
  /** Machine code for send/planner sub-reasons — map to CJ copy; never display raw (UX §11 Resolved (c)). */
  detail?: string;
  others: NeedsAttentionReason[];
  fix: NeedsAttentionFix;
  /** Per-row: still flagged after the last trip (D6). Distinct from `AttentionGroup.stuck`, which is the group count {k}. */
  stuck: boolean;
}
```

Enums and precedence are defined in Architect [6745010424](https://github.com/Rhionin/stockbot/issues/571#issuecomment-6745010424) §3–§4. W3 only requires that **the same read pass** that fills **`planBasis`** feeds `attention.Evaluate` with **no provider calls**.

### 4.8 Client rendering (UX)

- **`detail`:** A **code** (`store_sign_in`, `request_incomplete`, `no_product_picked`, …). Clients map it to approved sentences in `shared/listCopy.ts` / CJ copy — **never** show the wire value to users (UX §11 Resolved (c)).
- **Roles on the wire:** `fix.who` may include `{ role: 'owner' }` for permission checks. UI copy uses **"household admin"** per CJ §F — **never** show the word **"owner"** (UX §11 Resolved (e)).
- **Errors:** List and summary surfaces use **approved strings** from copy modules and `HostedErrorCode` sentences. On **`500`**, show UX generic load/refresh lines only — **never** the response body `error` field (UX §11 Resolved (f)).

### 4.9 Client behaviors (informational, Frontend Builder)

Not server requirements unless noted; expected web/native handling for W3:

| Behavior | Client |
|---|---|
| **`listStale: true`** | Render last-good rows immediately; trigger **one quiet refetch after ~1 s** (no full-page loading state). |
| **`409` `list_stale`** on send | Until `list_stale` is in `HostedErrorCode`, treat like any **unknown `409` code** (generic send error + retry on user tap). After lock: mapped copy. Retry **only** on user tap (no auto-retry loop). |
| **Live updates** | Merge patches by **`lineKey`** (and `offList[].id`); ignore unknown `rowKeys` in `attentionGroups`. |
| **`detail`** | Map through copy keys; unknown → generic fallback (UX Resolved (c)). |
| **Changed marker** | Last-viewed stamp stored **per device + household** (UTC instant). |
| **`list_stale` sentence** | §3.5 — **CJ approval** required before release. |

---

## 5. `list_line_changes` storage

Schema (W1 migration `031_trip_scoped_skips.sql`; **already on master as schema-only** until W3 writes):

```sql
list_line_changes (
  household_id  TEXT NOT NULL,
  line_key      TEXT NOT NULL,      -- 'item:<itemId>' | 'group:<groupId>'
  effect        TEXT NOT NULL,
  reason        TEXT,
  cause         TEXT NOT NULL,
  from_qty      INTEGER,
  to_qty        INTEGER,
  n             INTEGER,
  actor_user_id TEXT,
  at            TEXT NOT NULL,
  trip_seq      INTEGER NOT NULL,
  PRIMARY KEY (household_id, line_key)
);
```

### 5.1 Write rules

- Upsert in the **same transaction** as the shopping list line write and `list` version bump.
- **One row per line key** — latest change only (PM v1).
- Person-driven edits (manual quantity, remove skip, purchased toggle) **do not** write `list_line_changes` (UX: no marker on person edits).
- **Idempotency:** Re-running scoped save with the same outcome produces the same row (same PK); timestamps may advance only when qty/effect actually changes.

### 5.2 Read rules

- List GET loads changes for the household in **one query** keyed by `line_key` (no per-entry query).
- Expose only rows with `trip_seq === households.trip_seq` for off-list `taken_off` cases; stale trips are deleted at advance in the **same writer transaction** as the bump (§2.1; today's `AdvanceSeq` delete must move inside the unified trip-advance txn).

### 5.3 Retention

- **Latest change only, current trip only.** No time-based history in v1; older trips do not leave rows (PM ruling, §11 Resolved).
- On `trip_seq` advance, **one writer transaction** only (§2.1): bump `trip_seq` → **`DELETE` older `trip_seq` rows** → upsert boundary `list_line_changes` (including `new_trip`) with the **new** `trip_seq` → commit. No split commits.
- Rows written after the delete must survive it because they carry the new `trip_seq`; order is a **hard requirement**.

### 5.4 Re-read behavior

| Action | `list_line_changes` |
|---|---|
| `GET` with no pending job | Read only |
| `GET` draining one pending job | Writes only if that job changes lines |
| Repeated `GET` | No duplicate rows for unchanged lines |

---

## 6. Household scoping and auth

- All reads and coalescer writes use `requestScope` → `household_id` from session + `X-Household-Id`.
- Cross-household access: `404 not_found` (same as today), not a silent empty list.
- Any member may read; coalescer runs per household, not per user.
- Actor fields on changes use the commit user when uniform; `NULL` when mixed or unknown.

---

## 7. Performance budget

| Constraint | Requirement |
|---|---|
| Provider I/O on list GET / summary | **None** (no Kroger, no credential refresh side effects) |
| N+1 | **Forbidden** — batch ledger, adjustments (where still needed), `list_line_changes`, send results, store context |
| Read pool | List GET, summary, status use **read pool** `Plan`; writer not held for planning |
| Coalescer | At most **one** plan per burst; put-away of 30 items → **one** plan + **one** hint |
| Drain on read | **250 ms** max wait on the **writer**; then `listStale: true` and `200` (never `5xx`) |
| Drain on export | Drain pending job on **writer**; then up to **2 s** writer wait; else **`409` `list_stale`**, zero provider calls — **not** the GET **250 ms** / `listStale` path |
| Target | List GET **p95 ≤ 300 ms** on Pi 3B+ with synthetic **300-product** household (Architect Q8) |

Measure before merge; publish numbers in the build PR.

---

## 8. Backward compatibility

| Client | Behavior |
|---|---|
| Web/mobile without `status=1` | Still receive array or `{ cursor, changed, items }`; ignore unknown fields |
| **Legacy `basis`** | Stays today's `ShoppingListBasis` shape when present; W3 UI uses **`planBasis`** for new number lines. Default GET must remain safe for `ShoppingListPage` `basis.instanceCount`. |
| **`planBasis` / `lastChange` absent** | Treat as "numbers unavailable" (UX error state), not zero |
| **`lineKey`** | Omitted on default GET is fine for legacy clients; **`status=1`** requires `lineKey` on every `items[]` row for group merge (§4.9). |
| `?since=` refresh | Without `status=1`, `changed: true` → `{ cursor, changed, items }` as today. With **`status=1`**, `changed: true` → **full §4.2 envelope**; clients merge by `lineKey` (§4.9). |
| `provider` query | Still selects ledger provider for cart lines; **must not** stamp provider on in-person lines after #576 fix |
| **Pre-W3 / old server rows** | If **`lineKey`**, **`planBasis`**, **`storeId`**, **`canSendToCart`**, or **`sent`** is **missing**, treat the row as **pre-W3 shape** and fall back to today's master client behavior: merge patches by entry **`id`** (not `lineKey`); legacy **`basis`** / `ShoppingListPage` paths; no store-scoped Short now grouping; do not invent `sent: 0` or `canSendToCart: false` from absence alone. Optional W3-only keys (`planBasis`, `lastChange`) absent → §8 numbers-unavailable rule when a W3 UI needs them. |

---

## 9. API mapping for UX five states

Applies to list GET + `status=1` (and summary for native row).

| UX state | API behavior |
|---|---|
| **Ideal** | `200`; `items` + optional `offList`; `planBasis.short` matches planner; `lastChange` present when coalescer wrote; `needsAttention` populated when flagged |
| **Empty** | `200`; `items: []`, `shortNow: 0` on summary; no error |
| **Loading** | Client-side first fetch; server responds synchronously (no `202`). Subsequent hints: `since` with `changed: false` or patch object |
| **Edge** | Stale cursor → full patch (`status=1` full envelope); `listStale: true` after slow drain; `touched: true` rows preserve quantity; off-list `id` = `line_key`; offline clients use last `200` body |
| **Error** | `401` / `404` / `400` as today; drain timeout is **not** an error (`200` + `listStale`); export writer timeout → `409 list_stale` via mapped copy; **`500`:** clients show approved refresh/load strings only, never the JSON `error` body (§4.8) |

---

## 10. Migration

| Question | Answer |
|---|---|
| New migration for W3 core? | **No**, when W1 `031` is merged as on master: `list_line_changes`, `attention_marks`, trip columns, and adjustment actor columns already exist. |
| CJ approval | W1 SQL approval covers `list_line_changes` creation. W3 build **must not** add a second migration for this table. |

**Enums (Architect §11 Resolved (d)):** `effect`, `cause`, and `reason` are **application-enforced** in v1 (contract tests + handler validation). **No migration `032`.** A SQLite `CHECK` constraint is an **optional later** cut if CJ wants DB enforcement (would require a rebuild migration).

No recommended `032` for W3 at contract time.

---

## 11. Open questions

### Resolved (Architect)

1. **Drain on GET:** Wait **250 ms** on the **writer** for the pending job. If it does not finish: **`200`** with last-good staged rows, a fresh read-pool `Plan` for **`planBasis`**, and **`listStale: true`**. Never **`5xx`** for drain timeout. Plan always runs on the **read pool**; drain on the **writer**.
2. **`since` + `status=1`:** `changed: true` returns the **full** `ShoppingListStatusResponse` envelope; `changed: false` stays `{ cursor, changed: false }` only.
3. **Off-list identity:** `OffListRow.id` is the **`line_key`** (`item:<itemId>` / `group:<groupId>`), **never `""`** (§4.2).
4. **Enums:** App-enforced with tests in v1; **no `032`**; optional `CHECK` constraint later.

### Resolved (UX)

1. **`status=1` page order:** `attentionGroups`, `items`, and `offList` together support one web page: **Needs attention** (grouped by `reason` + `detail`, one shared fix per group via `AttentionGroup`), then **Short now** (`items` grouped by `storeId`), then collapsed **Not on the list** (`offList` by `offListReason`). See §4.2 row-key invariant for group membership.
2. **`Changed {when}` marker:** Device **last-viewed UTC instant** vs server **`lastChange.at`** (§4.3). `new_trip` rows stay available for copy; marker does not stay up all trip once viewed.

### Resolved (PM)

1. **Web first paint uses `GET /api/shopping-list?status=1`:** one page in UX order — Needs attention, then Short now grouped by store, then collapsed Not on the list. The phone stays on **`GET /api/shopping-list/summary`** for the interim Household row. A native shopping list is a **follow-up after W7**, not part of W3.
2. **No history beyond the latest change per line.** Rows from older trips are cleared when `trip_seq` advances; v1 explicitly dropped change history (item status and the list marker use this one row only).

---

## 12. Test plan (build PR)

Synthetic fixtures only; no live Kroger. Prefer handler tests with `exchanges()` per AGENTS.md. Each row is **fixture → action → assert**. Guard/refusal rows include a **no-side-effect lock**: table `COUNT` and row content unchanged, or a injected fake call counter stays **0**.

| # | Fixture | Action | Observable assert (incl. no-side-effect lock) |
|---|---|---|---|
| **T1** | Household `H`; pantry items `item-571-01` … `item-571-12` (12 distinct ids); staged auto lines exist; record `list` changes cursor / version `V0` and coalescer `Plan` call counter `0`. | Single HTTP transaction: batch stock-out commit for all 12 items in one burst (mixed directions allowed). | After coalescer drains: **`Plan` call count = 1** (not 12); `list` version = **`V0+1` exactly once**; exactly **one** `list` SSE hint published; `list_line_changes` rows exist only for affected keys. |
| **T2** | Same pattern as T1 but **30** put-away commits in one burst. | Put-away batch for 30 items. | **`Plan` call count = 1**; one version bump; one hint. |
| **T3** | `H` with scoped-save trigger on `item-571-a`; **second** unpurchased auto line `item-571-unrelated` that a full `POST /api/shopping-list/fill` would delete; spy on `deleteUnplannedAuto` counter `0`; record `COUNT(*)` of `shopping_list_items` for `item-571-unrelated` = `1`. | Coalescer scoped save after stock change on `item-571-a` only. | `deleteUnplannedAuto` counter **0**; unrelated line row still present; **`COUNT` unchanged**; only `item-571-a` keys touched in `list_line_changes`. |
| **T4** | Auto line `entry-touched`: `quantity=4`, `touched=true` in DB; plan would compute `quantity=7` without touch. Record `quantity` before coalescer. | Run coalescer after unrelated household dirty signal (or direct scoped save on another key). | DB `quantity` still **4**; `GET /api/shopping-list` returns that entry with **`quantity: 4`**, **`touched: true`**. |
| **T5** | Isolated writes per enum case (separate subtests or rows): **effects** `added`, `amount_up`, `amount_down`, `taken_off`; **taken_off reasons** `on_hand_meets_par`, `target_zero`, `nothing_to_replace`; **causes** `taken_out`, `put_away`, `target_changed`, `group_changed`, `setup_finished`, `restocked_here` (others covered in T7/T18/T17/T19). Record `COUNT` before each write. | Coalescer or direct writer path that produces each `(effect, cause[, reason])` tuple once. | Row exists with exact enums; GET `lastChange` matches DB; failed fixture without the write path is not this lock. |
| **T6** | Burst with stock-out `n=3` on one line and put-away `n=1` on another in same debounce window. | Mixed-direction burst. | Single change row uses **`n=3`** (larger total); **`actorUserId` null** on burst row. |
| **T7** | **Unfaulted T19 fixture** (see T19): `H`, `trip_seq=T`, two seeded `list_line_changes` at `T`, skip expiring into `new_trip` for `item:item-571-c`. Record `COUNT` and row snapshots. | Start trip advance (new cart or mass stock-in close) **without** fault injection. | `trip_seq=T+1`; `COUNT` of `list_line_changes` where `trip_seq<T+1` is **0**; `new_trip` row for `item:item-571-c` has `trip_seq=T+1`; `GET /api/shopping-list` shows it as `lastChange.tripSeq=T+1`. |
| **T8** | `H` with pending coalescer job; one `list_line_changes` row at `at=A0`. | `GET /api/shopping-list` twice (drain completes on first). | Second GET: **`COUNT` and content of `list_line_changes` unchanged**; `at` still `A0`. |
| **T9** | `H` with 50 parallel `GET /api/shopping-list` goroutines; writer `SetMaxOpenConns(1)`. | Parallel GETs. | All `200`; no deadlock; plans use read pool (no writer held for full plan duration). |
| **T10** | Four pantry fixtures: **amount** target, **months** target, **group_amount** group line, **none** target; each would yield distinct `planBasis.target.kind`. | `GET /api/shopping-list` after read-time plan. | Each matching item JSON has `planBasis.short` and `planBasis.target.kind` matching fixture; legacy `basis` still present where master populated it. |
| **T11** | Line `item:item-571-marker` with automatic `list_line_changes` row (`effect=amount_up`, `at=T0`); record `COUNT` and full row content. | Person-driven edit: PATCH quantity, remove skip, or purchased toggle (one case per subtest). | **`COUNT` unchanged**; row **byte-identical** on `effect`, `cause`, `at`, qty fields; GET still returns **same** `lastChange` (not absent, not new row). |
| **T12** | Staged line taken off this trip; `offListReason=taken_off`, `reason=on_hand_meets_par` inputs in DB. | `GET ?status=1`. | `offList[]` entry: `id` = `line_key`, never `""`; `lastChange.reason` present for copy. |
| **T13** | `H` with known `cursor` C0; change one line quantity. | `GET ?since=C0` default; then `GET ?since=C0&status=1`. | Default: `{changed:true, items:[...]}` patches by entry **`id`**. `status=1`: **full** `ShoppingListStatusResponse` on `changed:true`. |
| **T14** | `H` with saved Kroger app, connected `provider_connections` (product access), Kroger-linked store + `location_id`, cart-eligible line; `http.RoundTripper` fake with **call counter 0**. | `GET /api/shopping-list` and `GET /api/shopping-list/summary`. | Fake HTTP **call count 0**; no credential refresh side effect. Fixture without credentials is not this lock. |
| **T15** | Synthetic household with **N** list lines (e.g. N=50); enable SQL query counter in test harness. | One `GET /api/shopping-list?status=1`. | Total SQL statements **`Q(N) ≤ Q0 + k·ceil(N/50)`** with **`k ≤ 8`** fixed constants documented in the test (batch loads for changes, ledger, stores — **not** O(N) per-row queries). Query count must **not** grow linearly with N beyond that bound. |
| **T16** | Household **A** (caller) and **B** (foreign). Seed `B` `list_line_changes` on `item:item-571-b-only` with `cause=taken_out`, unique `actorDisplayName="B-ONLY-ACTOR"`. Seed `A` with distinct row on `item:item-571-a`. Record `B` `COUNT`. | As **A**: `GET /api/shopping-list` and `GET ?status=1`; then `GET` with `X-Household-Id=B` as A. | A responses contain only A `lastChange` keys; raw body **must not** contain `item:item-571-b-only`, `B-ONLY-ACTOR`, or B's effect. Cross-household request → **`404` `not_found`**; **`B` COUNT and content unchanged**. |
| **T17** | Household timezone **America/Denver**; setup clock anchor; inject wall clock at **5:00 PM MST on local day 30** (= **00:00 UTC** next calendar day). Line eligible for replace→rate flip. Record `COUNT` before. | Advance clock; run day-30 eligibility job / coalescer. | New `list_line_changes` with **`cause=usage_learned`**; fired on **Denver day 30**, not UTC-only day 30. UTC-only clock test is not this lock. |
| **T18** | Server restart fixture with dirty household lost mid-burst; no scan in flight. | Process startup catch-up coalescer. | `list_line_changes` with **`cause=list_updated`**, **`actorUserId` null**. |
| **T19** | Household **`H`**, `households.trip_seq = T`. Seed **two** `list_line_changes` at `trip_seq=T`: `item:item-571-a` (`effect=added`, `cause=taken_out`, …) and `item:item-571-b` (`effect=taken_off`, `reason=on_hand_meets_par`, `cause=put_away`, …); record each field (`from_qty`, `to_qty`, `n`, `actor_user_id`, `at`). Seed skip expiring at advance so boundary **`new_trip`** upsert for **`item:item-571-c`** would succeed. Record `COUNT(*)` of `list_line_changes`, affected lines, skips, adjustments, `attention_marks` **before** action. **Unfaulted path** must match **T7**. | **Fault path:** start trip advance; inject fault **after** `DELETE … WHERE trip_seq < new_seq` and **before** any `INSERT`/`UPSERT` of new-trip `list_line_changes` (and other boundary writes); abort writer txn. Concurrently: read-pool observer runs SQL + `GET /api/shopping-list` + `GET /api/shopping-list/summary` while writer paused. | **Rollback:** `trip_seq` still **T**; `COUNT(*)` equals pre-advance; each seeded row **byte-identical**; no row with `trip_seq=T+1`; lines/skips/adjustments/`attention_marks` match pre snapshot. **Read observer:** never sees `trip_seq=T+1` with `COUNT=0`, missing `lastChange`, or mixed `trip_seq`; after rollback, GET shows **same** prior `lastChange` values. |
| **T20** | `H`; staged line `item-571-stale` at **`quantity=6`**; coalescer job held on writer **>250 ms** (injected gate). Record fake provider HTTP count **0**. | `GET /api/shopping-list`; `GET /api/shopping-list/summary` (same gate). | Both **`200`**; **`listStale: true`**; response qty still **6** (last-good staged); fresh `planBasis` on list rows; **never `5xx`**. Summary counts consistent with same drain (no disagreement with list). |
| **T21** | `H` with cursor C1; mutate one line to bump version. | `GET ?since=C1&status=1` twice: once changed, once unchanged. | `changed:true` → JSON has exactly **`cursor`, `listStale`, `items`, `offList`, `stores`, `attentionGroups`, `storeDisabled`, `lastTripEndedAt`, `lastTripEndedBy`** (no extras required). `changed:false` → **only** `{cursor, changed:false}` + `Cache-Control: no-store`. |
| **T22** | Off-list taken_off row (see T12). | `GET ?status=1`. | `offList[].id` equals `line_key`; `lastChange` keyed by same id. |
| **T23** | **T14 provider fixture** (would call Kroger); pending coalescer **or** writer busy **>2 s** before export; record provider fake count **0** and send-result row `COUNT`. | `POST /api/shopping-list/export`; on **same fixture** after job completes, export again. | Busy path: **`409`**, body **`code":"list_stale"`** (sentence proposed / CJ); provider fake **0**; send-result **`COUNT` unchanged**. Success path after drain: provider fake **≥1** (proves fake would have fired). |
| **T24** | `GET ?status=1` with flagged and unflagged lines. | Single GET. | Every `items[]` and `offList[]` object has **`needsAttention` key** (`null` when clean). |
| **T25** | Valid automatic write fixture; record `COUNT`. | App write with invalid `effect` or `cause` string (not in enum). | **Rejected** by validation; **`COUNT` and content unchanged**; value not stored. |
| **T26** | Flagged planner row with **no** `shopping_list_items` staged line. | `GET ?status=1`. | Row in **`offList`**; every `attentionGroups[].rowKeys[]` resolves to `items[].lineKey` or `offList[].id`; `count === rowKeys.length`. |
| **T27** | Group with `k` rows `needsAttention.stuck=true`. | `GET ?status=1`. | `attentionGroups[].stuck === k` (non-negative integer). |
| **T28** | Golden master snapshot household (same skus as master basis test); W3 server. | `GET /api/shopping-list` (no `status`). | Legacy **`basis`** JSON **byte-compatible** with master for each populated row; **`planBasis` additive** only. Every row includes keys **`sent`** (value `0` when none), **`canSendToCart`** (boolean), **`storeId`** (present, `null` when no store). |
| **T29** | `H`; line `item-571-pb` with staged **`quantity` unchanged** across coalescer run but read-time plan changes **`planBasis.short` only**; record list version `V`. | Complete one coalescer run. | List version **`V+1` once**; one `list` hint; quantities unchanged in DB. |
| **T30** | Authenticated household `H` with rows in `list_line_changes`; provider fake **0**; record `COUNT`. | **401:** unauthenticated `GET /api/shopping-list`, `/summary`, `POST /export`. **404:** member session, `X-Household-Id` = foreign / unknown id. **400:** malformed `since` on GET. | **`401`** `{"error":"Sign in to continue.","code":"unauthorized"}`; **`404` `not_found`** (not empty list); **`400` `validation`**. Each: **`COUNT` unchanged**, provider fake **0**. |
| **T31** | Scan queue pending item; `Plan` counter **0**; coalescer enqueue counter **0**. | Single or batch scan **commit** HTTP. | **`Plan` counter 0** during commit txn; coalescer **enqueued once** (not run inline). |
| **T32** | `H` dirty; coalescer enqueue counter **0**; optional one **pre-queued** job for export drain test. | `GET /api/shopping-list`, hint handler tick, other read-only routes, `POST /export` (may drain existing job only). | Enqueue counter **0** after each (export does not schedule **new** job). |
| **T33** | Line with existing `list_line_changes` PK row; record `at=T0`, `COUNT=1`. | Run scoped save twice with **identical** planned qty/effect. | **`COUNT` still 1**; same PK; **`at` unchanged** on second run. Third run that changes qty/effect: **`at` advances**. |
| **T34** | Put-away to par on **purchased**, **touched**, and **group** auto lines (fixtures per subtest). | Stock-in / scoped put_away when buy→0. | **Purchased / touched / group-held:** staged row **unchanged**; **no** new `list_line_changes`; row stays in **`items[]`**, not **`offList`**. **Purchased:** **Start a new cart** removes row in same txn as trip advance. **Unpurchased untouched auto:** covered by T5 put_away subtest (delete + `offList`). |

Optional load test: 300-product household, list GET p95 ≤ 300 ms (document in PR, not necessarily CI).

### Implementation acceptance (Architect)

After CJ approves the `list_stale` sentence, the **W3 export implementation PR** (not this docs PR) must land in **one** change set:

1. Move **`list_stale`** from the proposed section into the locked **`### Error code registry`** in `api-contract.md`.
2. Add **`list_stale`** to the `HostedErrorCode` union in `shared/contract.ts`.
3. Add the approved sentence to **`HOSTED_ERROR_SENTENCES`** (and any server pin / `existingContractSentences` update the lock tests require).
4. Green **`TestHostedAPIContract`** and **`frontend/src/api/hostedContract.test.ts`** on that PR.

Do **not** add `list_stale` to the locked registry in a docs-only cut before CJ approval.

---

## 13. Related contracts

- W1 trip / skips: [api-contract.md](./api-contract.md) undo-remove section.
- W2 send results: `GET /api/shopping-list/send-results` (implementation on master; normative JSON in `shared/contract.ts`).
- W4 scoped save/send: issue #571 W4 export `itemIds` contract comments.
- Copy: `shared/listCopy.ts` + CJ `571-copy-for-cj-rev2.md`.
