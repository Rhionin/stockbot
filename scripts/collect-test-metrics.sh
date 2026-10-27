#!/usr/bin/env bash
# Collect the coverage inputs count-tests.py reads and write one report.
#
# The Go suite is run twice: once without -coverpkg (package-local coverage,
# for flag C) and once with -coverpkg over the module (the density denominator).
# Vitest writes a v8 istanbul coverage-final.json. A failing test does not
# throw the coverage away; density is about the lines that ran.
#
# Usage: scripts/collect-test-metrics.sh <repo> <output-dir> [count-tests.py]
# Exits 0 when <output-dir>/metrics.json was written and Vitest wrote
# coverage-final.json. Missing web coverage is a hard error. A warning used
# to let that file disappear while the metrics job stayed green.

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
web_missing=0
web_pid=""

run_vitest_coverage() {
  local repo="$1"
  local out="$2"
  pushd "$repo/frontend" >/dev/null
  npm ci
  local npm_ci_status=$?
  if [ "$npm_ci_status" -ne 0 ]; then
    echo "::error::npm ci failed (${npm_ci_status}); web coverage cannot be collected" >&2
    popd >/dev/null
    return "$npm_ci_status"
  fi
  # @vitest/coverage-v8 is an optional peer of vitest and must be the same
  # version. It is not a direct dependency, so the npm Dependabot group never
  # sees it. The lockfile's vitest is the only pin. A literal @5.0.3 here
  # stayed behind the bump to vitest 5.0.5, npm refused the install
  # (ERESOLVE: peer vitest@5.0.3), and web density was reported as zero.
  # --no-save keeps package.json untouched. Do not pass --no-package-lock:
  # that flag ignores the lockfile and re-resolves the rest of the tree.
  local vitest_version=""
  if ! vitest_version=$(node -p "require('./package-lock.json').packages['node_modules/vitest'].version"); then
    echo "::error::could not read vitest from frontend/package-lock.json" >&2
    popd >/dev/null
    return 1
  fi
  if [ -z "$vitest_version" ] || [ "$vitest_version" = "undefined" ]; then
    echo "::error::vitest is missing from frontend/package-lock.json" >&2
    popd >/dev/null
    return 1
  fi
  if ! npm install --no-save "@vitest/coverage-v8@${vitest_version}"; then
    echo "::error::could not install @vitest/coverage-v8@${vitest_version} to match locked vitest" >&2
    popd >/dev/null
    return 1
  fi
  # shared/ sits outside this package. allowExternal lets v8 count it, and
  # the test files stay out of the production denominator.
  npx vitest --run \
    --coverage.enabled \
    --coverage.provider=v8 \
    --coverage.reporter=json \
    --coverage.reportsDirectory="$out/vitest-coverage" \
    --coverage.allowExternal=true \
    --coverage.include='src/**/*.{ts,tsx}' \
    --coverage.include='../shared/**/*.{ts,tsx}' \
    --coverage.exclude='../shared/**/*.test.{ts,tsx}' \
    --coverage.reportOnFailure \
    --reporter=default \
    --reporter=json \
    --outputFile="$out/vitest-results.json"
  local status=$?
  popd >/dev/null
  return "$status"
}

echo "Starting Vitest coverage in the background..."
(
  set +e
  run_vitest_coverage "$REPO" "$OUT"
  echo $? >"$OUT/vitest.exit"
) >"$OUT/vitest.log" 2>&1 &
web_pid=$!

pushd "$REPO" >/dev/null
mapfile -t PKGS < <(go list ./... | grep -v '/node_modules/')
COVERPKG=$(IFS=,; echo "${PKGS[*]}")
echo "Running package-local Go coverage..."
go test -count=1 -json -coverprofile="$OUT/go-local.cover" "${PKGS[@]}" >"$OUT/go-local.json"
go_local_status=$?
echo "Running cross-package Go coverage..."
go test -count=1 -json -coverpkg="${COVERPKG}" -coverprofile="$OUT/go.cover" "${PKGS[@]}" >"$OUT/go-test.json"
go_status=$?
popd >/dev/null

if [ "$go_local_status" -ne 0 ]; then
  echo "::warning::package-local go test exited ${go_local_status}; flag C uses whatever profile was written"
fi
if [ "$go_status" -ne 0 ]; then
  echo "::warning::cross-package go test exited ${go_status}; density uses whatever profile was written"
fi

echo "Waiting for Vitest coverage (pid ${web_pid})..."
if ! wait "$web_pid"; then
  echo "::warning::vitest background shell exited non-zero"
fi
if [ -f "$OUT/vitest.exit" ]; then
  web_status=$(cat "$OUT/vitest.exit")
else
  web_status=1
fi
echo "Vitest log:"
cat "$OUT/vitest.log"

if [ "$web_status" -ne 0 ]; then
  echo "::warning::vitest exited ${web_status}"
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
else
  echo "::error::web coverage was not produced at ${web_final}" >&2
  web_missing=1
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
if [ "$web_missing" -ne 0 ]; then
  echo "web coverage was not produced; the metrics job cannot report web density" >&2
  exit 1
fi
exit 0
