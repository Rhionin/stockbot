# Stockbot Frontend Development Guide

This directory contains the React frontend for Stockbot, built with TypeScript and Vite.

## Requirements

Node.js 24 or newer. `.nvmrc` pins `24` (run `nvm use` in this directory). Vitest crashes under Node 20 (jsdom/undici). `package.json` sets `"engines": { "node": ">=24" }` and `.npmrc` sets `engine-strict=true`, so `npm ci` on an older Node fails immediately.

## Local Development Workflow

Stockbot runs as two separate processes during development:

1. **Vite dev server** - serves the React frontend with hot module replacement
2. **Go API server** - serves the `/api` routes and embedded assets

### Starting Development

1. **Start the Go API server** (from the repository root):
   ```bash
   go run ./cmd/server
   ```
   This starts the server on the address specified by the `ADDR` environment variable (default: `:8080`).

2. **Start the Vite dev server** (from the `frontend/` directory):
   ```bash
   npm run dev
   ```
   This starts the Vite dev server, typically on `http://localhost:5173`.

3. **Browse to the Vite dev server URL** for development. The frontend will have hot module replacement and all the modern development features.

### API Proxy Configuration

The Vite dev server is configured in `vite.config.ts` to proxy `/api` requests to the Go server:

```typescript
server: {
  proxy: {
    '/api': {
      target: process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:8080',
    },
  },
}
```

This means that when the frontend makes same-origin requests to `/api/products`, `/api/scans`, etc., Vite forwards them to the Go server. This proxy configuration is why the frontend can use the same API paths in both development and production without environment-specific base URLs.

### Development vs Production Serving

- **Development**: Browse the Vite dev server port (usually `:5173`) to get the live UI with hot module replacement
- **Production**: The Go server serves the compiled frontend assets embedded in the binary

**Important**: Moving between dev-server serving and embedded serving requires no build step and no build flag. If you browse directly to the Go server's address during development (e.g., `http://localhost:8080`), you'll see the placeholder assets document rather than the live UI. This is expected behavior - the placeholder identifies itself so you know you're not looking at the development version.

## Available Scripts

### Development
- `npm run dev` - Start the Vite development server with hot module replacement
- `npm run preview` - Preview the production build locally

### Building
- `npm run build` - Build the frontend for production (runs TypeScript compilation and Vite build)

### Code Quality
- `npm run lint` - Run ESLint on all source files
- `npm test` - Run unit tests with Vitest in watch mode
- `npm run test:mutation` - Run Stryker mutation tests (see below)
- `npm run e2e` - Run end-to-end tests with Playwright

### Testing Approach

The frontend uses a multi-layered testing strategy:

1. **Unit Tests** (Vitest + React Testing Library)
   - Located alongside source files with `.test.tsx` extension
   - Test individual components and utility functions
   - Run with `npm test` (watch mode) or `npm test run` (single run)

2. **End-to-End Tests** (Playwright)
   - Located in the `e2e/` directory
   - Test complete user workflows across the full application
   - Run with `npm run e2e`

3. **Type Checking**
   - TypeScript provides compile-time type safety
   - Run explicitly with `npx tsc -b` or as part of the build process

4. **Mutation Testing** (Stryker + Vitest)
   - Checks that the unit tests fail when production code is changed
   - Run from this directory with `npm run test:mutation`
   - Does not run in pull-request CI

The test configuration includes:
- **jsdom** environment for unit tests (configured in `vite.config.ts`)
- **Setup file** at `./src/test-setup.ts` for test utilities and global configuration
- **@testing-library/jest-dom** for additional DOM matchers

## Mutation testing

Stryker (`@stryker-mutator/core` and `@stryker-mutator/vitest-runner`) mutates `src/**/*.ts` and `src/**/*.tsx`, excluding tests and `src/test-setup.ts`, then re-runs the related Vitest tests. Use Node 24 from `.nvmrc`. `@stryker-mutator/vitest-runner` is pinned to exact `10.0.0` (no version range) because [stryker-js#6210](https://github.com/stryker-mutator/stryker-js/issues/6210) is still open. A newer runner without a matching workaround reports false survivors.

```bash
npm run test:mutation
```

The HTML report is `reports/mutation/index.html` and the JSON report is `reports/mutation/mutation-report.json`. Both directories are gitignored.

### Scope a run

`--mutate` accepts a comma-separated list of globs, relative to this directory. npm forwards arguments after `--`.

```bash
# One module
npm run test:mutation -- --mutate src/utils/inventoryFilter.ts

# The utils directory. --mutate replaces the list in stryker.config.mjs,
# so repeat the test exclusion.
npm run test:mutation -- --mutate 'src/utils/**/*.ts,!src/utils/**/*.test.ts'

# Production sources changed on this branch (paths stay relative to frontend/)
npm run test:mutation -- --mutate "$(git diff --name-only --relative --diff-filter=ACMRT origin/master -- 'src/**/*.ts' 'src/**/*.tsx' | grep -v -e '\.test\.' -e 'test-setup.ts' | paste -sd, -)"
```

If the changed-file command prints nothing, omit `--mutate`. An empty value is not a valid file list.

Incremental mode reuses `reports/mutation/incremental.json` and only rechecks mutants whose code or covering tests changed:

```bash
npm run test:mutation -- --incremental --incrementalFile reports/mutation/incremental.json
```

Add `--force` to rebuild that file. Incremental results are only as honest as the run that wrote them, so use a full run when the Stryker or Vitest version changes.

### Vitest 5 and the test-name patch

`npm run test:mutation` applies `scripts/ensure-stryker-vitest5.mjs` before Stryker starts. The installed `@stryker-mutator/vitest-runner` must be exactly `10.0.0`. That release joins suite and test names with a space. Vitest 5 matches `testNamePattern` against the full name joined with ` > `, so the per-test filter matches nothing and covered mutants are reported as survivors after zero tests. The script patches both copies of `collectTestName` (test ids and dry-run coverage keys). Patching only one of them marks every mutant as having no coverage.

The script exits before Stryker starts when the installed version is not `10.0.0`, when either `collectTestName` file is missing, or when the function body is not the `10.0.0` text it knows how to patch. It does not continue into a run that would print a false 0% score.

The Vitest runner still returns that per-test coverage when `coverageAnalysis` is `off`, so the planner keeps filtering and the false survivors remain. The config therefore uses `perTest`.

Remove the pin and `scripts/ensure-stryker-vitest5.mjs` when a published runner includes the fix for [stryker-js#6210](https://github.com/stryker-mutator/stryker-js/issues/6210).

Do not invoke `npx stryker run` on its own. The score is only meaningful after the patch.

### Canary

`npm run test:mutation` runs `scripts/mutation-canary.mjs` before the requested mutate set. The canary mutation-tests only `src/utils/inventoryFilter.ts` and requires the line 13 `LogicalOperator` mutant (`||` replaced with `&&`) to be Killed with at least one test executed. Those unit tests already fail when that edit is applied by hand. The canary writes `reports/mutation/canary.json` and leaves the main report for the run that follows. A failure exits non-zero and skips the rest of the command.

### Optional GitHub workflow

`.github/workflows/frontend-mutation.yml` runs only when someone starts it from the Actions tab (`workflow_dispatch`). It does not run on pull requests. The default mutate glob is `src/utils/**/*.ts,!src/utils/**/*.test.ts`. The workflow input can widen it; include a `!` exclusion for tests, because the input replaces the list in `stryker.config.mjs`.

## Project Structure

- `src/` - Source code
  - `components/` - React components organized by feature
  - `api/` - API client and types
  - `assets/` - Static assets
- `e2e/` - End-to-end tests
- `public/` - Static public assets
- `dist/` - Build output (created by `npm run build`)

## Technology Stack

- **React 19** with TypeScript
- **Vite** for fast development and building
- **Mantine** for UI components
- **React Router** for client-side routing
- **Vitest** for unit testing
- **Stryker** for mutation testing
- **Playwright** for end-to-end testing
- **ESLint** for code linting
