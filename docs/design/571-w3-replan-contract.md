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
| **Background coalescer** | After stock or setting commits, mark the household dirty; one debounced job runs `Plan` and **scoped save** (no `deleteUnplannedAuto`). Each run upserts affected lines and `list_line_changes`, then bumps the `list` version once (even when quantities are unchanged but `basis` moved). |
| **Read path** | `GET /api/shopping-list` (and `/summary`, item status) **drain at most one pending coalescer job** for the household, then run a **read-only `Plan` on the read pool** to fill `basis` / `sent` / attention inputs. **No provider I/O** on read. |
| **Explanation** | Latest automatic change per line lives in `list_line_changes` (schema from W1). The API exposes it as `lastChange` on entries and off-list rows. Copy uses effect + cause + optional `reason` (taken off only) via `shared/listCopy.ts`. |
| **Compatibility** | Without `?status=1`, the list body stays today's **bare array** when `since` is absent; new entry fields are **additive** with `omitempty`. Cursor refresh (`?since=`) is unchanged. |

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

**Not triggers:** `GET /api/shopping-list`, live hint handling, export/send, read-only routes.

**Rules:**

- Do **not** call `Plan` inside the scan-commit transaction (writer pool is single-connection).
- Debounce: reset a per-household timer on each trigger in a burst; **one** `Plan` per burst (Architect W3).
- Save path: **scoped save only** — upsert changed auto lines; delete untouched unpurchased auto rows **only for affected keys** when buy goes to 0; never `deleteUnplannedAuto`; **touched** and **manual** rows unchanged.
- Each completed run: bump `list` version **once** and publish one `list` hint (even if only `basis` would change on read).

**Trip advance (`trip_seq` +1)** — same transaction when possible (Start a new cart or mass stock-in close):

1. Advance `trip_seq` (and write `trip_closed_at` / `trip_closed_cause` per W1).
2. **`DELETE` prior-trip `list_line_changes` first:** `DELETE … WHERE trip_seq < new_seq` **before** any new rows for the new trip are written.
3. Run boundary work that may upsert lines and `list_line_changes` (skip expiry restoring lines, trip-scoped adjustment expiry, attention marks, and any coalesced replan in that transaction).
4. New boundary rows (e.g. `cause: new_trip`) **must** use the **new** `trip_seq` so they belong to the new trip and are **not** removed by step 2 in the same transaction.

### 2.2 Read path (no new writes except draining one pending job)

Before building the response, the handler:

1. **Drain** — If a coalescer job is queued or running for this household, **finish that one job** synchronously (not a second plan, not full fill).
2. **Plan (read-only)** — Run `Plan` on the **read pool** (`supply.Open(readDB)`), not the writer used today by GET.
3. **Assemble** — Merge staged rows, read-time plan output, ledger, adjustments, skips, `list_line_changes`, `cart_line_results`, store context; compute `basis`, `sent`, `quantity` (`toGet`), `lastChange`, and (when `status=1`) `needsAttention` / `offList`.

Re-reading the list **does not** write `list_line_changes` again unless step 1 actually applied a new material change.

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

**Response encoding** (unchanged [change_view.go](https://github.com/Rhionin/stockbot/blob/master/internal/server/change_view.go)):

| `since` | Body |
|---|---|
| absent | `[]` or enriched object (§4.2 when `status=1`) as today's clients expect for a first load |
| present, `changed: false` | `{ "cursor", "changed": false }` + `Cache-Control: no-store` |
| present, `changed: true` | `{ "cursor", "changed": true, "items": [...] }` |

**Errors:** `401 unauthorized`, `404 not_found` (non-member / unknown household), `400 validation` (malformed `since`). No `502` from providers on this route.

### 3.2 `GET /api/shopping-list/summary` (W7 hook; W3 read path)

Same §2.2 processing, **counts only** (no per-row adjustment N+1). Returns `{ shortNow, needsAttention, notOnList, cursor, attentionGroups?, lastTripEndedAt?, lastTripEndedBy?, storeDisabled? }` per Architect rev 2 / 2.4. Full field list is owned with W7; W3 requires the **same drain + read-only plan** so counts cannot disagree with the list.

### 3.3 Routes that must **not** gain an implicit full fill

Inventory GET, export (except explicit send), and other reads keep today's behavior unless listed above. #570 "compare plan to row" diagnostics stay **read-only** and must not call scoped save (Architect W3).

### 3.4 Unchanged mutation routes (context)

| Route | W3 interaction |
|---|---|
| `POST /api/shopping-list/fill` | Full fill + `deleteUnplannedAuto` (manual "Update the list"); writes `list_updated` changes |
| `POST /api/shopping-list/items/{itemId}/undo-remove` | Scoped replan (W1); may write `restocked_here` or `list_updated` |
| `POST /api/shopping-list/restock` | W4 scoped replan; `restocked_here` |
| Scan commit routes | Enqueue coalescer; do not block on plan in commit txn |

---

## 4. JSON shapes

### 4.1 `ShoppingListEntry` (list `items[]`)

Extends today's [ShoppingListEntryResponse](https://github.com/Rhionin/stockbot/blob/master/internal/server/handler_shopping_list.go) / [shared/types.ts](https://github.com/Rhionin/stockbot/blob/master/shared/types.ts). **Additive** fields:

| Field | Type | Omitempty | Nullability | Meaning |
|---|---|---|---|---|
| `basis` | `ShoppingListBasis` | yes | absent when not computed | Planner numbers for copy §E.1 (read-time plan) |
| `sent` | `number` | yes | n/a | Ledger `requested` since last stock-in for this line's cart store |
| `touched` | `boolean` | yes | n/a | Person edited quantity; coalescer must not overwrite quantity |
| `storeId` | `string` | yes | `null` in JSON when no store | Line's effective store (rev 2.4 G8) |
| `canSendToCart` | `boolean` | yes | n/a | **Authoritative on the entry**; cart send UX for that line |
| `lastChange` | `ListLineChange` | yes | `null` when no row | Latest `list_line_changes` for this line key |
| `needsAttention` | `NeedsAttention` | when `status=1` only | `null` when not flagged | Server-side D5 rule (W6/W7 consumer; shape §4.4) |

**Unchanged:** `id`, `itemId`, `quantity` (= **`toGet`** in UX copy), `source`, `note`, `purchasedAt`, `replenishmentMode`, `provider` (only when line is cart-eligible), `computedQuantity`, `adjustment`, `group`.

**`ShoppingListBasis`** (replaces unused legacy basis; no DB column):

```ts
interface ShoppingListBasis {
  onHand: number;
  onHandAmount?: number;
  unit?: 'g' | 'ml';
  target: ShoppingListTarget;
  ratePerMonth?: number;
  replacing?: number;
  short: number;
}

type ShoppingListTarget =
  | { kind: 'amount'; value: number }
  | { kind: 'months'; value: number; par: number }
  | { kind: 'group_amount'; goal: number; unit: 'g' | 'ml' }
  | { kind: 'none' };
```

### 4.2 `GET /api/shopping-list?status=1` envelope

When `status=1`, the first-load body (no `since`) is an object so the web list can render Needs attention, Short now, and Not on the list without client-side rules:

```ts
interface ShoppingListStatusResponse {
  cursor: string;
  items: ShoppingListEntry[];
  offList: OffListRow[];
  stores: ShoppingListStoreInfo[];
  attentionGroups: AttentionGroup[];
  storeDisabled: { providerId: string } | null;
  lastTripEndedAt: string | null;
  lastTripEndedBy: 'new_cart' | 'put_away' | null;
}
```

- **`offList`:** Rows not in `items[]` but needed for §5 "Not on the list" (`removed`, `taken_off`, `opening`, trip-zero adjustment, etc.). Same `lastChange` / `needsAttention` / `basis` rules where applicable.
- **`stores`:** `{ id, name, kind: 'physical' | 'online', canSendToCart }[]`.
- **`attentionGroups`:** `{ reason, detail?, count, stuck, rowKeys: string[] }[]` in server priority order (rev 2.4 G1).
- With `since` + `status=1`, changed payloads use the same keys inside the `changed: true` object (`items`, `offList`, …) so patches stay consistent.

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

**Client marker:** Compare `at` to device-local last-viewed time (UX §3). Server holds no per-device state.

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

When `status=1`, each flagged row includes:

```ts
interface NeedsAttention {
  reason: NeedsAttentionReason;
  detail?: string;
  others: NeedsAttentionReason[];
  fix: NeedsAttentionFix;
  stuck: boolean;
}
```

Enums and precedence are defined in Architect [6745010424](https://github.com/Rhionin/stockbot/issues/571#issuecomment-6745010424) §3–§4. W3 only requires that **the same read pass** that fills `basis` feeds `attention.Evaluate` with **no provider calls**.

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
- Expose only rows with `trip_seq === households.trip_seq` for off-list `taken_off` cases; stale trips are deleted at advance ([trip.go](https://github.com/Rhionin/stockbot/blob/master/internal/trip/trip.go)).

### 5.3 Retention

- **Latest change only, current trip only.** No time-based history in v1; older trips do not leave rows (PM ruling, §11 Resolved).
- On `trip_seq` advance, in **one transaction** with the advance and boundary writes when possible:
  1. After `trip_seq` is incremented, run `DELETE FROM list_line_changes WHERE household_id = ? AND trip_seq < new_seq` (**before** inserting new-trip rows).
  2. Then upsert any boundary-driven rows (including `cause: new_trip`, e.g. W1 skip expiry restoring a line) with `trip_seq =` the new value.
- Rows written at step 2 must survive the delete in step 1; order inside the transaction is a **hard requirement** (see also §2.1 trip advance).

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
| Drain on read | At most **one** pending job awaited per request |
| Target | List GET **p95 ≤ 300 ms** on Pi 3B+ with synthetic **300-product** household (Architect Q8) |

Measure before merge; publish numbers in the build PR.

---

## 8. Backward compatibility

| Client | Behavior |
|---|---|
| Web/mobile without `status=1` | Still receive array or `{ cursor, changed, items }`; ignore unknown fields |
| `basis` / `lastChange` absent | Treat as "numbers unavailable" (UX error state), not zero |
| Cursor refresh | Unchanged; patch by entry `id` |
| `provider` query | Still selects ledger provider for cart lines; **must not** stamp provider on in-person lines after #576 fix |
| Old servers | New fields omitted; clients must not require `lastChange` |

---

## 9. API mapping for UX five states

Applies to list GET + `status=1` (and summary for native row).

| UX state | API behavior |
|---|---|
| **Ideal** | `200`; `items` + optional `offList`; `basis.short` matches planner; `lastChange` present when coalescer wrote; `needsAttention` populated when flagged |
| **Empty** | `200`; `items: []`, `shortNow: 0` on summary; no error |
| **Loading** | Client-side first fetch; server responds synchronously (no `202`). Subsequent hints: `since` with `changed: false` or patch object |
| **Edge** | Stale cursor → full patch; `touched: true` rows preserve quantity; off-list rows include `taken_off` + `lastChange`; offline clients use last `200` body (no special API mode) |
| **Error** | `401` / `404` / `400` as today; **no** silent empty list on auth failure; planner/internal errors → `500` with generic message (UX: "Couldn't load stock numbers" / "Couldn't refresh") |

---

## 10. Migration

| Question | Answer |
|---|---|
| New migration for W3 core? | **No**, when W1 `031` is merged as on master: `list_line_changes`, `attention_marks`, trip columns, and adjustment actor columns already exist. |
| CJ approval | W1 SQL approval covers `list_line_changes` creation. W3 build **must not** add a second migration for this table. |

**Optional proposal only** (needs CJ approval if build wants DB-enforced enums):

```sql
-- PROPOSAL ONLY — not part of W3 unless Architect and CJ explicitly approve.
-- SQLite cannot ADD CONSTRAINT to existing table without rebuild; would be 032+.
-- Prefer application-level validation and contract tests unless CJ requests CHECK constraints.
```

No recommended `032` for W3 at this time.

---

## 11. Open questions

### Architect

1. **Busy / deadlock:** If drain-on-read waits on a coalescer that needs the writer while read holds a lock, what is the timeout and HTTP code?
2. **`status=1` without `since`:** Should cursor refresh return the full envelope (`offList`, `attentionGroups`) on every `changed: true`, or only `items` deltas?
3. **Entry `id` for deleted auto lines:** Off-list `taken_off` rows have no `shopping_list_items` row — is `id` always `""` with stable `itemId` / `groupId`?
4. **CHECK constraints** on `effect` / `cause` / `reason`: application-only vs future migration?

### UX

1. Mark spec **ready to build** only after confirming `status=1` envelope matches section ordering (Needs attention → Short now → Not on the list).
2. **`Changed {when}` marker:** Confirm device local time vs server `at` for first paint after sleep.

### Resolved (PM)

1. **Web first paint uses `GET /api/shopping-list?status=1`:** one page in UX order — Needs attention, then Short now grouped by store, then collapsed Not on the list. The phone stays on **`GET /api/shopping-list/summary`** for the interim Household row. A native shopping list is a **follow-up after W7**, not part of W3.
2. **No history beyond the latest change per line.** Rows from older trips are cleared when `trip_seq` advances; v1 explicitly dropped change history (item status and the list marker use this one row only).

---

## 12. Test plan (build PR)

Synthetic fixtures only; no live Kroger. Prefer handler tests with `exchanges()` per AGENTS.md.

| # | Test | Lock |
|---|---|---|
| T1 | Stock-out burst of N items → one coalesced plan, one version bump, one hint | Coalescer debounce |
| T2 | Put-away 30 items → one plan, not 30 | Debounce |
| T3 | Scoped save never calls `deleteUnplannedAuto`; unrelated auto line untouched | W3/W4 boundary |
| T4 | Touched line quantity unchanged after plan; `touched: true` on GET | UX §6 rule 5 |
| T5 | `list_line_changes` upsert: `added`, `amount_up`, `taken_off` + `on_hand_meets_par` | Effect + reason |
| T6 | Mixed-direction burst → `n` = larger total, `actorUserId` null | Q2 burst rules |
| T7 | Trip advance deletes prior `trip_seq` rows | Retention |
| T19 | On trip advance: old-trip rows deleted; a boundary `new_trip` row (e.g. skip-expiry restore) written after delete with new `tripSeq` and visible on GET `lastChange` | §2.1 / §5.3 ordering |
| T8 | GET drains pending job once; second GET does not rewrite `list_line_changes` | Re-read idempotency |
| T9 | Read-only plan on read pool: no writer `SetMaxOpenConns(1)` contention in parallel GET test | Perf guard |
| T10 | `basis` populated for amount / months / group / none targets | Q1 |
| T11 | `lastChange` on GET; absent after manual edit only | Marker rule |
| T12 | `GET ?status=1` includes `offList` taken_off row with reason sentence inputs | Q3 |
| T13 | `since` cursor: `changed: false` does not clear client rows; `changed: true` patches by `id` | Q5 |
| T14 | List GET makes **zero** provider HTTP (count mock) | No provider I/O |
| T15 | Batch load: assert bounded query count vs item count (regression guard for N+1) | Perf |
| T16 | Household A cannot read B's `list_line_changes` via GET | Scope |
| T17 | `usage_learned` fired at day 30 in injected clock test | Q8 timer |
| T18 | Startup catch-up after restart writes `list_updated`, null actor | Q2 restart |

Optional load test: 300-product household, list GET p95 ≤ 300 ms (document in PR, not necessarily CI).

---

## 13. Related contracts

- W1 trip / skips: [api-contract.md](./api-contract.md) undo-remove section.
- W2 send results: `GET /api/shopping-list/send-results` (implementation on master; normative JSON in `shared/contract.ts`).
- W4 scoped save/send: issue #571 W4 export `itemIds` contract comments.
- Copy: `shared/listCopy.ts` + CJ `571-copy-for-cj-rev2.md`.
