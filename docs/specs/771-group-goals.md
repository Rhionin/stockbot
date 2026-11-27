# #771: one goal per group, item-count goals, one-trip list edits

Status: contract, docs-only, **rev 3 (final)**: UX CONFIRMED §8 rev 2; PM ruled every §13 item (2026-11-27). Rev 3 tightens §9 tests after SDET's docs NO-GO at `63851098`; no ruling and no contract shape changed. Intended path: `docs/specs/771-group-goals.md`. Owner of the build: Backend (§12 T1), then Frontend (§12 T2).
Issue: [#771](https://github.com/Rhionin/stockbot/issues/771). Requirements: PM rev 1, [issuecomment-7224912048](https://github.com/Rhionin/stockbot/issues/771#issuecomment-7224912048) (R1–R15, B1–B9, F1–F5, migration rules 1–7), cited as `PM Rn`.

Pinned sources:
- master `9adc206e0814e3e0a785466b2d3ce1f5f4bbe9ac` (cited as `M:`). All file:line references are at this commit.
- UX spec `771-group-goal-ux-spec.md` **rev 2** (sha256 prefix `a71bad6d`, verified), cited as `UX §n` / `UX An`. Its §12 is the contract as agreed, and §15 holds UX's answers; §8 below matches both.
- Copy `771-copy-for-cj.md` **rev 2** (sha256 prefix `2222438c`), approved by CJ 2026-11-27 11:22 AM MST, relayed by PM. Copy ids (D2a–f, E10–E12, G1–G3 …) are cited from it; this contract adds no strings.
- `docs/specs/686-needs-attention-reasons.md` (cited as `#686`), `docs/design/571-w3-replan-contract.md` (cited as `W3`), `docs/design/multi-household-staged.md` (cited as `MHS`).

Labels: **FACT** = read in code at `M:`. **RULING** = Architect decision within technical authority. **OPINION** = recommendation the owner may push back on with reasons. **RELAYED** = CJ decision relayed by PM as CJ's own words in PM's chat; not independently verified by Architect (CJ's words are not on #771).

CJ decisions this contract builds on (RELAYED by PM: CJ's own words in PM's chat, 2026-11-26, about 4:53–4:57 PM MST; not independently verified by Architect):
1. **D1:** a group is one shelf: replace-what-you-used is counted per group, not per product.
2. **D2 (option C of PM's decision note):** each group has one goal; inside a group a product's own setting is only Don't restock. (Options A, keep per-product amounts, and B, count the group's stock toward a product's amount, were not chosen.)
3. **D3:** a list edit lasts only this trip and clears early when the plan no longer needs the line.
4. **D4:** group goals always work or say why (items, months or amount with a server-derived unit; named blockers, no silent failure).
5. **Migration default (§13 Q1):** items = the largest member count, with **no one-time notice**. RELAYED by PM: CJ's own words in PM's chat, 2026-11-27 11:15 AM MST ("Migration: yes, no notice"); not independently verified by Architect.
6. **Copy rev 2** approved 2026-11-27 11:22 AM MST (RELAYED by PM).

## 1. Summary

- A group goal is one of **items** (count of units on hand across all members), **months**, or **amount** (oz or fl oz, unit derived by the server), plus the existing group **Don't restock**. One new column, `product_groups.goal_items` (§3).
- Item targets above 0 on grouped products are rejected in the store layer (`restock.Targets.SetItem`), so every writer gets the existing 409 `in_group` (§6.3). Value 0, Don't restock and clearing stay allowed.
- The planner never splits a member out of its group (R7), counts buy-back for the whole group (R1), and records a reason when a saved amount goal can't be counted (R14), instead of dropping it silently (§5).
- An edited list line keeps its edit until the trip advances, and a per-store quantity no longer counts as an edit (§7).
- **Migration `032_group_goals.sql`** (next free number; 029 is reserved by open PR #568): moves member targets into group goals per PM rules 1–7 (rule 3 = CJ's approved default), with row-level backup tables, in one transaction. There is no user-facing notice; the backup tables are the record. Run on a synthetic DB in a scratch worktree: green, 9 of 9 rule mutants killed (§4.7, re-run 2026-11-27).

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
| F15 | `M:internal/supply/sql.go:79-106` | Groups with no goal plan at `readMonths`: the household months value; `DefaultMonths` (3) when the household default is any amount, **including amount 0**, which stops ungrouped items; 0 when it's months 0 (Don't restock). Q7 changes the amount-0 case (§5.6). | `defaultKind`, `defaultMonths` |
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

### 4.2 Data rule (PM rules 1–7; rule 3 is CJ's approved default)

D2 (one goal per group) is what makes this migration necessary: member amounts can't stay. Rule 3 (largest member count becomes an items goal) is CJ's approved default (§13 Q1, RELAYED by PM: CJ's own words in PM's chat, 2026-11-27 11:15 AM MST, "Migration: yes, no notice"; not independently verified by Architect). There is **no one-time notice**: nothing reads the backup tables at runtime, and they are the record of what changed. Neither the notice removal nor Q7 changes the 032 SQL; A7/Q3 and Q7 are read- and plan-time rules, not stored data.
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
- **Case 2 with group Don't restock (§13 Q2, ruled):** Don't restock counts as a goal, so the group's Don't restock stays and the member amount is backed up and removed (approved rule "if the group already has a goal, that goal stays and product amounts are removed"). Today such a member still buys on its own (the split runs before `groupTargetIsZero`, `service.go:379-384`); after 032 it stops. The backup row is the record.

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
- **Re-plan afterward:** reads already re-plan `planBasis` (`W3:21`), but staged rows refresh only on the next coalescer job. **RULING:** T1 adds a one-time post-migration full list refresh per household after `RunMigrations` (`cmd/server/main.go:204`), guarded by a `household_settings` key (`list_replan_032`): for each household without the key, enqueue a full refresh through the list coalescer (`ListCoalescer.MarkDirty` + `StartupCatchup`, `shopping/coalescer.go:145-171`), then set the key once that household's refresh job has committed, so a crash before the refresh leaves the key unset and the next start retries. Tested in B8b. That way the stale solo lines from F3 go away at first start.
- Existing tests that count tables and migrations (`TestMigrationApplies` 62→65 tables, `TestMigrationIsIdempotent` 39→40 rows, `M:migrate_test.go:124,158`) must be updated; both failed as expected in the scratch run.

### 4.7 Scratch verification and the B8 golden (evidence, not shipped)
Worktree `/workspace/spec771/probe` (detached at `M:`), with the draft 032 (Appendix A), the restore (Appendix B) and scratch tests in `internal/app/zz771_*_test.go`, named as T1 names them: `TestMigration032Golden`, `TestMigration032Checks`, `TestMigration032RestoreRoundTrip`, `TestMigration032RestoreSkipsChangedRows`.

**Fixture (synthetic ids; B8 copies it).** Household H1 `trip_seq` 4, household H2 `trip_seq` 9. Products carry mixed unit labels (oz, can, fl oz, lb, ct, bag, box) and sized and unsized rows; amounts are counts and are never converted.

| Group (H1 unless noted) | Goal before | Member targets | Expected after 032 |
|---|---|---|---|
| g1 | none | amount **2**, amount **3**, months **4** | `goal_items=3`, `window_months` NULL, quantity NULL (MIN would give 2; "months beats amount" would give `window_months=4`, `goal_items` NULL) |
| g2 | none | months **2**, months **5** | `window_months=5`, `goal_items` NULL (MIN would give 2) |
| g4 | none | amount 2, amount 2 (tie) | `goal_items=2` |
| g8 | none | amount 2, months **0** | `goal_items=2`; the 0 stays |
| g3 | `window_months=3` | amount 6 | goal unchanged; the 6 is backed up and deleted |
| g6 | amount: `quantity_base_value=709.76`, `quantity_dimension='volume'` | amount 4 | goal unchanged; the 4 is backed up and deleted |
| g7 | Don't restock (`window_months=0`) | amount 1 | goal unchanged; the 1 is backed up and deleted (§13 Q2) |
| g5 | none | months **0** only | untouched: no goal written, the 0 kept, no backup rows |
| g9 (H2) | none | amount 7 | `goal_items=7` in H2 only |
| ungrouped | n/a | amount **5** (value > 0) | kept, not backed up |

List lines: H1 auto `touched=1` with no group → `touched=0`, `touched_expires_trip` NULL; H1 auto `touched=1` on g1 → `touched=1`, `touched_expires_trip=5` (`trip_seq+1`); H1 auto untouched on g2 and a manual line → byte-identical; H2 auto `touched=1` on g9 → `touched_expires_trip=10`.

**Golden.** `TestMigration032Golden` dumps every row of `product_groups` (all four goal columns, so the written column is asserted, not only the number), `item_targets`, `shopping_list_items` (`touched`, `touched_expires_trip`, `group_id`) and all three backup tables, and compares the **set** both ways (no missing row, no extra row). Backup expectations: `migration_backup_item_targets` has exactly the 12 grouped targets > 0 above and **no** `value=0` row; `migration_backup_group_goals` has exactly g1 items 3, g2 months 5, g4 items 2, g8 items 2, g9 items 7, and **no** row for g3, g5, g6 or g7; `migration_backup_list_edits` has exactly the three touched auto lines with `old_touched=1`. It then re-runs the DML part (no change) and a second `RunMigrations` (no change).

**Checks.** `TestMigration032Checks`: on the migrated schema, `goal_items` 0 and 1000 are rejected, `goal_items` with `window_months` set and `goal_items` on a group with an amount goal are rejected, and 1 and 999 are accepted.

**Restore.** `TestMigration032RestoreRoundTrip`: Appendix B returns goals, targets and `touched` byte for byte. `TestMigration032RestoreSkipsChangedRows`: after 032 a person sets g1 to `goal_items=4`, sets g2 to `window_months=6` and re-adds a Don't restock 0 on a g1 member; after Appendix B, g1 stays 4, g2 stays 6, that member keeps its 0 (the backed-up amount 2 is not written over it), while the untouched rows are restored (g4's `goal_items` back to NULL, its two amounts back, g1's other two members' targets back).

**Results (2026-11-27, rev 3):**
```
cd /workspace/spec771/probe
go test ./internal/app -run 'TestMigration032|TestMigrationThreeDigitPrefixUnique|TestPortableSQL' -count=1   # ok
python3 032-mutants.py                                                                                         # 15/15 KILLED, exit 0
```
The 15 mutants (Appendix C) are SDET's 9 rule mutants plus two CHECK mutants and four restore mutants. One draft CHECK mutant that dropped only `quantity_base_value IS NULL` survived; it is **equivalent**, because the `quantity_dimension IS NULL` clause still rejects every amount goal (the two columns are set together). It was replaced by the mutant that drops both clauses, which is KILLED. Two earlier candidates stay dropped as equivalent: removing the `household_id` clause from the goal update (group and item ids are global primary keys), and `value >= 0` in the delete while the backup-EXISTS guard stays.

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
- every member not on Don't restock, **plus** Don't restock members with on hand > 0 (UX A7). This narrows PM R13. **PM accepted A7 (§13 Q3 = (a)); PM's message to Architect of 2026-11-27 is the written record.** So an unsized Don't restock member blocks a total only while it has stock.

`amountAvailable = counted set non-empty AND every member has net_base_value > 0 AND all share one dimension`. `amountUnit` = `oz` (mass) / `fl oz` (volume) / null.

For a **saved** amount goal: `status = ok` when every counted member is sized in the goal's dimension. Otherwise `size_missing` if any counted member has no size (even when a mismatch also exists), else `units_mismatch`. Then:
- The planner emits **no line** and records `PlanExcluded{LineKey: group:G, Reason: "group_goal_size_missing" | "group_goal_units_mismatch", Blockers: […productId]}`. There's no hidden fallback to counting items (UX A6 agreed). This replaces the silent `buyQuantity` return (F8).
- `buyQuantity` counts only the counted set. Members outside it (Don't restock, none on hand) add 0.
- **Wire:** the existing `PlanView[...].Excluded` (`planview.go:105-125`) carries the reason, and `offList[]` shows it as the row's reason (W3 "Not on the list"). **Needs attention is unchanged in #771 (§13 Q6 = (b)):** these exclusions show only in Not on the list and on the group page; #686's closed set (`no_product_picked`, `#686:194-206`) doesn't move. Emitting `not_planned` with these two details (amending #686 §5, plus a copy row) is a later follow-up issue.

### 5.5 Unit handling (R12, A3)
- The server derives the unit. `inferDimension` stops accepting a client dimension unchecked and **never falls back to mass**. With no derivable unit, a request for an amount is 422 `amount_unavailable`.
- A legacy `{quantity}` (always ounces, F6) with no derivable unit → 422 `amount_unavailable`. A legacy `{quantity, dimension}` whose dimension contradicts the members → 422 `units_mismatch`. **A total or a legacy ounces amount never becomes an items goal** (UX A3 agreed). Counts become items only in the migration, and only from member counts.
- Storage stays grams/ml (`BaseFromAmount`, `netsize.go:259-268`), and display stays oz/fl oz at 1 decimal.
- `usage` (A14, UX change 2): sent for **every** goal kind and for no goal, as today (FACT: `GroupUsage`, `service.go:210-241`, and `withUsage`, `handler_group.go:295-308`, ignore the goal). Unit: `oz`/`fl oz` when the group's pace is sized (today's `usageFromMembers` rate, `group_plan.go:137-150`, which needs every member sized alike), else `{perMonth, unit:"items"}` from the item-count rate (`itemEvents` + `derive`, `:157-160,190-200`). null only when there's no pace yet. RULING: the unit follows the pace the planner uses for months goals, not `amountAvailable`. They differ only when an unsized Don't restock member has no stock (A7): a total is available, but usage is in items, because that member's uses can't be added in ounces. An amount goal with `status` ok always has a sized pace.

### 5.6 Household amount 0 is Don't restock for groups (§13 Q7 = (a), ships in T1)
`readMonths` (`supply/sql.go:79-106`) returns 0 when the household kind is `amount` and the stored value is 0, matching what amount 0 already means for ungrouped items. Any other amount keeps `DefaultMonths` (3); months handling is unchanged. `defaultKind`/`defaultMonths` (§8.1) come from the same function, so the read model and the planner can't disagree. Scratch verification in `/workspace/spec771/probe` (2026-11-27): the change plus `TestZZ771Q7ReadMonths` (no setting → 3, months 5 → 5, months 0 → 0, amount 2 → 3, amount 0 → 0, amount kind with no value → 3) PASS, the full `./internal/supply` suite PASS, and `python3 q7-mutants.py` → 4/4 KILLED (amount 0 → 3; every amount → 0; inverted `== 0`; reads the kind key instead of the value).

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
- **Membership changes:** `AddMembers`/`Create`/`Accept` keep `requireTargetOrNone` → 409 `target_decision_required` + `deleteOverrides` (R5, `groups.go:888-975,1067-1169`). For UX A12, the member's count is the **existing** `OverrideNotice.quantity` (`groups.go:51-58`; UX change 5), documented as "items kept on hand"; no new field. The 409 adds top-level `amountAvailable` and `amountUnit` for the combined members, and the client offers ounces only when that is true. The ounce conversion (`Amount`/`Dimension` on the notice) is kept for old clients but the new prompt doesn't use it. A choice of `{kind:"items",value:n}` is accepted as the join `target`.
- `RemoveMember` keeps a Don't restock 0 on the removed product, as today (`groups.go:397-430`; §13 Q5 = (a): target 0 is the only stop mechanism).

### 6.4 Trip-scoped list edits
State lives on `shopping_list_items` (§3.2). Backend owns it.
- **Writes:** `SwapAutoLine` sets `touched=1, touched_expires_trip = trip_seq+1, touched_by_user_id = principal, touched_at = now` in its transaction. `SetAdjustment` **no longer sets `touched`** (R10). The adjustment rides on the line and is deleted with it. #683 zero-for-trip is unchanged. `PATCH …/items/{id}` is not an edit (F11).
- **Active predicate:** `touched = 1 AND touched_expires_trip > (SELECT trip_seq FROM households WHERE id = household_id)`. Use it in `listTouchedAutoItems` (`auto_items.go:225-252`), `deleteUnplannedAuto` (`:373`), scoped save (`scoped_save.go:68,182,219-225,250-253,304-347`) and `batchTouched` (`server/list_assemble.go:336-352`). As with skips (`trip.go:52-62`), the trip advance needs no purge. OPINION: also reset `touched=0` on expired rows inside `PurgeExpiredSkips`'s transaction, to keep rows tidy.
- **R9 (RULING):** a touched auto row whose key (`item:` or `group:`) is **not** planned is deleted on the next fill, like an untouched one. A touched row whose key **is** planned keeps its `item_id` (the swap). **Its quantity and note refresh from the plan**, because the only edit that exists is the product choice. OPINION: if a quantity edit is added later, it would freeze quantity only.
- **Wire (list items, additive):** keep `touched` (meaning now "edited this trip"). Add `edited: {actorUserId: string|null, actorDisplayName: string|null, at: string|null} | null`, reusing the `OffListZeroForTrip` actor pattern and `resolveActorDisplayNames` (`handler_shopping_list.go:140-146`, `list_assemble_zero.go:228-250`). No new endpoint: an edit is made through `POST …/items/{id}/swap` and ends at the trip boundary or when the line is no longer planned.

### 6.5 Other endpoints
- `GET /api/restock` / item views (UX changes 1, 3): for a grouped item, `level:"group"`, `groupId`, `groupName` (existing, `targets.go:45-65`) plus a new `groupTarget: {kind, value, unit, status}|null` in the §8.1 `target` shape (`dont_restock` is a kind, `items` included), and `defaultKind` + `defaultMonths` (§8.1) when `groupTarget` is null. Statuses come from one member-size query per snapshot, not per group. The existing `target` field keeps its meaning (the item's own resolved target). So grouped rows render without a per-group GET. **RULING (§13 Q4 = (a)):** grouped items resolve group → household and skip categories, to match the planner (F14).
- No role gate is added: groups and restock have none today (UX §1, `server.go:540-578`). No `canEdit`, and no 403 `forbidden_role` (UX A9, option "keep it that way").

### 6.6 Ownership
| Piece | Owner |
|---|---|
| 032, backup tables, tenant registry, post-migration refresh | Backend |
| `in_group` guard, envelope fields, join-prompt extras | Backend |
| Planner R1/R2/R7/R11/R14, `PlanExcluded` reasons, `planBasis` | Backend |
| Group read model §8.1, PUT §6.2, version | Backend |
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

## 8. Editor contract

### 8.1 Read: `GET /api/groups/{id}`, and the same body from `PUT …/target`, `PUT …/rule` and member writes
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
| `defaultKind` | `"months"\|"amount"\|"dont_restock"` | UX change 1. Which household default `defaultMonths` came from, using `readMonths`' rules (`supply/sql.go:79-106`): household months > 0 (or no setting) → `months`; months 0 → `dont_restock`; household amount 0 → `dont_restock` (§5.6, Q7); any other household amount → `amount` (groups then use 3 months) |
| `defaultMonths` | int 0–12 | `readMonths` (F15): the household months; 3 (`DefaultMonths`) when `defaultKind` is `amount`; 0 when `dont_restock` (the editor leaves Months empty, never prefills 0) |
| `usage` | `{perMonth:number, unit:"oz"\|"fl oz"\|"items"}\|null` | every goal kind and no goal (UX change 2); unit per §5.5; null only with no pace |
| `version` | string | opaque hash of the goal columns; changes only when the goal changes |
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

**Client typing (UX change 6; FACT verified):** `ApiError` is declared in `frontend/src/api/client.ts:148-175`, not `shared/types.ts`, and `apiFetch` (`client.ts:208-225`) types the body's `members` as `TargetConflictMember[]` for every error (`members = body.members ?? []`), reads no `field`, `blockers`, `amountAvailable` or `amountUnit`, and so would hand the `in_group` object to callers as a "member list". T1 adds the wire type to `shared/types.ts`: `ErrorBody` with `field?`, `blockers?: Blocker[]`, `amountAvailable?`, `amountUnit?`, and `members` typed by `code` (`target_decision_required` → `OverrideNotice[]`; `in_group` → `{groupId, groupName}`). T2 changes `ApiError`/`apiFetch` in `client.ts` to carry those fields and narrow `members` by `code`.

### 8.4 States → responses
| State | Driven by |
|---|---|
| Ideal | 200, `target` set, `status:"ok"` |
| Empty | 200, `target:null` (show `defaultMonths`), or `members:[]` (`amountAvailable:false`, `amountBlockers:[]`) |
| Loading | GET/PUT in flight; PUT returns the full group |
| Edge | `amountAvailable:false` + `amountBlockers`; `status` `size_missing`/`units_mismatch` + `target.blockers`; `amountAvailable:true` with `amountUnit` ≠ the saved goal's unit ("Use fluid ounces instead" / "Use ounces instead"); `usage:null`; `usage.unit:"items"` on a months goal; every member `noRestock`; `target.kind:"dont_restock"`; `target:null` with `defaultKind:"dont_restock"` (`defaultMonths:0`, Months left empty; reached from household months 0 **or** household amount 0, §5.6) and with `defaultKind:"amount"` (`defaultMonths:3`) (UX change 7) |
| Error | §8.3 |

UI rule (UX, confirmed 2026-11-27): when `usage.unit` differs from the unit of the goal kind being edited, the editor hides the usage estimate rather than showing a mixed-unit hint. This covers an unsized Don't-restock member with no stock, where `amountAvailable` is true but usage is in items.

### 8.5 Other surfaces UX needs
- List line: `touched` (edited this trip) + `edited {actorUserId, actorDisplayName, at}|null`. A per-store quantity never sets them.
- Not on the list (UX change 4): the `group:G` off-list row (`OffListRowResponse`, `handler_shopping_list.go:148-164`) has `groupId`, `name` (the group name, existing), reason `group_goal_size_missing` / `group_goal_units_mismatch`, and two new `omitempty` fields: `blockers[]` in the §8.1 shape (with product names) and `goal: {kind, value, unit}` (the saved goal). Names in API responses are normal: the list already returns product and group names to the signed-in household. The spec's examples use synthetic names only (needs the W3 renderer).
- Join prompt (409 `target_decision_required`): `members[].quantity` (UX change 5: the **existing** `OverrideNotice.quantity`, "items kept on hand"; no `items` field) → choice `{kind:"items",value:n}`; top-level `amountAvailable`, `amountUnit` for the combined members.
- Grouped product settings: `GET /api/restock` item view `level:"group"`, `groupId`, `groupName`, `groupTarget` (§6.5, `dont_restock` as a kind, `items` included) and, when `groupTarget` is null, `defaultKind` + `defaultMonths` (UX change 3). No per-group fetch.

### 8.6 UX answers (CONFIRMED by UX, spec rev 2 `a71bad6d` §15)
1. **CONFIRMED:** `target.kind:"dont_restock"` on read and `{"dontRestock":true}` on write (A11).
2. **CONFIRMED:** error extras at the top level (`field`, `blockers`, `members.groupId/groupName`), not `details.*`.
3. **CONFIRMED:** saved-goal mismatch blockers list members sized the other way from the goal, while `amountBlockers` lists the smaller set. When `amountAvailable` is true and `amountUnit` differs from the saved goal's unit, the UI offers "Use fluid ounces instead" / "Use ounces instead" (a normal `{kind:"amount", value}` PUT; the server derives the new unit).
4. **CONFIRMED:** `defaultMonths` is 0 when the household is Don't restock and 3 when the household default is an amount; `defaultKind` (UX change 1) tells them apart, and a household Don't restock leaves Months empty, never 0.
5. **CONFIRMED:** no `canEdit` and no 403.
6. **CONFIRMED:** a swapped line's quantity follows the plan while its product stays (§6.4 R9).
7. **CONFIRMED:** suggestions apply needs no skip behavior, because it doesn't write targets (F13).
8. **CONFIRMED:** an items goal plans during opening (§5.2), and a months or amount goal doesn't.
9. **CONFIRMED:** the `edited` object shape replaces UX's `{trip, byName}` proposal (no `trip` number; it's always "this trip"). `{who}` is "You" when `actorUserId` is the signed-in user, else `actorDisplayName`; both null → the no-name wording; `at` is never shown.

## 9. Test strategy (automated; SDET GO on strength)

**Backend (Go):**
| AC | Test | Mutants (risky logic: exact commands required) |
|---|---|---|
| B1 R1/R2 | `supply` table test: 2 members, no pace; use X, stock in Y → no line; use X again → `replacing 1`, `planBasis.replacing=1`; stock in a Don't restock member also clears; ungrouped golden unchanged | per-member boundary; drop NoRestock stock-ins from the boundary |
| B2 R3/R4 | **Store:** `TestSetItemInGroup` (`internal/restock`). Fixture: product P in group G of household A, no `item_targets` row; product Q in household A's shelf and grouped in household **B only** (group G-B). `SetItem` on P with amount 2 and with months 4 → `*InGroupError{GroupID:G, GroupName}`, its sentence is exactly "This product is in a group. Change what the group keeps on hand.", no `item_targets` row, and `change_versions` (stock scope) not bumped. `SetItem` on P with value 0 writes the 0 row; `ClearItem` deletes it. `SetItem` on Q in household A with amount 2 **succeeds and writes the row** (kills the unscoped lookup); the same write in household B → `InGroupError{GroupID:G-B}`. **HTTP:** `TestRestockItemTargetInGroup` (`internal/server`), a table over every writer: `PUT /api/restock/items/{P}` amount>0 and months>0 → 409, `code:"in_group"`, `error` = the sentence above, top-level `members.groupId`/`members.groupName`, the row unchanged, and **no list sync** (the coalescer/`syncList` is not called); allowed paths each assert the effect: value 0 → row is 0, `POST …/dont-restock` → row is 0, `DELETE` → row gone, `PUT /api/groups/{id}/members/{productId}/restock` → row written, supply-override PUT on a non-grouped item → row written, a non-grouped item amount>0 → row written. Both test names match `-run 'InGroup\|TestRestock'` | **required**: Appendix E, `python3 docs/specs/771-mutants/guard-mutants.py`; guard off; `> 0`→`>= 0`; household-unscoped membership lookup; refusal writes anyway; wrong group name; plus "guard only in the handler" (move the check from `SetItem` to `PutItem`; the store test kills it) |
| B3 R7 | seeded target >0 on a member → one group line, no solo line; opening wipe doesn't plan it solo | restore split |
| B4 R8–R10 | per-store qty → `touched` stays 0, line refreshes and is removed when stock covers it; swap → touched + actor; stock covers → removed this trip; trip advance → planner owns it; still needed → product kept, qty refreshed | **required**: predicate `>`→`>=`; SetAdjustment sets touched |
| B5 R11 | items goal 2, on hand 1+0 → buy 1; 3 → no line; Don't restock member's stock counts | |
| B6 R12/R13 | first amount on all-volume group, no client dimension → volume; no sizes → 422 `amount_unavailable` (never mass); legacy `{quantity}` unsized → 422; conflicting `dimension` → 422 `units_mismatch`; ranges/decimals → 422 `invalid` + `field`; `{kind:months,value:0}` → 422; `dontRestock` ok; `expectedVersion` stale → 409 | mass fallback restored |
| B7 R14 | amount goal + unsized member added → `status:size_missing`, `PlanExcluded` reason + blocker, no line; mixed → `units_mismatch`; both → `size_missing` with both rows; A7 (accepted): unsized Don't restock member with stock blocks a total, with 0 on hand doesn't | buy-0 silently (drop the exclusion append) |
| B8 migration | `internal/app/migrate_032_test.go`: `TestMigration032Golden` (the §4.7 fixture and golden, including all three backup tables and the written goal column), `TestMigration032Checks`, `TestMigration032RestoreRoundTrip`, `TestMigration032RestoreSkipsChangedRows` (all §4.7), DML re-run no-op, second start no-op, rollback on a failure after the last statement (copy `migrate_031_test.go` pattern), `TestMigrationApplies`/`IsIdempotent` counts, `HouseholdTables`. The runner has no down migration (§4.6); rollback is §4.4/Appendix B | **required**: `docs/specs/771-mutants/032-mutants.py` (Appendix C), run `python3 docs/specs/771-mutants/032-mutants.py` from the repo root; all 15 KILLED, exit 0 (scratch: 15/15) |
| B8b `list_replan_032` | `TestMigration032ListReplan` (`internal/server`, real coalescer and planner). Fixture on a DB migrated through 031: group G with no goal, member P with item target amount 2, and a staged auto **solo** line `item:P` (F3) plus its staged row. Start 1 (032 + hook, then drain the coalescer): the `item:P` solo line is gone, the plan's `group:G` line is present (items goal 2 from 032), and `household_settings.list_replan_032` is set. Start 2 (hook again on the same DB): no refresh is enqueued (count `Enqueue` calls through an injected coalescer: 0), the key value is unchanged, and the list rows are byte-identical | skip the hook (solo line survives); never set the key (start 2 enqueues); set the key without refreshing (solo line survives) |
| B9 | one synthetic regression per reported item shape (group replace; target split; count-labelled-as-oz; touched freeze; unsized amount goal), no household data | |
| B10 read model | group GET: `defaultKind` for household months 3 / months 0 / amount 2 / **amount 0** / no setting → `months`/`dont_restock`/`amount`/`dont_restock`/`months` with `defaultMonths` 3/0/3/0/3, and a goal-less group under household amount 0 plans no line (Q7); `usage` present on a months goal and on no goal (oz when all sized alike, items otherwise, null with no pace); `GET /api/restock` grouped row carries `groupTarget` (each kind incl. `dont_restock`) or `defaultKind`/`defaultMonths`; `group:G` off-list row has `blockers` + `goal`; join 409 `members[].quantity`. Mutant tests: `TestZZ771Q7ReadMonths`'s six cases become the `readMonths` table in `internal/supply` (`TestReadMonthsQ7`), including "amount kind with no value → 3", a state the DB allows (`supply_target_kind` set, `supply_target_value` absent) | **required** (Q7 amount-0 mapping): T1 adds `docs/specs/771-mutants/q7-mutants.py` (Appendix D), run `python3 docs/specs/771-mutants/q7-mutants.py` from the repo root; mutants amount 0 → 3, every amount → 0, inverted `== 0`, wrong settings key must all be KILLED, exit 0. Also: `defaultKind` from the stored kind instead of `readMonths`; usage gated on goal kind |
| B11 Q4 category skip (§6.5, F14) | `TestRestockGroupedItemSkipsCategoryInGroup` (`internal/restock`): grouped product P (group G, **no** group goal) and ungrouped product U in the same category with a category target amount 6. Snapshot/`GET /api/restock`: P resolves to `level:"household"` with the household target (and the row's `defaultKind`/`defaultMonths`), U stays `level:"category"` with 6. After G gets a goal, P resolves to `level:"group"` and the level below it is the household, not the category. Planner (`internal/supply`, `TestPlanGroupIgnoresCategoryTarget`): the same fixture with household months 3 plans G at 3 months; no line or quantity comes from the category amount 6 | **required**: Appendix F, `python3 docs/specs/771-mutants/q4-mutants.py`; F14 fall-through restored; loader never marks grouped items; categories return below a group goal (scratch: 3/3 KILLED) |

Required mutant commands (all from the repo root; each prints KILLED for every mutant and exits 0):
- 032: `python3 docs/specs/771-mutants/032-mutants.py` (Appendix C) → `go test ./internal/app -run TestMigration032 -count=1`.
- `in_group`: `python3 docs/specs/771-mutants/guard-mutants.py` (Appendix E) → `go test ./internal/restock ./internal/server -run 'InGroup|TestRestock' -count=1`.
- Q7: `python3 docs/specs/771-mutants/q7-mutants.py` (Appendix D) → `go test ./internal/supply -run 'TestReadMonthsQ7|TestZZ771Q7' -count=1`.
- Q4: `python3 docs/specs/771-mutants/q4-mutants.py` (Appendix F) → `go test ./internal/restock -run 'InGroup|TestRestock' -count=1`.

**Scratch evidence (2026-11-27, `/workspace/spec771/probe`; prototypes, not shipped):** the `in_group` store guard, the Q4 resolve skip and the Q7 `readMonths` change were written in the probe with the store/unit tests above. `go test ./internal/restock ./internal/server -run 'InGroup|TestRestock'` ok; guard mutants 5/5 KILLED; Q4 mutants 3/3 KILLED; Q7 mutants 4/4 KILLED; `./internal/restock` and `./internal/supply` full suites ok. A full `./internal/...` run with the guard on shows **existing fixtures T1 must re-seed**: 9 `internal/server` tests (`TestStatusAttentionSendKeyGroup`, `TestStatusAttentionGroupName`, `TestStatusAttentionSendKeyGroupViaPlan`, `TestStatusAttentionSendKeyFallbackItem`, `TestStatusAttentionGroupDetailTieBreak`, `TestStatusAttentionSendUnsureGroupFixTargetHTTP`, `TestW3OffListReasonPerKind`, `TestW3T34PutAwayPreservesRowKinds`, `TestW3OffListGroupNameLookupFailsFirstCallOnlyHTTP`) set an item target > 0 on a grouped product through `PUT /api/restock/items` to make a group line. T1 re-seeds them with a group goal (`goal_items`) so each keeps locking the same #686/W3 behavior, and doesn't weaken their assertions. `TestHouseholdSchemaTablesRegistered` fails until the backup tables are registered (§4.4).

**Frontend (web, Vitest + one e2e):** F1 grouped settings show the group goal + link, no amount/months input; a forced 409 `in_group` shows inline with "Open group settings". F2 three kinds + `dontRestock`; the PUT body never has `unit`/`dimension`; amount disabled with blockers; the label comes from `amountUnit`. F3 status card for `status:"size_missing"` and `status:"units_mismatch"`, with names and fixes. F4 a per-store quantity never shows the edited tag; a swap does, with/without a name. F5 mobile: none (F17), so nothing to add. `apiFetch` narrows `members` by `code` and keeps `field`/`blockers`/`amountAvailable`/`amountUnit`; `defaultKind` months/amount/dont_restock each render their own copy (UX §16 7b), and `dont_restock` from household months 0 and from household amount 0 both leave Months empty (Q7); "Use fluid ounces instead" (UX §16 7c). E2E: open group, set items 2, save; the list asks for 1 with 1 on hand.

## 10. Copy/data hygiene
No household data in code, tests or docs: synthetic product ids only. The reporting household's one-time correction (PM "Reporting household") runs **after** 032 through the normal product-edit and group-goal APIs, owned by the Manager's chosen owner with CJ's approval. No SQL against the Pi DB from #771 work.

## 11. Rollback
§4.4 (full: pre-deploy R2; row-level: Appendix B via a corrective migration). Code rollback without data rollback is safe-degraded (§4.6). No user-facing notice exists (§13 Q1), so neither direction shows the household anything; the 032 backup tables are the record of what moved.

## 12. Tickets (contract first; few and large; no estimates)
| # | Ticket | Owner | Depends on | Done when |
|---|---|---|---|---|
| T0 | This docs PR | Architect | Done: UX CONFIRMED §8 rev 2; PM ruled §13 (CJ's migration default and copy rev 2 approvals RELAYED by PM) | merged; PM marks #771 ready-to-build |
| T1 | Backend: 032 + backup tables + tenant registry + post-migration refresh; `in_group` guard; group read model + PUT + version; planner R1/R2/R7/R11/R14; edit lifetime + `edited`; `defaultKind`, household amount 0 → Don't restock for groups (§5.6, Q7), category skip for grouped items (Q4), usage for all kinds, `GET /api/restock` `groupTarget`, off-list `blockers`/`goal`; `shared/types.ts` (`ErrorBody` with `field`, `blockers`, `amountAvailable`, `amountUnit`, `members` by `code`; §8.3); B1–B11 + B8b, the 032, guard, Q7 and Q4 mutant scripts (Appendices C–F), re-seeding the 9 server fixtures listed in §9 | Backend | T0 | SDET GO, Architect GO (migration, API), green CI |
| T2 | Frontend web: editor (3 kinds + Don't restock, server unit, blockers, status card, version), grouped-product settings + 409 inline, edited tag, join prompt items choice (from `members[].quantity`), Seesaw stops sending `dimension`; `ApiError`/`apiFetch` (`client.ts:148-225`) carry the §8.3 fields and narrow `members` by `code`; F1–F5 + e2e, with F3's status card tested for `status:"size_missing"` and `status:"units_mismatch"`, and the Q7 case: `target:null`, `defaultKind:"dont_restock"`, `defaultMonths:0`, Months left empty, for household months 0 **and** household amount 0 | Frontend | T1 merged (or T1's types on master) | UX GO, SDET GO, green CI |
| T3 | Reporting household data correction (after T1 deploy, with the pre-deploy R2 backup recorded) | Manager's chosen owner | T1 deployed, CJ approval (already given per PM) | values recorded privately with Manager |
| T4 | Follow-up issue (not #771): blocked amount goals in Needs attention as `not_planned` (amends #686 §5, needs a copy row) | PM files it | T1 | issue filed |

## 13. Rulings (all RELAYED by PM, 2026-11-27; final)
| # | Question | Ruling | Source | Effect in this doc |
|---|---|---|---|---|
| Q1 | Migration default for goal-less groups whose members have amounts | **(a)** items = the largest member count; months-only groups get months = the largest; **no one-time notice** | relayed by PM: CJ's own words in PM's chat, 2026-11-27 11:15 AM MST ("Migration: yes, no notice"); not independently verified by Architect | §4.2 rule 3; no `migrationNotice`, no dismiss endpoint or setting; backup tables are the record (§4.4, §11) |
| Q2 | Group Don't restock plus a member with its own amount | **(a)** Don't restock counts as a goal: it stays and the member amount is removed | PM: covered by CJ's approved rule "if the group already has a goal, that goal stays and product amounts are removed" | §4.2 case 2; the backup row is the record |
| Q3 | Unsized Don't restock members with stock (UX A7) | **(a)** they block a total while they have stock | PM-accepted; PM's message of 2026-11-27 is the written record | §5.4 counted set; B7 |
| Q4 | Category targets on grouped products (F14) | **(a)** grouped items resolve group → household and skip categories | PM | §6.5; T1 |
| Q5 | Don't restock on a product removed from a group | **(a)** keep the 0 | PM: CJ's rule that target 0 is the only stop mechanism | §6.3 |
| Q6 | Blocked amount goals in Needs attention | **(b)** for #771: Not on the list + group page only; (a) later as a follow-up issue | PM | §5.4; T4 |
| Q7 | Household default amount 0 | **(a)** Don't restock for groups too: `defaultKind:"dont_restock"`, `defaultMonths:0`, no group line; ships in T1 | PM | F15; §5.6; §8.1; §8.4; B10 + Q7 mutants; T1 |

## 14. Out of scope
Per PM: solo replace ignoring on-hand (A), pace rule gap (B), duplicate bought line (H), 3-month window (I), detecting bad sizes (N3), per-store quantities lasting beyond a trip, changing Remove or target 0. Also: dropping the 032 backup tables; any role gate; a migration notice (CJ: no notice); Needs attention for blocked goals (T4 follow-up).

## Appendix A: `032_group_goals.sql` (draft)
```sql
-- #771: one goal per group, item-count group goals, one-trip list edits.
-- Applied in one transaction (applyMigrationTx). No in-volume snapshot
-- (multi-household-staged.md 2.5); the pre-deploy R2 backup is the full
-- rollback, and the three backup tables below are the row-level undo.

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
File: `docs/specs/771-mutants/032-mutants.py`. Run `python3 docs/specs/771-mutants/032-mutants.py` from the repo root; it runs `go test ./internal/app -run TestMigration032 -count=1` per mutant. Scratch result 15/15 KILLED. T1 stores Appendix B at `internal/app/testdata/032_restore.sql` (the restore tests read it).
```python
#!/usr/bin/env python3
"""Mutant check for 032_group_goals.sql. Run from the repo root:
   python3 docs/specs/771-mutants/032-mutants.py
Each mutant edits the migration (or the Appendix B restore) text, runs
   go test ./internal/app -run TestMigration032 -count=1
and must make it FAIL (KILLED)."""
import pathlib, subprocess, sys
SQL = pathlib.Path("internal/app/migrations/032_group_goals.sql")
RESTORE = pathlib.Path("internal/app/testdata/032_restore.sql")  # Appendix B; scratch file: internal/app/zz771_restore.sql.txt
CMD = ["go", "test", "./internal/app", "-run", "TestMigration032", "-count=1"]
MUTANTS = [
    ("largest->smallest amount", "THEN MAX(CASE WHEN b.kind = 'amount'", "THEN MIN(CASE WHEN b.kind = 'amount'"),
    ("largest->smallest months", "ELSE MAX(CASE WHEN b.kind = 'months'", "ELSE MIN(CASE WHEN b.kind = 'months'"),
    ("months beats amount", "> 0 THEN 'items' ELSE 'months' END", "> 0 THEN 'months' ELSE 'items' END"),
    ("back up don't-restock 0 too", "WHERE t.value > 0\nON CONFLICT", "WHERE t.value >= 0\nON CONFLICT"),
    ("delete without the backup check", "DELETE FROM item_targets\nWHERE value > 0\n  AND EXISTS", "DELETE FROM item_targets\nWHERE value > 0\n  AND 1 OR EXISTS"),
    ("delete don't-restock 0 too (no backup check)", "DELETE FROM item_targets\nWHERE value > 0\n  AND EXISTS", "DELETE FROM item_targets\nWHERE value >= 0\n  AND 1 OR EXISTS"),
    ("plan a goal for groups that already have one", "WHERE b.migration_id = '032'\n  AND g.window_months IS NULL AND g.quantity_base_value IS NULL AND g.goal_items IS NULL", "WHERE b.migration_id = '032'"),
    ("un-mark group lines too", "AND COALESCE(group_id, '') = ''", ""),
    ("no edit lifetime", "SET touched_expires_trip = (SELECT h.trip_seq + 1", "SET touched_expires_trip = (SELECT h.trip_seq"),
    ("CHECK allows 0 and 1000", "goal_items BETWEEN 1 AND 999", "goal_items BETWEEN 0 AND 1000"),
    ("CHECK allows goal_items with an amount goal", "AND window_months IS NULL\n        AND quantity_base_value IS NULL\n        AND quantity_dimension IS NULL));", "AND window_months IS NULL));"),
    ("restore clears an items goal a person changed", "AND product_groups.goal_items = bg.new_value ", "", RESTORE),
    ("restore clears a months goal a person changed", "AND product_groups.window_months = bg.new_value ", "", RESTORE),
    ("restore overwrites a re-added target", "ON CONFLICT (household_id, item_id) DO NOTHING", "ON CONFLICT (household_id, item_id) DO UPDATE SET kind = excluded.kind, value = excluded.value", RESTORE),
    ("restore skips untouched rows too", "SET goal_items = NULL, window_months = NULL\nWHERE EXISTS", "SET goal_items = goal_items, window_months = window_months\nWHERE EXISTS", RESTORE),
]
origs = {SQL: SQL.read_text(), RESTORE: RESTORE.read_text()}
survived = []
try:
    for m in MUTANTS:
        name, old, new = m[:3]
        path = m[3] if len(m) > 3 else SQL
        orig = origs[path]
        if old not in orig:
            sys.exit(f"mutant anchor missing: {name}")
        path.write_text(orig.replace(old, new, 1))
        r = subprocess.run(CMD, capture_output=True, text=True)
        status = "KILLED" if r.returncode != 0 else "SURVIVED"
        print(f"{status}: {name}")
        if r.returncode == 0:
            survived.append(name)
        path.write_text(orig)
finally:
    for path, text in origs.items():
        path.write_text(text)
sys.exit(1 if survived else 0)
```

## Appendix D: `q7-mutants.py`
File: `docs/specs/771-mutants/q7-mutants.py`. Run `python3 docs/specs/771-mutants/q7-mutants.py` from the repo root; it runs `go test ./internal/supply -run 'TestReadMonthsQ7|TestZZ771Q7' -count=1` per mutant against `internal/supply/sql.go` (§5.6). The anchors match the scratch `readMonths` change; T1 re-anchors them if its code differs, keeping the four mutants. Scratch result 4/4 KILLED.
```python
#!/usr/bin/env python3
"""Run from the repo root: python3 docs/specs/771-mutants/q7-mutants.py
#771 Q7 mutants: each must make TestReadMonthsQ7 (scratch name TestZZ771Q7ReadMonths) fail (KILLED)."""
import subprocess, sys
P = "internal/supply/sql.go"
MUTANTS = [
    ("amount 0 -> default 3 (mapping removed)", "n == 0 {\n\t\t\t\treturn 0, nil", "n == 0 {\n\t\t\t\treturn DefaultMonths, nil"),
    ("every amount -> 0", "\t\treturn DefaultMonths, nil\n\t}\n\tif kind == \"\" ||", "\t\treturn 0, nil\n\t}\n\tif kind == \"\" ||"),
    ("amount check inverted (n != 0)", "perr == nil && n == 0", "perr == nil && n != 0"),
    ("amount branch reads the kind key", "raw, err := readHouseholdSetting(ctx, scope, q, keyTargetValue)\n\t\tif err != nil {\n\t\t\treturn 0, fmt.Errorf(\"could not read the supply length: %w\", err)\n\t\t}\n\t\tif raw != \"\" {", "raw, err := readHouseholdSetting(ctx, scope, q, keyTargetKind)\n\t\tif err != nil {\n\t\t\treturn 0, fmt.Errorf(\"could not read the supply length: %w\", err)\n\t\t}\n\t\tif raw != \"\" {"),
]
orig = open(P).read()
bad = 0
try:
    for name, a, b in MUTANTS:
        assert orig.count(a) == 1, name
        open(P, "w").write(orig.replace(a, b))
        r = subprocess.run(["go", "test", "./internal/supply", "-run", "TestReadMonthsQ7|TestZZ771Q7", "-count=1"], capture_output=True)
        ok = r.returncode != 0
        bad += not ok
        print(("KILLED  " if ok else "SURVIVED") + " " + name)
finally:
    open(P, "w").write(orig)
sys.exit(1 if bad else 0)
```

## Appendix E: `guard-mutants.py`
File: `docs/specs/771-mutants/guard-mutants.py`. Run `python3 docs/specs/771-mutants/guard-mutants.py` from the repo root. Anchors match the scratch guard in `SetItem` (§6.3); T1 re-anchors them to its code and adds the "guard only in the handler" mutant once the HTTP test exists. Scratch result 5/5 KILLED.
```python
#!/usr/bin/env python3
"""#771 in_group guard mutants (store level). Run from the repo root.
Each mutant edits internal/restock/targets.go, runs
   go test ./internal/restock ./internal/server -run 'InGroup|TestRestock' -count=1
and must make it FAIL (KILLED)."""
import pathlib, subprocess, sys
P = pathlib.Path("internal/restock/targets.go")
CMD = ["go", "test", "./internal/restock", "./internal/server", "-run", "InGroup|TestRestock", "-count=1"]
MUTANTS = [
    ("guard off", "\tif target.Value > 0 {\n\t\t// #771", "\tif false {\n\t\t// #771"),
    ("value 0 refused too (> 0 -> >= 0)", "\tif target.Value > 0 {\n\t\t// #771", "\tif target.Value >= 0 {\n\t\t// #771"),
    ("household-unscoped membership lookup",
     "JOIN product_group_members m ON m.product_id = i.product_id AND m.household_id = i.household_id\n\t\t\tJOIN product_groups g ON g.id = m.group_id AND g.household_id = m.household_id",
     "JOIN product_group_members m ON m.product_id = i.product_id\n\t\t\tJOIN product_groups g ON g.id = m.group_id"),
    ("refusal writes anyway", "\t\t\treturn &InGroupError{GroupID: groupID, GroupName: groupName}\n", "\t\t\t_ = &InGroupError{GroupID: groupID, GroupName: groupName}\n"),
    ("wrong group name", "return &InGroupError{GroupID: groupID, GroupName: groupName}", "return &InGroupError{GroupID: groupID, GroupName: groupID}"),
]
orig = P.read_text()
survived = []
try:
    for name, old, new in MUTANTS:
        if old not in orig:
            sys.exit(f"mutant anchor missing: {name}")
        P.write_text(orig.replace(old, new, 1))
        r = subprocess.run(CMD, capture_output=True, text=True)
        killed = r.returncode != 0
        print(("KILLED: " if killed else "SURVIVED: ") + name)
        if not killed:
            survived.append(name)
finally:
    P.write_text(orig)
sys.exit(1 if survived else 0)
```

## Appendix F: `q4-mutants.py`
File: `docs/specs/771-mutants/q4-mutants.py`. Run `python3 docs/specs/771-mutants/q4-mutants.py` from the repo root. Anchors match the scratch `Input.InGroup` skip in `restock/target.go` and the loader in `targets.go`; T1 re-anchors them to its code. Scratch result 3/3 KILLED.
```python
#!/usr/bin/env python3
"""#771 Q4 mutants: grouped items skip category targets. Run from the repo root.
Each mutant edits internal/restock/target.go or targets.go, runs
   go test ./internal/restock -run 'InGroup|TestRestock' -count=1
and must make it FAIL (KILLED)."""
import pathlib, subprocess, sys
T = pathlib.Path("internal/restock/target.go")
S = pathlib.Path("internal/restock/targets.go")
CMD = ["go", "test", "./internal/restock", "-run", "InGroup|TestRestock", "-count=1"]
MUTANTS = [
    ("F14 fall-through restored", T, "\tif in.InGroup {\n", "\tif false {\n"),
    ("loader never marks grouped items", S, 'InGroup: item.view.GroupID != ""', "InGroup: false"),
    ("below a group goal, categories come back", T,
     "\t\t\tnext := Resolution{Target: in.Household, Level: LevelHousehold}\n\t\t\treturn Resolution{Target: *in.Group",
     "\t\t\tnext := resolveCategories(in)\n\t\t\treturn Resolution{Target: *in.Group"),
]
origs = {T: T.read_text(), S: S.read_text()}
survived = []
try:
    for name, path, old, new in MUTANTS:
        orig = origs[path]
        if old not in orig:
            sys.exit(f"mutant anchor missing: {name}")
        path.write_text(orig.replace(old, new, 1))
        r = subprocess.run(CMD, capture_output=True, text=True)
        killed = r.returncode != 0
        print(("KILLED: " if killed else "SURVIVED: ") + name)
        if not killed:
            survived.append(name)
        path.write_text(orig)
finally:
    for path, text in origs.items():
        path.write_text(text)
sys.exit(1 if survived else 0)
```
