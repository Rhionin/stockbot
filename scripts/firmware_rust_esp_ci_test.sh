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

assert_detect_skip() {
  local out="$1" label="$2"
  grep -q '^ready=false$' "$out" || fail "${label}: must set ready=false"
  if grep -q '^ready=true$' "$out"; then
    fail "${label}: must not set ready=true"
  fi
}

assert_detect_ready() {
  local out="$1" label="$2"
  grep -q '^ready=true$' "$out" || fail "${label}: must set ready=true"
  if grep -q '^ready=false$' "$out"; then
    fail "${label}: must not set ready=false"
  fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# detect: absent crate, pin absent
detect_dir="$tmp/detect_absent"
mkdir -p "$detect_dir/firmware/rust/crates/stockbot-scanner-esp"
export GITHUB_OUTPUT="$detect_dir/output"
: >"$GITHUB_OUTPUT"
STOCKBOT_REPO_ROOT="$detect_dir" bash "$SCRIPT" detect
assert_detect_skip "$GITHUB_OUTPUT" "absent crate and pin"

# detect: pin present, crate absent (no Cargo.toml)
detect_pin_only="$tmp/detect_pin_only"
mkdir -p "$detect_pin_only/firmware/rust/crates/stockbot-scanner-esp"
cat >"$detect_pin_only/firmware/rust/esp-toolchain.env" <<'EOF'
ESPUP_VERSION=1.0.0
ESP_IDF_VERSION=v5.0.0
ESPFLASH_VERSION=3.0.0
LDPROXY_VERSION=0.1.0
EOF
export GITHUB_OUTPUT="$detect_pin_only/output"
: >"$GITHUB_OUTPUT"
STOCKBOT_REPO_ROOT="$detect_pin_only" bash "$SCRIPT" detect
assert_detect_skip "$GITHUB_OUTPUT" "pin present, crate absent"

# detect: crate present but pin missing
detect_pin="$tmp/detect_pin"
mkdir -p "$detect_pin/firmware/rust/crates/stockbot-scanner-esp"
touch "$detect_pin/firmware/rust/crates/stockbot-scanner-esp/Cargo.toml"
export GITHUB_OUTPUT="$detect_pin/output"
: >"$GITHUB_OUTPUT"
STOCKBOT_REPO_ROOT="$detect_pin" bash "$SCRIPT" detect
assert_detect_skip "$GITHUB_OUTPUT" "missing pin file"

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
assert_detect_ready "$GITHUB_OUTPUT" "crate + pins"

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
run_expect sdk_bad_partition 1 bash "$SCRIPT" verify-sdkconfig "$FIXTURES/sdkconfig_bad_partition"

targets=(
  "sticks3_u173:build_log_sticks3"
  "sticks3_u173_uart:build_log_sticks3_uart"
  "atoms3lite_u173_bench:build_log_atoms3lite_u173_bench"
  "atoms3lite_symcode_hid:build_log_atoms3lite_symcode_hid"
)
for entry in "${targets[@]}"; do
  target="${entry%%:*}"
  log="${entry##*:}"
  run_expect "target_ok_${target}" 0 bash "$SCRIPT" verify-target-id "$FIXTURES/${log}" "$target"
done

run_expect target_sticks3_vs_uart_fail 1 bash "$SCRIPT" verify-target-id "$FIXTURES/build_log_sticks3_uart" sticks3_u173
run_expect target_uart_vs_sticks3_fail 1 bash "$SCRIPT" verify-target-id "$FIXTURES/build_log_sticks3" sticks3_u173_uart

incomplete="$tmp/incomplete"
mkdir -p "$incomplete/firmware/rust"
echo 'ESPUP_VERSION=1.0.0' >"$incomplete/firmware/rust/esp-toolchain.env"
run_expect load_pins_missing_required 1 env STOCKBOT_REPO_ROOT="$incomplete" bash "$SCRIPT" load-pins

# Workflow reproduction: redirect must surface load-pins failure (not hidden by process substitution)
run_expect load_pins_redirect_missing_required 1 env STOCKBOT_REPO_ROOT="$incomplete" bash -c \
  "set -euo pipefail; bash \"$SCRIPT\" load-pins >\"$tmp/pins.env\""

empty_val="$tmp/empty_val"
mkdir -p "$empty_val/firmware/rust"
cat >"$empty_val/firmware/rust/esp-toolchain.env" <<'EOF'
ESPUP_VERSION=1.0.0
ESP_IDF_VERSION=
ESPFLASH_VERSION=3.0.0
LDPROXY_VERSION=0.1.0
EOF
run_expect load_pins_empty_value 1 env STOCKBOT_REPO_ROOT="$empty_val" bash "$SCRIPT" load-pins

invalid_line="$tmp/invalid_line"
mkdir -p "$invalid_line/firmware/rust"
cat >"$invalid_line/firmware/rust/esp-toolchain.env" <<'EOF'
not-valid-line
ESPUP_VERSION=1.0.0
ESP_IDF_VERSION=v5.0.0
ESPFLASH_VERSION=3.0.0
LDPROXY_VERSION=0.1.0
EOF
run_expect load_pins_invalid_line 1 env STOCKBOT_REPO_ROOT="$invalid_line" bash "$SCRIPT" load-pins

echo "firmware_rust_esp_ci_test ok"