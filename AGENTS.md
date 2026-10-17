# Agent Guidelines

## Avoid Asking
Never use shell find or grep for discovery; use built-in search tools. If there's already a way to find / modify code, use that instead of asking to use gated tools like sed, find, and grep. I want to make as few approvals as possible.

## File Editing

Use built-in file editing tools (read_file, write_file, str_replace) to read and modify code directly. Do not fall back to shell commands (cat, echo, heredocs, python -c, etc.) for file I/O — that requires unnecessary user approval and is more error-prone.

## Testing

After each task list, run `./scripts/test-coverage.sh` to enforce coverage thresholds. The script auto-updates its threshold when coverage increases, so that it can be committed with the rest of the code changes and enforced.

Prefer API tests using the apitest framework in `internal/server/`. These exercise behavior from the customer's perspective and often eliminate the need for lower-level unit tests. Write unit tests only when API tests are insufficient (e.g., testing internal algorithms, edge cases in pure functions).

Test reproducible errors (bad requests, validation failures) but skip internal error paths (database errors, network timeouts) that API callers can't trigger. Focus coverage on customer-facing behavior.

Handler tests use a declarative table-driven framework with HTTP exchanges. Use `afterRequest: exchanges()` to verify behavior through subsequent HTTP requests rather than direct database queries. Test the HTTP contract, not implementation details. Direct DB queries in `afterRequest` are only acceptable when the data being verified is not exposed through any API endpoint.

### Handler test framework

Handler tests use `handlerTestCase` and `runHandlerTests` (defined in `test_runner_test.go`). Do not introduce separate test case types.

The `setup` and `afterRequest` callbacks both receive a `testEnv` struct:

```go
type testEnv struct {
    T             *testing.T
    DB            *sql.DB
    ProductStore  *product.Catalog
    OpenFoodFacts *fakeOpenFoodFacts
    Res           *http.Response // populated only inside afterRequest callbacks
}
```

The `testEnv.DB` is the **same connection** the handler under test uses, so rows inserted in `setup` are immediately visible to the handler.

```go
handlerTestCase{
    name: "example",
    setup: func(env testEnv) {
        // seed state — env.DB, env.ProductStore, env.T all available
    },
    httpExchange: httpExchange{...},
    afterRequest: exchanges(
        httpExchange{...}, // verify state through HTTP, not DB queries
    ),
}
```

Use `exchanges()` for `afterRequest` whenever the effect is observable via the HTTP API. Direct DB queries are a last resort for internal state that no endpoint exposes (e.g. timestamp precision checks).

## Database Setup in Tests

Use `app.RunMigrations(conn)` to initialize test databases—never duplicate the schema inline. The `newTestPantry` helper in `internal/inventory/inventory_test.go` is the canonical pattern for the inventory package. Other packages follow the same convention (see `newTestCatalog` in `internal/product/product_test.go`, `newTestQueue` in `internal/scan/scan_test.go`, `newTestConsumptionLog` in `internal/suggestion/suggestion_test.go`).

## Naming

Data-access types are named for *what* they store, not *how* they store it — never `Repo` or generic `Store`. Each feature package has exactly one data-access type, named after its domain concept:

| Package      | Type              | Constructor           |
|---------------|--------------------|------------------------|
| `product`     | `Catalog`          | `NewCatalog`           |
| `inventory`   | `Pantry`           | `NewPantry`            |
| `scan`        | `Queue`            | `NewQueue`             |
| `suggestion`  | `ConsumptionLog`   | `NewConsumptionLog`    |
| `shopping`    | `Store`            | `NewStore`             |

`shopping.Store` is acceptable because it's namespaced by the `shopping` package (there's no ambiguity about what it stores) and that package has only one data-access type. Outside its own package, or in a handler struct field, disambiguate by what it holds — e.g. a handler depending on the shopping store names the field `ShoppingList`, not `Store`. Local variables follow the same rule: `catalog`, `pantry`, `scanQueue`, `consumptionLog`, `shoppingList` — never `repo` or generic `store`.

## Code Style

Error messages should be user-friendly without function names. Avoid redundant comments that restate code—keep godoc and WHY comments, remove WHAT comments and numbered steps.

## UI work

The web UI is React, Vite, and Mantine (`@mantine/core`, plus dates, hooks, and notifications). `frontend/src/main.tsx` wraps the app in `MantineProvider`. Build and revise screens with Mantine components and this repo's existing theme and layout patterns.

Also follow the `frontend-design` skill when the work is visual: new UI, or a reshape of an existing screen. The skill is the upstream Anthropic skill, unmodified, including `LICENSE.txt`:

- `.cursor/skills/frontend-design/SKILL.md`
- `.agents/skills/frontend-design/SKILL.md` (same files, installed with `npx skills add anthropics/skills --skill frontend-design`; `skills-lock.json` records the source)

Apply that skill's guidance on palette, type, layout, copy, and critique through Mantine components, the existing theme, and the conventions in this file. The skill complements Mantine; screens stay on Mantine components and the project theme.

## Running the app for Playwright MCP

Local development is two processes. From the repository root, start the API:

```bash
go run ./cmd/server
```

It listens on `ADDR` (default `:8080`). In another terminal, from `frontend/`:

```bash
npm run dev
```

Vite serves the live UI, usually at `http://127.0.0.1:5173`, and proxies `/api` and `/brand` to the Go server (`frontend/vite.config.ts`, override with `VITE_API_PROXY_TARGET`). Open that Vite URL for layout checks. During development the Go address serves the placeholder assets document; the live UI is the Vite origin.

Playwright MCP is the `playwright` server in `.cursor/mcp.json`. The server command is `npx @playwright/mcp@latest`, matching the [Playwright MCP getting-started guide](https://playwright.dev/docs/next/getting-started-mcp). After both processes are up, point that browser at the Vite origin (the URL Vite printed, typically `http://127.0.0.1:5173`) and use its snapshot and screenshot tools to check layout. That origin proxies API calls, so the page is the running app. On a machine with no display, add `--headless` to the server args in `.cursor/mcp.json`.

## Frontend Node version

The frontend requires Node.js 24. `frontend/.nvmrc` pins `24`. Vitest crashes under Node 20 (jsdom/undici). `frontend/package.json` sets `"engines": { "node": ">=24" }` and `frontend/.npmrc` sets `engine-strict=true`, so `npm ci` on an older Node fails immediately.

## Cursor Cloud specific instructions

Go coverage is `./scripts/test-coverage.sh` (needs `bc` and the Go version in `go.mod`). Frontend checks from `frontend/` need Node 24 (`frontend/.nvmrc`; Vitest crashes under Node 20): `npm test` (`vitest --run`), `npm run lint`, and `npx tsc -b`. `npm ci` installs frontend dependencies. The Cloud Agent image is `.cursor/Dockerfile`, referenced by `.cursor/environment.json`.
