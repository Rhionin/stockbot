#!/usr/bin/env bash
# Fixture tests for firmware_rust_esp_ci.sh (no ESP toolchain required).

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SCRIPT="${ROOT}/scripts/firmware_rust_esp_ci.sh"
FIXTURES="${ROOT}/scripts/testdata/firmware_rust_esp"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

grep -q -F 'set -euo pipefail' "$SCRIPT" || fail "script must keep pipefail"

run_expect() {
  local name="$1" expect="$2"
  shift 2
  set +e
  "$@" >"$tmp/stdout" 2>"$tmp/stderr"
  local code=$?
  set -e
  if [ "$code" -ne "$expect" ]; then
    echo "--- $name stdout ---" >&2
    cat "$tmp/stdout" >&2
    echo "--- $name stderr ---" >&2
    cat "$tmp/stderr" >&2
    fail "$name exited $code, want $expect"
  fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# detect: absent crate → ready=false, success
detect_dir="$tmp/detect_absent"
mkdir -p "$detect_dir/firmware/rust/crates/stockbot-scanner-esp"
export GITHUB_OUTPUT="$detect_dir/output"
: >"$GITHUB_OUTPUT"
STOCKBOT_REPO_ROOT="$detect_dir" bash "$SCRIPT" detect
grep -q '^ready=false$' "$GITHUB_OUTPUT" || fail "absent crate must set ready=false"

# detect: crate present but pin missing
detect_pin="$tmp/detect_pin"
mkdir -p "$detect_pin/firmware/rust/crates/stockbot-scanner-esp"
touch "$detect_pin/firmware/rust/crates/stockbot-scanner-esp/Cargo.toml"
export GITHUB_OUTPUT="$detect_pin/output"
: >"$GITHUB_OUTPUT"
STOCKBOT_REPO_ROOT="$detect_pin" bash "$SCRIPT" detect
grep -q '^ready=false$' "$GITHUB_OUTPUT" || fail "missing pin file must set ready=false"

# detect: both present → ready=true
detect_ready="$tmp/detect_ready"
mkdir -p "$detect_ready/firmware/rust/crates/stockbot-scanner-esp"
touch "$detect_ready/firmware/rust/crates/stockbot-scanner-esp/Cargo.toml"
cat >"$detect_ready/firmware/rust/esp-toolchain.env" <<'EOF'
ESPUP_VERSION=1.0.0
ESP_IDF_VERSION=v5.0.0
ESPFLASH_VERSION=3.0.0
LDPROXY_VERSION=0.1.0
EOF
export GITHUB_OUTPUT="$detect_ready/output"
: >"$GITHUB_OUTPUT"
STOCKBOT_REPO_ROOT="$detect_ready" bash "$SCRIPT" detect
grep -q '^ready=true$' "$GITHUB_OUTPUT" || fail "crate + pins must set ready=true"

# Live repo guard: S7 crate and pin file not on master yet
export GITHUB_OUTPUT="$tmp/live_output"
: >"$GITHUB_OUTPUT"
bash "$SCRIPT" detect
grep -q '^ready=false$' "$tmp/live_output" || fail "master must skip ESP build until S7 lands"

export GITHUB_STEP_SUMMARY="$tmp/summary"

limit_ok="$tmp/limit_ok.bin"
limit_fail="$tmp/limit_fail.bin"
head -c 2516582 /dev/zero >"$limit_ok"
head -c 2516583 /dev/zero >"$limit_fail"

: >"$GITHUB_STEP_SUMMARY"
run_expect size_limit_ok 0 bash "$SCRIPT" verify-app-size "$limit_ok"
grep -q '2516582' "$GITHUB_STEP_SUMMARY" || fail "summary must mention size limit"

run_expect size_limit_fail 1 bash "$SCRIPT" verify-app-size "$limit_fail"

run_expect sdk_ok 0 bash "$SCRIPT" verify-sdkconfig "$FIXTURES/sdkconfig_ok"
run_expect sdk_rollback_fail 1 bash "$SCRIPT" verify-sdkconfig "$FIXTURES/sdkconfig_no_rollback"

run_expect target_ok 0 bash "$SCRIPT" verify-target-id "$FIXTURES/build_log_sticks3" sticks3_u173
run_expect target_fail 1 bash "$SCRIPT" verify-target-id "$FIXTURES/build_log_sticks3" atoms3lite_symcode_hid

incomplete="$tmp/incomplete"
mkdir -p "$incomplete/firmware/rust"
echo 'ESPUP_VERSION=1.0.0' >"$incomplete/firmware/rust/esp-toolchain.env"
run_expect load_pins_fail 1 env STOCKBOT_REPO_ROOT="$incomplete" bash "$SCRIPT" load-pins

echo "firmware_rust_esp_ci_test ok"