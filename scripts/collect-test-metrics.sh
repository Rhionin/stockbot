#!/usr/bin/env bash
# Collect the coverage inputs count-tests.py reads and write one report.
#
# The Go suite is run twice: once without -coverpkg (package-local coverage,
# for flag C) and once with -coverpkg over the module (the density denominator).
# Vitest writes a v8 istanbul coverage-final.json. A failing test does not
# throw the coverage away; density is about the lines that ran.
#
# Usage: scripts/collect-test-metrics.sh <repo> <output-dir> [count-tests.py]
# Exits 0 when <output-dir>/metrics.json was written.

set -u

if [ "$#" -lt 2 ]; then
  echo "usage: collect-test-metrics.sh <repo> <output-dir> [count-tests.py]" >&2
  exit 1
fi

REPO=$(cd "$1" && pwd)
OUT=$2
SCRIPT=${3:-"$REPO/scripts/count-tests.py"}
mkdir -p "$OUT"

if [ ! -f "$SCRIPT" ]; then
  echo "count-tests.py not found at $SCRIPT" >&2
  exit 1
fi

go_status=0
web_status=0

pushd "$REPO" >/dev/null
mapfile -t PKGS < <(go list ./... | grep -v '/node_modules/')
COVERPKG=$(IFS=,; echo "${PKGS[*]}")
go test -count=1 -json -coverprofile="$OUT/go-local.cover" "${PKGS[@]}" >"$OUT/go-local.json"
go_local_status=$?
go test -count=1 -json -coverpkg="${COVERPKG}" -coverprofile="$OUT/go.cover" "${PKGS[@]}" >"$OUT/go-test.json"
go_status=$?
popd >/dev/null

if [ "$go_local_status" -ne 0 ]; then
  echo "::warning::package-local go test exited ${go_local_status}; flag C uses whatever profile was written"
fi
if [ "$go_status" -ne 0 ]; then
  echo "::warning::cross-package go test exited ${go_status}; density uses whatever profile was written"
fi

pushd "$REPO/frontend" >/dev/null
npm ci
npm install --no-save --no-package-lock @vitest/coverage-v8@5.0.3
npx vitest --run \
  --coverage.enabled \
  --coverage.provider=v8 \
  --coverage.reporter=json \
  --coverage.reportsDirectory="$OUT/vitest-coverage" \
  --coverage.include='src/**/*.{ts,tsx}' \
  --coverage.reportOnFailure \
  --reporter=default \
  --reporter=json \
  --outputFile="$OUT/vitest-results.json"
web_status=$?
popd >/dev/null

if [ "$web_status" -ne 0 ]; then
  echo "::warning::vitest exited ${web_status}; web density uses coverage-final.json if it was written"
fi

args=(report --repo "$REPO" --json "$OUT/metrics.json" --top 8)
if [ -s "$OUT/go.cover" ]; then
  args+=(--go-cover "$OUT/go.cover")
fi
if [ -s "$OUT/go-local.cover" ]; then
  args+=(--go-cover-local "$OUT/go-local.cover")
fi
if [ -s "$OUT/go-test.json" ]; then
  args+=(--go-json "$OUT/go-test.json")
fi
web_final="$OUT/vitest-coverage/coverage-final.json"
if [ -s "$web_final" ]; then
  args+=(--web-cover "$web_final")
fi
if [ -s "$OUT/vitest-results.json" ]; then
  args+=(--web-json "$OUT/vitest-results.json")
fi

python3 "$SCRIPT" "${args[@]}" | tee "$OUT/metrics.txt"
report_status=${PIPESTATUS[0]}

printf '{"go_local_exit":%s,"go_test_exit":%s,"web_test_exit":%s,"report_exit":%s}\n' \
  "$go_local_status" "$go_status" "$web_status" "$report_status" >"$OUT/status.json"

if [ "$report_status" -ne 0 ] || [ ! -s "$OUT/metrics.json" ]; then
  echo "metrics.json was not written" >&2
  exit 1
fi
exit 0
