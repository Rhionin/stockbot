# Kroger legacy `provider_connections` cleanup (issue #545)

Production applies this cleanup **only** by shipping the server binary that runs `RunKrogerLegacyConnectionCleanup` after migrations and after `RunKrogerAppCutover` (`cmd/server/main.go`). There is **no** separate operator script: the same transactional and idempotency guarantees apply only on process startup.

## What the code does (synthetic behavior)

- **Completion marker:** `app_settings.key = kroger_legacy_connection_cleanup_v1`, value `1`, written **inside** the same transaction as all data changes (or alone when there is no legacy row).
- **Legacy row:** primary key `id = user-1:kroger` and `provider_id = kroger` (historical single-tenant shape).
- **Per household:**
  - If both a canonical row (`id = <household_uuid>:kroger`, same `household_id`) and the legacy row exist for the same household, **delete the legacy row only**; canonical tokens and payload stay untouched.
  - If **only** the legacy row exists for a household and `user_id = user-1`, **re-key** it to `id = <household_uuid>:kroger` and `user_id = <household_uuid>` with an `UPDATE` that does not assign token columns (full payload preserved).
- **Fail closed:** unknown `household_id` on the legacy row, multiple non-legacy Kroger rows for one household, unexpected lone-legacy `user_id`, or legacy `id` still present after the planned changes → startup error (transaction rolls back; no flag).

## Startup sequence (relevant steps)

1. `RunMigrations`
2. `RunKrogerAppCutover` (may backfill `app_key` from primary app credentials)
3. `RunKrogerLegacyConnectionCleanup`

Failures surface as `kroger legacy connection cleanup: …` and stop startup before auth/bootstrap.

## Pre-deploy backup (Backend / Manager)

Use a **supported online backup** for SQLite (not “copy the file while the process runs” as a consistency claim):

1. **Snapshot:** `VACUUM INTO '…/pantry-pre-kroger-legacy-cleanup-<UTC-timestamp>.db';` (or the equivalent `sqlite3` backup API) against the **live** DB while the server is **stopped**, or use SQLite’s online backup API with the app holding a reader if your runbook allows; document whichever method your environment supports as the standard **online** snapshot.
2. **Integrity:** on the snapshot file, run `PRAGMA integrity_check;` → single row `ok`.
3. **Non-secret inventory (read-only):** record counts that match expectations before merge/deploy:
   - `SELECT COUNT(*) FROM provider_connections WHERE id = 'user-1:kroger';`
   - `SELECT household_id, COUNT(*) FROM provider_connections WHERE provider_id = 'kroger' GROUP BY household_id HAVING COUNT(*) > 1;` (should be empty pre-fix if data matches assumptions; if not, stop and escalate.)
4. Store the snapshot path and checksum with the deploy record.

This runbook does **not** execute production steps in CI or in the cloud agent.

## Post-deploy checks (read-only, no Kroger API)

After the new binary starts successfully once (cleanup runs at startup):

1. `SELECT value FROM app_settings WHERE key = 'kroger_legacy_connection_cleanup_v1';` → `1`.
2. `SELECT COUNT(*) FROM provider_connections WHERE id = 'user-1:kroger';` → `0`.
3. `SELECT COUNT(*) FROM (SELECT household_id FROM provider_connections WHERE provider_id = 'kroger' GROUP BY household_id HAVING COUNT(*) > 1);` → `0`.
4. Spot-check one affected household (if known): compare hashed or length-only fingerprints of `app_key` / token columns against the pre-deploy snapshot—not raw secrets in logs.

## Rerun and rollback

- **Rerun:** safe no-op once the flag is `1`.
- **Rollback decision:** if startup fails on the cleanup step, **do not** partial-edit the DB. Restore from the verified snapshot taken pre-deploy and run the previous binary, or fix data under Architect guidance. No Kroger OAuth calls are required for this decision.

## Expected row effects (from inventory)

Derive expected **deletes** and **re-keys** from the read-only queries above before deploy (counts of legacy rows, households with two Kroger rows, legacy-only households). The implementation does not print token values.
