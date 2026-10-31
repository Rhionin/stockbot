#!/usr/bin/env bash
# Pins the path regex in .github/workflows/web.yml against fixtures so trigger
# paths stay aligned with web Vitest/lint/e2e (see Web workflow changes job).
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORKFLOW="$ROOT/.github/workflows/web.yml"

pattern=$(
  awk -F"'" '/^[[:space:]]*pattern=/ { print $2; exit }' "$WORKFLOW"
)
if [ -z "$pattern" ]; then
  echo "Could not read pattern= from $WORKFLOW" >&2
  exit 1
fi

matches() {
  printf '%s\n' "$1" | grep -Eq "$pattern"
}

must_match=(
  docs/release-notes/2026-10-25.md
  mobile/App.tsx
  eslint.config.mjs
  docs/design/api-contract.md
  frontend/x
  shared/x
  internal/x
  cmd/x
  testdata/x
  go.mod
  go.sum
  .github/workflows/web.yml
)

must_not_match=(
  docs/other.md
  docs/foo/bar.md
  README.md
  .github/workflows/ci.yml
)

fail=0
for path in "${must_match[@]}"; do
  if ! matches "$path"; then
    echo "expected to match: $path" >&2
    fail=1
  fi
done
for path in "${must_not_match[@]}"; do
  if matches "$path"; then
    echo "expected not to match: $path" >&2
    fail=1
  fi
done

if [ "$fail" -ne 0 ]; then
  echo "pattern: $pattern" >&2
  exit 1
fi

echo "check-web-change-pattern ok"
