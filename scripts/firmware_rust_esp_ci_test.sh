#!/usr/bin/env bash
# Fixture tests for firmware_rust_esp_ci.sh (no ESP toolchain required).

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SCRIPT="${FIRMWARE_RUST_ESP_CI_SCRIPT:-${ROOT}/scripts/firmware_rust_esp_ci.sh}"
FIXTURES="${ROOT}/scripts/testdata/firmware_rust_esp"
PINS_VALID="$FIXTURES/pins_valid.env"
OKCFG="$FIXTURES/sdkconfig_ok"
WF="${ROOT}/.github/workflows/firmware-rust.yml"
PORCELAIN_BEFORE=$(git -C "$ROOT" status --porcelain)

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

grep -q -F 'set -euo pipefail' "$SCRIPT" || fail "script must keep pipefail"
grep -qF '>&2' "$SCRIPT" || fail "github_error must write annotations to stderr"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Do not append to the Actions runner's GITHUB_OUTPUT when this suite runs in CI.
unset GITHUB_OUTPUT

case_run() {
  local name="$1" want="$2" re="$3"
  shift 3
  set +e
  "$@" >"$tmp/co" 2>&1
  local rc=$?
  set -e
  if [ "$rc" -ne "$want" ]; then
    cat "$tmp/co" >&2
    fail "$name: rc $rc want $want"
  fi
  if [ -n "$re" ]; then
    grep -Eq -- "$re" "$tmp/co" || {
      cat "$tmp/co" >&2
      fail "$name: output must match /$re/"
    }
  fi
}

pins_repo() {
  rm -rf "$1"
  mkdir -p "$1/firmware/rust"
  cp "$PINS_VALID" "$1/firmware/rust/esp-toolchain.env"
  if [ -n "$(tail -c1 "$1/firmware/rust/esp-toolchain.env" 2>/dev/null || true)" ]; then
    echo >>"$1/firmware/rust/esp-toolchain.env"
  fi
}

lp() {
  STOCKBOT_REPO_ROOT="$1" bash "$SCRIPT" load-pins
}

det() {
  local d="$1"
  mkdir -p "$d"
  env GITHUB_OUTPUT="$d/out" STOCKBOT_REPO_ROOT="$d" bash "$SCRIPT" detect
}

sdk() {
  bash "$SCRIPT" verify-sdkconfig "$1" "${2:-$ROOT}"
}

mut_cfg() {
  sed "$1" "$OKCFG" >"$tmp/cfg"
  if [ -n "$(tail -c1 "$tmp/cfg" 2>/dev/null || true)" ]; then
    echo >>"$tmp/cfg"
  fi
}

# --- workflow contract (static) ---

grep -qF -- '--no-default-features' "$WF" || fail "workflow must build with --no-default-features"
grep -qF 'target-app-partition' "$WF" || fail "workflow must use target-app-partition for save-image"
grep -qF 'firmware_rust_esp_ci.sh load-pins' "$WF" \
  || fail "workflow must load pins via command substitution"
# shellcheck disable=SC2016
grep -qF 'pins="$(' "$WF" || fail "workflow must use pins=\$(...) command substitution"
grep -qF '.crates.toml' "$WF" || fail "workflow cache must include ~/.cargo/.crates.toml"
grep -qF '.crates2.json' "$WF" || fail "workflow cache must include ~/.cargo/.crates2.json"
grep -qF 'install --list' "$WF" || fail "workflow must skip ldproxy when cached version matches"

# --- load-pins ---

pins_repo "$tmp/p"
expected=$(STOCKBOT_REPO_ROOT="$tmp/p" bash "$SCRIPT" load-pins)
got=$(lp "$tmp/p")
[ "$got" = "$expected" ] || fail "load-pins output must match validated pins"
for line in $expected; do
  echo "$got" | grep -qxF "$line" || fail "load-pins missing line: $line"
done

pins_repo "$tmp/p"
printf '\nESPUP_VERSION=9.9.9\n' >>"$tmp/p/firmware/rust/esp-toolchain.env"
set +e
STOCKBOT_REPO_ROOT="$tmp/p" bash "$SCRIPT" load-pins >"$tmp/pout" 2>"$tmp/perr"
rc=$?
set -e
[ "$rc" -ne 0 ] || fail "duplicate/invalid pin must fail"
grep -q '::error::' "$tmp/perr" || fail "load-pins failure annotation must be on stderr"
[ ! -s "$tmp/pout" ] || fail "load-pins must print nothing on stdout when invalid"

while IFS='|' read -r bad reason; do
  pins_repo "$tmp/p"
  printf '%s\n' "$bad" >>"$tmp/p/firmware/rust/esp-toolchain.env"
  case_run "load-pins line: $bad" 1 "::error::${reason}" lp "$tmp/p"
done <<'CASES'
ESPUP_VERSION=9.9.9|duplicate pin key ESPUP_VERSION
EXTRA_KEY=1.0.0|unknown pin key EXTRA_KEY
PATH=1.2.3|unknown pin key PATH
BASH_ENV=1.0.0|unknown pin key BASH_ENV
not-valid-line|invalid pin line
espup_version=0.19.0|invalid pin line
ESPUP_VERSION = 0.19.0|invalid pin line
export ESPUP_VERSION=0.19.0|invalid pin line
CASES

pins_repo "$tmp/p"
sed -i 's/$/\r/' "$tmp/p/firmware/rust/esp-toolchain.env"
case_run "load-pins CRLF ok" 0 '^ESPUP_VERSION=0.19.0$' lp "$tmp/p"

pins_repo "$tmp/p"
printf '%s' "$(cat "$tmp/p/firmware/rust/esp-toolchain.env")" >"$tmp/p/nonl.env"
mv "$tmp/p/nonl.env" "$tmp/p/firmware/rust/esp-toolchain.env"
[ -n "$(tail -c1 "$tmp/p/firmware/rust/esp-toolchain.env")" ] \
  || fail "fixture must end without newline"
case_run "load-pins last line without trailing newline" 0 '^ESP_HOST_RUST_VERSION=1.86.0$' lp "$tmp/p"

for k in ESPUP_VERSION ESPFLASH_VERSION LDPROXY_VERSION ESP_HOST_RUST_VERSION; do
  for v in '' latest v0.19.0 0.19 0.19.0.1 0.19.0-rc1 abc.19.0 0.19.0x '"0.19.0"' 0.19.0* '0.19.0 junk'; do
    pins_repo "$tmp/p"
    esc="${v//\//\\/}"
    sed -i "s/^${k}=.*/${k}=${esc}/" "$tmp/p/firmware/rust/esp-toolchain.env"
    case_run "load-pins ${k}=${v}" 1 "::error::(empty value for ${k}|invalid value for ${k}|invalid pin line)" \
      lp "$tmp/p"
  done
done

for v in 5.3.2 v5 v5.3.2.1 v5.3.2-rc1 release/v5.3 master v5.3.x; do
  pins_repo "$tmp/p"
  sed -i "s|^ESP_IDF_VERSION=.*|ESP_IDF_VERSION=${v}|" "$tmp/p/firmware/rust/esp-toolchain.env"
  case_run "load-pins ESP_IDF_VERSION=$v" 1 'invalid value for ESP_IDF_VERSION' lp "$tmp/p"
done

for v in v5.3 v5.3.2; do
  pins_repo "$tmp/p"
  sed -i "s|^ESP_IDF_VERSION=.*|ESP_IDF_VERSION=${v}|" "$tmp/p/firmware/rust/esp-toolchain.env"
  case_run "load-pins ESP_IDF_VERSION=$v ok" 0 "^ESP_IDF_VERSION=${v}$" lp "$tmp/p"
done

for v in 1.86.0 1.86.0.0.0 1.86.0.0-nightly 1.86.0.x latest; do
  pins_repo "$tmp/p"
  sed -i "s/^ESP_RUST_VERSION=.*/ESP_RUST_VERSION=${v}/" "$tmp/p/firmware/rust/esp-toolchain.env"
  case_run "load-pins ESP_RUST_VERSION=$v" 1 'invalid value for ESP_RUST_VERSION' lp "$tmp/p"
done

for k in ESPUP_VERSION ESP_RUST_VERSION ESP_IDF_VERSION ESPFLASH_VERSION LDPROXY_VERSION ESP_HOST_RUST_VERSION; do
  pins_repo "$tmp/p"
  sed -i "/^${k}=/d" "$tmp/p/firmware/rust/esp-toolchain.env"
  case_run "load-pins missing $k" 1 "missing required key ${k}" lp "$tmp/p"
done

pins_repo "$tmp/p"
sed -i 's/^ESPUP_VERSION=.*/   ESPUP_VERSION=0.19.0   # trailing comment/' \
  "$tmp/p/firmware/rust/esp-toolchain.env"
case_run "load-pins indent+trailing comment" 0 '^ESPUP_VERSION=0.19.0$' lp "$tmp/p"

rm -f "$tmp/p/firmware/rust/esp-toolchain.env"
case_run "load-pins no file" 1 'missing pin file' lp "$tmp/p"

pins_repo "$tmp/p"
sed -i '/^ESP_IDF_VERSION=/d' "$tmp/p/firmware/rust/esp-toolchain.env"
set +e
(
  export STOCKBOT_REPO_ROOT="$tmp/p"
  bash -c 'set -euo pipefail; bash "'"$SCRIPT"'" load-pins' >/dev/null
)
sub_ec=$?
set -e
[ "$sub_ec" -ne 0 ] || fail "command-substitution load-pins must fail under set -e when pins incomplete"

# --- detect ---

CRATE_REL=firmware/rust/crates/stockbot-scanner-esp
for dep in esp-idf-svc esp-idf-hal esp-idf-sys; do
  d="$tmp/stray_${dep//-/_}"
  mkdir -p "$d/firmware/rust/other"
  printf '[dependencies]\n%s = "1"\n' "$dep" >"$d/firmware/rust/other/Cargo.toml"
  case_run "detect stray $dep" 1 'stray ESP crate manifest' det "$d"
  mkdir -p "$d/$CRATE_REL"
  : >"$d/$CRATE_REL/Cargo.toml"
  cp "$PINS_VALID" "$d/firmware/rust/esp-toolchain.env"
  echo >>"$d/firmware/rust/esp-toolchain.env"
  case_run "detect stray $dep alongside crate" 1 'stray ESP crate manifest' det "$d"
done

d="$tmp/det_hostonly"
mkdir -p "$d/firmware/rust/crates/core"
printf '[package]\nname="core"\n' >"$d/firmware/rust/crates/core/Cargo.toml"
case_run "detect host-only crate skips" 0 '' det "$d"
[ "$(cat "$d/out")" = "ready=false" ] || fail "host-only tree must write exactly ready=false, got: $(cat "$d/out")"

# N1: the expected crate manifest legitimately depends on esp-idf-*; it must NOT count as stray.
for dep in esp-idf-svc esp-idf-hal esp-idf-sys; do
  d="$tmp/det_crate_with_${dep//-/_}"
  mkdir -p "$d/$CRATE_REL"
  printf '[dependencies]\n%s = "1"\n' "$dep" >"$d/$CRATE_REL/Cargo.toml"
  cp "$PINS_VALID" "$d/firmware/rust/esp-toolchain.env"
  echo >>"$d/firmware/rust/esp-toolchain.env"
  case_run "detect expected crate may use $dep" 0 '' det "$d"
  [ "$(cat "$d/out")" = "ready=true" ] || fail "expected crate using $dep + pins must write exactly ready=true"
done

# N2: pin file present but crate absent => skip (ready=false), not ready=true.
d="$tmp/det_pin_only"
mkdir -p "$d/firmware/rust"
cp "$PINS_VALID" "$d/firmware/rust/esp-toolchain.env"
echo >>"$d/firmware/rust/esp-toolchain.env"
case_run "detect pin-only skips" 0 'pin file present' det "$d"
[ "$(cat "$d/out")" = "ready=false" ] || fail "pin-only must write exactly ready=false, got: $(cat "$d/out")"

# N3: blank lines and whole-line comments in the pin file are accepted.
pins_repo "$tmp/p"
{
  echo '# header comment'
  echo
  cat "$tmp/p/firmware/rust/esp-toolchain.env"
  echo '   '
  echo '# tail'
} >"$tmp/p/pins.new"
mv "$tmp/p/pins.new" "$tmp/p/firmware/rust/esp-toolchain.env"
case_run "load-pins blank+comment lines" 0 '^ESPUP_VERSION=0.19.0$' lp "$tmp/p"

# N4: unknown keys fail with the unknown-key reason (not the value validator).
for bad in 'EXTRA_KEY=1.0.0' 'PATH=1.2.3' 'BASH_ENV=1.0.0'; do
  pins_repo "$tmp/p"
  printf '%s\n' "$bad" >>"$tmp/p/firmware/rust/esp-toolchain.env"
  case_run "load-pins unknown key: $bad" 1 'unknown pin key' lp "$tmp/p"
done

# N5: a stray manifest deeper than the common layout is still found.
d="$tmp/stray_deep"
mkdir -p "$d/firmware/rust/crates/a/b/c"
printf 'esp-idf-sys = "1"\n' >"$d/firmware/rust/crates/a/b/c/Cargo.toml"
case_run "detect deep stray" 1 'stray ESP crate manifest at firmware/rust/crates/a/b/c/Cargo.toml' det "$d"

d="$tmp/det_crate_own_esp"
mkdir -p "$d/$CRATE_REL"
printf '[dependencies]\nesp-idf-svc = "1"\n' >"$d/$CRATE_REL/Cargo.toml"
cp "$PINS_VALID" "$d/firmware/rust/esp-toolchain.env"
echo >>"$d/firmware/rust/esp-toolchain.env"
case_run "detect crate own esp-idf manifest ok" 0 '' det "$d"
[ "$(cat "$d/out")" = "ready=true" ] || fail "crate own esp-idf manifest must be ready=true"
mkdir -p "$d/firmware/rust/crates/other-esp"
printf '[dependencies]\nesp-idf-hal = "1"\n' >"$d/firmware/rust/crates/other-esp/Cargo.toml"
case_run "detect stray esp manifest under crates/other-esp" 1 \
  'stray ESP crate manifest at firmware/rust/crates/other-esp' det "$d"

# #748: the USB host driver crate may depend on esp-idf-*, and only at its own path.
d="$tmp/det_usb_host"
mkdir -p "$d/$CRATE_REL" "$d/firmware/rust/crates/stockbot-usb-host"
printf '[dependencies]\nesp-idf-svc = "1"\n' >"$d/$CRATE_REL/Cargo.toml"
printf '[dependencies]\nesp-idf-svc = "1"\n' >"$d/firmware/rust/crates/stockbot-usb-host/Cargo.toml"
cp "$PINS_VALID" "$d/firmware/rust/esp-toolchain.env"
echo >>"$d/firmware/rust/esp-toolchain.env"
case_run "detect usb host crate ok" 0 '' det "$d"
[ "$(cat "$d/out")" = "ready=true" ] || fail "usb host crate manifest must be ready=true"
mkdir -p "$d/firmware/rust/crates/stockbot-usb-host/nested"
printf '[dependencies]\nesp-idf-sys = "1"\n' >"$d/firmware/rust/crates/stockbot-usb-host/nested/Cargo.toml"
case_run "detect stray esp manifest nested in the usb host crate" 1 \
  'stray ESP crate manifest at firmware/rust/crates/stockbot-usb-host/nested' det "$d"

d="$tmp/det_crate_no_pin"
mkdir -p "$d/$CRATE_REL"
: >"$d/$CRATE_REL/Cargo.toml"
case_run "detect crate without pin" 1 'esp-toolchain.env is required' det "$d"

d="$tmp/det_ready"
mkdir -p "$d/$CRATE_REL"
: >"$d/$CRATE_REL/Cargo.toml"
cp "$PINS_VALID" "$d/firmware/rust/esp-toolchain.env"
echo >>"$d/firmware/rust/esp-toolchain.env"
det "$d"
[ "$(cat "$d/out")" = "ready=true" ] || fail "ready path must write exactly ready=true, got: $(cat "$d/out")"
grep -q 'ready=false' "$d/out" && fail "ready path must not contain ready=false"

d="$tmp/det_stale_out"
mkdir -p "$d/firmware/rust"
echo 'ready=true' >"$d/out"
GITHUB_OUTPUT="$d/out" STOCKBOT_REPO_ROOT="$d" bash "$SCRIPT" detect
[ "$(cat "$d/out")" = "ready=false" ] || fail "detect must not leave stale ready=true in GITHUB_OUTPUT"

case_run "detect without GITHUB_OUTPUT" 1 'GITHUB_OUTPUT is required' \
  env -u GITHUB_OUTPUT STOCKBOT_REPO_ROOT="$d" bash "$SCRIPT" detect

# --- workflow detect step (normalization) ---

step_body="$tmp/detect_step.sh"
awk '
  /^      - name: Detect ESP crate and toolchain pins$/ { in_step = 1; next }
  in_step && /^        run: \|$/ { in_run = 1; next }
  in_run && /^      - name:/ { exit }
  in_run { sub(/^          /, ""); print }
' "$WF" >"$step_body"
[ -s "$step_body" ] || fail "could not extract the detect step run block from $WF"
step_tree() {
  rm -rf "$1"
  mkdir -p "$1/scripts" "$1/firmware/rust"
  cp "$ROOT/scripts/firmware_rust_esp_ci.sh" "$1/scripts/firmware_rust_esp_ci.sh"
}
run_step() {
  : >"$1/out"
  # shellcheck disable=SC2016
  (cd "$1" && env GITHUB_OUTPUT="$1/out" bash -c "$(cat "$step_body")") >"$tmp/co" 2>&1
}
step_case() {
  local name="$1" want_rc="$2" want_out="$3" t="$4" rc
  set +e
  run_step "$t"
  rc=$?
  set -e
  [ "$rc" -eq "$want_rc" ] || {
    cat "$tmp/co" >&2
    fail "detect step $name: rc $rc want $want_rc"
  }
  [ "$(cat "$t/out")" = "$want_out" ] || fail "detect step $name: GITHUB_OUTPUT is '$(cat "$t/out")' want '$want_out'"
}
st="$tmp/step_real"
step_tree "$st"
mkdir -p "$st/$CRATE_REL"
printf '[dependencies]\nesp-idf-svc = "1"\n' >"$st/$CRATE_REL/Cargo.toml"
cp "$PINS_VALID" "$st/firmware/rust/esp-toolchain.env"
echo >>"$st/firmware/rust/esp-toolchain.env"
step_case "crate+pins" 0 "ready=true" "$st"
step_tree "$st"
cp "$PINS_VALID" "$st/firmware/rust/esp-toolchain.env"
echo >>"$st/firmware/rust/esp-toolchain.env"
step_case "pin-only" 0 "ready=false" "$st"
step_tree "$st"
step_case "nothing" 0 "ready=false" "$st"
step_tree "$st"
mkdir -p "$st/$CRATE_REL"
: >"$st/$CRATE_REL/Cargo.toml"
set +e
run_step "$st"
rc=$?
set -e
[ "$rc" -ne 0 ] || fail "detect step: crate without pins must fail the step"
grep -q 'esp-toolchain.env is required' "$tmp/co" || fail "detect step: crate without pins must print its reason"
[ "$(cat "$st/out")" != "ready=true" ] || fail "detect step: crate without pins must not output ready=true"
step_tree "$st"
printf 'ready=true\nESPUP_VERSION=0.19.0\n# ready=true\n' >"$st/firmware/rust/esp-toolchain.env"
mkdir -p "$st/firmware/rust/crates/ready=true"
printf 'ready=true\n' >"$st/firmware/rust/crates/ready=true/Cargo.toml"
step_case "ready= text in pin file and paths, crate absent" 0 "ready=false" "$st"

fake_step() {
  local name="$1" want_rc="$2" want_out="$3" body="$4"
  step_tree "$st"
  printf '%s\n' "$body" >"$st/scripts/firmware_rust_esp_ci.sh"
  step_case "fake: $name" "$want_rc" "$want_out" "$st"
}
# shellcheck disable=SC2016
fake_step "one true" 0 "ready=true" 'echo ready=true >>"$GITHUB_OUTPUT"'
# shellcheck disable=SC2016
fake_step "one false" 0 "ready=false" 'echo ready=false >>"$GITHUB_OUTPUT"'
# shellcheck disable=SC2016
fake_step "false then true" 0 "ready=true" $'echo ready=false >>"$GITHUB_OUTPUT"\necho ready=true >>"$GITHUB_OUTPUT"'
# shellcheck disable=SC2016
fake_step "true then false" 0 "ready=false" $'echo ready=true >>"$GITHUB_OUTPUT"\necho ready=false >>"$GITHUB_OUTPUT"'
# shellcheck disable=SC2016
fake_step "other keys ignored" 0 "ready=true" $'echo a=b >>"$GITHUB_OUTPUT"\necho ready=true >>"$GITHUB_OUTPUT"\necho z=1 >>"$GITHUB_OUTPUT"'
fake_step_fails() {
  local name="$1" body="$2" re="$3" rc
  step_tree "$st"
  printf '%s\n' "$body" >"$st/scripts/firmware_rust_esp_ci.sh"
  set +e
  run_step "$st"
  rc=$?
  set -e
  [ "$rc" -ne 0 ] || fail "detect step fake $name: must fail closed"
  if [ -n "$re" ]; then
    grep -q "$re" "$tmp/co" || {
      cat "$tmp/co" >&2
      fail "detect step fake $name: must print /$re/"
    }
  fi
}
fake_step_fails "no ready line" 'exit 0' 'detect must set ready=true or ready=false'
# shellcheck disable=SC2016
fake_step_fails "ready=maybe" 'echo ready=maybe >>"$GITHUB_OUTPUT"' 'detect must set ready=true or ready=false'
# shellcheck disable=SC2016
fake_step_fails "ready=true with trailing space" 'echo "ready=true " >>"$GITHUB_OUTPUT"' 'detect must set ready=true or ready=false'
# shellcheck disable=SC2016
fake_step_fails "ready=TRUE" 'echo ready=TRUE >>"$GITHUB_OUTPUT"' 'detect must set ready=true or ready=false'
# shellcheck disable=SC2016
fake_step_fails "ready=True" 'echo ready=True >>"$GITHUB_OUTPUT"' 'detect must set ready=true or ready=false'
# shellcheck disable=SC2016
fake_step_fails "prefixed key xready=true" 'echo xready=true >>"$GITHUB_OUTPUT"' ''
# shellcheck disable=SC2016
fake_step_fails "empty ready=" 'echo ready= >>"$GITHUB_OUTPUT"' 'detect must set ready=true or ready=false'
# shellcheck disable=SC2016
fake_step_fails "detect exits 1 after writing ready=true" $'echo ready=true >>"$GITHUB_OUTPUT"\nexit 1' ''
fake_step_fails "ready= only on stdout" 'echo ready=true' ''

after_detect="$tmp/after_detect.yml"
awk '/^      - name: Detect ESP crate and toolchain pins$/ { f = 1; next } f' "$WF" >"$after_detect"
steps_after=$(grep -c '^      - name:' "$after_detect")
gated_after=$(grep -c "^        if: steps.esp.outputs.ready == 'true'$" "$after_detect")
[ "$steps_after" -gt 0 ] && [ "$steps_after" -eq "$gated_after" ] \
  || fail "every ESP step after detect must have if: steps.esp.outputs.ready == 'true' (steps=$steps_after gated=$gated_after)"

# --- esp32-s3 job skip-guard (static) ---

esp_job() {
  awk '/^  esp32-s3:/{f=1;next} f&&/^  [A-Za-z0-9_-]+:/{exit} f' "$WF"
}
esp_job >"$tmp/esp_job.yml"
ungated=$(awk '/^      - name:/{ if (n!="" && !g) print n; n=$0; g=0
  if ($0 ~ /Detect ESP crate and toolchain pins/) {seen=1; n=""} else if (!seen) {n=""} next }
  /^        if: steps\.esp\.outputs\.ready == .true.$/{g=1}
  END{ if (n!="" && !g) print n }' "$tmp/esp_job.yml")
[ -z "$ungated" ] || fail "esp32-s3 steps after detect must be gated on steps.esp.outputs.ready == 'true': $ungated"
[ "$(grep -c "^        if: steps.esp.outputs.ready == 'true'$" "$tmp/esp_job.yml")" -ge 13 ] \
  || fail "esp32-s3 job must keep >=13 ready-gated steps"
! grep -nE 'continue-on-error' "$tmp/esp_job.yml" \
  || fail "esp32-s3 job must not set continue-on-error (job or step)"
# Every step after detect (not a hardcoded later name), so a step inserted between detect and Load pins cannot hide failures. Detect body is excluded.
awk '/^      - name: Detect ESP crate and toolchain pins$/{f=1; next}
     f && /^      - name:/{p=1}
     p' "$tmp/esp_job.yml" >"$tmp/after_gate.yml"
[ -s "$tmp/after_gate.yml" ] || fail "could not extract steps after detect"
! grep -nE '\|\| *(true|:|exit +0|echo)|; *true( |$)|set \+e|< *<\(' "$tmp/after_gate.yml" \
  || fail "esp32-s3 steps after detect must not mask failures"
# shellcheck disable=SC2016
grep -qxF '          pins="$(bash scripts/firmware_rust_esp_ci.sh load-pins)"' "$tmp/esp_job.yml" \
  || fail "load-pins must run as a bare command substitution (failure fails the step)"
for gate in verify-target-id verify-sdkconfig verify-app-size; do
  grep -qF "firmware_rust_esp_ci.sh ${gate}" "$tmp/esp_job.yml" \
    || fail "esp32-s3 job must run ${gate}"
done
step_block() {
  awk -v n="      - name: $1" '$0==n{f=1;print;next} f&&/^      - name:/{exit} f' "$tmp/esp_job.yml"
}
grep -q 'ubuntu-24.04' "$tmp/esp_job.yml" \
  || fail "esp32-s3 job must pin runs-on to ubuntu-24.04 for libxml2.so.2"
build_line=$(grep -n '^      - name: Build ESP32-S3 firmware$' "$tmp/esp_job.yml" | head -n1 | cut -d: -f1)
libxml_line=$(grep -n '^      - name: Provision ESP-IDF host dependencies$' "$tmp/esp_job.yml" | head -n1 | cut -d: -f1)
[ -n "$build_line" ] && [ -n "$libxml_line" ] && [ "$libxml_line" -lt "$build_line" ] \
  || fail "Provision ESP-IDF host dependencies must appear before Build ESP32-S3 firmware"
step_block 'Provision ESP-IDF host dependencies' | grep -q 'libxml2\.so\.2' \
  || fail "libxml2 provisioning step must assert libxml2.so.2 via ldconfig"
grep -qxF '    runs-on: ubuntu-24.04' "$tmp/esp_job.yml" \
  || fail "esp32-s3 job must set runs-on: ubuntu-24.04 exactly"

prov=$(step_block 'Provision ESP-IDF host dependencies')
printf '%s\n' "$prov" | grep -qxF '          set -euo pipefail' \
  || fail "provision step must set -euo pipefail"
printf '%s\n' "$prov" | grep -qxF '            to_install+=(libxml2)' \
  || fail "provision step must queue the libxml2 apt package"

# Lock the exact post-install fail-closed block (helper avoids `ldconfig -p | grep -q` SIGPIPE under pipefail). The earlier
# `grep -q 'libxml2\.so\.2'` check does not match the workflow's escaped
# `libxml2\.so\.2` regex and is satisfied by an unescaped error string alone.
assert_block="$(cat <<'ASSERT'
          if ! have_libxml2; then
            echo "::error::libxml2.so.2 is not on the runner after apt install libxml2; esp-clang sanity checks need it"
            exit 1
          fi
          ldconfig -p | grep 'libxml2\.so\.2 '
ASSERT
)"
case "$prov" in
  *"$assert_block"*) ;;
  *) fail "provision step must keep the post-install ldconfig fail-closed assertion (if ! ldconfig ... exit 1 plus final grep)" ;;
esac

# --- ESP-IDF host provisioning step (behavior, with stubbed ldconfig/dpkg/sudo/apt-get) ---
prov_body="$tmp/provision_step.sh"
awk '
  /^      - name: Provision ESP-IDF host dependencies$/ { in_step = 1; next }
  in_step && /^        run: \|$/ { in_run = 1; next }
  in_run && /^      - name:/ { exit }
  in_run { sub(/^          /, ""); print }
' "$WF" >"$prov_body"
[ -s "$prov_body" ] || fail "could not extract the provisioning step run block from $WF"
PROV_PKGS="git wget flex bison gperf ccache libffi-dev libssl-dev dfu-util libusb-1.0-0 python3-venv"
prov_bin="$tmp/prov_bin"
mkdir -p "$prov_bin"
cat >"$prov_bin/ldconfig" <<'STUB'
#!/usr/bin/env bash
[ "${1:-}" = "-p" ] || exit 2
if [ -f "$PROV_STATE/xml_so2" ]; then printf '\tlibxml2.so.2 (libc6,x86-64) => /lib/x86_64-linux-gnu/libxml2.so.2\n'; fi
if [ -f "$PROV_STATE/xml_so16" ]; then printf '\tlibxml2.so.16 (libc6,x86-64) => /lib/x86_64-linux-gnu/libxml2.so.16\n'; fi
# More than a pipe buffer of output after the match: `ldconfig -p | grep -q` under pipefail would exit 141.
awk 'BEGIN { for (i = 0; i < 4000; i++) printf "\tlibfiller%d.so.1 (libc6,x86-64) => /lib/x86_64-linux-gnu/libfiller%d.so.1\n", i, i }'
STUB
cat >"$prov_bin/dpkg" <<'STUB'
#!/usr/bin/env bash
[ "${1:-}" = "-s" ] || exit 2
grep -qxF "$2" "$PROV_STATE/missing" 2>/dev/null && exit 1
exit 0
STUB
cat >"$prov_bin/sudo" <<'STUB'
#!/usr/bin/env bash
echo "sudo $*" >>"$PROV_STATE/log"
exec "$@"
STUB
cat >"$prov_bin/apt-get" <<'STUB'
#!/usr/bin/env bash
echo "apt-get $*" >>"$PROV_STATE/aptlog"
case "${1:-}" in
  update) [ ! -f "$PROV_STATE/update_fail" ] || exit 100 ;;
  install)
    case " $* " in *" -y "*) ;; *) echo "E: needs -y (non-interactive)" >&2; exit 100 ;; esac
    [ ! -f "$PROV_STATE/install_fail" ] || exit 100
    shift
    printf '%s\n' "$@" | grep -v '^-' >"$PROV_STATE/installed"
    if [ ! -f "$PROV_STATE/install_noeffect" ] && grep -qxF libxml2 "$PROV_STATE/installed"; then : >"$PROV_STATE/xml_so2"; fi
    ;;
  *) exit 2 ;;
esac
STUB
chmod +x "$prov_bin"/*
prov_run() {
  # args: state-dir; reads state files there
  (cd "$1" && env PATH="$prov_bin:$PATH" PROV_STATE="$1" bash -c "$(cat "$prov_body")") >"$tmp/prov_out" 2>&1
}
prov_case() {
  local name="$1" want_rc="$2" st="$3" rc
  : >"$st/log"
  : >"$st/aptlog"
  set +e
  prov_run "$st"
  rc=$?
  set -e
  if [ "$want_rc" = nonzero ]; then
    [ "$rc" -ne 0 ] || fail "provision $name: rc 0 want nonzero"
  else
    [ "$rc" -eq "$want_rc" ] || { cat "$tmp/prov_out" >&2; fail "provision $name: rc $rc want $want_rc"; }
  fi
  [ "$(wc -l <"$st/log")" -eq "$(wc -l <"$st/aptlog")" ] \
    || fail "provision $name: every apt-get call must go through sudo: $(cat "$st/log") / $(cat "$st/aptlog")"
}
prov_state() {
  rm -rf "$1"
  mkdir -p "$1"
  : >"$1/missing"
}
ps="$tmp/prov_state"
prov_state "$ps"
: >"$ps/xml_so2"
prov_case "all present, big ldconfig output (no SIGPIPE misread)" 0 "$ps"
[ ! -s "$ps/log" ] && [ ! -s "$ps/aptlog" ] || fail "provision all-present must not call sudo/apt-get: $(cat "$ps/log")"
grep -q 'libxml2\.so\.2 ' "$tmp/prov_out" || fail "provision must print the libxml2.so.2 ldconfig entry"
prov_state "$ps"
for _ in 1 2 3 4 5; do
  : >"$ps/xml_so2"
  prov_case "present repeated" 0 "$ps"
done
prov_state "$ps"
prov_case "libxml2 absent, apt installs it" 0 "$ps"
[ "$(sed -n 1p "$ps/log")" = "sudo apt-get update" ] || fail "provision must run sudo apt-get update first: $(cat "$ps/log")"
[ "$(sed -n 2p "$ps/log")" = "sudo apt-get install -y libxml2" ] || fail "provision must install only libxml2 with sudo -y: $(cat "$ps/log")"
prov_state "$ps"
: >"$ps/xml_so16"
prov_case "only libxml2.so.16 present counts as absent" 0 "$ps"
grep -qxF 'sudo apt-get install -y libxml2' "$ps/log" || fail "provision must install libxml2 when only libxml2.so.16 exists"
prov_state "$ps"
: >"$ps/xml_so16"
: >"$ps/install_noeffect"
prov_case "libxml2.so.2 still absent after install" 1 "$ps"
grep -q '::error::libxml2.so.2 is not on the runner' "$tmp/prov_out" || fail "provision must print ::error:: when libxml2.so.2 stays absent"
prov_state "$ps"
: >"$ps/install_fail"
prov_case "apt-get install failure is fatal" nonzero "$ps"
prov_state "$ps"
: >"$ps/update_fail"
prov_case "apt-get update failure is fatal" nonzero "$ps"
prov_state "$ps"
: >"$ps/xml_so2"
for pkg in $PROV_PKGS; do echo "$pkg"; done >"$ps/missing"
prov_case "all host packages missing" 0 "$ps"
[ "$(sed -n 2p "$ps/log")" = "sudo apt-get install -y $PROV_PKGS" ] \
  || fail "provision must install exactly the documented host packages when missing: $(cat "$ps/log")"
for pkg in $PROV_PKGS; do
  prov_state "$ps"
  : >"$ps/xml_so2"
  echo "$pkg" >"$ps/missing"
  prov_case "only $pkg missing" 0 "$ps"
  [ "$(sed -n 2p "$ps/log")" = "sudo apt-get install -y $pkg" ] || fail "provision must install missing $pkg alone: $(cat "$ps/log")"
done
prov_state "$ps"
echo gperf >"$ps/missing"
prov_case "missing package and libxml2" 0 "$ps"
[ "$(sed -n 2p "$ps/log")" = "sudo apt-get install -y libxml2 gperf" ] || fail "provision must install libxml2 plus missing packages together: $(cat "$ps/log")"

rustup_line=$(grep -n '^      - name: Install rustup$' "$tmp/esp_job.yml" | head -n1 | cut -d: -f1)
xtensa_line=$(grep -n '^      - name: Install Xtensa Rust toolchain$' "$tmp/esp_job.yml" | head -n1 | cut -d: -f1)
[ -n "$libxml_line" ] && [ -n "$rustup_line" ] && [ "$libxml_line" -lt "$rustup_line" ] \
  || fail "Provision ESP-IDF host dependencies must appear before Install rustup"
[ -n "$libxml_line" ] && [ -n "$xtensa_line" ] && [ "$libxml_line" -lt "$xtensa_line" ] \
  || fail "Provision ESP-IDF host dependencies must appear before Install Xtensa Rust toolchain"
step_block 'Build ESP32-S3 firmware' | grep -qxF '          set -euo pipefail' \
  || fail "build step must set -euo pipefail (cargo | tee would mask failure)"
step_block 'Load ESP toolchain pins' | grep -qxF '          set -euo pipefail' \
  || fail "load-pins step must set -euo pipefail"
step_block 'Verify target id, sdkconfig, and size gate' | grep -qxF '          set -euo pipefail' \
  || fail "verify step must set -euo pipefail"

# --- verify-app-size ---

export GITHUB_STEP_SUMMARY="$tmp/summary"
: >"$GITHUB_STEP_SUMMARY"
head -c 2516582 /dev/zero >"$tmp/limit_ok.bin"
head -c 2516583 /dev/zero >"$tmp/limit_fail.bin"
case_run "size ok" 0 'size ok' bash "$SCRIPT" verify-app-size "$tmp/limit_ok.bin"
grep -q '2516582' "$GITHUB_STEP_SUMMARY" || fail "summary must mention size limit"
case_run "size over limit" 1 'exceeds limit' bash "$SCRIPT" verify-app-size "$tmp/limit_fail.bin"
case_run "size missing arg" 1 'missing image' bash "$SCRIPT" verify-app-size
case_run "size missing file" 1 'missing image' bash "$SCRIPT" verify-app-size "$tmp/nope.bin"
: >"$GITHUB_STEP_SUMMARY"
head -c 10 /dev/zero >"$tmp/ten.bin"
bash "$SCRIPT" verify-app-size "$tmp/ten.bin" >/dev/null
grep -qF '| 10 | 2516582 |' "$GITHUB_STEP_SUMMARY" || fail "summary row must be | 10 | 2516582 |"

# --- verify-sdkconfig ---

[ -f "${ROOT}/firmware/partitions_8mb_ota.csv" ] || fail "repo partition CSV missing"
case_run "sdk baseline" 0 'checks ok' sdk "$OKCFG"

mut_cfg '/ROLLBACK/d'
case_run "sdk no rollback line" 1 'ROLLBACK_ENABLE=y' sdk "$tmp/cfg"
mut_cfg 's/ROLLBACK_ENABLE=y/ROLLBACK_ENABLE=n/'
case_run "sdk rollback n" 1 'ROLLBACK_ENABLE=y' sdk "$tmp/cfg"
mut_cfg 's/^CONFIG_BOOTLOADER/#CONFIG_BOOTLOADER/'
case_run "sdk rollback commented" 1 'ROLLBACK_ENABLE=y' sdk "$tmp/cfg"
mut_cfg 's/ROLLBACK_ENABLE=y/ROLLBACK_ENABLE=yes/'
case_run "sdk rollback yes" 1 'ROLLBACK_ENABLE=y' sdk "$tmp/cfg"
mut_cfg 's/^\(CONFIG_PARTITION_TABLE_CUSTOM=y\)$/\1x/'
case_run "sdk custom suffix" 1 'TABLE_CUSTOM=y' sdk "$tmp/cfg"
mut_cfg 's/^CONFIG_PARTITION_TABLE_CUSTOM=y$/XCONFIG_PARTITION_TABLE_CUSTOM=y/'
case_run "sdk custom prefix" 1 'TABLE_CUSTOM=y' sdk "$tmp/cfg"
mut_cfg 's/^CONFIG_PARTITION_TABLE_CUSTOM=y$/# CONFIG_PARTITION_TABLE_CUSTOM is not set/'
case_run "sdk custom unset" 1 'TABLE_CUSTOM=y' sdk "$tmp/cfg"
mut_cfg '/TABLE_OFFSET/d'
case_run "sdk no offset" 1 'TABLE_OFFSET=0x8000' sdk "$tmp/cfg"
mut_cfg 's/OFFSET=0x8000/OFFSET=0x9000/'
case_run "sdk offset 9000" 1 'TABLE_OFFSET=0x8000' sdk "$tmp/cfg"
mut_cfg 's/OFFSET=0x8000/OFFSET=0x80000/'
case_run "sdk offset 80000" 1 'TABLE_OFFSET=0x8000' sdk "$tmp/cfg"
mut_cfg 's/OFFSET=0x8000/OFFSET=0x8001/'
case_run "sdk offset 8001" 1 'TABLE_OFFSET=0x8000' sdk "$tmp/cfg"
mut_cfg 's/^CONFIG_PARTITION_TABLE_OFFSET/XCONFIG_PARTITION_TABLE_OFFSET/'
case_run "sdk offset prefixed" 1 'TABLE_OFFSET=0x8000' sdk "$tmp/cfg"
{ cat "$OKCFG"; echo; echo 'CONFIG_PARTITION_TABLE_SINGLE_APP=y'; } >"$tmp/cfg"
case_run "sdk single app" 1 'SINGLE_APP' sdk "$tmp/cfg"
mut_cfg '/CUSTOM_FILENAME/d'
case_run "sdk no filename" 1 'CUSTOM_FILENAME' sdk "$tmp/cfg"
mut_cfg 's|^CONFIG_PARTITION_TABLE_CUSTOM_FILENAME=|# CONFIG_PARTITION_TABLE_CUSTOM_FILENAME=|'
case_run "sdk commented filename" 1 'missing CONFIG_PARTITION_TABLE_CUSTOM_FILENAME' sdk "$tmp/cfg"
case_run "sdk missing file" 1 'missing sdkconfig' bash "$SCRIPT" verify-sdkconfig "$tmp/none" "$ROOT"
case_run "sdk missing base" 1 'missing base_dir' bash "$SCRIPT" verify-sdkconfig "$OKCFG" "$tmp/nodir"

mut_cfg "s|^\(CONFIG_PARTITION_TABLE_CUSTOM_FILENAME=\).*|\1\"$ROOT/firmware/partitions_8mb_ota.csv\"|"
mkdir -p "$tmp/other_base"
case_run "sdk absolute ok (other base)" 0 'checks ok' sdk "$tmp/cfg" "$tmp/other_base"

cp "$OKCFG" "$tmp/cfg"
case_run "sdk relative bad base" 1 'not found' sdk "$tmp/cfg" "$tmp/other_base"

mkdir -p "$tmp/fakerepo/firmware"
{ cat "$ROOT/firmware/partitions_8mb_ota.csv"; echo '# drift'; } >"$tmp/fakerepo/firmware/partitions_8mb_ota.csv"
mkdir -p "$tmp/fakerepo/crate"
sed 's|"firmware/partitions_8mb_ota.csv"|"../firmware/partitions_8mb_ota.csv"|' "$OKCFG" >"$tmp/cfg"
case_run "sdk partition content drift (relative)" 1 'does not match' sdk "$tmp/cfg" "$tmp/fakerepo/crate"

mut_cfg "s|^\(CONFIG_PARTITION_TABLE_CUSTOM_FILENAME=\).*|\1\"$tmp/fakerepo/firmware/partitions_8mb_ota.csv\"|"
case_run "sdk partition content drift (abs)" 1 'does not match' sdk "$tmp/cfg" "$tmp/other_base"

mut_cfg "s|^\(CONFIG_PARTITION_TABLE_CUSTOM_FILENAME=\).*|\1\"$tmp/missing.csv\"|"
case_run "sdk partition file absent (abs)" 1 'not found' sdk "$tmp/cfg" "$tmp/other_base"

rel_tree="$tmp/rel_partition_tree"
mkdir -p "$rel_tree/firmware/rust/crates/stockbot-scanner-esp"
cp "${ROOT}/firmware/partitions_8mb_ota.csv" "$rel_tree/firmware/"
case_run "sdk relative partition ok" 0 'checks ok' bash "$SCRIPT" verify-sdkconfig \
  "$FIXTURES/sdkconfig_relative_partition" "$rel_tree/firmware/rust/crates/stockbot-scanner-esp"

# --- verify-target-id ---

img="$tmp/real.bin"
printf '\xe9\x04\x02\x3f\x00\x01junk\x00stockbot-fw-target:sticks3_u173_uart\x00tail\xff\xfe' >"$img"
case_run "tid binary uart ok" 0 'matches' bash "$SCRIPT" verify-target-id "$img" sticks3_u173_uart
case_run "tid binary base vs uart" 1 'does not match' bash "$SCRIPT" verify-target-id "$img" sticks3_u173
printf 'x\x00stockbot-fw-target:sticks3_u173\x00y' >"$tmp/short.bin"
case_run "tid image base, expect uart" 1 'does not match' bash "$SCRIPT" verify-target-id "$tmp/short.bin" sticks3_u173_uart

printf 'x\x00stockbot-fw-target:sticks3_u173\x00y\x00stockbot-fw-target:sticks3_u173\x00' >"$tmp/dupmark.bin"
case_run "tid same marker twice" 1 'exactly one .*found 2' bash "$SCRIPT" verify-target-id "$tmp/dupmark.bin" sticks3_u173
head -c 64 /dev/zero >"$tmp/nomark.bin"
case_run "tid no marker" 1 'exactly one .*found 0' bash "$SCRIPT" verify-target-id "$tmp/nomark.bin" sticks3_u173
for badid in Sticks3 a-b 'a b' ../x ''; do
  case_run "tid invalid id '$badid'" 1 'need readable|invalid target id' bash "$SCRIPT" verify-target-id "$img" "$badid"
done
case_run "tid missing file" 1 'need readable' bash "$SCRIPT" verify-target-id "$tmp/none.bin" sticks3_u173

targets=(sticks3_u173 sticks3_u173_uart atoms3lite_u173_bench atoms3lite_symcode_hid)
for target in "${targets[@]}"; do
  printf 'pad stockbot-fw-target:%s tail' "$target" >"$tmp/image_${target}.bin"
  case_run "tid ok $target" 0 "matches ${target}" bash "$SCRIPT" verify-target-id "$tmp/image_${target}.bin" "$target"
done

# --- find-sdkconfig ---

f3="$tmp/f3"
mkdir -p "$f3/a" "$f3/b"
: >"$f3/a/sdkconfig"
: >"$f3/b/sdkconfig"
case_run "find two" 1 'found 2' bash "$SCRIPT" find-sdkconfig "$f3"
f1="$tmp/f1"
mkdir -p "$f1/a" "$f1/b"
: >"$f1/a/sdkconfig"
: >"$f1/a/sdkconfig.old"
: >"$f1/b/sdkconfig.defaults"
found=$(bash "$SCRIPT" find-sdkconfig "$f1")
[ "$found" = "$f1/a/sdkconfig" ] || fail "find-sdkconfig must ignore sdkconfig.old/.defaults"
mkdir -p "$tmp/empty_find"
case_run "find zero" 1 'found 0' bash "$SCRIPT" find-sdkconfig "$tmp/empty_find"
f0="$tmp/f0"
mkdir -p "$f0"
: >"$f0/sdkconfig.old"
case_run "find only sdkconfig.old" 1 'found 0' bash "$SCRIPT" find-sdkconfig "$f0"

[ "$(git -C "$ROOT" status --porcelain)" = "$PORCELAIN_BEFORE" ] \
  || fail "test left repo dirty: $(git -C "$ROOT" status --porcelain)"

echo "firmware_rust_esp_ci_test ok"