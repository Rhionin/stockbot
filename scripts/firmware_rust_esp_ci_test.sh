#!/usr/bin/env bash
# Fixture tests for firmware_rust_esp_ci.sh (no ESP toolchain required).

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SCRIPT="${ROOT}/scripts/firmware_rust_esp_ci.sh"
FIXTURES="${ROOT}/scripts/testdata/firmware_rust_esp"
SCANNER_BASE="${ROOT}/firmware/rust/crates/stockbot-scanner-esp"

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

write_valid_pins() {
  local repo="$1"
  mkdir -p "$repo/firmware/rust"
  cp "$FIXTURES/pins_valid.env" "$repo/firmware/rust/esp-toolchain.env"
}

make_target_image() {
  local file="$1" id="$2"
  # Pad so grep -a scans a realistic binary-ish blob.
  printf 'pad stockbot-fw-target:%s tail' "$id" >"$file"
}

make_two_marker_image() {
  local file="$1"
  printf 'stockbot-fw-target:sticks3_u173 stockbot-fw-target:sticks3_u173_uart' >"$file"
}

make_zero_marker_image() {
  head -c 64 /dev/zero >"$1"
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# --- detect (tmp dirs only; never STOCKBOT_REPO_ROOT unset against live tree for skip) ---

detect_dir="$tmp/detect_absent"
mkdir -p "$detect_dir/firmware/rust/crates/stockbot-scanner-esp"
export GITHUB_OUTPUT="$detect_dir/output"
: >"$GITHUB_OUTPUT"
STOCKBOT_REPO_ROOT="$detect_dir" bash "$SCRIPT" detect
assert_detect_skip "$GITHUB_OUTPUT" "absent crate and pin"

detect_pin_only="$tmp/detect_pin_only"
mkdir -p "$detect_pin_only/firmware/rust/crates/stockbot-scanner-esp"
write_valid_pins "$detect_pin_only"
export GITHUB_OUTPUT="$detect_pin_only/output"
: >"$GITHUB_OUTPUT"
STOCKBOT_REPO_ROOT="$detect_pin_only" bash "$SCRIPT" detect
assert_detect_skip "$GITHUB_OUTPUT" "pin present, crate absent"

detect_crate_no_pin="$tmp/detect_crate_no_pin"
mkdir -p "$detect_crate_no_pin/firmware/rust/crates/stockbot-scanner-esp"
touch "$detect_crate_no_pin/firmware/rust/crates/stockbot-scanner-esp/Cargo.toml"
export GITHUB_OUTPUT="$detect_crate_no_pin/output"
: >"$GITHUB_OUTPUT"
run_expect detect_crate_without_pin_fail 1 env GITHUB_OUTPUT="$GITHUB_OUTPUT" \
  STOCKBOT_REPO_ROOT="$detect_crate_no_pin" bash "$SCRIPT" detect

detect_ready="$tmp/detect_ready"
mkdir -p "$detect_ready/firmware/rust/crates/stockbot-scanner-esp"
touch "$detect_ready/firmware/rust/crates/stockbot-scanner-esp/Cargo.toml"
write_valid_pins "$detect_ready"
export GITHUB_OUTPUT="$detect_ready/output"
: >"$GITHUB_OUTPUT"
STOCKBOT_REPO_ROOT="$detect_ready" bash "$SCRIPT" detect
assert_detect_ready "$GITHUB_OUTPUT" "crate + pins"

detect_stray="$tmp/detect_stray"
mkdir -p "$detect_stray/firmware/rust/wrong-crate"
cat >"$detect_stray/firmware/rust/wrong-crate/Cargo.toml" <<'EOF'
[package]
name = "stray"
version = "0.0.0"

[dependencies]
esp-idf-hal = "0.45"
EOF
export GITHUB_OUTPUT="$detect_stray/output"
: >"$GITHUB_OUTPUT"
run_expect detect_stray_esp_manifest_fail 1 env GITHUB_OUTPUT="$GITHUB_OUTPUT" \
  STOCKBOT_REPO_ROOT="$detect_stray" bash "$SCRIPT" detect

# --- app size gate ---

export GITHUB_STEP_SUMMARY="$tmp/summary"
limit_ok="$tmp/limit_ok.bin"
limit_fail="$tmp/limit_fail.bin"
head -c 2516582 /dev/zero >"$limit_ok"
head -c 2516583 /dev/zero >"$limit_fail"

: >"$GITHUB_STEP_SUMMARY"
run_expect size_limit_ok 0 bash "$SCRIPT" verify-app-size "$limit_ok"
grep -q '2516582' "$GITHUB_STEP_SUMMARY" || fail "summary must mention size limit"
run_expect size_limit_fail 1 bash "$SCRIPT" verify-app-size "$limit_fail"

# --- sdkconfig ---

[ -f "${ROOT}/firmware/partitions_8mb_ota.csv" ] || fail "repo partition CSV missing"

run_expect sdk_ok 0 bash "$SCRIPT" verify-sdkconfig "$FIXTURES/sdkconfig_ok" "$ROOT"
run_expect sdk_rollback_fail 1 bash "$SCRIPT" verify-sdkconfig "$FIXTURES/sdkconfig_no_rollback" "$ROOT"
run_expect sdk_bad_partition 1 bash "$SCRIPT" verify-sdkconfig "$FIXTURES/sdkconfig_bad_partition" "$ROOT"
run_expect sdk_single_app_fail 1 bash "$SCRIPT" verify-sdkconfig "$FIXTURES/sdkconfig_single_app" "$ROOT"
[ -d "$SCANNER_BASE" ] || mkdir -p "$SCANNER_BASE"
run_expect sdk_relative_partition_ok 0 bash "$SCRIPT" verify-sdkconfig \
  "$FIXTURES/sdkconfig_relative_partition" "$SCANNER_BASE"

find_one="$tmp/find_one"
mkdir -p "$find_one/nested"
cp "$FIXTURES/sdkconfig_ok" "$find_one/nested/sdkconfig"
run_expect find_sdkconfig_one 0 bash "$SCRIPT" find-sdkconfig "$find_one"
run_expect find_sdkconfig_zero 1 bash "$SCRIPT" find-sdkconfig "$tmp/empty_find"
mkdir -p "$find_one/other"
cp "$FIXTURES/sdkconfig_ok" "$find_one/other/sdkconfig"
run_expect find_sdkconfig_two 1 bash "$SCRIPT" find-sdkconfig "$find_one"

# --- verify-target-id (app image bytes, not build logs) ---

targets=(
  sticks3_u173
  sticks3_u173_uart
  atoms3lite_u173_bench
  atoms3lite_symcode_hid
)
for target in "${targets[@]}"; do
  img="$tmp/image_${target}.bin"
  make_target_image "$img" "$target"
  run_expect "target_ok_${target}" 0 bash "$SCRIPT" verify-target-id "$img" "$target"
done

img_sticks3="$tmp/image_sticks3.bin"
img_uart="$tmp/image_uart.bin"
make_target_image "$img_sticks3" "sticks3_u173"
make_target_image "$img_uart" "sticks3_u173_uart"
run_expect target_sticks3_vs_uart_fail 1 bash "$SCRIPT" verify-target-id "$img_uart" "sticks3_u173"
run_expect target_uart_vs_sticks3_fail 1 bash "$SCRIPT" verify-target-id "$img_sticks3" "sticks3_u173_uart"

for target in "${targets[@]}"; do
  zero="$tmp/zero_${target}.bin"
  two="$tmp/two_${target}.bin"
  make_zero_marker_image "$zero"
  make_two_marker_image "$two"
  run_expect "target_zero_${target}" 1 bash "$SCRIPT" verify-target-id "$zero" "$target"
  run_expect "target_two_${target}" 1 bash "$SCRIPT" verify-target-id "$two" "$target"
done

# --- load-pins ---

valid_load="$tmp/load_ok"
write_valid_pins "$valid_load"
run_expect load_pins_ok 0 env STOCKBOT_REPO_ROOT="$valid_load" bash "$SCRIPT" load-pins
run_expect load_pins_ok_capture 0 env STOCKBOT_REPO_ROOT="$valid_load" bash -c \
  "set -euo pipefail; pins=\$(bash \"$SCRIPT\" load-pins); [ \"\$(printf '%s\n' \"\$pins\" | grep -c '^[A-Z_]*=')\" -eq 6 ]"

incomplete="$tmp/incomplete"
mkdir -p "$incomplete/firmware/rust"
echo 'ESPUP_VERSION=1.0.0' >"$incomplete/firmware/rust/esp-toolchain.env"
run_expect load_pins_missing_required 1 env STOCKBOT_REPO_ROOT="$incomplete" bash "$SCRIPT" load-pins
run_expect load_pins_subst_missing_required 1 env STOCKBOT_REPO_ROOT="$incomplete" bash -c \
  "set -euo pipefail; pins=\$(bash \"$SCRIPT\" load-pins); printf '%s\n' \"\$pins\""

empty_val="$tmp/empty_val"
write_valid_pins "$empty_val"
sed -i 's/^ESP_IDF_VERSION=.*/ESP_IDF_VERSION=/' "$empty_val/firmware/rust/esp-toolchain.env"
run_expect load_pins_empty_value 1 env STOCKBOT_REPO_ROOT="$empty_val" bash "$SCRIPT" load-pins

invalid_line="$tmp/invalid_line"
write_valid_pins "$invalid_line"
printf 'not-valid-line\n' >>"$invalid_line/firmware/rust/esp-toolchain.env"
run_expect load_pins_invalid_line 1 env STOCKBOT_REPO_ROOT="$invalid_line" bash "$SCRIPT" load-pins

unknown_key="$tmp/unknown_key"
write_valid_pins "$unknown_key"
echo 'BASH_ENV=1.0.0' >>"$unknown_key/firmware/rust/esp-toolchain.env"
run_expect load_pins_unknown_key 1 env STOCKBOT_REPO_ROOT="$unknown_key" bash "$SCRIPT" load-pins

latest_pins="$tmp/latest_pins"
write_valid_pins "$latest_pins"
sed -i 's/^ESPUP_VERSION=.*/ESPUP_VERSION=latest/' "$latest_pins/firmware/rust/esp-toolchain.env"
run_expect load_pins_latest_value 1 env STOCKBOT_REPO_ROOT="$latest_pins" bash "$SCRIPT" load-pins

dup_pins="$tmp/dup_pins"
write_valid_pins "$dup_pins"
echo 'ESPUP_VERSION=1.0.0' >>"$dup_pins/firmware/rust/esp-toolchain.env"
run_expect load_pins_duplicate 1 env STOCKBOT_REPO_ROOT="$dup_pins" bash "$SCRIPT" load-pins

crlf_pins="$tmp/crlf_pins"
mkdir -p "$crlf_pins/firmware/rust"
printf 'ESPUP_VERSION=0.19.0\r\nESP_RUST_VERSION=1.86.0.0\r\nESP_IDF_VERSION=v5.3.2\r\nESPFLASH_VERSION=3.3.0\r\nLDPROXY_VERSION=0.1.0\r\nESP_HOST_RUST_VERSION=1.86.0\r\n' \
  >"$crlf_pins/firmware/rust/esp-toolchain.env"
run_expect load_pins_crlf_ok 0 env STOCKBOT_REPO_ROOT="$crlf_pins" bash "$SCRIPT" load-pins

comment_pins="$tmp/comment_pins"
write_valid_pins "$comment_pins"
sed -i '1i# toolchain pins for CI' "$comment_pins/firmware/rust/esp-toolchain.env"
run_expect load_pins_comments_ok 0 env STOCKBOT_REPO_ROOT="$comment_pins" bash "$SCRIPT" load-pins

echo "firmware_rust_esp_ci_test ok"