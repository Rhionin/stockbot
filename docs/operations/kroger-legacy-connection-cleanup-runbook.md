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

**Goal:** one WAL-consistent snapshot suitable for restore, with proof, **without** trusting a raw filesystem copy of an open database.

**Documented SQLite commands** (see [SQLite CLI: `.backup`](https://sqlite.org/cli.html) and [Online Backup API](https://sqlite.org/backup.html)):

1. **Stop the server** so no process holds the main DB file or `-wal` / `-shm` sidecars for that path. If the deployment cannot stop the process, stop here and have **CJ / Network Engineering** confirm an approved online-backup procedure (`sqlite3` interactive `.backup DESTINATION` uses the Backup API under the hood; alternatives must be explicitly approved).
2. **Quiesce / freshness:** note UTC time and image/version **after** the process exit you rely on, so the snapshot is known not to predate concurrent writers.
3. **Snapshot (offline DB + no WAL readers):** from a shell, run a backup into a new file (example path only):

   `sqlite3 /path/to/pantry.db ".backup '/secure/path/pantry-pre-kroger-legacy-cleanup-<UTC>.db'"`

   Optional: `VACUUM INTO '…';` is valid SQL run inside `sqlite3` for an offline file; it produces a compact copy (see [SQLite VACUUM](https://sqlite.org/lang_vacuum.html)). Pick **one** standard method per environment and record which.
4. **Private storage:** keep the snapshot and checksum on storage restricted to the backend/manager role (no world-readable paths).
5. **Integrity:** on the snapshot file only:

   `sqlite3 /secure/path/pantry-pre-kroger-legacy-cleanup-<UTC>.db "PRAGMA integrity_check;"`

   Require a **single** result row exactly `ok` ([`PRAGMA integrity_check`](https://sqlite.org/pragma.html#pragma_integrity_check)).
6. **Checksum:** record a cryptographic hash of the snapshot file (e.g. `sha256sum`) in the deploy ticket.
7. **Non-secret inventory (read-only):** record counts for escalation if the DB shape is not one the cleanup is designed for (any non-zero count below means **stop** and engage Architect — do not deploy this change until resolved):
   - `SELECT COUNT(*) FROM provider_connections WHERE id = 'user-1:kroger';`
   - `SELECT household_id, COUNT(*) AS n FROM provider_connections WHERE provider_id = 'kroger' GROUP BY household_id HAVING n > 2;` (must be empty: more than two Kroger rows per household is out of spec for this cleanup)
   - `SELECT household_id, COUNT(*) AS n FROM provider_connections WHERE provider_id = 'kroger' GROUP BY household_id HAVING n = 2;` (only the **legacy + canonical** duplicate shape is expected where cleanup deletes one row; any other two-row pattern needs Architect review)

This runbook does **not** execute production steps in CI or in the cloud agent.

## Post-deploy checks (read-only, no Kroger API)

After the new binary starts successfully once (cleanup runs at startup):

1. `SELECT value FROM app_settings WHERE key = 'kroger_legacy_connection_cleanup_v1';` → `1`.
2. `SELECT COUNT(*) FROM provider_connections WHERE id = 'user-1:kroger';` → `0`.
3. `SELECT COUNT(*) FROM (SELECT household_id FROM provider_connections WHERE provider_id = 'kroger' GROUP BY household_id HAVING COUNT(*) > 1);` → `0`.
4. Spot-check one affected household (if known): compare hashed or length-only fingerprints of `app_key` / token columns against the pre-deploy snapshot—not raw secrets in logs.

## Rerun and rollback

- **Rerun:** safe no-op once the flag is `1`.
- **Rollback (stopped process, WAL-safe restore — documentation only):**
  1. Stop the server (same requirement as backup: no open handles on the DB path).
  2. Remove or rename the **current** main file and any `-wal` / `-shm` siblings beside that path (stale WAL from an old main file must not be reused).
  3. Copy the verified snapshot file into the configured DB path with correct ownership and **private** permissions matching production policy.
  4. Run `PRAGMA integrity_check;` on the restored path; require `ok`.
  5. Install and start the **prior** server binary that was known good before this deploy (restore code and data together; do not run the new binary on rolled-back data unless Architect approves).

No Kroger OAuth calls are required for rollback decisions. If your layout attaches auxiliary DBs or uses non-default paths, confirm the exact file set with **CJ / Network Engineering** before touching production disks.

## Expected row effects (from inventory)

Derive expected **deletes** and **re-keys** from the read-only queries above before deploy (counts of legacy rows, households with two Kroger rows, legacy-only households). The implementation does not print token values.
