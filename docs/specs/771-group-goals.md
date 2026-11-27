# #771: one goal per group, item-count goals, one-trip list edits

Status: contract, docs-only, **rev 1 (draft)**. Intended path: `docs/specs/771-group-goals.md`. Owner of the build: Backend (§12 T1), then Frontend (§12 T2).
Issue: [#771](https://github.com/Rhionin/stockbot/issues/771). Requirements: PM rev 1, [issuecomment-7224912048](https://github.com/Rhionin/stockbot/issues/771#issuecomment-7224912048) (R1–R15, B1–B9, F1–F5, migration rules 1–7), cited as `PM Rn`.

Pinned sources:
- master `9adc206e0814e3e0a785466b2d3ce1f5f4bbe9ac` (cited as `M:`). All file:line references are at this commit.
- UX draft spec `771-group-goal-ux-spec.md` rev 1 (sha256 prefix `5f909c0f`), cited as `UX §n` / `UX An`. Its §12 is answered in §8 below.
- `docs/specs/686-needs-attention-reasons.md` (cited as `#686`), `docs/design/571-w3-replan-contract.md` (cited as `W3`), `docs/design/multi-household-staged.md` (cited as `MHS`).

Labels: **FACT** = read in code at `M:`. **RULING** = Architect decision within technical authority. **OPINION** = recommendation the owner may push back on with reasons. **RELAYED** = CJ decision relayed by Manager, not read in CJ's own words.

CJ decisions this contract builds on (RELAYED by Manager, 2026-11-27):
1. Replace-what-you-used is counted per group, not per product.
2. Each group has one goal; products in a group cannot have their own amount.
3. A list edit lasts only this trip.
Pending CJ ruling: the migration default (largest member amount becomes the group goal, with a backup). This contract is written for that default (§4.3) and lists the alternatives (§13 Q1); the schema and API hold either way.

## 1. Summary

- A group goal is one of **items** (count of units on hand across all members), **months**, or **amount** (oz or fl oz, unit derived by the server), plus the existing group **Don't restock**. One new column, `product_groups.goal_items` (§3).
- Item targets above 0 on grouped products are rejected in the store layer (`restock.Targets.SetItem`), so every writer gets the existing 409 `in_group` (§6.3). Value 0, Don't restock and clearing stay allowed.
- The planner never splits a member out of its group (R7), counts buy-back for the whole group (R1), and records a reason when a saved amount goal can't be counted (R14), instead of dropping it silently (§5).
- An edited list line keeps its edit until the trip advances, and a per-store quantity no longer counts as an edit (§7).
- **Migration `032_group_goals.sql`** (next free number; 029 is reserved by open PR #568): moves member targets into group goals per PM rules 1–7, with row-level backup tables, in one transaction. It has been run on a synthetic DB in a scratch worktree, with 9 of 9 rule mutants killed (§4.7).

```mermaid
flowchart LR
  subgraph Write
    A[PUT /api/groups/{id}/target] --> G[(product_groups<br/>goal_items · window_months · quantity_*)]
    B[PUT /api/restock/items/{id}<br/>PUT /api/products/{id}/supply-override] -->|value>0 on grouped product| X[409 in_group]
    B -->|value 0 / ungrouped| T[(item_targets)]
  end
  subgraph Plan
    G --> P[planProductGroup]
    T -->|0 = NoRestock only; >0 ignored for grouped| P
    P -->|buy ≥ 1| L[group line]
    P -->|amount goal blocked| E[PlanExcluded group_goal_*]
  end
  subgraph List
    L --> S[(shopping_list_items<br/>touched + touched_expires_trip)]
    E --> O[offList / Not on the list]
  end
```

## 2. Today on master (FACT)

| # | Where | Today | Requirement |
|---|---|---|---|
| F1 | `M:internal/supply/group_plan.go:59-82`, `service.go:630-644` | Group replace sums each member's uses since **that member's** latest stock-in. | R1 |
| F2 | `M:internal/supply/planview.go:167-170` | `planBasis.replacing` for a group line is the picked member's own count (`linePlanViewFromLine` uses `byProduct[line.Product]`, `:72-80`). | R2 |
| F3 | `M:internal/supply/service.go:435-447` (`splitResolvedGroup`), `:413-433` (`openingAmountLines`) | A member with an item target > 0 is planned alone, with only its own stock. | R7 |
| F4 | `M:internal/server/handler_restock.go:104-123` → `M:internal/restock/targets.go:354-384` | `PUT /api/restock/items/{itemId}` writes any target with no group check. | R3/R4 |
| F5 | `M:internal/server/handler_supply.go:99,123,193-212` | `PUT`/`GET /api/products/{id}/supply-override` refuse grouped products: 409, code `in_group`, body `{"error":"This product is in a group. Change what the group keeps on hand.","code":"in_group","members":{"groupId":"…"}}` (envelope `M:handler_wrapper.go:46-56`; `ConflictDetails` `M:handler_errors.go:203-205`). **The wire key is `members.groupId`, not `details.groupId`.** No web or mobile code reads `in_group` today (code search). | R4 |
| F6 | `M:internal/group/groups.go:60-67,195-224` | A group target is `windowMonths` (0–12) or `quantity` in ounces + `dimension` (mass/volume). No count kind. | R11 |
| F7 | `M:internal/group/groups.go:1218-1257` | `inferDimension` returns the client's `dimension` unchecked (`:1219-1221`) and falls back to **mass** when there are no members (`:1222-1224`) or no sized member (`:1253-1254`). The web editor always sends `dimension` (`M:frontend/src/components/groups/Seesaw.tsx:111`, `seesaw.ts:77`). | R12 |
| F8 | `M:internal/group/buy.go:48-65`, `group_plan.go:43-60` | An amount goal with an unsized or other-dimension member returns buy 0 plus an explanation that is only attached when buy ≥ 1, so the reason is lost. | R14 |
| F9 | `M:internal/shopping/shopping.go:522-525` | `SetAdjustment` sets `touched = 1`. | R10 |
| F10 | `M:internal/shopping/shopping.go:595-639` | `SwapAutoLine` sets `item_id` and `touched = 1`; refuses a line that isn't `auto` with a `group_id` (`:606-608`). | R8 |
| F11 | `M:internal/server/handler_shopping_list_item_update.go:11-51` | `PATCH /api/shopping-list/items/{id}` only marks a line purchased. It does not set `touched`. **The only writers of `touched` are F9 and F10** (`rg "UPDATE shopping_list_items"`, 4 hits). | R8 answer |
| F12 | `M:internal/shopping/auto_items.go:58-100,225-252,373-375`; `scoped_save.go:68-123,180-225` | A touched unpurchased auto row is never refreshed or deleted, and freezes its whole group key. | R8/R9 |
| F13 | `M:internal/restock/targets.go:745-780,684-700` | `POST /api/restock/suggestions/apply` writes `item_categories` only, never `item_targets`. | R4 list |
| F14 | `M:internal/restock/target.go:89-104`, `targets.go:1128-1133`; `M:supply/group_plan.go:34-40` | `Resolve` for a grouped item with no group goal falls through to category targets, but the group planner ignores categories and uses the household window. The settings screen can show a category target that the list doesn't use. | R6 (adjacent) |
| F15 | `M:internal/supply/sql.go:79-106` | Groups with no goal plan at `readMonths`: the household months value; `DefaultMonths` when the household default is an amount; 0 when it's Don't restock. | `defaultMonths` |
| F16 | `M:internal/server/handler_group.go:585-604` | Group `invalid` errors are **400** `validation`, not 422. | API |
| F17 | `M:mobile/` | No call to any group, restock or supply endpoint (code search; UX §1 agrees). | F5 |

`item_targets` writers on master (FACT, `rg "item_targets"` non-test): `restock/targets.go:371` (`SetItem`, used by `PUT /api/restock/items/{id}`, `POST …/dont-restock` with value 0 `handler_restock.go:170`, and `PUT /api/products/{id}/supply-override` `handler_supply.go:139-145`); `targets.go:396,1367` (deletes); `targets.go:843-871` (`CopyLegacy`, migration 019 only; already skips grouped products except their 0s); `group/groups.go:520-527` (`SetMemberRestock`, value 0 only); `groups.go:1158` (`deleteOverrides`, deletes > 0). `POST /api/items/{itemId}/target-quantity` writes the legacy `items.target_quantity`, which the resolved planner doesn't read (`M:handler_suggestion_target_quantity.go:11-66`); no client calls it.
## 3. Data model

### 3.1 Tables involved (FACT, schema after 031)
- `product_groups` (`M:024_household_keys.sql:142-183`): `window_months` INTEGER 0–12, `quantity_base_value` REAL ≥ 0 (grams or ml), `quantity_dimension` mass|volume. CHECKs: window and quantity are exclusive; quantity columns are null together. 0 in either means group Don't restock.
- `product_group_members` (`M:024:22-38`): `(household_id, product_id)` PK, `group_id`, `no_restock` (legacy; the live Don't restock is an item target 0, `groups.go:490-492`).
- `item_targets` (`M:019_restock_targets.sql`): `kind` months|amount, `value` 0–999 whole (`restock/targets.go:286-300`). **An amount is a count of units, with no unit column** (issue cause D).
- `products.net_base_value` / `net_dimension` (`M:010_product_net_size.sql`): per-unit size in g or ml.
- `shopping_list_items.touched` (`M:009_staged_cart.sql:4`), `group_id`, `actor_user_id`.
- `households.trip_seq` (`M:031:2`).

### 3.2 Changes (RULING)
```sql
ALTER TABLE product_groups ADD COLUMN goal_items INTEGER
    CHECK (goal_items IS NULL OR (goal_items BETWEEN 1 AND 999
        AND window_months IS NULL AND quantity_base_value IS NULL AND quantity_dimension IS NULL));
ALTER TABLE shopping_list_items ADD COLUMN touched_expires_trip INTEGER;
ALTER TABLE shopping_list_items ADD COLUMN touched_by_user_id TEXT;
ALTER TABLE shopping_list_items ADD COLUMN touched_at TEXT;
```
Plus three backup tables (§4.4). The column CHECK references other columns. It is enforced on ADD COLUMN and on later updates in both directions; this was verified on the repo's SQLite (`modernc.org/sqlite v1.62.0`, SQLite 3.54.1) in the scratch run (§4.7).

Why a column, not a `goal_kind` rebuild: no table rebuild, nothing changes for readers that ignore it, and the exclusive CHECK keeps one goal per group. **OPINION:** this is the smallest change that keeps the invariant in the database.

### 3.3 Invariants
- **I1.** A group has at most one goal: exactly one of {`goal_items`; `window_months`; `quantity_base_value`+`quantity_dimension`}, or none (household default). Enforced by CHECKs.
- **I2.** Group Don't restock is `window_months = 0` (legacy `quantity_base_value = 0` reads the same). `goal_items` is never 0.
- **I3.** No `item_targets` row with `value > 0` exists for a product in `product_group_members` of the same household, once 032 has run. Enforced in `Targets.SetItem` (§6.3). The planner also ignores such a row (R7 guard, §5.2).
- **I4.** An item target 0 on a grouped product means Don't restock for that member (unchanged).
- **I5.** `touched = 1` is in effect only while `touched_expires_trip > households.trip_seq` (§7).

### 3.4 Read model of a group goal (RULING)
| Stored | `target.kind` | `value` | `unit` |
|---|---|---|---|
| none | `target: null` (household default, see `defaultMonths`) | | |
| `goal_items = n` | `items` | n | `items` |
| `window_months = n`, n ≥ 1 | `months` | n | `months` |
| `quantity_base_value = b > 0`, dim | `amount` | `DisplayNetSize(b, dim)` (oz/fl oz, 1 decimal, `product/netsize.go:272-288`) | `oz` or `fl oz` |
| `window_months = 0` or `quantity_base_value = 0` | `dont_restock` | 0 | null |

`restock` gains `KindItems Kind = "items"` for group-level resolution only. It is never stored in `item_targets` or `category_targets` (their CHECKs stay). `groupTarget` (`restock/targets.go:1282-1293`) maps `goal_items` to `{items, n}`. The copy for the "items" kind is Frontend + UX (D6 "items").

### 3.5 Per-group replace-what-you-used (R1, RELAYED decision 1)
For a group line with no qualified group rate and a months goal (or no goal):
- `boundary = max(started, latest stock-in of ANY member)`. Stock-ins of Don't restock members count: their stock is on the shelf.
- `replacing = Σ uses with qty > 0 strictly after boundary, over members that are not Don't restock.` **RULING:** uses of Don't restock members stay excluded, as today (`group_plan.go:67-71`). Buying back a product the household said not to restock would be surprising. If PM reads R1 literally ("any member"), it's a one-line change in the same function.
- `planBasis.replacing` and the note "replacing N you used" use this group number (R2). `linePlanViewFromLine` gets the group facts for group lines (F2).
- Items and amount goals never use replace (they are fixed goals, like an item amount today, `service.go:536-543`).
- Ungrouped items are unchanged (`planGroup`/`replaceLine`, `service.go:530-625`).

## 4. Migration `032_group_goals.sql`

### 4.1 Number and runner
- Highest on master is `031_trip_scoped_skips.sql`. `029_firmware_ota.sql` is reserved by open PR #568 (`M:docs/design/firmware-ota.md:363`, `multi-store-sourcing.md:97`), so **032**. The runner applies missing files in name order (`M:internal/app/migrate.go:47-160`), and `TestMigrationThreeDigitPrefixUnique` guards duplicates (`M:migration_prefix_test.go:11-36`). Re-check the highest number when T1 opens its PR.
- Applied with `applyMigrationTx` (add the filename to the list at `M:migrate.go:143`, as 024/027/030/031 do). DDL, data and the `schema_migrations` row commit together or not at all.
- Keep the file in the portable subset (`M:portable_sql_test.go:63-81`): no `datetime()`, `INSERT OR REPLACE`, triggers or `PRAGMA`. The draft uses `ON CONFLICT … DO NOTHING` and `CURRENT_TIMESTAMP`. It passed `TestPortableSQL` in the scratch run.

### 4.2 Data rule (PM rules 1–7; rule 3 is the pending CJ default)
Per household, for each item target with `value > 0` on a grouped product:

| Case | Group goal before | Members' targets > 0 | Result |
|---|---|---|---|
| 1 | any | target 0 | **Kept** (never selected: `value > 0` filter). |
| 2 | has a goal (items, months, amount, or Don't restock 0) | any | Goal kept. Member targets > 0 backed up and deleted. |
| 3 | none | ≥ 1 amount | `goal_items = MAX(amount values)`. All member targets > 0 (amount and months) backed up and deleted. |
| 4 | none | months only | `window_months = MAX(months values)`. Backed up and deleted. |
| 5 | amount goal | n/a | Stored unit unchanged. If it can't be counted, the §5.4 status shows. Nothing is converted. |
| 6 | n/a | n/a | List edits: see §4.5. |

- **Units across members (FACT):** an item `amount` is a count of units, whatever `products.unit_of_measure` says (issue cause D; `supply/service.go:458-471`). So comparing counts needs no conversion, and **no size data is read**. Members with different unit labels or sizes (for example "can" and "oz") are compared as counts. **Never** convert a count into ounces (that is what the old R5 prompt does, `M:frontend/src/components/groups/copy.ts:128-158`).
- **Mixed amount and months members:** amount wins and becomes items (PM rule 3). Note: this is the reverse of `restock.Higher` (`target.go:62-87`, months beat amounts). OPINION: correct here, because an explicit count is the concrete setting.
- **Ties:** equal values give the same goal, so there's nothing to break.
- **No amounts at all:** groups with no member targets > 0 aren't touched.
- **Range:** item amounts are whole 1–999 by CHECK and `Validate`, so `goal_items` fits 1–999. Months are 1–12. The SQL still clamps with `MIN/MAX` as a guard.
- **Case 2 with group Don't restock:** today such a member still buys on its own (the split runs before `groupTargetIsZero`, `service.go:379-384`). After 032 it stops. See §13 Q2.
### 4.3 Forward SQL (draft; ran green in the scratch worktree)
The full file is in Appendix A. In order: (1) DDL §3.2 + backup tables; (2) back up every grouped item target > 0; (3) compute each goal-less group's new goal into `migration_backup_group_goals`; (4) apply `goal_items` / `window_months` only where the group still has no goal; (5) delete the backed-up targets > 0; (6) back up and adjust list edits.

### 4.4 Backup and restore
- **Full backup (gate, Manager/Backend):** run `stockbot-backup run -reason pre-deploy` immediately before deploying 032, and record the checksum and manifest line in the deploy ticket (`M:docs/operations/kroger-legacy-connection-cleanup-runbook.md:23-45`). This is the full rollback: restore the verified artifact, remove stale `-wal`/`-shm`, check `PRAGMA integrity_check` = `ok`, then start the prior binary (`:67`).
- **No new in-volume snapshot (RULING):** 032 does **not** add a `writeSnapshot` `pantry-pre-*.db` copy. That pattern leaves a plaintext full pantry on the SD card that no purge reaches, and MHS §2.5 forbids it for new migrations (`M:docs/design/multi-household-staged.md:203`, `:770`). The pre-deploy R2 backup replaces it.
- **Row-level backup (PM rule 7):** `migration_backup_item_targets` (household, item, product, group, kind, value), `migration_backup_group_goals` (old goal columns + new kind/value), `migration_backup_list_edits` (entry, old `touched`). Each row carries `migration_id = '032'`. They hold ids and numbers only, no names.
  - Add all three to `tenant.HouseholdTables` (`M:internal/tenant/sqlcheck.go:16-71`). `TestHouseholdSchemaTablesRegistered` fails until you do (seen in the scratch run). Household deletion and purge must cover them. They are not wiped by "wipe inventory" (OPINION: the backup outlives a stock wipe on purpose).
  - **Restore without a full rollback:** Appendix B SQL re-inserts backed-up targets where no target exists now, clears a migrated goal only if it still equals the migrated value, and restores `touched`. There's no `sqlite3` on the Pi or in the container (`runbook:33`), so the restore ships as a corrective migration if Manager/CJ ever choose it. It is kept as a tested constant in T1 (`TestMigration032RestoreRoundTrip`). It was run in the scratch worktree: the pre-migration state of goals, targets and `touched` came back byte for byte.
  - **Retention (OPINION):** drop the backup tables in a later migration after one stable release and a verified R2 backup. That is a separate decision, not part of #771.

### 4.5 List edits (PM rule 6)
- Back up every `source='auto' AND touched=1` row.
- **Un-mark where provable:** `touched=1 AND COALESCE(group_id,'')=''`. `SwapAutoLine` refuses lines without a group (F10), so on those lines the only writer was `SetAdjustment` (F9, F11). This is a proof from code, not a guess.
- Other touched rows (group lines) get `touched_expires_trip = trip_seq + 1`: edited this trip. R9 still clears them early (§7). `touched_by_user_id`/`touched_at` stay NULL (unknown), and the UI uses the copy without a name (UX G3).

### 4.6 Idempotency and down path
- The runner records `032` in the same transaction, so a second start applies nothing (verified: state identical after a second `RunMigrations`).
- The DML is written so that re-executing it changes nothing: backup inserts are `ON CONFLICT DO NOTHING`, goals are only set where the group has none, and deletes only target backed-up rows. Verified by re-running the DML part on the migrated DB.
- **Down:** the runner has no down migrations (`migrate.go`). Rollback is §4.4. An older binary on a migrated DB ignores `goal_items` and `touched_expires_trip`: items-goal groups plan at the household window, and touched group lines stay frozen as today. That's degraded but safe, with no crash or data loss. OPINION: acceptable for a rollback window.
- **Re-plan afterward:** reads already re-plan `planBasis` (`W3:21`), but staged rows refresh only on the next coalescer job. **RULING:** T1 adds a one-time post-migration full list refresh per household after `RunMigrations`, guarded by a `household_settings` key (`list_replan_032`). That way the stale solo lines from F3 go away at first start.
- Existing tests that count tables and migrations (`TestMigrationApplies` 62→65 tables, `TestMigrationIsIdempotent` 39→40 rows, `M:migrate_test.go:124,158`) must be updated; both failed as expected in the scratch run.

### 4.7 Scratch verification (evidence, not shipped)
Worktree `/workspace/spec771/probe` (detached at `M:`), with the draft 032 and scratch tests `internal/app/zz771_*_test.go`. The synthetic fixture covers every case: 2 households; mixed unit labels (oz, can, fl oz, lb, ct, bag); sized and unsized products; amount+months mix (→ items 3); months only (→ 5); a tie (2,2 → 2); an existing window, amount and Don't restock goal (kept, members removed); member target 0 kept; an ungrouped target untouched; a second household (→ 7); touched lines with and without a group; a manual line.
```
cd /workspace/spec771/probe
go test ./internal/app -run 'TestZZ771|TestMigrationThreeDigitPrefixUnique|TestPortableSQL' -count=1   # ok
python3 032-mutants.py                                                                                   # 9/9 KILLED
```
Two candidate mutants were dropped as **equivalent**: removing the `household_id` clause from the goal update (group and item ids are global primary keys), and `value >= 0` in the delete while the backup-EXISTS guard stays.

## 5. Planner (`internal/supply`, `internal/group`)

### 5.1 R1/R2: group buy-back
As §3.5: one group boundary in `planProductGroup` (`group_plan.go:62-82`) replacing the per-member `unreplacedQty` loop, and group facts for `planBasis` on group lines.

### 5.2 R7: never split
`splitResolvedGroup` (`service.go:435-447`): a grouped member with an item target > 0 stays pooled and that target is **ignored**. A target of 0 still sets `NoRestock`. `openingAmountLines` (`:413-433`) no longer plans grouped members from item amounts.
**RULING:** during opening (wipe), an **items** goal plans like an item amount does today (it is a fixed count and needs no rate). Months and amount goals don't plan during opening, as today.

### 5.3 R11: items goal
`group.BuyTarget` gains `Items int`. `buy = max(0, Items − Σ OnHand over all members, including Don't restock)`, bought as the picked member. No note text beyond the pick reason. No sizes or rate needed, and no replace fallback.

### 5.4 R12–R14: amount goals that work or say why (UX A6, A7)
Define the **counted set** for an amount goal or for `amountAvailable`:
- every member not on Don't restock, **plus** Don't restock members with on hand > 0 (UX A7). **This narrows PM R13 and needs PM sign-off (§13 Q3).** If PM rejects A7, the counted set is the non–Don't restock members, and an unsized Don't restock member's stock is left out of the total, which overbuys.

`amountAvailable = counted set non-empty AND every member has net_base_value > 0 AND all share one dimension`. `amountUnit` = `oz` (mass) / `fl oz` (volume) / null.

For a **saved** amount goal: `status = ok` when every counted member is sized in the goal's dimension. Otherwise `size_missing` if any counted member has no size (even when a mismatch also exists), else `units_mismatch`. Then:
- The planner emits **no line** and records `PlanExcluded{LineKey: group:G, Reason: "group_goal_size_missing" | "group_goal_units_mismatch", Blockers: […productId]}`. There's no hidden fallback to counting items (UX A6 agreed). This replaces the silent `buyQuantity` return (F8).
- `buyQuantity` counts only the counted set. Members outside it (Don't restock, none on hand) add 0.
- **Wire:** the existing `PlanView[...].Excluded` (`planview.go:105-125`) carries the reason, and `offList[]` shows it as the row's reason (W3 "Not on the list"). For Needs attention, emitting `not_planned` with these two details **amends #686 §5** (closed set today: `no_product_picked`, `#686:194-206`). See §13 Q6.

### 5.5 Unit handling (R12, A3)
- The server derives the unit. `inferDimension` stops accepting a client dimension unchecked and **never falls back to mass**. With no derivable unit, a request for an amount is 422 `amount_unavailable`.
- A legacy `{quantity}` (always ounces, F6) with no derivable unit → 422 `amount_unavailable`. A legacy `{quantity, dimension}` whose dimension contradicts the members → 422 `units_mismatch`. **A total or a legacy ounces amount never becomes an items goal** (UX A3 agreed). Counts become items only in the migration, and only from member counts.
- Storage stays grams/ml (`BaseFromAmount`, `netsize.go:259-268`), and display stays oz/fl oz at 1 decimal.
- `usage` (A14): for an items goal, `{perMonth, unit:"items"}` from the item-count rate (`itemEvents` + `derive`, `group_plan.go:157-160,190-200`). For an amount goal, today's oz/fl oz rate (`usageFromMembers`, `:137-150`). Otherwise null.
## 6. API contract

### 6.1 `GET /api/groups/{id}` and `GET /api/groups` (additive)
Existing fields stay (`group.Group`, `M:groups.go:69-84`; `groupView` `handler_group.go:164-167`), including legacy `windowMonths`/`quantity`/`dimension` until the web client migrates. New fields are in §8.1.

### 6.2 `PUT /api/groups/{id}/target` (RULING)
Request, one of:
```jsonc
{"kind":"items","value":2}            // whole 1-999
{"kind":"months","value":3}           // whole 1-12
{"kind":"amount","value":24}          // >0-999, at most 1 decimal; unit derived
{"dontRestock":true}                  // group Don't restock (stores window_months 0)
{"clear":true}                        // household default
// any of the above may add "expectedVersion":"<version from GET>"
// legacy, accepted during transition:
{"windowMonths":0..12} {"quantity":24} {"quantity":24,"dimension":"mass|volume"}
```
- Exactly one of {kind+value, dontRestock, clear, a legacy shape}; else 422 `invalid`. `{"kind":"months","value":0}` is **not** accepted; use `dontRestock` (UX A11). Legacy `{"windowMonths":0}` maps to `dontRestock`, so `StopDialog` and `GroupDetailPage.tsx:484` keep working.
- A client `unit` field is ignored. A client `dimension` is checked against the derived unit.
- Writing a goal dismisses the migration notice for that group (§8.1).
- Response **200**: the full group (§8.1), so the client doesn't need a second GET. Triggers `syncList` as today (`handler_group.go:279-281`).

| Status | `code` | When | Body extras |
|---|---|---|---|
| 200 | | saved | full group |
| 404 | `not_found` | group not in this household | |
| 409 | `conflict` | `expectedVersion` ≠ current `version` | |
| 422 | `invalid` | wrong shape, kind, range, or decimals | `field` (`kind`/`value`/`body`) |
| 422 | `amount_unavailable` | `kind:"amount"` (or legacy quantity) and `amountAvailable` is false | `blockers[]` |
| 422 | `units_mismatch` | legacy `dimension` contradicts the derived unit | `blockers[]` |
| 503 | `busy`/`no_space` | as `docs/api/errors.md` | |

**RULING on the envelope:** extras go in the **existing top-level envelope** (`errorPayload`, `handler_wrapper.go:46-56`): `field` already exists, and `blockers` is a new top-level `omitempty` field. Not under `details` (no `details` key exists today). The new 422s apply to this endpoint only. Other group endpoints keep 400 `validation` (F16).

### 6.3 `in_group` on item targets (R3/R4)
- **Where:** in `restock.Targets.SetItem` (`targets.go:354`). If `target.Value > 0` and the item's product is in `product_group_members` for this household, return a typed `*restock.InGroupError{GroupID, GroupName}` before writing. This covers every current and future caller (F4, F5, §2 list). `refuseGroupedOverride` stays for `GET supply-override`.
- **Wire (existing shape, plus one field):** 409 `{"error":"This product is in a group. Change what the group keeps on hand.","code":"in_group","members":{"groupId":"g1","groupName":"…"}}`. Nothing is written, and there's no list sync.
- **Allowed:** value 0 (`PUT /api/restock/items/{id}` with `{"kind":"months","value":0}` or amount 0), `POST /api/restock/items/{id}/dont-restock`, `DELETE /api/restock/items/{id}`, and `PUT /api/groups/{id}/members/{productId}/restock`. The undo of Don't restock (`ShoppingListPage.tsx:556`, `InventoryPage.tsx:306`) re-sends `previous`, which for a grouped product is null or 0 after 032, so it never trips the guard.
- **Batch writers (UX A5):** none write `item_targets` on master (F13), so there is no batch to skip items in. If a future batch writer is added, it must skip grouped items one at a time and report them. This contract sets that rule now.
- **Membership changes:** `AddMembers`/`Create`/`Accept` keep `requireTargetOrNone` → 409 `target_decision_required` + `deleteOverrides` (R5, `groups.go:888-975,1067-1169`). For UX A12, `OverrideNotice` gains `items` (the member's count). The 409 adds top-level `amountAvailable` and `amountUnit` for the combined members, and the client offers ounces only when that is true. The ounce conversion (`Amount`/`Dimension` on the notice) is kept for old clients but the new prompt doesn't use it. A choice of `{kind:"items",value:n}` is accepted as the join `target`.
- `RemoveMember` keeps a Don't restock 0 on the removed product, as today (`groups.go:397-430`). See §13 Q5.

### 6.4 Trip-scoped list edits
State lives on `shopping_list_items` (§3.2). Backend owns it.
- **Writes:** `SwapAutoLine` sets `touched=1, touched_expires_trip = trip_seq+1, touched_by_user_id = principal, touched_at = now` in its transaction. `SetAdjustment` **no longer sets `touched`** (R10). The adjustment rides on the line and is deleted with it. #683 zero-for-trip is unchanged. `PATCH …/items/{id}` is not an edit (F11).
- **Active predicate:** `touched = 1 AND touched_expires_trip > (SELECT trip_seq FROM households WHERE id = household_id)`. Use it in `listTouchedAutoItems` (`auto_items.go:225-252`), `deleteUnplannedAuto` (`:373`), scoped save (`scoped_save.go:68,182,219-225,250-253,304-347`) and `batchTouched` (`server/list_assemble.go:336-352`). As with skips (`trip.go:52-62`), the trip advance needs no purge. OPINION: also reset `touched=0` on expired rows inside `PurgeExpiredSkips`'s transaction, to keep rows tidy.
- **R9 (RULING):** a touched auto row whose key (`item:` or `group:`) is **not** planned is deleted on the next fill, like an untouched one. A touched row whose key **is** planned keeps its `item_id` (the swap). **Its quantity and note refresh from the plan**, because the only edit that exists is the product choice. OPINION: if a quantity edit is added later, it would freeze quantity only.
- **Wire (list items, additive):** keep `touched` (meaning now "edited this trip"). Add `edited: {actorUserId: string|null, actorDisplayName: string|null, at: string|null} | null`, reusing the `OffListZeroForTrip` actor pattern and `resolveActorDisplayNames` (`handler_shopping_list.go:140-146`, `list_assemble_zero.go:228-250`). No new endpoint: an edit is made through `POST …/items/{id}/swap` and ends at the trip boundary or when the line is no longer planned.

### 6.5 Other endpoints
- `GET /api/restock` / item views: for a grouped item, `level:"group"` and the group target (including `items`). **OPINION (§13 Q4):** grouped items resolve group → household and skip categories, to match the planner (F14).
- `POST /api/groups/{id}/migration-notice/dismiss` → 200 full group. Stores `household_settings` key `group_goal_notice_dismissed:<groupId>` (no schema). Only if CJ chooses to show the note (UX D5).
- No role gate is added: groups and restock have none today (UX §1, `server.go:540-578`). No `canEdit`, and no 403 `forbidden_role` (UX A9, option "keep it that way").

### 6.6 Ownership
| Piece | Owner |
|---|---|
| 032, backup tables, tenant registry, post-migration refresh | Backend |
| `in_group` guard, envelope fields, join-prompt extras | Backend |
| Planner R1/R2/R7/R11/R14, `PlanExcluded` reasons, `planBasis` | Backend |
| Group read model §8.1, PUT §6.2, version, notice endpoint | Backend |
| Edit lifetime §6.4 + `edited` wire | Backend |
| `shared/types.ts` additions | Backend writes them in T1; Frontend consumes |
| Group editor, grouped-product settings, 409 inline, status card, edited tag, join prompt | Frontend (web). Mobile: none (F17, UX D7) |
| Copy (`{AMOUNT_UNAVAILABLE_COPY}` etc.) | UX → CJ |

## 7. Edit lifetime (R8–R10) summary
| Event | `touched` | Line |
|---|---|---|
| Swap a group line | 1, expires next trip, actor and time recorded | keeps product; qty/note refresh |
| Set/clear per-store quantity | unchanged (0) | refreshes normally; adjustment deleted with the line |
| Plan no longer needs the key | ignored | deleted on next fill |
| Trip advances (new cart / put-away) | inactive by predicate | planner owns it again |
| Mark purchased (PATCH) | unchanged | existing bought-line rules |
## 8. Editor contract (for UX to confirm; send this section)

### 8.1 Read: `GET /api/groups/{id}`, and the same body from `PUT …/target`, `PUT …/rule`, member writes and notice dismiss
| Field | Type | Notes |
|---|---|---|
| `id`, `name`, `rule`, `ruleConfirmed`, `pinnedProductId?`, `runningLow` | existing | unchanged |
| `members[]` | `{productId, name, onHand, noRestock?, sizeKind, size}` | `sizeKind`: `"weight"\|"volume"\|null` (mass→weight). `size`: `{amount:number, unit:"oz"\|"fl oz"}\|null` (per unit, display only) |
| `target` | object \| null | **null = no goal, household default applies** |
| `target.kind` | `"items"\|"months"\|"amount"\|"dont_restock"` | `dont_restock` is new; UX's list had 3 kinds (A11) |
| `target.value` | number | items int 1–999; months int 1–12; amount 0.1–999 (1 decimal); dont_restock 0 |
| `target.unit` | `"items"\|"months"\|"oz"\|"fl oz"\|null` | display only; null for dont_restock |
| `target.status` | `"ok"\|"size_missing"\|"units_mismatch"` | always `ok` for items, months, dont_restock |
| `target.blockers[]` | `{productId, name, reason:"size_missing"\|"units_mismatch", sizeKind}` | saved-goal problems; no-size first, then member order. For a saved goal, mismatch rows are **members sized the other way from the goal** (not "the smaller set") |
| `amountAvailable` | boolean | §5.4 counted set; false with no members |
| `amountUnit` | `"oz"\|"fl oz"\|null` | null when unavailable |
| `amountBlockers[]` | same shape | why a total can't be picked now. Mixed sizes: the **smaller set** (volume on a tie), per UX §3.4. Both problems: no-size rows, then mismatch rows |
| `defaultMonths` | int 0–12 | `readMonths` (F15): the household months; 3 (`DefaultMonths`) when the household default is an amount; 0 when it's Don't restock |
| `usage` | `{perMonth:number, unit:"oz"\|"fl oz"\|"items"}\|null` | items unit only for items goals (A14) |
| `version` | string | opaque hash of the goal columns; changes only when the goal changes |
| `migrationNotice` | `{case:2\|3\|4, products:[{productId,name}]}\|null` | from 032 backup rows; null once dismissed or after any goal write; only if D5 = show |
| legacy `windowMonths`, `quantity`, `dimension` | existing | kept until the web client migrates; the new editor doesn't read them |

### 8.2 Write: `PUT /api/groups/{id}/target`
Body as §6.2. The client never sends a unit. `expectedVersion` is optional.

### 8.3 Errors the editor tells apart
| Status | `code` | Extras (top-level) |
|---|---|---|
| 422 | `invalid` | `field` |
| 422 | `amount_unavailable` | `blockers[]` (§8.1 shape) |
| 422 | `units_mismatch` | `blockers[]` |
| 409 | `conflict` | — |
| 409 | `in_group` (item-target writes) | `members.groupId`, `members.groupName` |
| 404 | `not_found` | — |
| 503 | `busy` / `no_space` | — |
| network / other 5xx | — | — |
There is no 403 (no role gate).

### 8.4 States → responses
| State | Driven by |
|---|---|
| Ideal | 200, `target` set, `status:"ok"` |
| Empty | 200, `target:null` (show `defaultMonths`), or `members:[]` (`amountAvailable:false`, `amountBlockers:[]`) |
| Loading | GET/PUT in flight; PUT returns the full group |
| Edge | `amountAvailable:false` + `amountBlockers`; `status` `size_missing`/`units_mismatch` + `target.blockers`; `usage:null`; every member `noRestock`; `target.kind:"dont_restock"`; `migrationNotice` set |
| Error | §8.3 |

### 8.5 Other surfaces UX needs
- List line: `touched` (edited this trip) + `edited {actorUserId, actorDisplayName, at}|null`. A per-store quantity never sets them.
- Not on the list: `group:G` off-list row with reason `group_goal_size_missing` / `group_goal_units_mismatch` and the blocker products (needs the W3 renderer).
- Join prompt (409 `target_decision_required`): `members[].quantity` (count) → choice `{kind:"items",value:n}`; top-level `amountAvailable`, `amountUnit` for the combined members.
- Grouped product settings: `GET /api/restock` item view `level:"group"`, `groupId`, `groupName`, target including `items`.

### 8.6 UX to confirm
1. **UX to confirm:** `target.kind:"dont_restock"` on read and `{"dontRestock":true}` on write (A11).
2. **UX to confirm:** error extras at the top level (`field`, `blockers`, `members.groupId/groupName`), not `details.*`.
3. **UX to confirm:** saved-goal mismatch blockers list members sized the other way from the goal, while `amountBlockers` lists the smaller set.
4. **UX to confirm:** `defaultMonths` is 0 when the household is Don't restock and 3 when the household default is an amount, and the copy for those two cases.
5. **UX to confirm:** no `canEdit` and no 403.
6. **UX to confirm:** a swapped line's quantity follows the plan while its product stays (§6.4 R9).
7. **UX to confirm:** suggestions apply needs no skip behavior, because it doesn't write targets (F13).
8. **UX to confirm:** an items goal plans during opening (§5.2), and a months or amount goal doesn't.
9. **UX to confirm:** the `edited` object shape replaces UX's `{trip, byName}` proposal (no `trip` number; it's always "this trip").

## 9. Test strategy (automated; SDET GO on strength)

**Backend (Go):**
| AC | Test | Mutants (risky logic: exact commands required) |
|---|---|---|
| B1 R1/R2 | `supply` table test: 2 members, no pace; use X, stock in Y → no line; use X again → `replacing 1`, `planBasis.replacing=1`; stock in a Don't restock member also clears; ungrouped golden unchanged | per-member boundary; drop NoRestock stock-ins from the boundary |
| B2 R3/R4 | `restock` store test + `server` HTTP table over **every** writer: `PUT restock/items` (amount>0, months>0 → 409 `in_group`, `members.groupId/groupName`, row unchanged), value 0, dont-restock, DELETE, supply-override PUT, member restock → success; a non-grouped item → success | **required**: guard off; `>0`→`>=0`; household-unscoped membership lookup; guard only in the handler (a store-level test catches it) |
| B3 R7 | seeded target >0 on a member → one group line, no solo line; opening wipe doesn't plan it solo | restore split |
| B4 R8–R10 | per-store qty → `touched` stays 0, line refreshes and is removed when stock covers it; swap → touched + actor; stock covers → removed this trip; trip advance → planner owns it; still needed → product kept, qty refreshed | **required**: predicate `>`→`>=`; SetAdjustment sets touched |
| B5 R11 | items goal 2, on hand 1+0 → buy 1; 3 → no line; Don't restock member's stock counts | |
| B6 R12/R13 | first amount on all-volume group, no client dimension → volume; no sizes → 422 `amount_unavailable` (never mass); legacy `{quantity}` unsized → 422; conflicting `dimension` → 422 `units_mismatch`; ranges/decimals → 422 `invalid` + `field`; `{kind:months,value:0}` → 422; `dontRestock` ok; `expectedVersion` stale → 409 | mass fallback restored |
| B7 R14 | amount goal + unsized member added → `status:size_missing`, `PlanExcluded` reason + blocker, no line; mixed → `units_mismatch`; both → `size_missing` with both rows; A7 cases | buy-0 silently (drop the exclusion append) |
| B8 migration | `internal/app/migrate_032_test.go`: the §4.7 fixture as a golden (each case row), DML re-run no-op, second start no-op, CHECKs both ways, rollback on a failure after the last statement (copy `migrate_031_test.go` pattern), restore round-trip (Appendix B), `TestMigrationApplies`/`IsIdempotent` counts, `HouseholdTables` | **required**: `docs/specs/771-mutants/032-mutants.py` (Appendix C), run `python3 docs/specs/771-mutants/032-mutants.py` from the repo root; all mutants KILLED, exit 0 |
| B9 | one synthetic regression per reported item shape (group replace; target split; count-labelled-as-oz; touched freeze; unsized amount goal), no household data | |

Required mutant commands for the `in_group` guard (T1 adds `docs/specs/771-mutants/guard-mutants.sh` with the same structure as Appendix C): run `bash docs/specs/771-mutants/guard-mutants.sh`, which applies each mutant to `internal/restock/targets.go`, runs `go test ./internal/restock ./internal/server -run 'InGroup|TestRestock' -count=1`, and must print KILLED for every mutant and exit 0.

**Frontend (web, Vitest + one e2e):** F1 grouped settings show the group goal + link, no amount/months input; a forced 409 `in_group` shows inline with "Open group settings". F2 three kinds + `dontRestock`; the PUT body never has `unit`/`dimension`; amount disabled with blockers; the label comes from `amountUnit`. F3 status card for both statuses, with names and fixes. F4 a per-store quantity never shows the edited tag; a swap does, with/without a name. F5 mobile: none (F17), so nothing to add. E2E: open group, set items 2, save; the list asks for 1 with 1 on hand.

## 10. Copy/data hygiene
No household data in code, tests or docs: synthetic product ids only. The reporting household's one-time correction (PM "Reporting household") runs **after** 032 through the normal product-edit and group-goal APIs, owned by the Manager's chosen owner with CJ's approval. No SQL against the Pi DB from #771 work.

## 11. Rollback
§4.4 (full: pre-deploy R2; row-level: Appendix B via a corrective migration). Code rollback without data rollback is safe-degraded (§4.6).

## 12. Tickets (contract first; few and large; no estimates)
| # | Ticket | Owner | Depends on | Done when |
|---|---|---|---|---|
| T0 | This docs PR | Architect | UX confirms §8; PM signs off Q3; CJ: migration default + copy (via Manager/PM) | merged |
| T1 | Backend: 032 + backup tables + tenant registry + post-migration refresh; `in_group` guard; group read model + PUT + version + notice; planner R1/R2/R7/R11/R14; edit lifetime + `edited`; `shared/types.ts`; B1–B9 + mutants | Backend | T0 | SDET GO, Architect GO (migration, API), green CI |
| T2 | Frontend web: editor (3 kinds + Don't restock, server unit, blockers, status card, version), grouped-product settings + 409 inline, edited tag, join prompt items choice, Seesaw stops sending `dimension`; F1–F5 + e2e | Frontend | T1 merged (or T1's types on master) | UX GO, SDET GO, green CI |
| T3 | Reporting household data correction (after T1 deploy, with the pre-deploy R2 backup recorded) | Manager's chosen owner | T1 deployed, CJ approval (already given per PM) | values recorded privately with Manager |

## 13. Open questions for Manager/CJ (one at a time; each with a recommendation)
- **Q1 (CJ, pending): migration default.** (a) Items = largest member count. (b) Items = sum of member counts. (c) Remove member targets and leave the group on the household default. (d) Amount in oz/fl oz when every member is sized alike, else (a). **Recommend (a)**: no size data, no double-buying, honors the most generous explicit setting. (b) double-buys interchangeable products, (c) drops what people set, and (d) creates goals that can break when sizes change. The schema and API are the same for all four; only §4.3 step 3 changes.
- **Q2 (Manager→CJ): group Don't restock plus a member with its own amount.** Today that member still buys; after 032 it stops. (a) Keep the group's Don't restock and remove the member amount (PM rule 2), shown in the one-time notice. (b) Replace the group's Don't restock with items = the member's count. (c) Report and skip these groups, leaving them for manual fix. **Recommend (a)** (OPINION), because group Don't restock is the more deliberate setting; the notice tells the household.
- **Q3 (PM): unsized Don't restock members with stock (UX A7).** (a) They block a total while they have stock (UX). (b) R13 literal: they never block, and their stock is left out of the total, which overbuys. (c) They never block, and their stock counts as items, which can't be added to ounces, so this isn't workable. **Recommend (a).**
- **Q4 (Manager): category targets on grouped products (F14).** (a) Resolve grouped items as group → household and skip categories, to match the planner. (b) Leave as is. **Recommend (a)**: a small change in T1 that removes a second source of "why is this here".
- **Q5 (PM): Don't restock on a product removed from a group.** (a) Keep the 0, as today. (b) Clear it so the product inherits (R5 wording). **Recommend (a)**: it is the household's explicit choice.
- **Q6 (Manager): blocked amount goals in Needs attention.** (a) Emit `not_planned` with details `group_goal_size_missing`/`group_goal_units_mismatch` (amends #686 §5; needs a copy row). (b) Show them only in Not on the list + group page. **Recommend (b) for #771**, with (a) as a follow-up, so #686's closed set and its tests don't move inside this PR.

## 14. Out of scope
Per PM: solo replace ignoring on-hand (A), pace rule gap (B), duplicate bought line (H), 3-month window (I), detecting bad sizes (N3), per-store quantities lasting beyond a trip, changing Remove or target 0. Also: dropping the 032 backup tables; any role gate.
## Appendix A: `032_group_goals.sql` (draft)
```sql
-- #771: one goal per group, item-count group goals, one-trip list edits.
-- Applied in one transaction (applyMigrationTx). No in-volume snapshot
-- (multi-household-staged.md 2.5); the pre-deploy R2 backup is the full
-- rollback, and the two backup tables below are the row-level undo.

-- A group goal is exactly one of: window_months, quantity_base_value +
-- quantity_dimension, or goal_items. goal_items counts units on hand
-- across all members.
ALTER TABLE product_groups ADD COLUMN goal_items INTEGER
    CHECK (goal_items IS NULL OR (
        goal_items BETWEEN 1 AND 999
        AND window_months IS NULL
        AND quantity_base_value IS NULL
        AND quantity_dimension IS NULL));

-- An edit (touched = 1) lasts while touched_expires_trip > households.trip_seq.
-- touched_by_user_id / touched_at say who made the edit and when (UX A8).
ALTER TABLE shopping_list_items ADD COLUMN touched_expires_trip INTEGER;
ALTER TABLE shopping_list_items ADD COLUMN touched_by_user_id TEXT;
ALTER TABLE shopping_list_items ADD COLUMN touched_at TEXT;

CREATE TABLE IF NOT EXISTS migration_backup_item_targets (
    migration_id TEXT NOT NULL,
    household_id TEXT NOT NULL,
    item_id      TEXT NOT NULL,
    product_id   TEXT NOT NULL,
    group_id     TEXT NOT NULL,
    kind         TEXT NOT NULL,
    value        REAL NOT NULL,
    backed_up_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (migration_id, household_id, item_id)
);

CREATE TABLE IF NOT EXISTS migration_backup_group_goals (
    migration_id        TEXT NOT NULL,
    household_id        TEXT NOT NULL,
    group_id            TEXT NOT NULL,
    old_window_months   INTEGER,
    old_quantity_base   REAL,
    old_quantity_dim    TEXT,
    old_goal_items      INTEGER,
    new_kind            TEXT NOT NULL CHECK (new_kind IN ('items', 'months')),
    new_value           INTEGER NOT NULL,
    backed_up_at        TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (migration_id, household_id, group_id)
);

CREATE TABLE IF NOT EXISTS migration_backup_list_edits (
    migration_id TEXT NOT NULL,
    household_id TEXT NOT NULL,
    entry_id     TEXT NOT NULL,
    old_touched  INTEGER NOT NULL,
    backed_up_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (migration_id, household_id, entry_id)
);

-- DML
-- 1. Back up every item target above 0 on a grouped product (rule 7).
INSERT INTO migration_backup_item_targets (migration_id, household_id, item_id, product_id, group_id, kind, value)
SELECT '032', t.household_id, t.item_id, i.product_id, m.group_id, t.kind, t.value
FROM item_targets t
JOIN items i ON i.id = t.item_id AND i.household_id = t.household_id
JOIN product_group_members m ON m.product_id = i.product_id AND m.household_id = i.household_id
WHERE t.value > 0
ON CONFLICT (migration_id, household_id, item_id) DO NOTHING;

-- 2. Decide the goal for groups with no goal (rules 3 and 4).
-- Any amount member: items = largest amount. Months only: months = largest.
INSERT INTO migration_backup_group_goals (
    migration_id, household_id, group_id,
    old_window_months, old_quantity_base, old_quantity_dim, old_goal_items,
    new_kind, new_value)
SELECT '032', g.household_id, g.id,
       g.window_months, g.quantity_base_value, g.quantity_dimension, g.goal_items,
       CASE WHEN SUM(CASE WHEN b.kind = 'amount' THEN 1 ELSE 0 END) > 0 THEN 'items' ELSE 'months' END,
       CAST(ROUND(CASE WHEN SUM(CASE WHEN b.kind = 'amount' THEN 1 ELSE 0 END) > 0
            THEN MAX(CASE WHEN b.kind = 'amount' THEN b.value END)
            ELSE MAX(CASE WHEN b.kind = 'months' THEN b.value END) END) AS INTEGER)
FROM migration_backup_item_targets b
JOIN product_groups g ON g.id = b.group_id AND g.household_id = b.household_id
WHERE b.migration_id = '032'
  AND g.window_months IS NULL AND g.quantity_base_value IS NULL AND g.goal_items IS NULL
GROUP BY g.household_id, g.id
ON CONFLICT (migration_id, household_id, group_id) DO NOTHING;

-- 3. Apply those goals. Only a group that still has no goal is changed.
UPDATE product_groups
SET goal_items = (
    SELECT MIN(999, MAX(1, bg.new_value)) FROM migration_backup_group_goals bg
    WHERE bg.migration_id = '032' AND bg.household_id = product_groups.household_id
      AND bg.group_id = product_groups.id AND bg.new_kind = 'items')
WHERE window_months IS NULL AND quantity_base_value IS NULL AND goal_items IS NULL
  AND EXISTS (
    SELECT 1 FROM migration_backup_group_goals bg
    WHERE bg.migration_id = '032' AND bg.household_id = product_groups.household_id
      AND bg.group_id = product_groups.id AND bg.new_kind = 'items');

UPDATE product_groups
SET window_months = (
    SELECT MIN(12, MAX(1, bg.new_value)) FROM migration_backup_group_goals bg
    WHERE bg.migration_id = '032' AND bg.household_id = product_groups.household_id
      AND bg.group_id = product_groups.id AND bg.new_kind = 'months')
WHERE window_months IS NULL AND quantity_base_value IS NULL AND goal_items IS NULL
  AND EXISTS (
    SELECT 1 FROM migration_backup_group_goals bg
    WHERE bg.migration_id = '032' AND bg.household_id = product_groups.household_id
      AND bg.group_id = product_groups.id AND bg.new_kind = 'months');

-- 4. Remove member targets above 0 that were backed up (rules 2-4).
-- Item target 0 (don't restock) is never touched (rule 1).
DELETE FROM item_targets
WHERE value > 0
  AND EXISTS (
    SELECT 1 FROM migration_backup_item_targets b
    WHERE b.migration_id = '032' AND b.household_id = item_targets.household_id
      AND b.item_id = item_targets.item_id);

-- 5. List edits (rule 6). Back up, then un-mark lines whose only possible
-- edit was a per-store quantity: SwapAutoLine refuses a line with no group.
INSERT INTO migration_backup_list_edits (migration_id, household_id, entry_id, old_touched)
SELECT '032', household_id, id, touched FROM shopping_list_items
WHERE source = 'auto' AND touched = 1
ON CONFLICT (migration_id, household_id, entry_id) DO NOTHING;

UPDATE shopping_list_items SET touched = 0
WHERE source = 'auto' AND touched = 1 AND COALESCE(group_id, '') = '';

UPDATE shopping_list_items
SET touched_expires_trip = (SELECT h.trip_seq + 1 FROM households h WHERE h.id = shopping_list_items.household_id)
WHERE source = 'auto' AND touched = 1 AND touched_expires_trip IS NULL;
```

## Appendix B: row-level restore (tested constant, not shipped as a migration)
```sql
-- Row-level undo of 032 data changes. Not shipped as a migration unless
-- Manager/CJ choose to; kept as a tested constant. Skips rows a person has
-- changed since 032 ran.
INSERT INTO item_targets (household_id, item_id, kind, value)
SELECT b.household_id, b.item_id, b.kind, b.value
FROM migration_backup_item_targets b
WHERE b.migration_id = '032'
  AND EXISTS (SELECT 1 FROM items i WHERE i.id = b.item_id AND i.household_id = b.household_id)
ON CONFLICT (household_id, item_id) DO NOTHING;

UPDATE product_groups
SET goal_items = NULL, window_months = NULL
WHERE EXISTS (
    SELECT 1 FROM migration_backup_group_goals bg
    WHERE bg.migration_id = '032' AND bg.household_id = product_groups.household_id
      AND bg.group_id = product_groups.id
      AND ((bg.new_kind = 'items' AND product_groups.goal_items = bg.new_value AND product_groups.window_months IS NULL)
        OR (bg.new_kind = 'months' AND product_groups.window_months = bg.new_value AND product_groups.goal_items IS NULL))
      AND product_groups.quantity_base_value IS NULL);

UPDATE shopping_list_items
SET touched = 1
WHERE touched = 0 AND EXISTS (
    SELECT 1 FROM migration_backup_list_edits bl
    WHERE bl.migration_id = '032' AND bl.household_id = shopping_list_items.household_id
      AND bl.entry_id = shopping_list_items.id AND bl.old_touched = 1);
```

## Appendix C: `032-mutants.py`
```python
#!/usr/bin/env python3
"""Mutant check for 032_group_goals.sql. Run from the repo root:
   python3 docs/specs/771-mutants/032-mutants.py
Each mutant edits the migration text, runs the 771 migration tests, and must FAIL."""
import pathlib, subprocess, sys
SQL = pathlib.Path("internal/app/migrations/032_group_goals.sql")
CMD = ["go", "test", "./internal/app", "-run", "TestMigration032", "-count=1"]  # scratch run used TestZZ771
MUTANTS = [
    ("largest->smallest amount", "THEN MAX(CASE WHEN b.kind = 'amount'", "THEN MIN(CASE WHEN b.kind = 'amount'"),
    ("largest->smallest months", "ELSE MAX(CASE WHEN b.kind = 'months'", "ELSE MIN(CASE WHEN b.kind = 'months'"),
    ("months beats amount", "> 0 THEN 'items' ELSE 'months' END", "> 0 THEN 'months' ELSE 'items' END"),
    ("back up don't-restock 0 too", "WHERE t.value > 0\nON CONFLICT", "WHERE t.value >= 0\nON CONFLICT"),
    ("delete without the backup check", "DELETE FROM item_targets\nWHERE value > 0\n  AND EXISTS", "DELETE FROM item_targets\nWHERE value > 0\n  AND 1 OR EXISTS"),
    ("delete don't-restock 0 too (no backup check)", "DELETE FROM item_targets\nWHERE value > 0\n  AND EXISTS", "DELETE FROM item_targets\nWHERE value >= 0\n  AND 1 OR EXISTS"),
    ("overwrite an existing goal", "WHERE b.migration_id = '032'\n  AND g.window_months IS NULL AND g.quantity_base_value IS NULL AND g.goal_items IS NULL", "WHERE b.migration_id = '032'"),
    ("un-mark group lines too", "AND COALESCE(group_id, '') = ''", ""),
    ("no edit lifetime", "SET touched_expires_trip = (SELECT h.trip_seq + 1", "SET touched_expires_trip = (SELECT h.trip_seq"),
]
orig = SQL.read_text()
survived = []
try:
    for name, old, new in MUTANTS:
        if old not in orig:
            sys.exit(f"mutant anchor missing: {name}")
        SQL.write_text(orig.replace(old, new, 1))
        r = subprocess.run(CMD, capture_output=True, text=True)
        status = "KILLED" if r.returncode != 0 else "SURVIVED"
        print(f"{status}: {name}")
        if r.returncode == 0:
            survived.append(name)
finally:
    SQL.write_text(orig)
sys.exit(1 if survived else 0)
```
