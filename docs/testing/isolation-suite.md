# Cross-household isolation suite

This is the stage-1 scaffold for [#386](https://github.com/Rhionin/stockbot/issues/386). It is test-only. The server still has one household and one principal, `user-1`. Per-household authorization is [#377](https://github.com/Rhionin/stockbot/issues/377). The suite starts green. Its coverage check fails the build when a registered route has no isolation decision, so a new route cannot skip cross-household tests once that authorization lands.

The tests live in `internal/server` and run under `go test ./...`. No workflow change is required. CI already runs that command in `TZ=UTC` (the GitHub-hosted default) and in `TZ=America/Denver`.

## Categories

Every registered route is in exactly one category. The key is the pattern the router registered: `GET /api/products/{id}`, or `/api/sentry-tunnel` when the registration has no method.

| Category | What it means | Runs today |
| --- | --- | --- |
| Public allowlist | Open on the public entry. Must match `isPublicPath` or `isAuthEndpoint`. | Yes. The body must contain no household marker. |
| Ops allowlist | Process liveness, not household data. `GET /health` is the entry. | Yes. Same marker check. |
| Isolation cases | A real cross-household check. | The map is empty until a route leaves quarantine with a check. |
| Quarantine | Waiting on #377. Includes `GET /api/events` and `GET /api/sync/snapshot`, which carry household data and are not exempt. | No. |

`GET /api/telemetry` is on the public allowlist. The runner requires HTTP 200, a snapshot `status` of `ok`, and no household id, name, marker, or barcode from either fixture household.

## How a route is inventoried

`http.ServeMux` cannot list its routes, and this suite does not add a production registrar. The test parses `Handle` and `HandleFunc` calls in the non-test Go files of `internal/server` and `internal/brand`. The pattern argument must be a string literal or a string constant such as `sentryTunnelPath`. Anything else fails the test, with the file and line, so a registration cannot hide.

Each pattern is then requested on the live mux: `newAPIMux` for API routes, `brand.Register` for the brand and legal pages. The pattern `ServeMux.Handler` returns must be the same string. A wildcard the filler does not understand (`{name...}`, `{$}`, an empty name) fails instead of matching a different route.

The root mux also registers five composition mounts: `/api`, `/api/`, `/health`, `/health/`, and `/` (the SPA catch-all). That set is closed. A new `root.Handle` fails until the route has an isolation decision the live cross-check can see. Household routes belong on `apiMux` or the brand mux, not on a new root mount.

## How to add a case

1. Run `go test ./internal/server -run TestEveryRouteHasIsolationCase`. A new route fails with `unlisted route`.
2. Put the pattern in exactly one list in `isolation_cases_test.go`: `publicAllowlist`, `opsAllowlist`, `isolationCases`, or (only by removing something else, and only while it still waits on #377) `quarantine`.
3. A public entry needs a reason, and it must agree with `isPublicPath` / `isAuthEndpoint`. The test reads those functions and calls them. Adding a path to one side only fails the build.
4. An ops entry needs a reason and must not carry household data. The runner requests it and applies `assertNoLeak`. Do not park a household route here.
5. A real isolation case is an `isolationCases` entry with a `check` function. Drive the HTTP API as one household (`principal.Apply`) and call `assertNoLeak` with the other household. The kind is `foreign-id`, `list-leak`, `write-scope`, or `scoped-collection`.
6. A route that leaves quarantine is deleted from `quarantine` in the same change. Set `maxQuarantined` to the new length. Do not raise that constant.

Duplicate keys, a key with no route behind it, and a route missing from every list all fail `TestEveryRouteHasIsolationCase`.

## Quarantine shrinks only

`maxQuarantined` is the current size of `quarantine` (99). The test fails when the list is longer than that ceiling. It also fails when the list is shorter, until the constant is lowered to the new count in the same change, so the ceiling cannot drift back up.

New routes do not go into quarantine. They need a public allowlist entry, an ops allowlist entry, or a real isolation case. The 99 routes already listed stay there until their checks can pass, which waits on #377. `TestCoverageCheckRejectsUnlistedRoute`, `TestCoverageCheckRejectsDeletedCase`, and `TestCoverageCheckRejectsStaleAndGrowth` lock those failures.

## Invented names only

`newTwoHouseholds` builds Household Alder and Household Birch. Ids are fixed UUIDv7-shaped values, markers are sentinel strings (`alder-sentinel-k7m2q`, `birch-sentinel-q9p4w`), and barcodes are synthetic codes in the `200` in-store range. Never paste a real household name, a real barcode, a real account, or production data into this suite. The constructor rejects a shared marker, a non-v7 id, and a barcode outside that range.

## Assertions stay database-agnostic

Seed through the store API (`product.Catalog`), not SQL text in the test. Assert on the HTTP body with `assertNoLeak`. Do not use `rowid`, `datetime()`, `printf`, `INSERT OR REPLACE`, JSON1, or `PRAGMA` in this suite. Those are not portable to Postgres or DSQL.

When a later run selects the engine with `STOCKBOT_TEST_DB` (default `sqlite`), the same HTTP checks should run unchanged. A check that truly cannot leave SQLite, such as `secure_delete`, belongs behind a skip helper named for that engine so a Postgres or DSQL run skips it by name. Counts, if a future case needs them, are `SELECT COUNT(*) ... WHERE household_id = ?` with an explicit sort, not insertion order. This scaffold does not query the database.

## Acting as a household

`principal` is the seam. `Household()` returns the fixture household the call is on behalf of. `Apply` stamps the request.

Today the only implementation is `user1Principal`. The process has one account, `user-1`, and no household selector, so `Apply` records that principal and does not send a household id. When #377 adds scope, replace `Apply`. Call sites keep using `principalFor` and `Apply`.
