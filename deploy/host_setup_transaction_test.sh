#!/usr/bin/env bash
# Stub fault suite and byte invariance for #152 host setup transaction.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
UPDATE="$ROOT/deploy/systemd/pantry-update.sh"
BASE_SHA="${HOST_SETUP_BASE_SHA:-3d265d5ce4f6ea7aa3c470b1cc3c5d610a6010f2}"
TEST_REV=0123456789abcdef0123456789abcdef01234567

TEST_CASES=()

run_case() {
  local name="$1"
  shift
  TEST_CASES+=("$name")
  "$@" || fail "case failed: ${name}"
}

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

bash -n "$UPDATE"
bash -n "$0"

PROTECTED=(
  deploy/publish-mode.sh
  deploy/runtime-setup.sh
  deploy/vapid-env.sh
  deploy/systemd/pantry-backup.sh
  deploy/systemd/pantry-update.service
  deploy/systemd/pantry-update.timer
  deploy/systemd/pantry-update.path
  deploy/systemd/pantry-backup.service
  deploy/systemd/pantry-backup.timer
)

if ! git -C "$ROOT" cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null; then
  fail "frozen base ${BASE_SHA} is not available in this checkout"
fi

for path in "${PROTECTED[@]}"; do
  [[ -f "$ROOT/$path" ]] || fail "missing protected path $path"
  if ! git -C "$ROOT" cat-file -e "${BASE_SHA}:${path}" 2>/dev/null; then
    fail "protected path $path missing at base ${BASE_SHA}"
  fi
  if ! git -C "$ROOT" diff --quiet "${BASE_SHA}" -- "$path"; then
    fail "byte or mode change in protected path $path vs base ${BASE_SHA}"
  fi
done

ALLOWED_CHANGED=(
  deploy/systemd/pantry-update.sh
  deploy/host_setup_transaction_test.sh
  deploy/pantry_update_test.sh
  .github/workflows/ci.yml
  deploy/README.md
)

if [[ -n "${HOST_SETUP_ALLOWLIST_CHECK:-1}" ]]; then
  diff_head=$(git -C "$ROOT" rev-parse HEAD)
  if git -C "$ROOT" rev-parse --verify HEAD^2 >/dev/null 2>&1; then
    diff_head=$(git -C "$ROOT" rev-parse HEAD^2)
  fi
  changed=$(git -C "$ROOT" diff --name-only "${BASE_SHA}".."${diff_head}" 2>/dev/null || true)
  if [[ -n "$changed" ]]; then
    while IFS= read -r f; do
      [[ -z "$f" ]] && continue
      ok=0
      for a in "${ALLOWED_CHANGED[@]}"; do
        [[ "$f" == "$a" ]] && ok=1 && break
      done
      [[ "$ok" -eq 1 ]] || fail "unexpected changed file in PR: $f"
    done <<<"$changed"
  fi
fi

make_bin() {
  local bin="$1" analyze_fail="${2:-0}"
  mkdir -p "$bin"
  cat > "$bin/systemd-analyze" << EOF
#!/usr/bin/env bash
if [[ "\$1" == verify* ]] || [[ "\$*" == *verify* ]]; then
  [[ "$analyze_fail" -eq 1 ]] && exit 1
fi
exit 0
EOF
  chmod +x "$bin/systemd-analyze"
  cat > "$bin/docker" << 'EOF'
#!/usr/bin/env bash
if [[ "$1" == cp && "$2" == *host-bundle* ]]; then
  src=${STUB_HOST_BUNDLE_CP:-}
  dest=$3
  if [[ -n "$src" && -d "$src" ]]; then
    mkdir -p "$dest"
    cp -a "$src/." "$dest/"
    exit 0
  fi
fi
if [[ "$1" == inspect ]]; then
  if [[ "${*: -2:1}" == pantry || "$*" == *' pantry '* ]]; then
    printf 'cid-pantry\n'
    exit 0
  fi
fi
exit 0
EOF
  cat > "$bin/curl" << 'EOF'
#!/usr/bin/env bash
if [[ "$*" == *"/health"* ]]; then
  printf '{"status":"ok"}'
elif [[ "$*" == *"/api/build"* ]]; then
  printf '{"commit":"0123456789abcdef0123456789abcdef01234567"}'
fi
exit 0
EOF
  cat > "$bin/flock" << 'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "$bin"/*
}

prepare_root() {
  local root="$1"
  mkdir -p "$root/systemd" "$root/deploy-trigger"
  cp "$ROOT/deploy/publish-mode.sh" "$ROOT/deploy/runtime-setup.sh" "$ROOT/deploy/vapid-env.sh" "$root/"
  cp "$ROOT/deploy/systemd/pantry-update.sh" "$ROOT/deploy/systemd/pantry-backup.sh" "$root/systemd/"
  chmod +x "$root/systemd/pantry-update.sh" "$root/systemd/pantry-backup.sh"
  cp "$ROOT/deploy/systemd/pantry-update.service" "$ROOT/deploy/systemd/pantry-update.timer" \
    "$ROOT/deploy/systemd/pantry-update.path" "$ROOT/deploy/systemd/pantry-backup.service" \
    "$ROOT/deploy/systemd/pantry-backup.timer" "$root/systemd/" 2>/dev/null || true
  cat > "$root/.env" << 'EOF'
HOST_PORT=8080
PUBLIC_HOST=
PANTRY_IMAGE_TAG=latest
EOF
  cat > "$root/docker-compose.yml" << 'EOF'
services:
  pantry:
    image: ghcr.io/rhionin/pantry:latest
EOF
  mkdir -p "$root/setup"
}

write_lkg_snapshot() {
  local root="$1" revision="${2:-$TEST_REV}"
  PANTRY_DIR="$root" bash -c '
    set -euo pipefail
    # shellcheck disable=SC1091
    source "$1"
    snap="$PANTRY_DIR/host-setup-state/lkg"
    mkdir -p "$snap/files"
    for id in 1 2 3 4; do
      cp "$(host_txn_target_dest "$id")" "$snap/files/$id"
    done
    host_manifest_build_installed "'"$revision"'" "$snap/manifest"
    host_snapshot_verify "$snap"
  ' _ "$UPDATE"
}

write_clean_control() {
  local root="$1" revision="${2:-$TEST_REV}"
  PANTRY_DIR="$root" bash -c '
    set -euo pipefail
    source "$1"
    mkdir -p "$PANTRY_DIR/host-setup-state"
    HOST_CTRL_PHASE=clean
    HOST_CTRL_COMMITTED="'"$revision"'"
    HOST_CTRL_TARGET=none
    HOST_CTRL_PREV_MANIFEST=none
    HOST_CTRL_CAND_MANIFEST=none
    HOST_CTRL_REASON=none
    HOST_CTRL_FAILED_TARGET=none
    host_control_publish
  ' _ "$UPDATE"
}

run_entry_trace() {
  local root="$1" trace="$2"
  local work bin
  work=$(mktemp -d)
  bin="$work/bin"
  make_bin "$bin"
  : >"$trace"
  set +e
  PANTRY_DIR="$root" \
    PANTRY_SETUP_SKIP_ROOT=1 \
    PANTRY_TEST_ENTRY_TRACE="$trace" \
    PATH="$bin:$PATH" \
    bash "$root/systemd/pantry-update.sh" >/dev/null 2>&1
  set -e
  rm -rf "$work"
}

assert_order_before_source() {
  local trace="$1"
  local lock_line source_line flags_line trigger_line
  lock_line=$(grep -n '^deploy_lock$' "$trace" | head -1 | cut -d: -f1 || true)
  source_line=$(grep -n '^publish-mode-source$' "$trace" | head -1 | cut -d: -f1 || true)
  [[ -n "$lock_line" && -n "$source_line" ]] || fail "missing lock or publish-mode trace markers"
  [[ "$lock_line" -lt "$source_line" ]] || fail "publish-mode sourced before deploy lock"
  flags_line=$(grep -n '^host_flags_read$' "$trace" | head -1 | cut -d: -f1 || true)
  [[ -n "$flags_line" && "$flags_line" -lt "$source_line" ]] \
    || fail "flags read after publish-mode source"
  trigger_line=$(grep -n '^deploy_trigger_dir_ready$' "$trace" | head -1 | cut -d: -f1 || true)
  [[ -n "$trigger_line" && "$flags_line" -lt "$trigger_line" && "$trigger_line" -lt "$source_line" ]] \
    || fail "deploy-trigger mkdir must run after flags and before publish-mode source"
}

# --- fault-matrix cases (mapping comments in brief) ---

test_entry_order_recovery_before_publish_mode_sources_and_flags() {
  local trace_root
  trace_root=$(mktemp -d)
  prepare_root "$trace_root"
  run_entry_trace "$trace_root" "$trace_root/trace.log"
  assert_order_before_source "$trace_root/trace.log"
  rm -rf "$trace_root"
}

test_pending_recovery_exits_nonzero_before_image_work() {
  local pend_root work state rc
  pend_root=$(mktemp -d)
  prepare_root "$pend_root"
  write_lkg_snapshot "$pend_root"
  state="$pend_root/host-setup-state"
  printf 'version=1\nphase=applying\noriginal_reason=interrupted\nfailed_target_sha=none\n' >"$state/control"
  work=$(mktemp -d)
  make_bin "$work/bin"
  set +e
  PANTRY_DIR="$pend_root" PANTRY_SETUP_SKIP_ROOT=1 PATH="$work/bin:$PATH" \
    bash "$pend_root/systemd/pantry-update.sh" >/dev/null 2>&1
  rc=$?
  set -e
  [[ "$rc" -ne 0 ]] || fail "pending recovery should exit nonzero"
  [[ "$(awk -F= '$1=="phase"{print $2;exit}' "$state/control")" == recovered ]]
  rm -rf "$pend_root" "$work"
}

test_invalid_flag_matrix_pantry_host_refresh_invalid() {
  local env_root rc
  env_root=$(mktemp -d)
  prepare_root "$env_root"
  printf 'PANTRY_AUTO_SETUP=on\n' >>"$env_root/.env"
  set +e
  PANTRY_HOST_REFRESH=bad PANTRY_DIR="$env_root" bash -c '
    set -euo pipefail
    source "$1"
    host_flags_read
    exit "$HOST_FLAG_ANY_INVALID"
  ' _ "$UPDATE"
  rc=$?
  set -e
  [[ "$rc" -eq 1 ]] || fail "invalid PANTRY_HOST_REFRESH should set HOST_FLAG_ANY_INVALID"
  rm -rf "$env_root"
}

test_invalid_flag_matrix_either_invalid_wins_over_valid_other() {
  local both_invalid
  both_invalid=$(mktemp -d)
  prepare_root "$both_invalid"
  PANTRY_DIR="$both_invalid" PANTRY_HOST_REFRESH=maybe PANTRY_AUTO_SETUP=on bash -c '
    set -euo pipefail
    source "$1"
    host_flags_read
    [[ "$HOST_FLAG_ANY_INVALID" -eq 1 ]]
  ' _ "$UPDATE"
  rm -rf "$both_invalid"
}

test_pantry_auto_setup_env_beats_dot_env() {
  local auto_root
  auto_root=$(mktemp -d)
  prepare_root "$auto_root"
  printf 'PANTRY_AUTO_SETUP=off\n' >>"$auto_root/.env"
  PANTRY_DIR="$auto_root" PANTRY_AUTO_SETUP=on bash -c '
    set -euo pipefail
    source "$1"
    host_flags_read
    [[ "$HOST_FLAG_AUTO_SETUP" == on ]]
  ' _ "$UPDATE"
  rm -rf "$auto_root"
}

test_flag_matrix_both_off_valid() {
  local off_root
  off_root=$(mktemp -d)
  prepare_root "$off_root"
  PANTRY_DIR="$off_root" PANTRY_HOST_REFRESH=off PANTRY_AUTO_SETUP=off bash -c '
    set -euo pipefail
    source "$1"
    host_flags_read
    [[ "$HOST_FLAG_ANY_INVALID" -eq 0 ]]
    [[ "$HOST_FLAG_HOST_REFRESH" == deny ]]
    [[ "$HOST_FLAG_AUTO_SETUP" == off ]]
  ' _ "$UPDATE"
  rm -rf "$off_root"
}

test_guard_rejects_coordinator_change_whole_bundle() {
  local guard_root stage
  guard_root=$(mktemp -d)
  prepare_root "$guard_root"
  stage="$guard_root/stage"
  mkdir -p "$stage/systemd"
  cp "$guard_root/publish-mode.sh" "$guard_root/runtime-setup.sh" "$guard_root/vapid-env.sh" "$stage/"
  cp "$guard_root/systemd/pantry-backup.sh" "$stage/systemd/"
  cp "$guard_root/systemd/pantry-update.sh" "$stage/systemd/"
  printf '\n# changed\n' >>"$stage/systemd/pantry-update.sh"
  PANTRY_DIR="$guard_root" bash -c '
    set -euo pipefail
    source "$1"
    host_guard_validate "'"$stage"'" && exit 1 || exit 0
  ' _ "$UPDATE"
  rm -rf "$guard_root"
}

test_idempotent_restore_converges_manifest_driven() {
  local restore_root
  restore_root=$(mktemp -d)
  prepare_root "$restore_root"
  write_lkg_snapshot "$restore_root"
  printf '\n# drift\n' >>"$restore_root/publish-mode.sh"
  PANTRY_DIR="$restore_root" bash -c '
    set -euo pipefail
    source "$1"
    host_restore_transaction
    cmp -s "$PANTRY_DIR/host-setup-state/lkg/files/1" "$PANTRY_DIR/publish-mode.sh"
  ' _ "$UPDATE"
  rm -rf "$restore_root"
}

test_interrupted_restore_second_pass_converges() {
  local root
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  printf '\n# drift\n' >>"$root/runtime-setup.sh"
  PANTRY_DIR="$root" bash -c '
    set -euo pipefail
    source "$1"
    host_restore_transaction
    host_restore_transaction
    cmp -s "$PANTRY_DIR/host-setup-state/lkg/files/2" "$PANTRY_DIR/runtime-setup.sh"
  ' _ "$UPDATE"
  rm -rf "$root"
}

test_initialized_auto_off_freezes_no_legacy_bypass() {
  local freeze_root
  freeze_root=$(mktemp -d)
  prepare_root "$freeze_root"
  write_lkg_snapshot "$freeze_root"
  write_clean_control "$freeze_root"
  PANTRY_DIR="$freeze_root" bash -c '
    set -euo pipefail
    source "$1"
    HOST_STATE_INITIALIZED=1
    HOST_FLAG_AUTO_SETUP=off
    HOST_FLAG_HOST_REFRESH=allow
    host_should_freeze_writes
  ' _ "$UPDATE" || fail "initialized AUTO off should freeze writes"
  rm -rf "$freeze_root"
}

test_retention_before_prepared_blocks_promotion_with_prior() {
  local prior_root ret_rc
  prior_root=$(mktemp -d)
  prepare_root "$prior_root"
  write_lkg_snapshot "$prior_root"
  mkdir -p "$prior_root/host-setup-state/prior"
  write_clean_control "$prior_root"
  PANTRY_DIR="$prior_root" bash -c '
    set -euo pipefail
    source "$1"
    host_retained_prior_present || exit 1
  ' _ "$UPDATE" || fail "retained prior must be detected"
  set +e
  PANTRY_DIR="$prior_root" bash -c '
    set -euo pipefail
    source "$1"
    host_retained_prior_present
    host_subset_refresh_from_image /tmp/state
  ' _ "$UPDATE" 2>/dev/null
  ret_rc=$?
  set -e
  [[ "$ret_rc" -eq 3 ]] || fail "retained prior should refuse promotion (rc=$ret_rc)"
  rm -rf "$prior_root"
}

test_uninitialized_auto_on_refuses_legacy_refresh() {
  local uninit_auto
  uninit_auto=$(mktemp -d)
  prepare_root "$uninit_auto"
  PANTRY_DIR="$uninit_auto" bash -c '
    set -euo pipefail
    source "$1"
    HOST_STATE_INITIALIZED=0
    HOST_FLAG_AUTO_SETUP=on
    HOST_FLAG_HOST_REFRESH=allow
    HOST_FLAG_ANY_INVALID=0
    rc=0
    refresh_host_scripts_from_image /tmp/x || rc=$?
    [[ "$rc" -eq 3 ]]
  ' _ "$UPDATE"
  rm -rf "$uninit_auto"
}

test_corrupt_state_blocks_whole_updater() {
  local corrupt_root work rc
  corrupt_root=$(mktemp -d)
  prepare_root "$corrupt_root"
  touch "$corrupt_root/host-setup-state"
  work=$(mktemp -d)
  make_bin "$work/bin"
  set +e
  PANTRY_DIR="$corrupt_root" PANTRY_SETUP_SKIP_ROOT=1 PATH="$work/bin:$PATH" \
    bash "$corrupt_root/systemd/pantry-update.sh" >/dev/null 2>&1
  rc=$?
  set -e
  [[ "$rc" -ne 0 ]] || fail "corrupt host-setup-state file must block updater"
  rm -rf "$corrupt_root" "$work"
}

test_apply_failure_mid_apply_aborts_to_recovered() {
  local root state rc
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  write_clean_control "$root"
  mkdir -p "$root/host-setup-state/candidate/files"
  for id in 1 2 3 4; do
    cp "$root/host-setup-state/lkg/files/$id" "$root/host-setup-state/candidate/files/$id"
  done
  cp "$root/host-setup-state/lkg/manifest" "$root/host-setup-state/candidate/manifest"
  PANTRY_DIR="$root" PANTRY_TEST_APPLY_FAIL_ID=2 bash -c '
    set -euo pipefail
    source "$1"
    HOST_CTRL_PHASE=applying
    host_control_load_into_publish_vars
    HOST_CTRL_TARGET="'"$TEST_REV"'"
    host_control_publish
    rc=0
    host_apply_transaction "$PANTRY_DIR/host-setup-state/candidate" || rc=1
    [[ "$rc" -eq 1 ]]
    host_txn_abort_to_recovered apply_failed "'"$TEST_REV"'"
    [[ "$(host_control_field phase)" == recovered ]]
  ' _ "$UPDATE"
  rm -rf "$root"
}

test_validation_failure_before_prepared_write() {
  local root stage
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  write_clean_control "$root"
  stage="$root/stage"
  mkdir -p "$stage/systemd"
  cp "$root/publish-mode.sh" "$root/runtime-setup.sh" "$root/vapid-env.sh" "$stage/"
  cp "$root/systemd/pantry-backup.sh" "$stage/systemd/"
  cp "$root/systemd/pantry-update.sh" "$stage/systemd/"
  printf '\nbroken syntax\n' >>"$stage/publish-mode.sh"
  PANTRY_DIR="$root" bash -c '
    set -euo pipefail
    source "$1"
    host_guard_validate "'"$stage"'" || exit 1
    host_subset_validate "'"$stage"'" && exit 1
    [[ "$(host_control_field phase)" == clean ]]
  ' _ "$UPDATE"
  rm -rf "$root"
}

test_health_failure_after_apply() {
  local root
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  write_clean_control "$root"
  PANTRY_DIR="$root" PANTRY_TEST_RUN_BOUNDED_RC=1 bash -c '
    set -euo pipefail
    source "$1"
    mkdir -p "$PANTRY_DIR/host-setup-state/candidate/files"
    for id in 1 2 3 4; do
      cp "$PANTRY_DIR/host-setup-state/lkg/files/$id" "$PANTRY_DIR/host-setup-state/candidate/files/$id"
    done
    cp "$PANTRY_DIR/host-setup-state/lkg/manifest" "$PANTRY_DIR/host-setup-state/candidate/manifest"
    host_apply_transaction "$PANTRY_DIR/host-setup-state/candidate"
    host_txn_post_apply_validate
    host_txn_health_validate /tmp/x && exit 1 || exit 0
  ' _ "$UPDATE"
  rm -rf "$root"
}

test_health_ok_post_apply_validation_mismatch_fails() {
  local root rc
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  write_clean_control "$root"
  printf '\nif [[\n' >>"$root/publish-mode.sh"
  PANTRY_DIR="$root" PANTRY_TEST_SKIP_HEALTH=1 bash -c '
    set -euo pipefail
    source "$1"
    mkdir -p "$PANTRY_DIR/host-setup-state/candidate/files"
    cp "$PANTRY_DIR/publish-mode.sh" "$PANTRY_DIR/host-setup-state/candidate/files/1"
    for id in 2 3 4; do
      cp "$PANTRY_DIR/host-setup-state/lkg/files/$id" "$PANTRY_DIR/host-setup-state/candidate/files/$id"
    done
    host_manifest_build_installed "'"$TEST_REV"'" "$PANTRY_DIR/host-setup-state/candidate/manifest"
    host_apply_transaction "$PANTRY_DIR/host-setup-state/candidate"
    HOST_CTRL_TARGET="'"$TEST_REV"'"
    host_txn_post_apply_validate && exit 1 || exit 0
  ' _ "$UPDATE"
  rm -rf "$root"
}

test_interrupt_marker_prepared_via_pantry_test_interrupt_at() {
  local root rc trace
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  write_clean_control "$root"
  trace=$(mktemp)
  set +e
  PANTRY_DIR="$root" PANTRY_TEST_INTERRUPT_AT=prepared PANTRY_TEST_TRACE="$trace" bash -c '
    set -euo pipefail
    source "$1"
    staged="$PANTRY_DIR/stage"
    mkdir -p "$staged/systemd"
    cp "$PANTRY_DIR/publish-mode.sh" "$PANTRY_DIR/runtime-setup.sh" "$PANTRY_DIR/vapid-env.sh" "$staged/"
    cp "$PANTRY_DIR/systemd/pantry-backup.sh" "$staged/systemd/"
    cp "$PANTRY_DIR/systemd/pantry-update.sh" "$staged/systemd/"
    host_snapshot_build_from_staged "$PANTRY_DIR/host-setup-state/candidate" "$staged" "'"$TEST_REV"'"
    cand_man=$(host_manifest_hash "$PANTRY_DIR/host-setup-state/candidate/manifest")
    HOST_CTRL_PHASE=prepared
    HOST_CTRL_TARGET="'"$TEST_REV"'"
    HOST_CTRL_CAND_MANIFEST=$cand_man
    host_control_publish
    host_txn_test_hook prepared
  ' _ "$UPDATE"
  rc=$?
  set -e
  [[ "$rc" -eq 42 ]] || fail "interrupt at prepared expected rc 42 (got $rc)"
  grep -q '^prepared$' "$trace"
  rm -f "$trace"
  rm -rf "$root"
}

test_interrupt_marker_pending_recovery() {
  local root trace rc
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  trace=$(mktemp)
  printf 'version=1\nphase=applying\noriginal_reason=interrupted\n' >"$root/host-setup-state/control"
  set +e
  PANTRY_DIR="$root" PANTRY_TEST_TRACE="$trace" bash -c '
    set -euo pipefail
    source "$1"
    HOST_STATE_INITIALIZED=1
    host_recover_pending
  ' _ "$UPDATE"
  rc=$?
  set -e
  [[ "$rc" -eq 2 ]]
  grep -q '^pending-recover$' "$trace"
  rm -f "$trace"
  rm -rf "$root"
}

test_manifest_parse_rejects_unknown_lines() {
  local man
  man=$(mktemp)
  printf 'version=1\nrevision=%s\nbogus,row\n' "$TEST_REV" >"$man"
  PANTRY_DIR=/tmp bash -c '
    set -euo pipefail
    source "$1"
    host_manifest_parse_validate "'"$man"'" && exit 1 || exit 0
  ' _ "$UPDATE"
  rm -f "$man"
}

test_host_seed_refused_without_test_allow_manual_seed() {
  PANTRY_DIR=/tmp bash -c '
    set -euo pipefail
    source "$1"
    host_seed_from_manifest /x && exit 1 || exit 0
  ' _ "$UPDATE"
}

test_coordinator_temp_prefix_host_txn_v1() {
  PANTRY_DIR=/tmp bash -c '
    set -euo pipefail
    source "$1"
    grep -q "host-txn-v1" "$1"
  ' _ "$UPDATE" || fail "coordinator must use host-txn-v1 temp prefix"
}

test_protected_byte_and_mode_invariance_vs_frozen_base() {
  local path git_blob work_blob
  for path in "${PROTECTED[@]}"; do
    git_blob=$(git -C "$ROOT" ls-tree "${BASE_SHA}" "$path" | awk '{print $1}')
    work_blob=$(git -C "$ROOT" ls-files -s "$path" | awk '{print $1}')
    [[ "$git_blob" == "$work_blob" ]] || fail "git index mode drift on $path (protected)"
  done
}

test_sigkill_simulation_second_entry_recovers_pending() {
  local root work rc1 rc2
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  printf 'version=1\nphase=applying\noriginal_reason=interrupted\nfailed_target_sha=none\n' >"$root/host-setup-state/control"
  printf '\n# mid-apply-drift\n' >>"$root/publish-mode.sh"
  work=$(mktemp -d)
  make_bin "$work/bin"
  set +e
  PANTRY_DIR="$root" PANTRY_SETUP_SKIP_ROOT=1 PATH="$work/bin:$PATH" \
    bash "$root/systemd/pantry-update.sh" >/dev/null 2>&1
  rc1=$?
  PANTRY_DIR="$root" PANTRY_SETUP_SKIP_ROOT=1 PATH="$work/bin:$PATH" \
    bash "$root/systemd/pantry-update.sh" >/dev/null 2>&1
  rc2=$?
  set -e
  [[ "$rc1" -ne 0 && "$rc2" -ne 0 ]] || fail "sigkill-sim entries must exit nonzero"
  cmp -s "$root/host-setup-state/lkg/files/1" "$root/publish-mode.sh" \
    || fail "recovery must converge publish-mode to lkg manifest"
  rm -rf "$root" "$work"
}

test_ancestry_unreachable_defers_without_fatal() {
  local root state rc
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  write_clean_control "$root"
  state=$(mktemp -d)
  set +e
  PANTRY_DIR="$root" PANTRY_TEST_GITHUB_COMPARE_STATUS=unreachable bash -c '
    set -euo pipefail
    source "$1"
    rc=0
    host_subset_refresh_from_image "'"$state"'" || rc=$?
    exit "$rc"
  ' _ "$UPDATE"
  rc=$?
  set -e
  [[ "$rc" -eq 1 ]] || fail "unreachable ancestry should defer with rc 1 (got $rc)"
  PANTRY_DIR="$root" bash -c 'source "$1"; [[ "$(host_control_field phase)" == clean ]]' _ "$UPDATE" \
    || fail "unreachable ancestry must not publish prepared"
  rm -rf "$root" "$state"
}

test_ancestry_not_on_master_refuses_without_poison() {
  local root state rc
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  write_clean_control "$root"
  state=$(mktemp -d)
  PANTRY_DIR="$root" PANTRY_TEST_GITHUB_COMPARE_STATUS=diverged bash -c '
    set -euo pipefail
    source "$1"
    rc=0
    host_subset_refresh_from_image "'"$state"'" || rc=$?
    [[ "$rc" -eq 1 ]]
    [[ "$(host_control_field phase)" == clean ]]
  ' _ "$UPDATE"
  rm -rf "$root" "$state"
}

test_mixed_version_four_script_entry_recovery() {
  local root work rc
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  mkdir -p "$root/host-setup-state/candidate/files"
  printf '\n# candidate-mix\n' >"$root/host-setup-state/candidate/files/1"
  cp "$root/runtime-setup.sh" "$root/host-setup-state/candidate/files/2"
  printf 'version=1\nphase=applying\noriginal_reason=apply_failed\nfailed_target_sha=%s\n' "$TEST_REV" >"$root/host-setup-state/control"
  work=$(mktemp -d)
  make_bin "$work/bin"
  set +e
  PANTRY_DIR="$root" PANTRY_SETUP_SKIP_ROOT=1 PATH="$work/bin:$PATH" \
    bash "$root/systemd/pantry-update.sh" >/dev/null 2>&1
  rc=$?
  set -e
  [[ "$rc" -ne 0 ]] || fail "mixed-version pending must block normal exit 0"
  cmp -s "$root/publish-mode.sh" "$root/host-setup-state/lkg/files/1" \
    || fail "mixed-version recovery must restore lkg publish-mode"
  rm -rf "$root" "$work"
}

test_interrupt_marker_committing_phase() {
  local root rc
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  write_clean_control "$root"
  mkdir -p "$root/host-setup-state/candidate/files"
  cp "$root/host-setup-state/lkg/"{manifest,files/*} "$root/host-setup-state/candidate/" 2>/dev/null || true
  for id in 1 2 3 4; do
    cp "$root/host-setup-state/lkg/files/$id" "$root/host-setup-state/candidate/files/$id"
  done
  cp "$root/host-setup-state/lkg/manifest" "$root/host-setup-state/candidate/manifest"
  set +e
  PANTRY_DIR="$root" PANTRY_TEST_INTERRUPT_AT=committing bash -c '
    set -euo pipefail
    source "$1"
    HOST_CTRL_PHASE=committing
    HOST_CTRL_TARGET="'"$TEST_REV"'"
    host_control_publish
    host_txn_test_hook committing
  ' _ "$UPDATE"
  rc=$?
  set -e
  [[ "$rc" -eq 42 ]] || fail "interrupt at committing expected rc 42"
  rm -rf "$root"
}

test_invalid_flag_matrix_pantry_auto_setup_invalid() {
  local root
  root=$(mktemp -d)
  prepare_root "$root"
  PANTRY_DIR="$root" PANTRY_AUTO_SETUP=broken bash -c '
    set -euo pipefail
    source "$1"
    host_flags_read
    [[ "$HOST_FLAG_ANY_INVALID" -eq 1 ]]
  ' _ "$UPDATE"
  rm -rf "$root"
}

test_deploy_lock_uses_real_flock_serialization() {
  local root lock pid rc
  root=$(mktemp -d)
  mkdir -p "$root/deploy-trigger"
  lock="$root/deploy-trigger/.lock"
  ( exec 9>"$lock"; flock -x 9; sleep 3; ) &
  pid=$!
  sleep 0.2
  prepare_root "$root"
  work=$(mktemp -d)
  make_bin "$work/bin"
  rm -f "$work/bin/flock"
  ln -sf "$(command -v flock)" "$work/bin/flock"
  set +e
  PANTRY_DIR="$root" PANTRY_SETUP_SKIP_ROOT=1 PATH="$work/bin:$PATH" \
    timeout 5 bash "$root/systemd/pantry-update.sh" >/dev/null 2>&1
  rc=$?
  set -e
  wait "$pid" 2>/dev/null || true
  [[ "$rc" -ne 0 ]] || fail "blocked lock holder should not finish healthy deploy quickly"
  rm -rf "$root" "$work"
}

test_host_run_bounded_reaps_grandchild_ignoring_term() {
  local root ign marker rc
  root=$(mktemp -d)
  marker="pantry-bounded-survivor-$$-RANDOM"
  ign="$root/ignore-term.sh"
  cat >"$ign" <<EOF
#!/usr/bin/env bash
( trap true TERM; exec -a "${marker}-child" sleep 300 ) &
exec -a "${marker}-parent" sleep 300
EOF
  chmod +x "$ign"
  set +e
  PANTRY_DIR=/tmp bash -c '
    set -euo pipefail
    source "$1"
    host_run_bounded 1 bash "'"$ign"'" || true
  ' _ "$UPDATE"
  rc=$?
  sleep 0.2
  if pgrep -f "$marker" >/dev/null 2>&1; then
    rc=97
  fi
  set -e
  [[ "$rc" -eq 0 ]] || fail "bounded run left processes alive (rc=$rc)"
  rm -rf "$root"
}

live_signal_fresh_entries_converge() {
  local root="$1" work="$2" rc1 rc2
  set +e
  PANTRY_DIR="$root" PANTRY_SETUP_SKIP_ROOT=1 PATH="$work/bin:$PATH" \
    bash "$root/systemd/pantry-update.sh" >/dev/null 2>&1
  rc1=$?
  PANTRY_DIR="$root" PANTRY_SETUP_SKIP_ROOT=1 PATH="$work/bin:$PATH" \
    bash "$root/systemd/pantry-update.sh" >/dev/null 2>&1
  rc2=$?
  set -e
  [[ "$rc1" -ne 0 && "$rc2" -ne 0 ]] || return 1
  cmp -s "$root/host-setup-state/lkg/files/1" "$root/publish-mode.sh"
}

test_live_signal_term_during_applying_recovery_converges() {
  local root coord
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  printf 'version=1\nphase=applying\noriginal_reason=interrupted\nfailed_target_sha=none\n' \
    >"$root/host-setup-state/control"
  printf '\n# applying-live-signal-drift\n' >>"$root/publish-mode.sh"
  work=$(mktemp -d)
  make_bin "$work/bin"
  set +e
  PANTRY_DIR="$root" PANTRY_TEST_HOLD_AT=applying PANTRY_TEST_LIVE_SIGNAL=1 \
    PATH="$work/bin:$PATH" \
    bash -c 'set -euo pipefail; source "$1"; host_txn_test_hook applying' _ "$root/systemd/pantry-update.sh" &
  coord=$!
  sleep 0.35
  kill -TERM "$coord" 2>/dev/null || true
  sleep 0.1
  kill -KILL "$coord" 2>/dev/null || true
  wait "$coord" 2>/dev/null || true
  set -e
  live_signal_fresh_entries_converge "$root" "$work" \
    || fail "live TERM during applying must recover to lkg bytes"
  rm -rf "$work" "$root"
}

test_live_signal_int_during_applying_recovery_converges() {
  local root coord work
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  printf 'version=1\nphase=applying\noriginal_reason=interrupted\nfailed_target_sha=none\n' \
    >"$root/host-setup-state/control"
  printf '\n# applying-int-drift\n' >>"$root/publish-mode.sh"
  work=$(mktemp -d)
  make_bin "$work/bin"
  set +e
  PANTRY_DIR="$root" PANTRY_TEST_HOLD_AT=applying PANTRY_TEST_LIVE_SIGNAL=1 \
    PATH="$work/bin:$PATH" \
    bash -c 'set -euo pipefail; source "$1"; host_txn_test_hook applying' _ "$root/systemd/pantry-update.sh" &
  coord=$!
  sleep 0.35
  kill -INT "$coord" 2>/dev/null || true
  sleep 0.1
  kill -KILL "$coord" 2>/dev/null || true
  wait "$coord" 2>/dev/null || true
  set -e
  live_signal_fresh_entries_converge "$root" "$work" \
    || fail "live INT during applying must recover to lkg bytes"
  rm -rf "$work" "$root"
}

test_live_signal_term_during_committing_recovery_converges() {
  local root coord
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  mkdir -p "$root/host-setup-state/candidate/files"
  for id in 1 2 3 4; do
    cp "$root/host-setup-state/lkg/files/$id" "$root/host-setup-state/candidate/files/$id"
  done
  cp "$root/host-setup-state/lkg/manifest" "$root/host-setup-state/candidate/manifest"
  printf '\n# commit-live-marker\n' >>"$root/host-setup-state/candidate/files/1"
  work=$(mktemp -d)
  make_bin "$work/bin"
  PANTRY_DIR="$root" bash -c '
    set -euo pipefail
    source "$1"
    host_apply_transaction "$PANTRY_DIR/host-setup-state/candidate"
  ' _ "$root/systemd/pantry-update.sh"
  printf 'version=1\nphase=applying\noriginal_reason=interrupted\nfailed_target_sha=none\n' \
    >"$root/host-setup-state/control"
  set +e
  PANTRY_DIR="$root" PANTRY_TEST_HOLD_AT=committing PANTRY_TEST_LIVE_SIGNAL=1 \
    PATH="$work/bin:$PATH" \
    bash -c 'set -euo pipefail; source "$1"; host_commit_transaction' _ "$root/systemd/pantry-update.sh" &
  coord=$!
  sleep 0.35
  kill -TERM "$coord" 2>/dev/null || true
  sleep 0.1
  kill -KILL "$coord" 2>/dev/null || true
  wait "$coord" 2>/dev/null || true
  set -e
  live_signal_fresh_entries_converge "$root" "$work" \
    || fail "live TERM during committing must recover to lkg bytes"
  rm -rf "$work" "$root"
}

test_live_signal_int_during_committing_recovery_converges() {
  local root coord
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  mkdir -p "$root/host-setup-state/candidate/files"
  for id in 1 2 3 4; do
    cp "$root/host-setup-state/lkg/files/$id" "$root/host-setup-state/candidate/files/$id"
  done
  cp "$root/host-setup-state/lkg/manifest" "$root/host-setup-state/candidate/manifest"
  printf '\n# commit-int-marker\n' >>"$root/host-setup-state/candidate/files/1"
  work=$(mktemp -d)
  make_bin "$work/bin"
  PANTRY_DIR="$root" bash -c '
    set -euo pipefail
    source "$1"
    host_apply_transaction "$PANTRY_DIR/host-setup-state/candidate"
  ' _ "$root/systemd/pantry-update.sh"
  printf 'version=1\nphase=applying\noriginal_reason=interrupted\nfailed_target_sha=none\n' \
    >"$root/host-setup-state/control"
  set +e
  PANTRY_DIR="$root" PANTRY_TEST_HOLD_AT=committing PANTRY_TEST_LIVE_SIGNAL=1 \
    PATH="$work/bin:$PATH" \
    bash -c 'set -euo pipefail; source "$1"; host_commit_transaction' _ "$root/systemd/pantry-update.sh" &
  coord=$!
  sleep 0.35
  kill -INT "$coord" 2>/dev/null || true
  sleep 0.1
  kill -KILL "$coord" 2>/dev/null || true
  wait "$coord" 2>/dev/null || true
  set -e
  live_signal_fresh_entries_converge "$root" "$work" \
    || fail "live INT during committing must recover to lkg bytes"
  rm -rf "$work" "$root"
}

test_failed_target_sha_blocks_same_revision_retry() {
  local root
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  write_clean_control "$root"
  PANTRY_DIR="$root" bash -c '
    set -euo pipefail
    source "$1"
    HOST_CTRL_PHASE=clean
    HOST_CTRL_FAILED_TARGET="'"$TEST_REV"'"
    HOST_CTRL_TARGET="'"$TEST_REV"'"
    host_control_publish
    host_target_retry_allowed && exit 1 || exit 0
  ' _ "$UPDATE"
  rm -rf "$root"
}

test_acquire_deploy_lock_does_not_mkdir_deploy_trigger() {
  local body
  body=$(sed -n '/^host_acquire_deploy_lock()/,/^}/p' "$UPDATE")
  if [[ "$body" == *'mkdir -p "$PANTRY_DIR/deploy-trigger"'* ]]; then
    fail "host_acquire_deploy_lock must not mkdir deploy-trigger"
  fi
}

test_recovered_phase_normalizes_on_entry_and_continues() {
  local root work rc trace
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  printf 'version=1\nphase=recovered\noriginal_reason=apply_failed\n' \
    >"$root/host-setup-state/control"
  printf 'failed_target_sha=%s\n' "$TEST_REV" >>"$root/host-setup-state/control"
  printf 'target_revision=%s\n' "$TEST_REV" >>"$root/host-setup-state/control"
  work=$(mktemp -d)
  make_bin "$work/bin"
  trace="$root/entry-trace.log"
  : >"$trace"
  set +e
  PANTRY_DIR="$root" PANTRY_SETUP_SKIP_ROOT=1 PANTRY_UPDATE_HEALTH_TIMEOUT=0 \
    PANTRY_TEST_ENTRY_TRACE="$trace" \
    PATH="$work/bin:$PATH" \
    bash "$root/systemd/pantry-update.sh" >/dev/null 2>"$work/err"
  set -e
  grep -q 'blocked by host setup state' "$work/err" \
    && fail "recovered state must not block the updater: $(cat "$work/err")"
  [[ "$(awk -F= '$1=="phase"{print $2;exit}' "$root/host-setup-state/control")" == clean ]] \
    || fail "recovered should normalize control to clean on entry"
  grep -q '^host_normalize_recovered$' "$trace" \
    || fail "recovered entry must run host_normalize_recovered"
  grep -q '^publish-mode-source$' "$trace" \
    || fail "recovered entry must pass gate and source publish-mode"
  [[ "$(awk -F= '$1=="failed_target_sha"{print $2;exit}' "$root/host-setup-state/control")" == "$TEST_REV" ]] \
    || fail "failed_target_sha must survive normalization"
  rm -rf "$root" "$work"
}

test_pantry_auto_setup_quoted_in_dot_env_is_invalid() {
  local root rc
  root=$(mktemp -d)
  prepare_root "$root"
  printf 'PANTRY_AUTO_SETUP="on"\n' >>"$root/.env"
  set +e
  PANTRY_DIR="$root" bash -c '
    set -euo pipefail
    source "$1"
    host_flags_read
    [[ "$HOST_FLAG_ANY_INVALID" -eq 1 ]]
  ' _ "$UPDATE"
  rc=$?
  set -e
  [[ "$rc" -eq 0 ]] || fail "quoted PANTRY_AUTO_SETUP in .env must be invalid (rc=$rc)"
  rm -rf "$root"
}

test_pantry_auto_setup_duplicate_dot_env_keys_are_invalid() {
  local root rc
  root=$(mktemp -d)
  prepare_root "$root"
  printf 'PANTRY_AUTO_SETUP=on\nPANTRY_AUTO_SETUP=off\n' >>"$root/.env"
  set +e
  PANTRY_DIR="$root" bash -c '
    set -euo pipefail
    source "$1"
    host_flags_read
    [[ "$HOST_FLAG_ANY_INVALID" -eq 1 ]]
  ' _ "$UPDATE"
  rc=$?
  set -e
  [[ "$rc" -eq 0 ]] || fail "duplicate PANTRY_AUTO_SETUP keys must be invalid (rc=$rc)"
  rm -rf "$root"
}

test_uninitialized_quoted_auto_setup_blocks_legacy_refresh_on_entry() {
  local root state rc
  root=$(mktemp -d)
  prepare_root "$root"
  printf 'PANTRY_AUTO_SETUP="on"\n' >>"$root/.env"
  state=$(mktemp -d)
  set +e
  PANTRY_DIR="$root" bash -c '
    set -euo pipefail
    source "$1"
    host_flags_read
    [[ "$HOST_FLAG_ANY_INVALID" -eq 1 ]]
    rc=0
    refresh_host_scripts_from_image "'"$state"'" || rc=$?
    [[ "$rc" -eq 3 ]]
  ' _ "$UPDATE"
  rc=$?
  set -e
  [[ "$rc" -eq 0 ]] || fail "uninitialized quoted AUTO must fail closed at refresh (rc=$rc)"
  rm -rf "$root" "$state"
}

test_systemd_analyze_verify_failure_blocks_subset_validate() {
  local root stage work rc
  root=$(mktemp -d)
  prepare_root "$root"
  stage="$root/stage"
  mkdir -p "$stage/systemd"
  cp "$root/publish-mode.sh" "$root/runtime-setup.sh" "$root/vapid-env.sh" "$stage/"
  cp "$root/systemd/pantry-backup.sh" "$root/systemd/pantry-update.sh" "$stage/systemd/"
  cp "$ROOT/deploy/systemd/pantry-update.service" "$ROOT/deploy/systemd/pantry-update.timer" \
    "$ROOT/deploy/systemd/pantry-update.path" "$ROOT/deploy/systemd/pantry-backup.service" \
    "$ROOT/deploy/systemd/pantry-backup.timer" "$stage/systemd/"
  work=$(mktemp -d)
  make_bin "$work/bin" 1
  set +e
  PANTRY_DIR="$root" PATH="$work/bin:$PATH" bash -c '
    set -euo pipefail
    source "$1"
    host_subset_validate "'"$stage"'"
  ' _ "$UPDATE"
  rc=$?
  set -e
  [[ "$rc" -ne 0 ]] || fail "systemd-analyze verify failure must fail host_subset_validate"
  rm -rf "$root" "$work"
}

test_time_budget_before_commit_restores_lkg_not_clean() {
  local root rc
  root=$(mktemp -d)
  prepare_root "$root"
  write_lkg_snapshot "$root"
  mkdir -p "$root/host-setup-state/candidate/files"
  for id in 1 2 3 4; do
    cp "$root/host-setup-state/lkg/files/$id" "$root/host-setup-state/candidate/files/$id"
  done
  cp "$root/host-setup-state/lkg/manifest" "$root/host-setup-state/candidate/manifest"
  printf '\n# budget-commit-marker\n' >>"$root/host-setup-state/candidate/files/1"
  set +e
  PANTRY_DIR="$root" PANTRY_TEST_SKIP_HEALTH=1 PANTRY_TEST_TXN_START_ADJUST=121 bash -c '
    set -euo pipefail
    source "$1"
    host_apply_transaction "$PANTRY_DIR/host-setup-state/candidate"
    HOST_CTRL_PHASE=applying
    HOST_CTRL_TARGET="'"$TEST_REV"'"
    host_control_publish
    txn_start=$((SECONDS - PANTRY_TEST_TXN_START_ADJUST))
    host_txn_health_validate /tmp/x
    if [[ $((SECONDS - txn_start)) -gt "$HOST_TXN_BUDGET_TXN" ]]; then
      host_txn_abort_to_recovered time_budget_exceeded "'"$TEST_REV"'" || exit 2
      exit 3
    fi
    exit 0
  ' _ "$UPDATE"
  rc=$?
  set -e
  [[ "$rc" -eq 3 ]] || fail "time budget overrun path must return rc 3 (got $rc)"
  [[ "$(awk -F= '$1=="phase"{print $2;exit}' "$root/host-setup-state/control")" == recovered ]] \
    || fail "budget overrun must land in recovered, not committed clean"
  cmp -s "$root/host-setup-state/lkg/files/1" "$root/publish-mode.sh" \
    || fail "budget overrun must restore installed scripts to lkg"
  grep -q 'budget-commit-marker' "$root/publish-mode.sh" \
    && fail "budget overrun must not leave candidate bytes on disk"
  rm -rf "$root"
}

for fn in \
  entry_order_recovery_before_publish_mode_sources_and_flags \
  pending_recovery_exits_nonzero_before_image_work \
  invalid_flag_matrix_pantry_host_refresh_invalid \
  invalid_flag_matrix_either_invalid_wins_over_valid_other \
  pantry_auto_setup_env_beats_dot_env \
  flag_matrix_both_off_valid \
  guard_rejects_coordinator_change_whole_bundle \
  idempotent_restore_converges_manifest_driven \
  interrupted_restore_second_pass_converges \
  initialized_auto_off_freezes_no_legacy_bypass \
  retention_before_prepared_blocks_promotion_with_prior \
  uninitialized_auto_on_refuses_legacy_refresh \
  corrupt_state_blocks_whole_updater \
  apply_failure_mid_apply_aborts_to_recovered \
  validation_failure_before_prepared_write \
  health_failure_after_apply \
  health_ok_post_apply_validation_mismatch_fails \
  interrupt_marker_prepared_via_pantry_test_interrupt_at \
  interrupt_marker_pending_recovery \
  manifest_parse_rejects_unknown_lines \
  host_seed_refused_without_test_allow_manual_seed \
  coordinator_temp_prefix_host_txn_v1 \
  protected_byte_and_mode_invariance_vs_frozen_base \
  sigkill_simulation_second_entry_recovers_pending \
  ancestry_unreachable_defers_without_fatal \
  ancestry_not_on_master_refuses_without_poison \
  mixed_version_four_script_entry_recovery \
  interrupt_marker_committing_phase \
  invalid_flag_matrix_pantry_auto_setup_invalid \
  deploy_lock_uses_real_flock_serialization \
  failed_target_sha_blocks_same_revision_retry \
  acquire_deploy_lock_does_not_mkdir_deploy_trigger \
  recovered_phase_normalizes_on_entry_and_continues \
  pantry_auto_setup_quoted_in_dot_env_is_invalid \
  pantry_auto_setup_duplicate_dot_env_keys_are_invalid \
  uninitialized_quoted_auto_setup_blocks_legacy_refresh_on_entry \
  systemd_analyze_verify_failure_blocks_subset_validate \
  time_budget_before_commit_restores_lkg_not_clean \
  host_run_bounded_reaps_grandchild_ignoring_term \
  live_signal_term_during_applying_recovery_converges \
  live_signal_int_during_applying_recovery_converges \
  live_signal_term_during_committing_recovery_converges \
  live_signal_int_during_committing_recovery_converges
do
  run_case "$fn" "test_$fn"
done

echo "host_setup_transaction_test ok (${#TEST_CASES[@]} cases)"
printf 'cases:\n' >&2
printf '  %s\n' "${TEST_CASES[@]}" >&2
