# Kroger legacy `provider_connections` cleanup (issue #545)

Production applies this cleanup **only** by shipping the server binary that runs `RunKrogerLegacyConnectionCleanup` after migrations and after `RunKrogerAppCutover` (`cmd/server/main.go`). There is **no** separate operator script: the same transactional and idempotency guarantees apply only on process startup.

## What the code does (synthetic behavior)

- **Completion marker:** `app_settings.key = kroger_legacy_connection_cleanup_v1`, value `1`, written **inside** the same transaction as all data changes (or alone when there is no legacy row).
- **Legacy row:** primary key `id = user-1:kroger` and `provider_id = kroger` (historical single-tenant shape). Cleanup requires `user_id = user-1` before any delete or rekey.
- **Canonical row (when a non-legacy Kroger row exists for the household):** expects `id = <household_uuid>:kroger`, `user_id = <household_uuid>`, `household_id = <household_uuid>`, `provider_id = kroger`. Anything else is rejected (fail closed; no flag).
- **Per household:**
  - If both a verified canonical row and the legacy row exist for the same household, **delete the legacy row only**; canonical tokens and all other columns stay untouched.
  - If **only** the legacy row exists for a household and `user_id = user-1`, **re-key** it to `id = <household_uuid>:kroger` and `user_id = <household_uuid>` with an `UPDATE` that does not assign token columns (full payload preserved).
- **Fail closed:** unknown `household_id` on the legacy row, multiple non-legacy Kroger rows for one household, mismatched legacy or canonical identity, or legacy `id` still present after the planned changes → startup error (transaction rolls back; no flag).

## Startup sequence (relevant steps)

1. `RunMigrations`
2. `RunKrogerAppCutover` (may backfill `app_key` from primary app credentials)
3. `RunKrogerLegacyConnectionCleanup`

Failures surface as `kroger legacy connection cleanup: …` and stop startup before auth/bootstrap.

## Pre-deploy backup (Backend / Manager)

**Goal:** a single **online** copy of the live pantry DB that is consistent for both rollback journal **and** WAL (not a raw `cp` of the main file while writers may extend the WAL).

**Supported procedure in this repo:** `stockbot-backup` / `internal/backup.Snapshot` runs **`VACUUM INTO ?`** against the live database path using the same `modernc.org/sqlite` stack as the server (`internal/backup/snapshot.go`, CLI `cmd/backup`). SQLite documents that **`VACUUM INTO` produces a **complete**, **consistent** copy of the database**, independent of journal mode, including from a WAL-mode database ([SQLite `VACUUM INTO`](https://sqlite.org/lang_vacuum.html)). The implementation sets a busy timeout and retries when the DB is busy.

**Operator prerequisites (known Pi / pantrybot constraints):**

| Need | Why |
| --- | --- |
| A runnable **`stockbot-backup`** (or other build) that links this codebase’s backup path | The pantry **container** is distroless (no `sqlite3` shell). The **Pi host** may lack `sqlite3` and the operator has **no sudo** for ad-hoc installs. |
| Brief **write quiescence** or acceptance of busy-timeout retries | `VACUUM INTO` needs a read of the committed state; a continuously writing process may delay the snapshot until busy handling succeeds. Coordinate with **CJ / Network Engineering** if an auto-updater can restart services during the window. |
| Private output path + checksum | Snapshot files contain secrets; treat like production data. |

**Typical flow (adapt paths and flags to your deploy):**

1. **Coordinate** a deploy hold or low-write window if needed so the snapshot is “fresh” for the change and not racing an unplanned restart.
2. Run **`stockbot-backup run`** with `-db` set to the **live pantry file path**, `-reason pre-deploy`, and the usual secrets/R2 flags (see `internal/backup/cli.go`). The runner snapshots via **`VACUUM INTO`** to a private stage dir, then verifies (`PRAGMA integrity_check` → single `ok`), encrypts, and uploads per existing backup policy.
3. Record **checksum** and manifest line from backup output in the deploy ticket.

If your environment **cannot** run `stockbot-backup` (binary not shipped, path unavailable, or policy forbids live `VACUUM INTO`), treat **online WAL-safe snapshot** as **unmet** until Architect/operations selects an approved alternative (e.g. schedule maintenance + offline copy). Do **not** claim this gate met using only a stopped-process `.backup` recipe.

**Optional integrity spot-check on a copy** (requires *some* SQLite-capable tool — often the same `stockbot-backup verify` flow, not necessarily the `sqlite3` CLI): `PRAGMA integrity_check;` must return a **single** row exactly `ok` ([`PRAGMA integrity_check`](https://sqlite.org/pragma.html#pragma_integrity_check)).

**Non-secret inventory (read-only)** before merge/deploy — any non-zero unexpected count means **stop** and escalate (do not ship this cleanup until resolved):

- `SELECT COUNT(*) FROM provider_connections WHERE id = 'user-1:kroger';`
- `SELECT household_id, COUNT(*) AS n FROM provider_connections WHERE provider_id = 'kroger' GROUP BY household_id HAVING n > 2;` (must be empty)
- `SELECT household_id, COUNT(*) AS n FROM provider_connections WHERE provider_id = 'kroger' GROUP BY household_id HAVING n = 2;` (only the **legacy + canonical** duplicate shape is expected where cleanup deletes one row)

This runbook does **not** execute production steps in CI or in the cloud agent.

## Post-deploy checks (read-only, no Kroger API)

After the new binary starts successfully once (cleanup runs at startup):

1. `SELECT value FROM app_settings WHERE key = 'kroger_legacy_connection_cleanup_v1';` → `1`.
2. `SELECT COUNT(*) FROM provider_connections WHERE id = 'user-1:kroger';` → `0`.
3. `SELECT COUNT(*) FROM (SELECT household_id FROM provider_connections WHERE provider_id = 'kroger' GROUP BY household_id HAVING COUNT(*) > 1);` → `0`.
4. Spot-check one affected household (if known): compare hashed or length-only fingerprints of `app_key` / token columns against the pre-deploy snapshot—not raw secrets in logs.

## Rerun and rollback

- **Rerun:** safe no-op once the flag is `1`.
- **Rollback (documentation only):** restore from the **verified** pre-deploy artifact (same process your backup pipeline uses for DR), then install and start the **prior** server binary. Remove stale `-wal` / `-shm` next to the DB path if you replace the main file so SQLite never pairs a new main file with an old WAL. Confirm `PRAGMA integrity_check` → `ok` on the restored path before start.

No Kroger OAuth calls are required for rollback decisions. If your layout attaches auxiliary DBs or uses non-default paths, confirm the exact file set with **CJ / Network Engineering** before touching production disks.

## Expected row effects (from inventory)

Derive expected **deletes** and **re-keys** from the read-only queries above before deploy (counts of legacy rows, households with two Kroger rows, legacy-only households). The implementation does not print token values.
