# #686 / #687: Needs attention reasons, one exemption gate, `items[].name`, marks writer

Status: contract, docs-only, **rev 3** (un-reserves `not_connected` / `reconnect_required` per the #689 part 1 ruling; see §14). Rev 2 closed SDET's PR #688 test NO-GO at `395b8f9c` (§13). Owner: Backend (all tickets). Frontend owns rendering only (§9).
Issues: [#686](https://github.com/Rhionin/stockbot/issues/686) (ticket A), [#687](https://github.com/Rhionin/stockbot/issues/687) (ticket B: ships with A or after it, never before), [#689](https://github.com/Rhionin/stockbot/issues/689) (ticket C: connection reasons, §6.4; part 2 owner gate). Parent: #571 (W3).

Pinned sources:
- master `443a9b0673451514a5cd1cabe100dd957929f66c` (cited as `M:`; #683 merge, latest `origin/master` when rev 2 was written).
- master `fcd643f9e3c75f8515296b021f8d62bfa0b3cd18` (cited as `N:`; #688 merge). Rev 3 additions (§3.1a C column, §6.4, the §6.1 correction, the §8 note, ticket C tests) cite `N:`.
- #689 part 1 ruling (Architect issue comment on #689), cited as `R689`.
- Design: Architect #571 [6745010424](https://github.com/Rhionin/stockbot/issues/571#issuecomment-6745010424) (needsAttention rev2; supersedes rev1 6744916648), cited as `rev2 §n`.
- `docs/design/571-w3-replan-contract.md` (cited as `W3`), `docs/specs/679-next-send-zero-expiry.md` (cited as `#679`), `docs/design/api-contract.md`.
- UX copy rev2 `571-copy-for-cj-rev2.md` §B.1 (sha `03d438d9`, lines 90-111): **approved (Nov 9, per #571 issuecomment-6745454775)**. N1/N2 are new strings outside that file and are still pending with CJ (§11).
- PR #684 (open, head `948e4ac0`) touches the same exemption code; see §10.

Labels: **FACT** = read in code at `M:`. **RULING** = Architect decision within technical authority. **OPINION** = recommendation Backend may push back on with reasons.

## 1. Summary

A line is in Needs attention only for one of 8 fact-based reasons (rev2 §3). Ticket A can emit 5 of them. Ticket C (#689) adds `not_connected` and `reconnect_required` from one connection fact (§6.4). `probable_miss` stays in the wire union but is **reserved** (§3.1a) and must not be emitted. A mark (`attention_marks`) never flags a line; it only sets `stuck`. One exemption gate applies to every flag source. The planner reports one drop reason (`no_product_picked`). Send results map to the line the person sees. `items[]` under `status=1` gains a server `name`.

**Wire changes:** one additive field (`items[].name`, `status=1` only), plus three conformance fixes to the existing contract (§3.3). Ticket C changes no type: two union values become emittable, one `fix` row is added, and the existing `storeDisabled` field gets its writer (§6.4). **No migration** (§8, §6.4).

## 2. Defects on master this fixes (FACT)

| # | Where | Today |
|---|---|---|
| F1 | M:`internal/attention/evaluate.go:90-98` | Every mark flags its line as `attention` (`:92`) or a `not_planned` with no `detail` (`:95`, `:98`). Neither is in rev2 §3. |
| F2 | M:`evaluate.go:61-67` | The only exemption is `ZeroForTrip`. `Skipped` and `Opening` (`:50-51`) are never read. |
| F3 | M:`internal/server/list_assemble_zero.go:15-28` | The active-0 key set also requires `CanSendToCart`, narrower than #679 §5. |
| F4 | M:`evaluate.go:100-102`, `:146-148` | Send results keyed `item:<id>` even when the item is on a `group:<id>` line; only `status='failed'` loaded; provider ignored (map keyed by item, last provider wins); `reason` scanned twice; retention cutoff (M:`internal/cart/public_provision.go:63`, 90 days) ignored. |
| F5 | M:`list_assemble.go:112-115` | `LoadMarks`, `LoadSendFailures`, `batchActiveSkips`, `householdTripMeta` errors are discarded (`_`), so a DB error silently removes flags. |
| F6 | M:`list_assemble.go:361-366` | A flagged off-list row with no name is dropped and its flag deleted (`droppedUnnamed`). |
| F7 | M:`evaluate.go:24` / M:`handler_shopping_list.go:96` | `others` has no initializer and emits `null`. |
| F8 | M:`list_assemble.go:256-263` | `fix` is `{who:[{role:"member"}]}`; rev2 §2 shape is `{code, allowed, who, target}`. |
| F9 | M:`handler_shopping_list.go:54` | `items[].needsAttention` has `omitempty`; W3 `:150` and rev2 §2 say never omitted under `status=1`. |
| F10 | M:`internal/supply/service.go:181-187,245-266` | The planner has no `Excluded` output; a group with an empty pick is dropped silently (M:`group_plan.go:24-26`). |
| F11 | M:`internal/cart/last_send.go:29-32` | `storedLineReason` rewrites `provider_unavailable` to `api_error` for every per-line row, so a 502 can't be told apart from a store error. |
| F12 | — | Nothing writes `attention_marks` (only read M:`evaluate.go:123-125`, wipe M:`internal/supply/sql.go:266-267`, tenant list M:`internal/tenant/sqlcheck.go:67-68`). Required by W3 `:62`. |

## 3. API contract

### 3.1 Reason set (RULING): closed, 8 values

```ts
// shared/types.ts
export type NeedsAttentionReason =
  | 'not_connected' | 'reconnect_required' | 'no_barcode' | 'no_store_match'
  | 'send_unsure' | 'send_failed' | 'probable_miss' | 'not_planned';
// shared/contract.ts (hosted_contract_test.go already parses this file, M:hosted_contract_test.go:90)
export const NEEDS_ATTENTION_REASONS = [
  'not_connected', 'reconnect_required', 'no_barcode', 'no_store_match',
  'send_unsure', 'send_failed', 'probable_miss', 'not_planned',
] as const satisfies readonly NeedsAttentionReason[];
```

- Array order **is** precedence (rev2 §3). `others` is ordered the same way.
- Go: `internal/attention/reasons.go` declares `type Reason string`, the 8 constants, and `var Reasons = []Reason{…}` in the same order. `NeedsAttention.Reason`, `NeedsAttention.Others` and `AttentionGroup.Reason` use `Reason`.
- `attention` is **never** a reason, now or later. `store_disabled` stays a banner (rev2 §3).
- `not_planned` is emitted **only** with a non-empty `detail` from §5. No detail → no flag (UX Q5).
- `rev2 §8` names `shared/listCopy.ts`. **FACT:** that file doesn't exist at `M:`. The const lives in `shared/contract.ts` instead; the copy mapping file is Frontend's when W3 rendering lands.
- Clients (old and new) keep the Unknown fallback: an unrecognized `reason` still renders the row with the §B.1 "Unknown reason" copy and is never dropped (rev2 §8). Unknown `detail` → that reason's own sentence. Unknown `fix.code` → no button.
- Narrowing `reason: string` to the union is **not breaking**: FACT, no web/mobile code consumes `NeedsAttention` or calls `?status=1` (`frontend/`, `mobile/`, `ui/` search).

### 3.1a Reserved reasons (RULING; closes SDET item 1)

The union in §3.1 keeps all 8 values, in this order, so clients and the parity test don't change when a reserved reason ships. In ticket A, exactly 5 are **emittable**; ticket C (#689) makes it 7:

| Reason | Ticket A | Ticket C (#689) | Source of the fact |
|---|---|---|---|
| `not_connected` | RESERVED | emittable | §6.4 |
| `reconnect_required` | RESERVED | emittable | §6.4 |
| `no_barcode` | emittable | emittable | §6.3 (stored `skipped`/`no_upc`) |
| `no_store_match` | emittable | emittable | §6.3 (stored `skipped`/`no_match`) |
| `send_unsure` | emittable | emittable | §6.3 |
| `send_failed` | emittable | emittable | §6.3 |
| `not_planned` | emittable | emittable | §5 (`detail: no_product_picked` only) |
| `probable_miss` | **RESERVED** | **RESERVED** | none (W5) |

**Rule:** a PR must not put a value that is reserved at its stage in `needsAttention.reason`, `needsAttention.others` (items or `offList`) or `attentionGroups[].reason`. Tests A9 and H13 (§10) fail if one appears. Production Go code may name a reserved constant only in `Reasons` (the order and parity list). Ticket A ships with the ticket A column; ticket C ships with the ticket C column. Lifting a reservation needs a spec amendment with a fact row, a `fix` row, a §B.1 copy check, and tests that replace the H13 case for that reason. Rev 3 does that for the two connection reasons (§6.4, §3.2, §10 ticket C).

**Why ticket A kept them reserved (FACT, at `M:`; resolved for the two connection reasons by §6.4):** the raw data exists, but it didn't give one unambiguous fact.
- `not_connected` / `reconnect_required`: rev2 §3 defines `not_connected` as "linked store, no connection row" and `reconnect_required` as connection state `reauth_required` or `disconnected`. On master those two overlap:
  - `providerConnectionState` reports `disconnected` when there's no row (M:`internal/server/handler_providers.go:172,179`).
  - New rows default to `state 'disconnected'` (M:`internal/app/migrations/006_replace_cart_integrations_with_provider_ledger.sql:15`).
  - `store.checkReason` calls every non-`connected` household `not_connected` (M:`internal/store/checking.go:105-116`).
  - `reauth_required` is written by `MarkReauthRequired` (M:`internal/cart/connection/broker.go:162-176`) and by M:`handler_kroger_app.go:373`.
  - Separately, their `fix` (`connect_store`) is admin-only per rev2 §4a, but the authorize and callback handlers have no role check on master (routes M:`internal/server/server.go:756-757`, handlers M:`handler_providers.go:199,272`; the only owner gate is M:`handler_kroger_app.go:107-118`). So `fix.allowed` can't come from "the route's real permission check" yet.
  - Follow-up ticket: #689. Its ruling (R689) is §6.4. `TestProviderConnectRequiresOwner` is #689 part 2.
- `probable_miss`: `stock_in_clusters` (M:`031_trip_scoped_skips.sql:18`) and `sent` rows exist, but the rule and its fix (`answer_miss`) belong to W5, whose route hasn't shipped, and the in-person path is still gated (#571 issuecomment-6745454775).
- A stored `failed`/`reconnect_required` send row is **not** the wire reason `reconnect_required`, in any ticket. It maps to `send_failed` with `detail: reconnect_required` (§6.3). Only the §6.4 connection fact emits `reconnect_required`.
- **Name collision (RULING, R689 Q3):** `store.checkReason`'s `not_connected` (`GET /api/stores`, `CheckReason`, N:`docs/design/api-contract.md:1851`) is a different enum on a different surface. It means "no live connection for product checking", **including** `reauth_required` (N:`internal/store/checking.go:105-116`). It is not aligned with §6.4 and must not be "fixed" to match it, or vice versa.

Frontend still renders all 8 values (§9). A reserved value has §B.1 copy, so it never needs the Unknown fallback.

### 3.2 Shapes (`status=1` only; default GET unchanged)

```ts
interface NeedsAttention {
  reason: NeedsAttentionReason;
  detail?: string;                 // open string; copy key only, never displayed (W3 §4.8)
  others: NeedsAttentionReason[];  // never null; [] when none
  fix: NeedsAttentionFix;
  stuck: boolean;                  // §7
}
interface NeedsAttentionFix {
  code: FixCode;                   // rev2 §4 closed set
  allowed: boolean;
  who: { role: 'owner' } | null;   // non-null only when allowed is false
  target: { entryId: string | null; itemId: string | null; groupId: string | null;
            productId: string | null; providerId: string | null };
}
type FixCode = 'connect_store' | 'edit_product' | 'resolve_unknown' | 'send_item' | 'send_group'
  | 'answer_miss' | 'edit_target' | 'restock_item' | 'restock_group' | 'none';
interface AttentionGroup { reason: NeedsAttentionReason; detail?: string; count: number; stuck: number; rowKeys: string[] }
interface ShoppingListEntryStatus extends ShoppingListEntryW3 {
  lineKey: string;
  name: string;                    // NEW, additive (§6)
  needsAttention: NeedsAttention | null;  // key always present
}
```

**`fix` per reason (RULING):** only emittable reasons need a code. `probable_miss` has no `fix` row until it's un-reserved. The `connect_store` row ships with ticket C.

| reason / detail | `fix.code` | `allowed` | `target` |
|---|---|---|---|
| `not_connected`, `reconnect_required` (ticket C) | `connect_store` (`GET /api/providers/{providerId}/authorize`, connect and reconnect, rev2 §4) | the #689 part 2 shared owner predicate for this viewer, the same function the route calls (never re-derived). `who: {role:"owner"}` when `false`, else `null`. A non-`account` principal → `false` | `providerId` = target provider; `itemId`/`groupId` from the line key; `entryId`, `productId` `null` |
| `send_unsure` | `resolve_unknown` (route exists, rev2 §4) | result of the route's own scope check (household scope → `true` for any list viewer) | `entryId` from the result row, `itemId`, `providerId` |
| `send_failed` | `none` until W4 ships `send_item`/`send_group` | `false`, `who: null` | `itemId`/`groupId`, `providerId` |
| `not_planned` / `no_product_picked` | `none` (§B.1 group fix is "None"; per-item **Open group** is client navigation from `target.groupId`) | `false`, `who: null` | `groupId` |

- `who` is non-null only when the route exists and the viewer's role fails it (rev2 §4a). In ticket A `who` is always `null`; from ticket C it is `{role:"owner"}` on `connect_store` rows for non-owners. The client picks **Connect** or **Reconnect** from `reason`, never from `fix`. **OPINION:** `allowed:false, code:none` reads "no server action"; clients must key on `code`, not `allowed`.
- Target ids not applicable are `null`, never omitted.

### 3.3 Conformance fixes (no new semantics)

| Fix | Rule |
|---|---|
| `others` | Always an array. Go initializes `Others: []Reason{}`. |
| `fix` | Full §3.2 object on every flagged row (items and offList). |
| `items[].needsAttention` | Key always present under `status=1`. **Edge (FACT):** `ShoppingListEntryResponse` is shared with default GET, which T28 locks byte-for-byte (W3 §2.2). So **don't** just drop `omitempty`: add `ShoppingListStatusEntryResponse` (embeds the entry, adds `Name string json:"name"` and `NeedsAttention *NeedsAttentionResponse json:"needsAttention"` with no omitempty) used only by `ShoppingListStatusResponse.Items`. Default GET output must not change. |

Docs: `api-contract.md` blocks at M:`:1380-1419` updated to §3.2 in the **spec PR** (this one), plus `shared/types.ts` in ticket A.

## 4. Evaluation rule (D1) — Backend, `internal/attention`

**RULING.** Per line key, in this order:

1. **Exempt?** (§4.1) → not flagged. Stop. No fact, mark or send result can override it.
2. **Candidate?** `short(key)` (§4.2). If not → not flagged.
3. **Facts.** Collect every reason whose fact holds for `key`. In ticket A the only fact sources are §5 (`not_planned`) and §6.3 (send results). Ticket C adds §6.4 (connection). `probable_miss` (§3.1a) has no fact source. Future reasons add rows. None → not flagged.
4. `reason` = first fact in precedence; `others` = the rest; `detail` = that fact's detail.
5. `stuck` = `marks[key]` exists **and** `marks[key].FlaggedTrip < TripSeq`. A mark never adds, keeps or changes a reason.

`Evaluate` is pure (no I/O) and takes all inputs in `EvaluateInput`. Delete the marks loop at M:`evaluate.go:90-98`.

### 4.1 One exemption gate (all sources)

`exempt(key)` is true when **any** holds:
- `key ∈ Skipped` — active one-trip removal (M:`list_assemble.go:311-333` predicate `h.trip_seq < s.expires_trip`). Edges: `s.expires_trip = trip_seq` → **not** skipped, so not exempt by this clause. `s.expires_trip = trip_seq+1` → skipped. `staged_cart_skips.expires_trip` is `INTEGER NOT NULL DEFAULT 1` (M:`031_trip_scoped_skips.sql:9`), so skips have no NULL case. Tests A3a/H14.
- `Opening` — setup period; every line is exempt (rev2 §1, Q11).
- `key ∈ ActiveZero` — **every** active person-set 0 (#679 §5, §8). Edges (#679 §5, M:`internal/shopping/adjustment_predicate.go:7`): `expires_trip = trip_seq` → **not** active, so not exempt. `expires_trip = trip_seq+1` → exempt. Legacy `expires_trip IS NULL` → exempt (#679 §8, AC6) until the advance deletes it. Tests A5/H15.

`ActiveZero` comes from **one** batched SQL read, not from response rows:
```sql
SELECT DISTINCT CASE WHEN COALESCE(i.group_id,'') <> '' THEN 'group:' || i.group_id ELSE 'item:' || i.item_id END
FROM shopping_list_entry_adjustments a
JOIN shopping_list_items i ON i.id = a.entry_id AND i.household_id = a.household_id
JOIN households h ON h.id = a.household_id
WHERE a.household_id = ? AND a.quantity = 0 AND i.purchased_at IS NULL
  AND (a.expires_trip IS NULL OR a.expires_trip > h.trip_seq)   -- shopping.ActiveAdjustmentSQL zero branch
```
- It must reuse `shopping.ActiveAdjustmentSQL` (M:`internal/shopping/adjustment_predicate.go:7`) for the expiry text; no second copy.
- It does **not** depend on `CanSendToCart`, on whether the row moved to `offList`, on the row's name, or on the provider (a 0 for any provider is a person's choice for that line). **OPINION** on "any provider": the list shows one target provider today; Backend may restrict to `providerID` if it shows a case where that differs, with a test.
- **Production note (FACT):** an `expires_trip = trip_seq` 0 row is normally deleted in the same writer transaction that advances `trip_seq` (#679 §6, `LapsedZeroAdjustmentSQL`, M:`adjustment_predicate.go:10`), so readers rarely see one. The edge tests seed it directly ("read without delete", as #679 test 6), because the read predicate must stay correct even when the delete fails or is reverted.
- Replaces `activeZeroForTripKeys` (M:`list_assemble_zero.go:15-28`) as the attention input. `applyZeroForTrip` may keep its own placement logic; the exemption must not read its output.

Shelved, target 0 and every-member Don't restock need no explicit input: they are never short (§4.2). The one exception is a hand-added row (Q13), which stays a candidate on purpose.

### 4.2 `short(key)`

True when any holds:
- `PlanViews[key].Buy >= 1`;
- an **unpurchased** staged row (auto or manual) for `key` has `quantity >= 1` (covers Q13 hand-added). **Edge (FACT):** in status mode `provision.Rows` also contains purchased rows (M:`list_assemble.go:69-75`); they must not count;
- `PlanViews[key].Excluded != ""` (§5).

### 4.3 Errors

- Every attention input read (`trip_seq`, marks, send results, skips, active-0, settings, group membership, names) returns its error; the handler returns **500** `InternalError` with no partial body. Fixes F5. Lock with a fault test per loader (§10).
- In `status=1` mode a `PlanView` error is also a 500 (today swallowed, M:`list_assemble.go:94`). Default GET keeps today's behavior. **OPINION:** a silent empty plan hides every `not_planned` and planned-only candidate, which is the "quiet drop" class #677/#683 fixed.
- Drain timeout keeps W3 §2.2 (`listStale: true`, never 5xx).

## 5. Planner: `not_planned` (Backend, `internal/supply`)

**RULING.** v1 reports exactly one drop: `no_product_picked`, for a group line.

**FACT (finding):** `planProductGroup` gets an empty pick (M:`group_plan.go:24-26`) when `group.Pick` errors on an unknown rule kind (M:`internal/group/rule.go:86-91`, via M:`group_plan.go:98-105`) or when the picker returns empty. Pickers return empty for no members (unreachable here, guarded at `:15-17`) or `noneRestocked()` (M:`rule.go:267-269`) when **every member is Don't restock**.

- **Report** `no_product_picked` when the pick is empty **and** at least one member is restockable (not `NoRestock`), or when `Pick` returns an error.
- **Don't report** when every member is Don't restock: that's a person's choice, the same as target 0 (rev2 §1).
- Don't report the no-members path (`:15-17`) or the group-target-0 path (`:19-22`).

**Minimal output addition (OPINION on naming):**
- Internal `plan(...)` returns `planOutput{Lines []Line; Excluded []Excluded}` with `type Excluded struct{ LineKey string; Reason string }` (`LineKey` = `group:<id>`). Both `planResolved` (M:`service.go:376`) and `planLegacy` (M:`:485`) collect it from `planProductGroup`, which returns `(Line, excludedReason string, ok bool)`.
- `Service.Plan` keeps returning `[]Line` (callers at M:`handler_shopping_list_undo_remove.go:60`, `replenishment.go:81,171`, `handler_shopping_list_fill.go:45` are unchanged).
- `LinePlanView` gains `Excluded string`. `PlanView` sets it on the `group:<id>` view (that view already exists from the "dropped with buy 0" loop, M:`planview.go:88-100`).
- Wire: `reason:"not_planned", detail:"no_product_picked"` (W3 §4.8 already lists the code). Copy: §B.1 row "the group can't pick a product".
- Any further `Excluded` code needs an Architect ruling and a §B.1 copy row first. Until then §B.1's "any other reason" row is unused.

## 6. Send results (Backend; client mapping Frontend)

### 6.1 Loader

Replace `LoadSendFailures` with `LoadSendResults(ctx, q, scope, providerID, cutoff)`:
- Filters `provider_id = providerID` (the list's target provider, M:`handler_shopping_list_get.go:165`). Empty `providerID` (none configured) → no send facts, so no send flags. **Correction (rev 3, FACT):** `DISABLE_KROGER` does **not** empty `providerID` (N:`handler_shopping_list_get.go:165-175`, N:`shopping_line_store.go:55-96` never read the switch). Send facts keep loading while the switch is on, as approved copy §D.1 requires ("Problems from earlier sends still are"). Only connection facts are suppressed by the switch (§6.4).
- Applies the retention cutoff `cutoff = now - cart.SendResultRetention`, where `SendResultRetention = 90 * 24 * time.Hour` (M:`internal/cart/public_provision.go:63`, locked by M:`public_provision_test.go:51-52`). It's a fixed duration, not calendar days, so DST and America/Denver don't move it. `now` is the single `time.Now().UTC()` the request already reads (M:`list_assemble.go:64`). The handler passes `cutoff` to the loader, and the loader never reads the clock.
- **Boundary (RULING, same as `LatestResults`, M:`result_recorder.go:148-160`, `attempted_at >= cutoff` with `cutoff.UTC().Format(time.RFC3339)`):** inclusive. A row with `attempted_at == now - SendResultRetention` is loaded. A row with `attempted_at == now - SendResultRetention - 1s` is not. `attempted_at` is RFC3339 UTC with second precision (M:`internal/cart/last_send.go:37-39`), so a 1-second step is the smallest edge. This matches the sweeper (`attempted_at < cutoff` deletes, M:`result_recorder.go:60`) and M:`send_result_retention_test.go:46-80`. Tests H2c-1/H2c-2.
- **OPINION:** call `LatestResults` instead of a second query.
- Loads `status IN ('failed','unknown','skipped')`, scanning `status` and `reason` separately. Rows with any other `status` (`sent`, `added`, or anything else; the column has no CHECK, M:`031_trip_scoped_skips.sql:48`) are never facts. A NULL `reason` reads as `""` (M:`last_send.go:19-21`).

### 6.2 Line-key mapping (RULING)

For a result on item `X`, first match wins:
1. an unpurchased staged row for `X` → that row's key (`group:<gid>` when `row.GroupID != ""`, M:`list_assemble.go:121-126`);
2. `PlanViews` has `item:X` (a member planned on its own line, rev2 §7) → `item:X`;
3. `X`'s product is in `product_group_members` for group `G` and `group:G` is in `PlanViews` → `group:G`;
4. otherwise `item:X`.

Steps 1–4 are tested by H1, H2, H10 and H11 (§10).

**Several results on one line (RULING).** Several members of one group line can have results (one row per item and provider, PK M:`031_trip_scoped_skips.sql:52`). After §6.3 maps each row to a wire `(reason, detail)`:
- the line gets every distinct wire `reason` as a fact;
- for one wire `reason`, `detail` comes from the row with the **greatest `attempted_at`**;
- if `attempted_at` is equal, the row with the **smaller `item_id`** wins (Go string `<`, byte order). `attempted_at` compares as the stored RFC3339 UTC text, which sorts the same as time for that fixed format (as `LatestResults` already does);
- the result must not depend on map iteration or input order. A row whose §6.3 mapping is "not flagged" takes no part.

This selection lives in the pure `Evaluate` (or a pure helper it calls), so A10 tests it directly and H12 tests it end to end. **Edge:** the item-status route (#570, not on master) returns the group line's object for a grouped member (rev2 §6).

### 6.3 Stored → wire mapping (RULING)

| Stored `status` / `reason` | Wire `reason` / `detail` | §B.1 row |
|---|---|---|
| failed / `batch_rejected`, `invalid_line` | `send_failed` / same | not accepted |
| failed / `api_error` | `send_failed` / `api_error` | store error |
| failed / `request_incomplete` | `send_failed` / `request_incomplete` | send stopped before it reached them |
| failed / `store_sign_in` | `send_failed` / `store_sign_in` | couldn't sign in |
| failed / `reconnect_required` | `send_failed` / `reconnect_required` | couldn't sign in. Never the wire reason `reconnect_required`, which only §6.4 emits. When the §6.4 connection fact also holds, the connection reason is `reason` and `send_failed` goes in `others` (union order). |
| failed / `no_per_item_result`, `provider_unavailable` (legacy) | `send_failed` / same | store error (**OPINION**) |
| skipped / `unavailable` | `send_failed` / `unavailable` | none available |
| skipped / `no_upc` | `no_barcode` / `no_upc` | `no_barcode` |
| skipped / `no_match` | `no_store_match` / `no_match` | `no_store_match` |
| unknown / `request_incomplete` | `send_unsure` / `request_incomplete` | stopped answering partway |
| unknown / `no_per_item_result`, `provider_unavailable` | `send_unsure` / same | didn't respond |
| **catch-all:** any `(status, reason)` pair not listed above, including an empty or NULL reason, an unknown future code, or a listed code under a different status (for example `failed`/`no_upc`, `skipped`/`api_error`) | by status only: `failed`→`send_failed`, `unknown`→`send_unsure`, `skipped`→**not flagged**. `detail` is **omitted** (key absent), never the stored code. | the reason's own sentence |

- `detail` is the stored code, never a sentence (rev2 §2). Frontend maps it with this table.
- **The wire `reason` is always one of the §3.1a emittable values, and a §6.3 row is never one of the §6.4 reasons. A stored reason string is never copied into `reason`.** The catch-all row is what guarantees this. Test H3-catchall.
- **502 (F11, RULING):** for rows written with status `unknown`, `storedLineReason` keeps `provider_unavailable`; for `failed` it keeps today's `api_error` rewrite. Backend confirms in the ticket A PR which status the engine writes for a pre-PUT token failure (rev2 §5 says `unknown`) and adds the case to `TestSendStore502IsUnsure`. Side effect: W2 `lastSend.reason` may now carry `provider_unavailable` on unknown rows; that value is already in `LastSendReason` (M:`shared/contract.ts:1812-1816`), so it isn't a wire break.
- `AttentionGroup` keys stay `reason + detail` (M:`evaluate.go:75-78`). Merging `send_failed` groups into §B.1's "mixed reasons" heading per store is client rendering (Frontend + UX).

### 6.4 Connection fact (ticket C, #689; RULING R689)

**The fact is the send gate (FACT):** `Engine.Provision` refuses the whole send unless the `Directory.Read` row is `connected`, for every provider whose auth isn't `AuthNone` (N:`internal/cart/engine.go:129-141`). `Read` returns `nil` for no row and normalizes an empty or unknown state to `disconnected` (N:`internal/cart/connection/connection.go:83-97`).

**One helper, used by the send and by attention (RULING; name is OPINION):**
```go
// internal/cart
func ConnectionBlock(auth AuthCapability, conn *connection.Connection) string {
    if auth == AuthNone { return "" }
    if conn != nil && conn.State == connection.StateConnected { return "" }
    if conn != nil && conn.State == connection.StateReauthRequired { return "reconnect_required" }
    return "not_connected"
}
```
`Provision` replaces its inline check (N:`engine.go:136-137`) with `ConnectionBlock(...) != ""` and keeps the same `ProvisionConflict`. That's behavior-neutral.

**Mapping (fact table):**

| `Read(scope, providerID)` | Wire reason |
|---|---|
| `nil` (no row) | `not_connected` |
| `disconnected`: first authorize never finished (`GenerateAuthState`, N:`connection.go:186-196`) or after `Disconnect` (tokens cleared, N:`:152-166`) | `not_connected` |
| `disconnected` from normalizing an empty or unknown stored state | `not_connected` |
| `not_required` stored on an OAuth provider (no writer) | `not_connected` |
| `reauth_required` (`MarkReauthRequired`, N:`broker.go:162-176`; app client-ID change, N:`handler_kroger_app.go:366-377`) | `reconnect_required` |
| `connected` | none |
| `AuthNone` provider | none |

- No row and `disconnected` are **not** distinguished. `providerConnectionState` (N:`handler_providers.go:167-182`) and `GET /api/providers` are unchanged. No migration. Rev2 §3's "`disconnected` → `reconnect_required`" is superseded.

**Which lines (RULING).** `key` gets the §6.4 reason when **all** hold:
1. §4 steps 1–2 pass (not exempt, `short`).
2. `providerID != ""` **and** `providerGate(...).Configured(providerID)` (household store app present; closes the zero-store edge at N:`internal/store/linestore.go:90-92`).
3. `sendable(key)` = `store.CanSendToCart(lineCtx, lineCtx.LineStore(runStoreID, productID), providerID, gate)`. `runStoreID`/`productID` come from the key's unpurchased staged row, or else from `PlanViews[key].ProductID` with `runStoreID = nil`. It never reads `OffListRowResponse.CanSendToCart` (hard-coded `false`, N:`list_assemble.go:603`).
4. The switch is off: **not** (`providerID == "kroger"` && `EnvFallback.Disabled`), the same predicate as authorize (N:`handler_providers.go:212-214`).
5. `ConnectionBlock(auth(providerID), conn) != ""`.

**Inputs:** `EvaluateInput` gains `ConnFact Reason` (`""` when none; one value per request) and `Sendable map[string]bool`. `Evaluate` stays pure. The connection is read **once** per request. A read error → 500 (§4.3). The connection reasons carry **no** `detail`.

**`storeDisabled` (conformance fix, same PR):** set `{providerId: "kroger"}` from the step 4 predicate on `?status=1` and `/summary`, else `null`. FACT: it has no writer on master (N:`list_assemble.go:44`). Suppressing connection flags without the §D.1 banner would be a quiet drop.

**Precedence:** unchanged (§3.1 order). A connection reason outranks every §6.3 reason. `not_connected` and `reconnect_required` are mutually exclusive.

**Copy:** §B.1 rows `not_connected` / `reconnect_required` as approved. No new strings. Admin or member text is chosen from `fix.allowed`.

**Prerequisite:** #689 part 2 (the owner gate on authorize, callback and disconnect; `TestProviderConnectRequiresOwner`) merges before ticket C or in the same PR. Without it `allowed` can't come from a real check.

## 7. Unnamed rows and `items[].name` (Backend; copy UX/CJ)

**RULING (fixes F6).** No flagged line is ever dropped for lack of a name.
- Remove `droppedUnnamed` and the `delete(perLine, key)` at M:`list_assemble.go:346,361-366,397-414`. Emit the off-list row with `name: ""`. `attentionGroups` counts are then `Evaluate`'s, unfiltered.
- Keep the `slog.Warn` (renamed `off_list_row_unnamed`, no drop), with `household_id` and `line_key` only.

**`items[].name` (additive, `status=1` only):**
- One server resolver for `items[]` and `offList[]`: group line → `product_groups.name`; else the product display name with the household rename (`product.DisplayNameSQL`, M:`internal/product/override.go:19-27`); else `""`.
- The batch lookup covers the **union** of `PlanViews` product ids and staged rows' product ids (`productIDByItemID`, M:`shopping_line_store.go:15`), one query each for products and groups, per request. Lookup error → 500 (as #683).
- A group name is never empty (FACT: `nameKey` rejects it, M:`internal/group/groups.go:1449-1453`). A product name may be `''` (FACT: `001_initial_schema.sql`, `NOT NULL` without a check). So `""` only ever means "a product with no name".
- `name` is never the line key, an id, or a member's name standing in for the product. The client shows N1/N2 (§11) when it's `""`.
- `LineDisplayName` (`json:"-"`) is filled from this resolver so `zeroForTripOffListName` and `offList` agree.

## 8. Ticket B: marks writer (Backend, `internal/trip` + `internal/attention`)

**FACT:** the table exists (M:`internal/app/migrations/031_trip_scoped_skips.sql:55-60`, PK `(household_id, line_key)`, `flagged_trip INTEGER NOT NULL`). **No migration.**

**Where (RULING, W3 `:62`):** inside `tryAdvanceTrip`'s writer transaction (M:`internal/trip/trip.go:163-197`), the one path both triggers use (#679 §6).

**Constraint (FACT):** the mass stock-in advance runs **inside the scan-commit transaction** (M:`internal/scan/commit.go:131`), and W3 `:51` forbids `Plan` there (single-connection writer pool). So the boundary flag set can't call `Plan`.

**Design (OPINION; the alternatives need a persisted plan snapshot and a migration):**
1. After the no-empty-trip gate passes and **before** `AdvanceSeq`, compute `boundaryFlagged` with the **same** `attention.Evaluate`, fed by tx-bound loaders (`*sql.Tx`, never the writer `*sql.DB`, which would deadlock the single-connection pool). Exemptions use the closing trip's `trip_seq`, so a line with an active 0 or a removal at close gets **no** mark (#679 §8). Plan-derived inputs are replaced by persisted state: `short` uses only unpurchased staged rows; `Excluded` is empty. The provider is the household's sole configured provider, resolved through the tx.
2. After `AdvanceSeq`, `clearLapsedZeroAdjustments` and `writePlannedBoundaryChanges`:
   ```sql
   DELETE FROM attention_marks WHERE household_id = ? AND line_key NOT IN (<boundaryFlagged>);
   INSERT INTO attention_marks (household_id, line_key, flagged_trip) VALUES (?, ?, <old trip_seq>)
     ON CONFLICT(household_id, line_key) DO NOTHING;   -- per flagged key
   ```
   With an empty set the DELETE removes every mark for the household.
3. Any error rolls back the whole advance (`trip_seq`, skips, adjustments, boundary changes and marks together).

**Connection fact at the boundary (rev 3, ticket C):** the boundary `Evaluate` also gets `ConnFact` and `Sendable`. `Directory.Read` uses the writer `*sql.DB` (N:`connection.go:74`), so the tx loader must use `connection.ReadQ(ctx, q, scope, provider)`, which holds the one copy of the `SELECT … LIMIT 1` plus normalization; `Directory.Read` calls it with `d.db`. At the boundary `sendable` uses staged rows only. Whichever of ticket B or ticket C merges second carries this, with test B10.

**Other evaluation point:** in the result-recorder transaction, when a line's latest result becomes `sent`/`added`, or an unknown is resolved (`ResolveUnknownLineTx`, M:`result_recorder.go:115-139`), delete that line's mark (§6.2 key). The coalescer job writes no marks in v1 (**OPINION**: it changes no fact the boundary gate reads except staged rows, and the next advance reconciles).

**Semantics:**
- Under D1 a mark alone never flags (§4 step 5); a stale mark is inert until the line has a real fact.
- `flagged_trip` survives a reason change (key is `line_key`, rev2 §8a(b)); `DO NOTHING` keeps the first trip.
- A `line_key` change (member joins/leaves a group line) drops the old mark at the next advance; the new key starts fresh.
- Idempotent: the advance is one transaction; a retry after rollback recomputes from scratch. A rejected (empty-trip) advance writes no marks.
- Household wipe already deletes marks (M:`supply/sql.go:266-267`).
- **Known v1 limit:** `not_planned` lines never get a mark at the boundary (no `Plan` in the tx), so they never show `stuck`. Given §5's finding (it fires only on an invalid rule kind), the cost is low. A follow-up needs a persisted plan snapshot.

## 9. Ownership

| Area | Owner |
|---|---|
| §3 docs (`api-contract.md`) | Architect, in this spec PR |
| §3 `shared/types.ts`, `shared/contract.ts`, Go types, parity test | Backend (ticket A) |
| §4–§7 server | Backend (ticket A) |
| §8 writer | Backend (ticket B) |
| §6.4 connection fact, `storeDisabled` writer | Backend (ticket C, #689) |
| Owner gate on authorize/callback/disconnect | Backend (#689 part 2) |
| Rendering: union (all 8, including reserved), Unknown fallback, §6.3 detail→§B.1 map, N1/N2, mixed-reason merge | Frontend, in the W3 frontend PR; UX GO. Frontend must not ship a row using the generic fallback for a value master emits (#686 gate). |
| N1/N2 strings | UX drafts, CJ approves (§11) |

## 10. Sequencing and tests

**Order:** this spec PR (docs-only; SDET GO under the docs path) → ticket A PR → ticket B PR (or A+B in one PR). B never merges before A. Ticket C merges after A, and after #689 part 2 (or with it). Part 2 has no dependency on A or B. #684 (`948e4ac0`, open): whichever merges second rebases onto §4.1. #684 must not build the exemption from `offList` rows (`zeroForTripExemptKeys`), which is a blocking finding against #679 §8.

Every test below must **fail** when its rule is removed. SDET runs the listed mutants against the PR head.

**Where edge tests live.** `Evaluate` is pure, so the expiry predicates (`Skipped`, `ActiveZero`) and the retention cutoff are SQL in the loaders. Their edge tests seed SQLite rows and call the loader (A3a, A5, H2c-1/H2c-2) **and** go through `GET ?status=1` (H14, H15, H2c-3). A test that only hands `Evaluate` a pre-built map can't kill a predicate mutant. Fixtures use synthetic ids and names only (no household data).

**Ticket A — Go (`internal/attention`, table-driven `TestNeedsAttention`):**

| Test | Asserts | Mutant that must fail it |
|---|---|---|
| A1 mark only | mark, no fact → `nil` | restore the marks loop |
| A2 mark + fact | `send_failed`, `stuck:true` when `flagged_trip < trip_seq`; `false` when equal | `stuck` always false / `<=` |
| A3 exemptions × sources | removed, opening, active-0 each × {send result, `no_product_picked`, mark} → `nil` | drop any one gate clause |
| A3a skip expiry, both edges (loader `batchActiveSkips` or its replacement, real SQLite) | `trip_seq = T`. Skip with `expires_trip = T+1` → key in `Skipped`. Skip with `expires_trip = T` → key **not** in `Skipped`. Then `Evaluate` with a `send_failed` fact on each line: the `T+1` line → `nil`, the `T` line → flagged `send_failed` | `<` → `<=` in `h.trip_seq < s.expires_trip`, or drop the predicate (fails the `T` case); off-by-one `h.trip_seq + 1 < s.expires_trip`, or drop the `Skipped` gate clause (fails the `T+1` case) |
| A4 active-0 independent of placement | 0 on an unnamed line kept in `items[]`, a removed+0 line, a `canSendToCart:false` line → exempt | rebuild from `offList` or add `CanSendToCart` |
| A5 active-0 expiry, both edges (loader for `ActiveZero`, real SQLite, `quantity = 0`, unpurchased line, `trip_seq = T`) | `expires_trip = T` → key **not** in `ActiveZero`, and with a `send_failed` fact the line is flagged. `expires_trip = T+1` → in `ActiveZero` → `nil`. `expires_trip IS NULL` (legacy, AC6) → in `ActiveZero` → `nil`. A non-zero row with `expires_trip` NULL → not in `ActiveZero` | `>` → `>=`, or drop the expiry predicate (fails `T`); off-by-one `a.expires_trip > h.trip_seq + 1`, or drop the `ActiveZero` gate clause (fails `T+1`); drop `expires_trip IS NULL OR` (fails legacy); drop `quantity = 0` (fails non-zero) |
| A6 not short | send result on a line not short and with no staged unpurchased row → `nil`; purchased row doesn't count | drop `short` / count purchased rows |
| A7 Q13 hand-added | manual row + failed send → flagged | require `Buy>=1` |
| A8 precedence/others | `no_barcode` + `send_failed` → reason `no_barcode`, others `["send_failed"]`; none → `[]` (JSON `[]`, not `null`) | reverse order / nil slice |
| A9 closed + reserved | Over every `TestNeedsAttention` case: `reason` and every `others[i]` ∈ the emittable values for the PR's stage (§3.1a; 5 in ticket A, 7 from ticket C). None of them is `probable_miss` or `attention`; in ticket A none is `not_connected` or `reconnect_required`. From ticket C, a case with `ConnFact == ""` emits neither connection reason. `not_planned` ⇒ `detail != ""`. `attentionGroups[].reason` has the same check | emit `attention`; emit any reserved value; emit a connection reason with no `ConnFact`; empty `not_planned` detail |
| A10 one reason, several members (§6.2) | Line `group:G`, members `item-a` < `item-b`, both stored `failed`. (i) `item-a` `api_error` at `t`, `item-b` `batch_rejected` at `t+1s` → `send_failed`/`batch_rejected`. (ii) `item-a` `batch_rejected` at `t+1s`, `item-b` `api_error` at `t` → `batch_rejected`. (iii) both at `t`: `item-a` `api_error`, `item-b` `batch_rejected` → `api_error`. Each case runs with the input order `[a,b]` and `[b,a]` and gives the same result. Exactly one row, `count: 1` in the one `attentionGroups` entry | drop the time tie-break (fails (i) or (ii)); drop the item-id tie-break, or let map or input order decide (fails (iii) in one order); compare with `<` vs `>` reversed (fails all) |

**Ticket A — planner (`internal/supply`):** P1 unknown rule kind → `Excluded{group:G, no_product_picked}` and `PlanView["group:G"].Excluded`; P2 every member Don't restock → no `Excluded`; P3 group target 0 → none; P4 `Plan` callers' `[]Line` unchanged (golden). Mutants: report on `noneRestocked`; drop the excluded append.

**Ticket A — HTTP (`handler_shopping_list_w3_contract_test.go`):**
- H1 group key: a member's failed result flags `group:G`, and no `item:` off-list row appears. H2 solo-planned member → `item:X`. Mutant: key by `item:`.
- H2b provider filter: a failed row for another provider → not flagged.
- H2c retention, both edges (§6.1). Fixed `now` = `2026-11-11T14:00:00Z`, `cutoff = now - cart.SendResultRetention`:
  - **H2c-1** (loader): `failed` row at `attempted_at == cutoff` → loaded.
  - **H2c-2** (loader): `failed` row at `cutoff - 1s` → not loaded.
  - **H2c-3** (`GET ?status=1`, wall clock, no clock seam needed): a row at `now - SendResultRetention + 1h` → flagged; a row at `now - SendResultRetention - 1h` → `needsAttention: null` and absent from `attentionGroups`.
  - Mutants: `>=` → `>` (fails H2c-1); no cutoff (fails H2c-2/H2c-3); a different duration, such as 89 or 91 days, or hard-coded days in local time (fails H2c-3).
- H3 §6.3 table: one subtest per stored row → exact `reason`/`detail`; skipped `unavailable` and unknown rows are loaded. Mutant: `status='failed'` only.
- **H3-catchall** (§6.3 last row; `TestStatusAttentionStoredReasonCatchAll`). Each subtest seeds one row on a short line with no other fact:
  | Seeded status / reason | Expected |
  |---|---|
  | `failed` / NULL | `send_failed`, no `detail` key |
  | `failed` / `""` | `send_failed`, no `detail` key |
  | `failed` / `zz_future_code` | `send_failed`, no `detail` key |
  | `failed` / `no_upc` | `send_failed`, no `detail` key |
  | `unknown` / NULL, `""`, `zz_future_code` | `send_unsure`, no `detail` key |
  | `skipped` / NULL, `""`, `zz_future_code`, `api_error` | `needsAttention: null`, absent from `attentionGroups` |
  | `sent` / `zz_future_code` and `bogus_status` / `api_error` | `needsAttention: null` |

  Every subtest also checks that `reason` ∈ the §3.1a emittable set and that no wire string (`reason`, `others`, group `reason`) equals the seeded stored reason. Mutants: copy the stored reason into `reason`; copy it into `detail`; flag `skipped` catch-all rows; treat a listed code under another status as listed.
- H4 unnamed: flagged off-list row with product name `''` → present, `name:""`, counted in `attentionGroups` and summary. Mutant: restore `droppedUnnamed`.
- H5 `items[].name`: product with a household rename → renamed value; unnamed → `""`; group line → group name; default GET bytes unchanged (T28) and has no `name`/`needsAttention` keys. Mutant: put the fields on the shared struct.
- H6 shape: unflagged row has `"needsAttention": null` (key present); flagged row has `others: []`, full `fix` with `target` keys. Mutant: restore `omitempty`.
- H7 loader errors: a fault seam on each attention loader (unexported, as #683) → 500, no body rows. Mutant: `_ =` any one error.
- H8 `no_product_picked` end-to-end with an invalid stored rule kind.
- H9 502 (§6.3): extend `TestSendStore502IsUnsure` so a token failure lands as `send_unsure`/`provider_unavailable`.
- **H10** §6.2 step 3 (`TestStatusAttentionSendKeyGroupViaPlan`): item `X` has **no** unpurchased staged row, `X`'s product is in `product_group_members` for `G`, `PlanViews` has `group:G` and not `item:X`, and `X` has a stored `failed` row → `group:G` is flagged, and no `item:X` row appears in `items[]` or `offList`. Mutant: skip step 3 (falls to `item:X`).
- **H11** §6.2 step 4 (`TestStatusAttentionSendKeyFallbackItem`): (a) item `X`, product in no group, no staged row, not in `PlanViews`, stored `failed` → the flag is on `item:X` (an `offList` row, named per §7). (b) Product in group `G`, but `group:G` **not** in `PlanViews` → `item:X`, not `group:G`. Mutants: drop the "`group:G` is in `PlanViews`" condition (fails (b)); drop step 4 so the result is lost (fails (a)).
- **H12** §6.2 several members, end to end (`TestStatusAttentionGroupDetailTieBreak`): A10 cases (i) and (iii) through `GET ?status=1` with two grouped members' stored rows → one `group:G` row, with `detail` as in A10. Mutant: same as A10.
- **H13** reserved reasons never emitted (`TestStatusAttentionReservedReasonsNotEmitted`). **Ticket A form below; ticket C narrows it (see "Ticket C").** Seed, on short lines:
  - a linked store (`stores.linked_provider = 'kroger'`) with no `provider_connections` row;
  - a connection row with `state = 'reauth_required'`;
  - one with `state = 'disconnected'`;
  - a stored `failed`/`reconnect_required` result;
  - a `sent` result, then a `stock_in_clusters` row with `closed_at` after that `attempted_at`, and no stock-in of the item since.

  Expected: no `reason`, `others[i]` or `attentionGroups[].reason` anywhere in the body equals `not_connected`, `reconnect_required` or `probable_miss`. The `failed`/`reconnect_required` line is `send_failed`/`reconnect_required`. Reuse its `assertNoReservedReasons(t, body)` helper on every other `?status=1` fixture in `handler_shopping_list_w3_contract_test.go`. Mutant: add any branch that emits a reserved value.
- **H14** skip expiry through HTTP (`TestStatusAttentionSkipExpiryEdges`): A3a's two lines, each with a stored `failed` row → the `expires_trip = T+1` line has `needsAttention: null`; the `expires_trip = T` line is flagged `send_failed`. Mutant: as A3a.
- **H15** active-0 expiry through HTTP (`TestStatusAttentionZeroExpiryEdges`): A5's `T`, `T+1` and NULL lines, each with a stored `failed` row → `T` flagged; `T+1` and NULL `needsAttention: null`. Mutant: as A5.
- Parity: `hosted_contract_test.go` asserts Go `Reasons` == `NEEDS_ATTENTION_REASONS` (same order). Mutant: drop or reorder one.
- Migrate existing tests that seed marks alone to get a flag (M:`handler_shopping_list_w3_contract_test.go:701,713`; M:`evaluate_test.go:95-110`) to seed a real fact.

**Ticket C (#689; `internal/cart`, `internal/attention`, HTTP):**

| Test | Asserts | Mutant that must fail it |
|---|---|---|
| A11 `ConnectionBlock` | nil, `disconnected`, `""`-normalized, `not_required` on OAuth → `not_connected`; `reauth_required` → `reconnect_required`; `connected` → `""`; `AuthNone` with any conn (incl. nil) → `""` | map `disconnected` to `reconnect_required`; treat `not_required` as connected; drop the `AuthNone` short-circuit |
| A12 `Sendable` | `ConnFact` set, `Sendable[k]=false` → no connection reason on `k`; its §6.3 facts still apply | ignore `Sendable` |
| Engine golden | existing `Provision` not-connected tests pass unchanged after the refactor | gate on `reauth_required` only |

- **H13-reserved** (narrowed `TestStatusAttentionReservedReasonsNotEmitted`): only the `probable_miss` seed. `assertNoReservedReasons` checks `{probable_miss, attention}` on every `?status=1` fixture.
- **H13-conn** (`TestStatusAttentionConnectionReasons`): `stores.linked_provider='kroger'`, household app present, one short, non-exempt, sendable line, admin viewer. One subtest per §6.4 row: no row, `GenerateAuthState` only, `connected` then `Disconnect`, raw `state=''` and `'zz_future'` → `not_connected`; `reauth_required` via `MarkReauthRequired` and via an app client-ID change → `reconnect_required`; `connected` → `needsAttention: null`. Each flagged case has `others: []`, no `detail` key, `fix` = `connect_store`/`allowed:true`/`who:null`/`target.providerId:"kroger"`, and one `attentionGroups` entry with count 1. Mutants: collapse `reauth_required` into `not_connected`, or `disconnected` into `reconnect_required`; leave either reason reserved; parse the state a second time without normalizing.
- **H13-rank** (`TestStatusAttentionConnectionOutranksSendReconnect`): (a) `connected` with a stored `failed`/`reconnect_required` → `send_failed`/`reconnect_required`, `others: []`. (b) `reauth_required` with the same row → `reconnect_required`, `others: ["send_failed"]`, no `detail`. (c) no row with stored `skipped`/`no_upc` → `not_connected`, `others: ["no_barcode"]`. Mutants: copy the stored reason onto the wire; drop the outrank; carry the send `detail` onto a connection reason.
- **H13-gates** (`TestStatusAttentionConnectionGates`), on a `reauth_required` household: an exempt line (removed, active-0 `T+1`, opening) → `null`; a not-short line → `null`; an in-person store line (`linked_provider` NULL) → `null`; a zero-store household with the app → flagged; no store app (`?provider=kroger`) → `null`. Mutants: skip the §4 gates; drop `sendable`; use `OffListRowResponse.CanSendToCart`; drop the `Configured` check.
- **H13-disabled** (`TestStatusAttentionConnectionStoreDisabled`): `EnvFallback.Disabled` plus `reauth_required` → no connection reason anywhere; `storeDisabled: {"providerId":"kroger"}` on `?status=1` and `/summary`; a stored `failed`/`api_error` row on another short line is still `send_failed`. With the switch off → `storeDisabled: null`. Mutants: emit while disabled; key the switch on empty `providerID`; leave `storeDisabled` unset; drop send facts while disabled.
- **H13-role:** the "no row" seed as a member → `allowed:false`, `who:{"role":"owner"}`; as the owner → `allowed:true`, `who:null`. Mutant: hard-code `allowed`.
- **H7:** add a fault seam on the connection read → 500, no body rows.
- **B10** (with whichever of B or C merges second): `reauth_required` at close on a short sendable staged line → mark written; the next `?status=1` has `stuck:true`. The scan-commit path reads through the tx (1-connection pool, as B6). Mutant: read via `Directory.Read`.
- **#689 part 2:** `TestProviderConnectRequiresOwner` as in #689 §2 (authorize, callback before `ConsumeAuthState`, disconnect).

**Ticket B (`internal/trip` + HTTP):**

| Test | Asserts | Mutant |
|---|---|---|
| B1 insert | flagged at close → mark with old `trip_seq`; next `?status=1` `stuck:true` | write the new `trip_seq` |
| B2 delete | unflagged at close → mark removed | skip DELETE |
| B3 survive | mark kept with its first trip across two advances and a reason change | `DO UPDATE` |
| B4 exempt | active-0 / removed / opening at close → no mark | evaluate after `AdvanceSeq` |
| B5 rollback | fault after the marks write (extend `SetFaultAfterAdvanceSeqForTest` with a post-marks point) → no marks, `trip_seq` unchanged | write outside the tx |
| B6 both triggers | Start a new cart and mass stock-in close both write marks; the scan path makes no writer-pool call outside the tx (test with a 1-connection pool) | read via `*sql.DB` |
| B7 empty trip | rejected advance → marks unchanged | write before the gate |
| B8 result clears | a `sent` result deletes the line's mark | skip the delete |
| B9 mark alone | mark present, fact cleared → `needsAttention:null` | (A1 at HTTP level) |

## 11. Decisions

- **CJ (through Manager), copy only:** UX's N1 "A product with no name" and N2 "Add a name" (UX file `attention-reason-copy-for-cj.md`, sha `4a491c8e`, "NEW strings"). This spec ships `name:""`, but no client may render a no-name row until N1/N2 are approved. D1 itself conforms to approved D5/D6 and rev2; it isn't a new product choice.
- **Copy rev2 §B.1 (`03d438d9`): approved (Nov 9, per #571 issuecomment-6745454775).** No open action. N1/N2 above are separate new strings and still pending with CJ.
- No other open decisions. §8's v1 limit, the §3.2 `fix` codes and the §3.1a reservations are technical. The connection ruling (§6.4, R689) is technical and uses approved §B.1/§D.1 copy as is; `probable_miss` waits for the W5 route.

## 12. Rollback

Ticket A: revert the PR; the wire is additive (`name`) or conformance-only, and no client consumes it yet. Ticket B: revert; existing marks are inert under A's D1 rule and are cleaned up on the next advance after re-landing. If B is reverted while A stays, `stuck` is always `false`. Ticket C: revert; the two reasons go back to reserved, and `storeDisabled` goes back to always `null`. No data changes.

## 13. Rev 2 changes (SDET PR #688 NO-GO at `395b8f9c`)

| SDET item | Closed by |
|---|---|
| 1. Union reachability, plus a catch-all | §3.1a reserves `not_connected`, `reconnect_required` and `probable_miss` (with FACT reasons). §4 step 3 limits fact sources. §6.3 catch-all row and the "never a raw stored reason" rule. Tests A9, H13, H3-catchall. |
| 2. H2c retention edges | §6.1 boundary: 90 days as a duration, inclusive at `cutoff`, excluded at `cutoff - 1s`. Tests H2c-1, H2c-2, H2c-3. |
| 3. §6.2 tie-breaks and steps 3–4 | §6.2 multi-result rule. Tests A10, H12 (tie-breaks), H10 (step 3), H11 (step 4). |
| 4. A5 and A3 skip edges | §4.1 edge bullets. Tests A5 (`T` not exempt, `T+1` and NULL exempt), A3a (`T` not skipped, `T+1` skipped), H14, H15. These agree with the code (M:`adjustment_predicate.go:7`, M:`list_assemble.go:315`) and #679 §5/§8. |

Also: §B.1 copy approval recorded (header, §11).

## 14. Rev 3 changes (#689 part 1 ruling)

| Change | Where |
|---|---|
| `not_connected` / `reconnect_required` emittable from ticket C; `probable_miss` still reserved | §1, §3.1a, §4 step 3 |
| Connection fact = the send gate; `reauth_required` → `reconnect_required`, every other blocking state → `not_connected`; no row and `disconnected` not distinguished; supersedes rev2 §3 for `disconnected` | §6.4 |
| `connect_store` `fix` row, `allowed` from the #689 part 2 owner predicate | §3.2 |
| `store.checkReason` not aligned (different enum) | §3.1a |
| `storeDisabled` writer; connection facts suppressed while the switch is on; send facts are not (corrects §6.1's `DISABLE_KROGER` parenthetical) | §6.1, §6.4 |
| Precedence with stored `failed`/`reconnect_required` | §6.3 |
| Boundary tx reader for the connection fact | §8 |
| Tests: A9 widened per stage; A11, A12, engine golden, H13-reserved/conn/rank/gates/disabled/role, H7 seam, B10 | §10 |
| No wire type change, no migration | §1 |

— Stockbot Architect
