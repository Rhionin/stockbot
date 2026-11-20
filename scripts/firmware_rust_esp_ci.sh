#!/usr/bin/env bash
# CI helpers for the Rust ESP32-S3 scanner crate (issue #714).
# Pin file (added by the S7 crate PR): firmware/rust/esp-toolchain.env
# Expected keys (no guessed versions in this ticket):
#   ESPUP_VERSION, ESP_IDF_VERSION, ESPFLASH_VERSION, LDPROXY_VERSION

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
REPO_ROOT="${STOCKBOT_REPO_ROOT:-$ROOT}"
CRATE_CARGO="${REPO_ROOT}/firmware/rust/crates/stockbot-scanner-esp/Cargo.toml"
PIN_FILE="${REPO_ROOT}/firmware/rust/esp-toolchain.env"
PARTITIONS_CSV_REL="firmware/partitions_8mb_ota.csv"
MAX_APP_BYTES=2516582

usage() {
  cat <<'EOF'
Usage: firmware_rust_esp_ci.sh <command> [args]

Commands:
  detect                          GitHub Actions guard; writes ready= to GITHUB_OUTPUT
  load-pins                       Print KEY=value lines for GITHUB_ENV from the pin file
  verify-app-size <image>         Enforce max app size; append summary table
  verify-sdkconfig <sdkconfig>    Require rollback + partitions_8mb_ota.csv path
  verify-target-id <file> <id>    Require matrix target id in file (log or binary)
EOF
}

github_notice() {
  echo "::notice::$*"
}

github_error() {
  echo "::error::$*"
}

cmd_detect() {
  local out="${GITHUB_OUTPUT:-}"
  if [ -z "$out" ]; then
    github_error "GITHUB_OUTPUT is required for detect"
    exit 1
  fi
  : >"$out"
  if [ ! -f "$CRATE_CARGO" ]; then
    github_notice "firmware/rust/crates/stockbot-scanner-esp/Cargo.toml is absent; skipping ESP32-S3 build until slice S7 adds the crate"
    echo "ready=false" >>"$out"
    return 0
  fi
  if [ ! -f "$PIN_FILE" ]; then
    github_notice "firmware/rust/esp-toolchain.env is absent; skipping ESP32-S3 build until slice S7 pins espup and ESP-IDF versions"
    echo "ready=false" >>"$out"
    return 0
  fi
  echo "ready=true" >>"$out"
}

cmd_load_pins() {
  if [ ! -f "$PIN_FILE" ]; then
    github_error "missing pin file ${PIN_FILE#"$REPO_ROOT"/}"
    exit 1
  fi
  local key value
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%%#*}"
    line="${line#"${line%%[![:space:]]*}"}"
    [ -z "$line" ] && continue
    if [[ ! "$line" =~ ^[A-Z0-9_]+= ]]; then
      github_error "invalid pin line (expected KEY=value): $line"
      exit 1
    fi
    key="${line%%=*}"
    value="${line#*=}"
    value="${value%\"}"
    value="${value#\"}"
    if [ -z "$value" ]; then
      github_error "empty value for ${key} in esp-toolchain.env"
      exit 1
    fi
    printf '%s=%s\n' "$key" "$value"
  done <"$PIN_FILE"
  for required in ESPUP_VERSION ESP_IDF_VERSION ESPFLASH_VERSION LDPROXY_VERSION; do
    if ! grep -q "^${required}=" "$PIN_FILE"; then
      github_error "esp-toolchain.env missing required key ${required}"
      exit 1
    fi
  done
}

cmd_verify_app_size() {
  local image="${1:-}"
  if [ -z "$image" ] || [ ! -f "$image" ]; then
    github_error "verify-app-size: missing image file"
    exit 1
  fi
  local size
  size=$(wc -c <"$image" | tr -d ' ')
  local summary="${GITHUB_STEP_SUMMARY:-/dev/null}"
  {
    echo "## Firmware Rust ESP32-S3 app image"
    echo
    echo "| Image | Bytes | Limit |"
    echo "|---|---:|---:|"
    echo "| \`$(basename "$image")\` | ${size} | ${MAX_APP_BYTES} |"
  } >>"$summary"
  if [ "$size" -gt "$MAX_APP_BYTES" ]; then
    github_error "app image ${size} bytes exceeds limit ${MAX_APP_BYTES} (2.4 MiB OTA gate)"
    exit 1
  fi
  echo "app image size ok (${size} bytes)"
}

cmd_verify_sdkconfig() {
  local sdkconfig="${1:-}"
  if [ -z "$sdkconfig" ] || [ ! -f "$sdkconfig" ]; then
    github_error "verify-sdkconfig: missing sdkconfig file"
    exit 1
  fi
  if ! grep -qE '^CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE=y$' "$sdkconfig"; then
    github_error "sdkconfig missing CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE=y"
    exit 1
  fi
  if ! grep -qF "$PARTITIONS_CSV_REL" "$sdkconfig"; then
    github_error "sdkconfig does not reference ${PARTITIONS_CSV_REL} by path"
    exit 1
  fi
  echo "sdkconfig OTA partition and rollback checks ok"
}

target_id_token_pattern() {
  local expected="${1:?}"
  if [[ ! "$expected" =~ ^[a-z0-9_]+$ ]]; then
    github_error "verify-target-id: invalid target id ${expected}"
    exit 1
  fi
  printf '%s' "STOCKBOT_FW_TARGET=${expected}([^[:alnum:]_]|$)"
}

file_contains_target_token() {
  local file="${1:?}" expected="${2:?}"
  local pattern
  pattern="$(target_id_token_pattern "$expected")"
  if grep -a -qE "$pattern" "$file" 2>/dev/null; then
    return 0
  fi
  if command -v strings >/dev/null 2>&1; then
    strings "$file" 2>/dev/null | grep -qE "$pattern"
    return $?
  fi
  return 1
}

cmd_verify_target_id() {
  local file="${1:-}"
  local expected="${2:-}"
  if [ -z "$file" ] || [ -z "$expected" ] || [ ! -f "$file" ]; then
    github_error "verify-target-id: need readable file and expected target id"
    exit 1
  fi
  if file_contains_target_token "$file" "$expected"; then
    echo "embedded target id matches ${expected}"
    return 0
  fi
  github_error "target id ${expected} not found in ${file}"
  exit 1
}

main() {
  local cmd="${1:-}"
  shift || true
  case "$cmd" in
    detect) cmd_detect "$@" ;;
    load-pins) cmd_load_pins "$@" ;;
    verify-app-size) cmd_verify_app_size "$@" ;;
    verify-sdkconfig) cmd_verify_sdkconfig "$@" ;;
    verify-target-id) cmd_verify_target_id "$@" ;;
    -h | --help | help | "") usage; exit "${cmd:+0}"; [ -z "$cmd" ] && exit 1 ;;
    *)
      github_error "unknown command: $cmd"
      usage
      exit 1
      ;;
  esac
}

main "$@"