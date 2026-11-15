#!/usr/bin/env bash
# Regression tests for scripts/test-coverage.sh cover parsing and threshold ratchet.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SOURCE="${ROOT}/scripts/test-coverage.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

grep -q -F 'set -euo pipefail' "$SOURCE" || fail "test-coverage.sh must use pipefail"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

make_copy() {
  local threshold="$1"
  local dest="$2"
  sed "s/^COVERAGE_THRESHOLD=.*/COVERAGE_THRESHOLD=${threshold}/" "$SOURCE" >"$dest"
  chmod +x "$dest"
}

threshold_line() {
  grep '^COVERAGE_THRESHOLD=' "$1"
}

run_script() {
  local script="$1"
  local expect="$2"
  local dir="$tmp/cases/${3:?}"
  mkdir -p "$dir"
  set +e
  STOCKBOT_COVERAGE_SKIP_GO_TEST=1 \
    STOCKBOT_COVERAGE_SELF="$script" \
    bash "$script" >"$dir/stdout" 2>"$dir/stderr"
  local code=$?
  set -e
  if [ "$code" -ne "$expect" ]; then
    echo "stdout: $(cat "$dir/stdout")" >&2
    echo "stderr: $(cat "$dir/stderr")" >&2
    fail "${3} exited $code, want $expect"
  fi
  printf '%s' "$dir"
}

assert_threshold_unchanged() {
  local script="$1"
  local before="$2"
  local name="$3"
  local after
  after=$(threshold_line "$script")
  if [ "$before" != "$after" ]; then
    fail "$name changed threshold: before=$before after=$after"
  fi
}

script="$tmp/test-coverage.sh"
make_copy 85.1 "$script"
before=$(threshold_line "$script")

# (a) cover output with no total line -> non-zero, no ratchet
export STOCKBOT_COVER_FUNC_OUTPUT=$'stale/pkg.go:1: dead 0.0% of statements'
unset STOCKBOT_COVER_FUNC_CMD
no_total=$(run_script "$script" 1 no-total)
grep -q 'no total:' "$no_total/stderr" || fail "no-total missing error message"
assert_threshold_unchanged "$script" "$before" no-total

# (b) cover command exits non-zero -> non-zero, no ratchet
unset STOCKBOT_COVER_FUNC_OUTPUT
export STOCKBOT_COVER_FUNC_CMD='printf "cover: open stale: no such file\n" >&2; exit 1'
cmd_fail=$(run_script "$script" 1 cover-cmd-fail)
grep -q 'go tool cover -func failed' "$cmd_fail/stderr" || fail "cover-cmd-fail missing error message"
assert_threshold_unchanged "$script" "$before" cover-cmd-fail

# (c) normal output above threshold -> pass (within 0.5% so ratchet does not run)
export STOCKBOT_COVER_FUNC_OUTPUT=$'total: (statements) 85.5%'
unset STOCKBOT_COVER_FUNC_CMD
pass_dir=$(run_script "$script" 0 above-threshold)
grep -q 'PASS:' "$pass_dir/stdout" || fail "above-threshold did not pass"
assert_threshold_unchanged "$script" "$before" above-threshold

# (d) below threshold -> fail, no ratchet
export STOCKBOT_COVER_FUNC_OUTPUT=$'total: (statements) 80.0%'
below=$(run_script "$script" 1 below-threshold)
grep -q 'below threshold' "$below/stdout" || fail "below-threshold missing fail message"
assert_threshold_unchanged "$script" "$before" below-threshold

echo "test-coverage_test ok"