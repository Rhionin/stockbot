# Test Coverage Scripts

This directory contains scripts for managing test coverage in the Stockbot project.

## Scripts

### test-coverage.sh

Runs all tests with coverage and enforces a minimum coverage threshold.

```bash
./scripts/test-coverage.sh
```

**Features:**
- Runs tests with `-coverpkg=./...` to measure coverage across all packages
- Calculates total line coverage percentage
- Enforces minimum coverage threshold (configurable)
- Exits with non-zero code if tests fail or coverage is below threshold
- **Automatically updates the threshold** when actual coverage pulls more than 0.5% ahead of it (ratchet mechanism)

**Configuration:**
The `COVERAGE_THRESHOLD` variable is set at the top of the script. 

**Important:** When the script detects that actual coverage has moved more than 0.5% above the current threshold, it automatically updates itself to use the new, higher coverage value as the threshold. The 0.5% gap gives coverage some wiggle room to drift up and down in small doses during development without tripping the ratchet, while still enforcing a generally-increasing baseline over time. When the threshold does update, commit the updated script file to preserve the new baseline.

### show-coverage.sh

Runs tests and displays coverage without enforcement. Useful during development.

```bash
./scripts/show-coverage.sh

# Generate HTML coverage report
./scripts/show-coverage.sh --html
```

**Features:**
- Shows total coverage percentage
- Optional HTML report generation with `--html` flag
- No threshold enforcement - purely informational

### count-tests.py

Advisory test calorie density. It never fails a build and never blocks a merge.

```bash
python3 scripts/count_tests_test.py

scripts/collect-test-metrics.sh . /tmp/stockbot-metrics
python3 scripts/count-tests.py diff /tmp/base/metrics.json /tmp/head/metrics.json \
  --base-repo /tmp/base-tree --head-repo /tmp/head-tree
```

**Density** is non-blank, non-comment test lines divided by production lines a test actually ran. Lower means fewer test lines per line that ran. The headline is per language, plus an overall unit total that sums those lines. It is not the average of the two densities.

| | What counts |
| --- | --- |
| Test LOC | Go `*_test.go`. Web unit: `frontend/src/**/*.test.{ts,tsx}`, `frontend/src/test-setup.ts`, and `shared/**/*.test.ts`. |
| Covered prod LOC | Go: code lines inside a `-coverpkg` coverprofile block with count > 0. Web: code lines that start a statement with hits > 0 in Vitest v8 `coverage-final.json`. |
| E2E | `frontend/e2e/**/*.ts`, reported on its own and left out of unit density. |
| Excluded | `node_modules`, `vendor`, `dist`, `build`, `coverage`, `testdata/`, `fixtures/`, `__snapshots__/`, `*.snap`, `*.bak`, `*.d.ts`, and Go files whose header is `// Code generated … DO NOT EDIT.` |

A line with code and a trailing comment counts. A `//` inside a string on that same line does not start a comment. Block comments continue across lines. A raw string or template quote does not: the next physical line is scanned on its own. That is the v1.0 rule the 2026-10-17 master baseline was measured with (Go about 2.62, web unit about 3.15, overall about 2.74).

`diff` reports marginal density, which is the test lines added between two reports divided by the covered production lines added. It is undefined when covered lines did not increase.

| Flag | Meaning |
| --- | --- |
| A | Marginal density is above twice that baseline: Go > 5.2, web > 6.3. |
| B | Not computed. The hook is in `flag_b_todo()`: pair the delta with diff-scoped mutation scores (gremlins for Go, Stryker for web) before reading a shorter suite as weaker. |
| C | A new production line is covered, and the tests in its own package did not cover it. Go uses a second, package-local coverprofile (`go test` without `-coverpkg`). Web, unless a package-local coverage file is passed, counts only new covered lines in a directory that has no test file. |

`vi.fn(` is the web mock count (the call with the parenthesis). `rapid.Check(` and `fc.assert(`, `.prop(`, `fc.property(` are the property-test counts. Go sleeps are `time.Sleep(`. Assertions per 100 test lines use `t.Error` / `t.Fatal` (and the f variants), `assert.` / `require.`, and web `expect(`.

`.github/workflows/test-metrics.yml` runs this on pull requests (one sticky comment) and on a weekly schedule plus `workflow_dispatch` (the metrics JSON is uploaded as an artifact). On a pull request the head tree is the merge commit and the base tree is that commit's first parent, so other commits that landed on the base branch are not part of the delta. The workflow is not `ci.yml`, and a failure there does not fail the build. The metrics job itself fails when Vitest does not write `coverage-final.json`, so a missing web density is not a green job with a warning. `@vitest/coverage-v8` is installed at the vitest version in `frontend/package-lock.json`, which keeps it on the same release as vitest when Dependabot bumps the lockfile.

## Usage Guidelines

- Run `test-coverage.sh` after completing each task (required by AGENTS.md)
- Use `show-coverage.sh` during development to check current coverage
- Use `show-coverage.sh --html` to see detailed line-by-line coverage
