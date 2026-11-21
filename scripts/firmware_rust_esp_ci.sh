#!/usr/bin/env bash
# CI helpers for the Rust ESP32-S3 scanner crate (issue #714).
# Pin file (S7 crate PR): firmware/rust/esp-toolchain.env — six required keys:
#   ESPUP_VERSION, ESP_RUST_VERSION, ESP_IDF_VERSION, ESPFLASH_VERSION,
#   LDPROXY_VERSION, ESP_HOST_RUST_VERSION (unquoted exact semver pins; no guessing here).
# S7 crate must embed a NUL-terminated `stockbot-fw-target:<id>` string in the app image.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
REPO_ROOT="${STOCKBOT_REPO_ROOT:-$ROOT}"
CRATE_CARGO="${REPO_ROOT}/firmware/rust/crates/stockbot-scanner-esp/Cargo.toml"
PIN_FILE="${REPO_ROOT}/firmware/rust/esp-toolchain.env"
PARTITIONS_CSV_REL="firmware/partitions_8mb_ota.csv"
MAX_APP_BYTES=2516582
TARGET_MARKER_PREFIX="stockbot-fw-target:"

ALLOWED_PIN_KEYS=(
  ESPUP_VERSION
  ESP_RUST_VERSION
  ESP_IDF_VERSION
  ESPFLASH_VERSION
  LDPROXY_VERSION
  ESP_HOST_RUST_VERSION
)

usage() {
  cat <<'EOF'
Usage: firmware_rust_esp_ci.sh <command> [args]

Commands:
  detect                          GitHub Actions guard; writes ready= to GITHUB_OUTPUT
  load-pins                       Validated KEY=value lines for GITHUB_ENV (stdout only if ok)
  verify-app-size <image>         Enforce max app size; append summary table
  verify-sdkconfig <sdkconfig> <base_dir>
  verify-target-id <app.bin> <id> Require exactly one stockbot-fw-target marker in image
  find-sdkconfig <search_root>    Print the only sdkconfig path under root
EOF
}

github_notice() {
  echo "::notice::$*"
}

github_error() {
  echo "::error::$*" >&2
}

key_allowed() {
  local key="$1" k
  for k in "${ALLOWED_PIN_KEYS[@]}"; do
    [ "$k" = "$key" ] && return 0
  done
  return 1
}

validate_pin_value() {
  local key="$1" value="$2"
  case "$key" in
    ESPUP_VERSION | ESPFLASH_VERSION | LDPROXY_VERSION | ESP_HOST_RUST_VERSION)
      [[ "$value" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
      ;;
    ESP_IDF_VERSION)
      [[ "$value" =~ ^v[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || return 1
      ;;
    ESP_RUST_VERSION)
      [[ "$value" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
      ;;
    *)
      return 1
      ;;
  esac
}

has_stray_esp_manifest() {
  local manifest
  while IFS= read -r -d '' manifest; do
    [ "$manifest" = "$CRATE_CARGO" ] && continue
    if grep -qE 'esp-idf-(svc|hal|sys)' "$manifest" 2>/dev/null; then
      echo "${manifest#"$REPO_ROOT"/}"
      return 0
    fi
  done < <(find "${REPO_ROOT}/firmware/rust" -name Cargo.toml -print0)
  return 1
}

cmd_detect() {
  local out="${GITHUB_OUTPUT:-}"
  if [ -z "$out" ]; then
    github_error "GITHUB_OUTPUT is required for detect"
    exit 1
  fi

  local stray
  if stray="$(has_stray_esp_manifest)"; then
    github_error "stray ESP crate manifest at ${stray}; expected only firmware/rust/crates/stockbot-scanner-esp"
    exit 1
  fi

  if [ -f "$CRATE_CARGO" ]; then
    if [ ! -f "$PIN_FILE" ]; then
      github_error "firmware/rust/esp-toolchain.env is required when stockbot-scanner-esp/Cargo.toml exists"
      exit 1
    fi
    printf 'ready=true\n' >"$out"
    return 0
  fi

  if [ -f "$PIN_FILE" ]; then
    github_notice "firmware/rust/crates/stockbot-scanner-esp/Cargo.toml is absent; skipping (pin file present)"
  else
    github_notice "firmware/rust/crates/stockbot-scanner-esp/Cargo.toml is absent; skipping until slice S7 adds the crate"
  fi
  printf 'ready=false\n' >"$out"
}

cmd_load_pins() {
  if [ ! -f "$PIN_FILE" ]; then
    github_error "missing pin file ${PIN_FILE#"$REPO_ROOT"/}"
    exit 1
  fi

  declare -A seen=()
  declare -A values=()
  local line key value

  while IFS= read -r line || [ -n "$line" ]; do
    line="${line//$'\r'/}"
    line="${line%%#*}"
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [ -z "$line" ] && continue

    if [[ ! "$line" =~ ^([A-Z_]+)=([^[:space:]]*)$ ]]; then
      github_error "invalid pin line (expected KEY=value with no spaces): ${line}"
      exit 1
    fi
    key="${BASH_REMATCH[1]}"
    value="${BASH_REMATCH[2]}"

    if ! key_allowed "$key"; then
      github_error "unknown pin key ${key} in esp-toolchain.env"
      exit 1
    fi
    if [ -n "${seen[$key]+x}" ]; then
      github_error "duplicate pin key ${key} in esp-toolchain.env"
      exit 1
    fi
    if [ -z "$value" ]; then
      github_error "empty value for ${key} in esp-toolchain.env"
      exit 1
    fi
    if ! validate_pin_value "$key" "$value"; then
      github_error "invalid value for ${key}: ${value}"
      exit 1
    fi
    seen[$key]=1
    values[$key]="$value"
  done <"$PIN_FILE"

  local req
  for req in "${ALLOWED_PIN_KEYS[@]}"; do
    if [ -z "${values[$req]+x}" ]; then
      github_error "esp-toolchain.env missing required key ${req}"
      exit 1
    fi
  done

  for req in "${ALLOWED_PIN_KEYS[@]}"; do
    printf '%s=%s\n' "$req" "${values[$req]}"
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

read_sdkconfig_kv() {
  local sdkconfig="$1" key="$2"
  { grep -E "^${key}=" "$sdkconfig" 2>/dev/null || true; } \
    | head -n1 | sed -E "s/^${key}=//" | tr -d '"' | tr -d "'"
}

cmd_verify_sdkconfig() {
  local sdkconfig="${1:-}" base_dir="${2:-}"
  if [ -z "$sdkconfig" ] || [ ! -f "$sdkconfig" ]; then
    github_error "verify-sdkconfig: missing sdkconfig file"
    exit 1
  fi
  if [ -z "$base_dir" ] || [ ! -d "$base_dir" ]; then
    github_error "verify-sdkconfig: missing base_dir"
    exit 1
  fi

  if ! grep -qE '^CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE=y$' "$sdkconfig"; then
    github_error "sdkconfig missing CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE=y"
    exit 1
  fi
  if ! grep -qE '^CONFIG_PARTITION_TABLE_CUSTOM=y$' "$sdkconfig"; then
    github_error "sdkconfig missing CONFIG_PARTITION_TABLE_CUSTOM=y"
    exit 1
  fi
  if ! grep -qE '^CONFIG_PARTITION_TABLE_OFFSET=0x8000$' "$sdkconfig"; then
    github_error "sdkconfig missing CONFIG_PARTITION_TABLE_OFFSET=0x8000"
    exit 1
  fi
  if grep -qE '^CONFIG_PARTITION_TABLE_SINGLE_APP=y$' "$sdkconfig"; then
    github_error "sdkconfig must not set CONFIG_PARTITION_TABLE_SINGLE_APP=y"
    exit 1
  fi

  local custom_file
  custom_file="$(read_sdkconfig_kv "$sdkconfig" CONFIG_PARTITION_TABLE_CUSTOM_FILENAME)"
  if [ -z "$custom_file" ]; then
    github_error "sdkconfig missing CONFIG_PARTITION_TABLE_CUSTOM_FILENAME"
    exit 1
  fi

  local resolved canonical="${REPO_ROOT}/${PARTITIONS_CSV_REL}"
  if [[ "$custom_file" = /* ]]; then
    resolved="$custom_file"
  else
    resolved="$(cd "$base_dir" && realpath -m "$custom_file")"
  fi
  if [ ! -f "$resolved" ]; then
    github_error "partition table not found: ${resolved}"
    exit 1
  fi
  if ! cmp -s "$resolved" "$canonical"; then
    github_error "partition table ${resolved} does not match ${PARTITIONS_CSV_REL}"
    exit 1
  fi
  echo "sdkconfig OTA partition and rollback checks ok"
}

cmd_verify_target_id() {
  local file="${1:-}" expected="${2:-}"
  if [ -z "$file" ] || [ -z "$expected" ] || [ ! -f "$file" ]; then
    github_error "verify-target-id: need readable app image and expected target id"
    exit 1
  fi
  if [[ ! "$expected" =~ ^[a-z0-9_]+$ ]]; then
    github_error "verify-target-id: invalid target id ${expected}"
    exit 1
  fi

  mapfile -t markers < <(grep -a -o "${TARGET_MARKER_PREFIX}[a-z0-9_]\+" "$file" || true)
  if [ "${#markers[@]}" -ne 1 ]; then
    github_error "expected exactly one ${TARGET_MARKER_PREFIX} marker, found ${#markers[@]}"
    exit 1
  fi
  local want="${TARGET_MARKER_PREFIX}${expected}"
  if [ "${markers[0]}" != "$want" ]; then
    github_error "target marker ${markers[0]} does not match ${want}"
    exit 1
  fi
  echo "embedded target id matches ${expected}"
}

cmd_find_sdkconfig() {
  local search_root="${1:-}"
  if [ -z "$search_root" ] || [ ! -d "$search_root" ]; then
    github_error "find-sdkconfig: missing search root"
    exit 1
  fi
  mapfile -d '' -t hits < <(find "$search_root" -name sdkconfig -print0 2>/dev/null || true)
  if [ "${#hits[@]}" -ne 1 ]; then
    github_error "expected exactly one sdkconfig under ${search_root}, found ${#hits[@]}"
    exit 1
  fi
  printf '%s\n' "${hits[0]}"
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
    find-sdkconfig) cmd_find_sdkconfig "$@" ;;
    -h | --help | help | "") usage; exit "${cmd:+0}"; [ -z "$cmd" ] && exit 1 ;;
    *)
      github_error "unknown command: $cmd"
      usage
      exit 1
      ;;
  esac
}

main "$@"