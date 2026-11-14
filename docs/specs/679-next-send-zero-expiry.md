# #679: next-send 0 expires at the trip boundary

Status: contract, docs-only. Owner: Backend. Lands after #677 (W3 backend).
Issue: https://github.com/Rhionin/stockbot/issues/679 · Parent: #571 (`571-needs-attention.md` §8a(a)) · W3 contract: `docs/design/571-w3-replan-contract.md`.

Pinned sources:
- master `34b0b0fad8c6cd511620835182dd5e4696a818b7` (cited as `M:`).
- PR #677 head `097ecdbf47828dffd6254df99dc2cb00176d0056` (cited as `P:`). #679 builds on it.

## 1. Summary

A person who sets a line's next send to 0 holds it off this trip only. At the next trip boundary (Start a new cart, or a mass stock-in close) the 0 lapses and the line plans and sends normally again. Non-zero next-send values keep today's lifetime.

**No migration is needed.** The three columns already exist on master. They were added by W1 migration 031 (#578), which CJ approved and which shipped schema-only (M:`internal/app/migrations/031_trip_scoped_skips.sql:14-16`; W3 contract §5 M:`docs/design/571-w3-replan-contract.md:400`). Nothing writes them today. #679 is code-only.

## 2. Dependency

#679 lands **after #677 merges**. It uses these #677 parts:
- `offList[]` assembly and `offListReason` (P:`internal/server/list_assemble.go:317-392`), and the summary count (P:`list_assemble.go:404-413`).
- The boundary-change writer in the production advance path: `tryAdvanceTrip` → `AdvanceSeq` → `writePlannedBoundaryChanges` (P:`internal/trip/trip.go:183-188`, P:`internal/trip/boundary_hook.go:20-38`, P:`internal/trip/trip_boundary.go:74-114`).
- The skipped HTTP subtest `TestW3OffListReasonPerKind/zero_for_trip` (P:`internal/server/handler_shopping_list_w3_contract_test.go:513-515`). #679 replaces the skip.

If #677 changes these seams before it merges, Backend adapts #679 to the merged names. The rules below don't change.

## 3. Schema (already on master; no DDL in #679)

```sql
-- M: internal/app/migrations/031_trip_scoped_skips.sql:14-16 (applied via applyMigrationTx, M:internal/app/migrate.go:143,175)
ALTER TABLE shopping_list_entry_adjustments ADD COLUMN actor_user_id TEXT;
ALTER TABLE shopping_list_entry_adjustments ADD COLUMN set_at TEXT;
ALTER TABLE shopping_list_entry_adjustments ADD COLUMN expires_trip INTEGER;
```

- All three are nullable with no default. Existing rows read NULL, so nothing expires early.
- `set_at` is `TEXT` (RFC 3339 UTC), not `TIMESTAMP` as #679 states. It matches `staged_cart_skips.removed_at` and `trip.parseTime` (M:`internal/trip/trip.go:411-434`).
- **No index.** Every query below filters on `household_id`, and the table holds at most one row per (entry, provider). A per-household scan costs less than maintaining an index.
- **Gate:** if review finds DDL is needed after all (a new column, index or CHECK), stop. That SQL needs CJ approval via Manager before build. Nothing in this contract needs it.

Base table: M:`internal/app/migrations/006_replace_cart_integrations_with_provider_ledger.sql:41-47`; `household_id` added by M:`022b_household_columns.sql:52`.

## 4. Write rule: `SetAdjustment`

Today the upsert writes only `quantity` (M:`internal/shopping/shopping.go:457-462`). Replace it with:

```sql
INSERT INTO shopping_list_entry_adjustments
  (household_id, entry_id, provider_id, quantity, actor_user_id, set_at, expires_trip)
SELECT ?, ?, ?, ?, ?, ?, CASE WHEN ? = 0 THEN trip_seq + 1 ELSE NULL END
FROM households WHERE id = ?
ON CONFLICT(entry_id, provider_id) DO UPDATE SET
  quantity      = excluded.quantity,
  actor_user_id = excluded.actor_user_id,
  set_at        = excluded.set_at,
  expires_trip  = excluded.expires_trip
WHERE shopping_list_entry_adjustments.household_id = excluded.household_id;
```

- **Actor:** `auth.PrincipalFrom(ctx).UserID`, or NULL when there is no principal or the ID is empty. This is the same source Remove uses (M:`shopping.go:194-197`). The signature of `SetAdjustment` doesn't change.
- **`set_at`:** `time.Now().UTC().Format(time.RFC3339Nano)`, the same format as `removed_at` (M:`shopping.go:198`).
- **`expires_trip`:** `trip_seq + 1` read in the same writer transaction when quantity is 0, otherwise NULL. Setting 0 again in the same trip refreshes actor and `set_at` and keeps the same `expires_trip`. Changing 0 to a non-zero value clears `expires_trip`. Setting 0 on a legacy 0 row (NULL `expires_trip`) writes `trip_seq + 1` through `excluded`, so it becomes a normal one-trip 0.
- **`updated_at`** is unchanged (still first-insert time). `set_at` is the field for "when".
- `RemoveAdjustment` (M:`shopping.go:773`) and `ClearAdjustmentTx` (M:`shopping.go:493-505`) still delete the row.
- **No list change row** is written on set. A person's direct edit isn't an automatic change (W3 §4.3).

## 5. Read and expiry rule

**Active 0:** `quantity = 0 AND (expires_trip IS NULL OR expires_trip > households.trip_seq)`.
**Expired 0:** `quantity = 0 AND expires_trip IS NOT NULL AND expires_trip <= households.trip_seq`.
**Lapses at the next advance (AC6):** every active 0. A legacy 0 (`expires_trip` NULL, set before #679 deploys) is treated as expiring at the household's first trip boundary after deploy, like existing skips under W1. Per D1, target 0 is the only lasting control.
This mirrors the skip predicates (M:`internal/shopping/trip_skip.go:28`, M:`auto_items.go:266`, M:`internal/trip/trip.go:56`).

- Every adjustment read ignores an expired 0. On master the only read is `GetAdjustment` (M:`shopping.go:474-491`), used by the list GET (M:`internal/server/handler_shopping_list_get.go:110-117`) and the cart send quantity (M:`internal/cart/engine.go:567-572`). Add to its query:
  ```sql
  JOIN households h ON h.id = a.household_id
  ... AND (a.expires_trip IS NULL OR a.expires_trip > h.trip_seq)
  ```
  Any batch read added by #677/#679 (plan, `?status=1`, summary, needsAttention, export) uses the same predicate. There is one Go helper for the predicate text.
- `Adjustment` gains `ActorUserID *string`, `SetAt *time.Time` and `ExpiresTrip *int`.
- **Non-zero rows** (NULL `expires_trip`) never expire, as today.
- **Legacy 0** (`quantity = 0 AND expires_trip IS NULL`, AC6): it stays active on reads until the next trip advance, which deletes it in the writer transaction (§6). Because the advance is one transaction, no reader ever sees a legacy 0 at the new `trip_seq`. No data migration or backfill is used. A backfill would be a deploy-time data write that needs its own gate and would race the first advance.
- The read rule is defence in depth. In production the advance deletes expired rows in the same transaction (§6), so no reader should ever see one.

## 6. Trip advance (production path)

Both production triggers converge on `trip.tryAdvanceTrip` (M:`internal/trip/trip.go:163-187`):
- **Start a new cart:** `shopping.Store.EndTrip` (M:`internal/shopping/auto_items.go:461-510`) → `AdvanceTripForNewCart` (M:`trip.go:65-67`).
- **Mass stock-in close:** `scan` commit (M:`internal/scan/commit.go:131`) → `MaybeCloseMassStockIn` → `closeReadyPutAwayClusters` (M:`trip.go:70-156`).

`trip.TripAdvance.Advance` (P:`internal/trip/trip_boundary.go:33-72`) isn't called in production, so #679 doesn't touch it.

Inside the same writer transaction, after `AdvanceSeq` returns (trip_seq +1, then `DELETE` of older `list_line_changes`, M:`trip.go:270-299`), `tryAdvanceTrip` does this, in order:

1. **Collect lapsing lines.**
   ```sql
   SELECT DISTINCT CASE WHEN COALESCE(i.group_id, '') <> '' THEN 'group:' || i.group_id
                        ELSE 'item:' || i.item_id END
   FROM shopping_list_entry_adjustments a
   JOIN shopping_list_items i ON i.id = a.entry_id AND i.household_id = a.household_id
   JOIN households h ON h.id = a.household_id
   WHERE a.household_id = ? AND a.quantity = 0
     AND (a.expires_trip IS NULL OR a.expires_trip <= h.trip_seq)
     AND i.purchased_at IS NULL;
   ```
   EndTrip has already deleted purchased auto rows and their adjustments (M:`auto_items.go:494-498,513-524`). Only lines still on the list come back.
2. **Delete expired 0 rows.**
   ```sql
   DELETE FROM shopping_list_entry_adjustments
   WHERE household_id = ? AND quantity = 0
     AND (expires_trip IS NULL
          OR expires_trip <= (SELECT trip_seq FROM households WHERE id = ?));
   ```
   This also removes orphans whose entry is gone. `expires_trip IS NULL` here is AC6: legacy 0s lapse at the first advance after deploy. Non-zero rows are never matched (`quantity = 0`).
3. **Write one `list_line_changes` row per collected `line_key`** through `trip.WriteBoundaryChange` (P:`trip_boundary.go:74-114`) at the new `trip_seq`:

   | Column | Value |
   |---|---|
   | `effect` | `added` |
   | `reason` | NULL |
   | `cause` | `new_trip` |
   | `from_qty`, `to_qty`, `n` | NULL (W3 §4.3: qty only for `amount_up`/`amount_down`) |
   | `actor_user_id` | NULL (copy: "because a new trip started"; no `{who}`) |
   | `at` | the boundary instant passed to `tryAdvanceTrip` (RFC3339Nano UTC) |
   | `trip_seq` | new `trip_seq` |

   If a skip expiry for the same `line_key` also writes a `new_trip` row through #677's planner, the PK upsert keeps one row. The content is the same.
4. Then #677's `writePlannedBoundaryChanges` runs, and the caller commits once.

When the no-empty-trip rule declines the advance (M:`trip.go:171-181`), nothing in this section runs.

**No-empty-trip rule (AC5).** A 0 set after the last close already counts as activity: `hasTripActivitySince` reads `set_at` where `expires_trip IS NOT NULL` (M:`trip.go:241-264`). A legacy 0 has no `set_at`, so it doesn't count as activity. It lapses at the first boundary that advances for another reason, which matches "first trip boundary after deploy". #679 only has to write `set_at` in a `parseTime` format. Edge case: 0 then changed to non-zero in the same trip leaves `expires_trip` NULL, so it doesn't count. That's correct, because no 0 is still held.

**Fault seam.** Add an unexported hook in `trip`, `faultAfterAdvanceSeq func() error`. Tests set it through `SetFaultAfterAdvanceSeqForTest(fn) (restore func())`. Call it in `tryAdvanceTrip` immediately after `AdvanceSeq`, before steps 1–4. This is the W3 T19 point ("after DELETE … before any INSERT"), but on the production path.

## 7. `?status=1` and `offListReason = 'zero_for_trip'`

**Emission rule.** In the `status=1` envelope, a staged row is in `offList[]` with `offListReason: 'zero_for_trip'` (not in `items[]`) when all of these hold:
- the row can send to the cart for the list's target provider (the same `cartLine && providerID != ""` test as M:`handler_shopping_list_get.go:110-111`);
- that provider's adjustment is an **active 0** (§5), including a legacy 0 with NULL `expires_trip`.

Precedence in `offListReason` (P:`list_assemble.go:378-392`): `removed` > `zero_for_trip` > `taken_off` > `opening` > `not_on_list`. A skip is already off the list, and a person's 0 explains the line better than an automatic change.

- `id` = the line's `line_key` (W3 row-key invariant). The row isn't repeated in `items[]`.
- A legacy 0 (`expires_trip` NULL) is also `zero_for_trip`, since under AC6 it really does come back next trip. Its `actorUserId`, `actorDisplayName` and `setAt` are `null`, so the client uses the UX partial form ("Set to 0 for this trip.").
- `countW3NotOnList` (P:`list_assemble.go:404-413`) adds `zero_for_trip`. `shortNow` already excludes it, because the row isn't in `items[]`.
- Clients that don't send `status=1` get today's bare array, with the row and `adjustment: 0`.

**`lastChange`.** This is the line's existing `list_line_changes` row for the current trip, if there is one. Setting 0 writes none. Copy `{who}`/`{when}` comes from `zeroForTrip` below, not `lastChange`. After the lapse, `lastChange` is the §6 `new_trip`/`added` row, and the line is back in `items[]`.

**New wire field** (additive, present only when `offListReason === 'zero_for_trip'`):

```ts
interface OffListRow {
  // ...existing fields...
  zeroForTrip?: {
    entryId: string;                 // shopping_list_items.id, for PUT/DELETE /api/shopping-list/items/{id}/adjustment ("Change amount")
    providerId: string;              // adjustment's provider_id
    actorUserId: string | null;      // adjustments.actor_user_id
    actorDisplayName: string | null; // users.name; null if the user is gone or has no name
    setAt: string | null;            // adjustments.set_at, RFC 3339 UTC
  };
}
```

Go: `ZeroForTrip *OffListZeroForTripResponse \`json:"zeroForTrip,omitempty"\`` on `OffListRowResponse` (P:`internal/server/handler_shopping_list.go:120-135`). Inner fields have no `omitempty`, so nulls go out as `null`. Resolve display names with the same batched `users` lookup as `lastChange` (P:`internal/shopping/linechange.go:180-203`). Add the field to `shared/types.ts` and `docs/design/api-contract.md` (the `offListReason` block near M:`api-contract.md:1393`). `zero_for_trip` is already reserved in the union (M:`571-w3-replan-contract.md:252`).

**Copy:** `"{who} set it to 0 for this trip {when}. It comes back next trip."` with the partial forms in UX copy rev2 §E.3. `{who}` comes from `actorDisplayName`, and `{when}` from `setAt`.

## 8. needsAttention

- **Before expiry:** a line with an active 0 is exempt. `attention.Evaluate` gets `ZeroForTrip map[string]struct{}` (line keys) and skips those keys in every flag loop (marks and send failures, P:`internal/attention/evaluate.go:46-99`). It also writes no `attention_marks` row at the boundary for that trip.
- **After expiry:** there's no exemption. The line is evaluated by the normal rules from the first read at the new trip.
- A legacy 0 (`expires_trip` NULL) is exempt the same way until the advance deletes it.

## 9. Error codes

None new. `SetAdjustment` keeps 0–999 validation (M:`internal/server/handler_shopping_list_adjustment.go:70-73`). The trip-advance errors keep today's wrapping ("could not start a new cart", M:`auto_items.go:499-501`).

## 10. Ownership

- **Backend:** everything in §§4–9, plus `shared/types.ts` and the `api-contract.md` field.
- **Frontend:** rendering the `zero_for_trip` row (sentence plus **Change amount**) needs Frontend work. See conflict C5.
- **UX:** copy already exists (§E.3). UX checks the row's rendering.

## 11. Rollback

- No DDL, so there's no down migration. Reverting the code leaves the columns populated. Old code ignores `actor_user_id`/`set_at`/`expires_trip`, and every 0 becomes lasting again. That's safe.
- Rows already deleted at a boundary stay deleted. That's the intended lapse. Data loss is limited to "this 0 already expired."
- Backup: the standard pre-deploy Pi snapshot. No special step is needed.

## 12. Test plan

Each test fails if its rule is removed (Backend states the planted mutant per test in the PR body).

1. **Schema lock (no migration).** Extend `assertFull031State` (M:`internal/app/migrate_031_test.go:304`): `PRAGMA table_info(shopping_list_entry_adjustments)` has `actor_user_id TEXT`, `set_at TEXT`, `expires_trip INTEGER`, all `notnull=0`, `dflt_value` NULL. Assert no migration file after `031_` is added by this PR.
2. **Write, insert at 0:** with a principal, `expires_trip = trip_seq+1`, `actor_user_id` = principal, `set_at` parses with `trip.parseTime` and is within the test clock window.
3. **Write, update existing row:** non-zero → 0 then 0 → 0 in the same trip. Actor and `set_at` are refreshed and `expires_trip` is unchanged. 0 → 5 gives `expires_trip` NULL and refreshes actor and `set_at`.
4. **Write, no principal:** `actor_user_id` NULL, other fields set.
5. **Read before expiry:** `GetAdjustment` returns 0, and the cart engine send quantity is 0.
6. **Read after expiry without delete:** seed `expires_trip <= trip_seq` directly. `GetAdjustment` returns nil, and the send quantity is the staged quantity.
7. **Legacy rows (AC6), both triggers:** seed a 0 and a 3 with NULL `actor_user_id`/`set_at`/`expires_trip` on unpurchased lines. Before the advance: `GetAdjustment` returns 0 for the legacy 0, and GET `?status=1` shows it as `zero_for_trip` with `zeroForTrip.actorUserId/actorDisplayName/setAt` all `null`. Advance once (Start a new cart, then separately a mass stock-in close; a second activity is needed because a legacy 0 doesn't count for AC5): the legacy 0 row is gone, one `new_trip`/`added` change row exists for its line at T+1, and the line is in `items[]`. The 3 row is byte-identical after two advances by both triggers. Also: `SetAdjustment(0)` on a legacy 0 row writes `expires_trip = trip_seq+1`. Mutant check: removing `expires_trip IS NULL OR` from the §6 delete must fail this test.
8. **Non-zero unaffected:** a 3 set via `SetAdjustment` survives both triggers byte-identical and clears only on a confirmed send (`ClearAdjustmentTx`).
9. **Advance, Start a new cart (unfaulted):** an active 0 on an unpurchased line goes through `EndTrip`. Then: `trip_seq` is T+1, the row is gone, and one `list_line_changes` row has `effect=added`, `cause=new_trip`, reason/qty/n/actor NULL and `trip_seq=T+1`. GET `?status=1` shows the line in `items[]` with that `lastChange`, and not in `offList`.
10. **Advance, mass stock-in close (unfaulted):** same assertions as test 9, triggered through `scan` commit closing a K-product cluster.
11. **Purchased line:** the 0 row is deleted and no `new_trip` row is written for it (`EndTrip` deletes the line; on the stock-in trigger it is kept and purchased).
12. **Fault on the production path (T19 pattern), both triggers:** set `SetFaultAfterAdvanceSeqForTest` to pause on a channel and then return an error. While it's paused, a read-pool observer runs SQL, `GET ?status=1` and `GET /summary`. It sees `trip_seq=T`, the 0 row present, `zero_for_trip` in `offList`, and no `trip_seq=T+1` change rows. After the abort: `trip_seq` is T, the adjustment row is byte-identical, the `list_line_changes` count and rows are byte-identical, and the no-empty-trip state is unchanged.
13. **No-empty-trip (AC5):** a second `EndTrip` with no activity doesn't advance. Then `SetAdjustment(0)` followed by `EndTrip` does advance. A 0 changed to 3 before `EndTrip` doesn't count.
14. **HTTP `zero_for_trip`:** replace the skip at P:`handler_shopping_list_w3_contract_test.go:513-515`. Set 0 via `PUT /api/shopping-list/items/{id}/adjustment`, then GET `?status=1`. Expect: `offList` row `id=line_key`, `offListReason=zero_for_trip`, `zeroForTrip.entryId/providerId/actorUserId/actorDisplayName/setAt` exact, not in `items[]`, not in any `attentionGroups[].rowKeys`, and `/summary.notOnList` includes it. The legacy no-`status` GET still returns the row with `adjustment: 0`.
15. **Precedence:** with a skip and an active 0 on the same key, the reason is `removed`. With a `taken_off` change and an active 0, it's `zero_for_trip`.
16. **needsAttention exemption:** `internal/attention/evaluate_test.go` has cases with a mark and a send failure on a zero-for-trip key. Before expiry, not flagged. After expiry (key absent from `ZeroForTrip`), flagged.
17. **Cross-household isolation:** household B has an active 0 and household A advances. B's row is untouched and B gets no change row. `SetAdjustment` with A's scope on B's entry fails to find the entry (M:`shopping.go:444-450`) or leaves B unchanged.
18. **Contract parity:** `hosted_contract_test.go` and Vitest show that `zeroForTrip` field names and nullability match between Go and `shared/types.ts`.

## 13. PM acceptance criteria (#679) vs this contract

| AC | Status |
|---|---|
| Intended data shape and the "CJ must approve exact SQL" gate | **Conflict C1.** |
| 1 actor + `set_at` on every set | Agrees (§4). |
| 2 `expires_trip = trip_seq+1` at 0, NULL otherwise | Agrees (§4). |
| 3 reads ignore an expired 0 (plan, status, summary, needsAttention, export) | Agrees. Clarification C2. |
| 4 advance deletes expired rows "the same way it handles skips", `new_trip` after the clear | Agrees on outcome. Clarification C3. |
| 5 a 0 counts for no-empty-trip | Agrees. Already implemented on read side (§6). Only the write is needed. |
| 6 Legacy 0 (NULL `expires_trip`) lapses at the first trip boundary after deploy; non-zero NULL rows unchanged | Agrees (rev 3). The §6 delete and collect treat NULL as lapsing; reads keep it active until then (§5); shown as `zero_for_trip` with null actor (§7); exempt from needsAttention until then (§8); test 7. |
| 7 tests incl. `TestNeedsAttention` + re-enable #677 skipped test | **Conflict C4.** |
| Frontend: "No new UI or copy" | **Conflict C5.** |

- **C1. No migration exists to approve.** The columns shipped in 031 (#578, CJ-approved, merged 2026-11-12). *Recommendation:* drop the migration gate from #679. Manager tells CJ as an FYI that #679 adds no schema and no backup step beyond the standard snapshot. If CJ wants a gate anyway, it becomes "approve the behavior change", not SQL.
- **C2. "Plan" and "export" don't read adjustments on master.** The only read is `GetAdjustment`. *Recommendation:* the rule lives in one predicate helper used by `GetAdjustment` and any batch read #677/#679 adds. The tests cover list, summary, send and needsAttention.
- **C3. Skips aren't deleted in the advance transaction.** They're purged lazily by the next `MaintainSkips` (M:`trip.go:45,52-62`). *Recommendation:* follow AC4's explicit text (delete in the same writer transaction, §6), not the skip mechanics.
- **C4. There's no `TestNeedsAttention` at #677 head, and the skipped test is an HTTP test.** It's `TestW3OffListReasonPerKind/zero_for_trip`. *Recommendation:* re-enable that subtest (test 14) and add the exemption cases to `internal/attention/evaluate_test.go` (test 16).
- **C5. The Frontend AC ("no new UI or copy") conflicts with the UX spec.** The UX spec puts a person-set 0 in "Not on the list" with a NEW sentence and a NEW **Change amount** action (UX spec §5 table; copy rev2 §E.3). Today's clients would show the row only through the generic fallback. *Recommendation:* Backend ships the wire field in #679. PM adds a Frontend AC (or a small Frontend ticket) to render `zero_for_trip` with `zeroForTrip` and **Change amount**, owned by Frontend with UX review. It can merge after #679.

## 14. Out of scope

- Per-change adjustment history.
- Lapsing non-zero values.
- `TripAdvance.Advance` (not a production path).
