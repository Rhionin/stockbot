#!/usr/bin/env bash
# Stub fault suite and byte invariance for #152 host setup transaction.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
UPDATE="$ROOT/deploy/systemd/pantry-update.sh"
BASE_SHA="${HOST_SETUP_BASE_SHA:-3d265d5ce4f6ea7aa3c470b1cc3c5d610a6010f2}"

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
  changed=$(git -C "$ROOT" diff --name-only "${BASE_SHA}" 2>/dev/null || true)
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
  local bin="$1"
  mkdir -p "$bin"
  cat > "$bin/docker" << 'EOF'
#!/usr/bin/env bash
exit 0
EOF
  cat > "$bin/curl" << 'EOF'
#!/usr/bin/env bash
printf '{"status":"ok"}'
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
  local lock_line source_line
  lock_line=$(grep -n '^deploy_lock$' "$trace" | head -1 | cut -d: -f1 || true)
  source_line=$(grep -n '^publish-mode-source$' "$trace" | head -1 | cut -d: -f1 || true)
  [[ -n "$lock_line" && -n "$source_line" ]] || fail "missing lock or publish-mode trace markers"
  [[ "$lock_line" -lt "$source_line" ]] || fail "publish-mode sourced before deploy lock"
  local flags_line
  flags_line=$(grep -n '^host_flags_read$' "$trace" | head -1 | cut -d: -f1 || true)
  [[ -n "$flags_line" && "$flags_line" -lt "$source_line" ]] \
    || fail "flags read after publish-mode source"
}

# Entry ordering with uninitialized state
trace_root=$(mktemp -d)
prepare_root "$trace_root"
run_entry_trace "$trace_root" "$trace_root/trace.log"
assert_order_before_source "$trace_root/trace.log"
rm -rf "$trace_root"

# Pending recovery before helpers: exits nonzero
pend_root=$(mktemp -d)
prepare_root "$pend_root"
state="$pend_root/host-setup-state"
mkdir -p "$state/lkg/files"
cp "$pend_root/publish-mode.sh" "$state/lkg/files/1"
printf 'phase=applying\nversion=1\n' >"$state/control"
work=$(mktemp -d)
make_bin "$work/bin"
set +e
PANTRY_DIR="$pend_root" PANTRY_SETUP_SKIP_ROOT=1 PATH="$work/bin:$PATH" \
  bash "$pend_root/systemd/pantry-update.sh" >/dev/null 2>&1
rc=$?
set -e
[[ "$rc" -ne 0 ]] || fail "pending recovery should exit nonzero"
rm -rf "$pend_root" "$work"

# Flag matrix via embedded parser
env_root=$(mktemp -d)
prepare_root "$env_root"
printf 'AUTO_SETUP=on\n' >>"$env_root/.env"
set +e
HOST_REFRESH=bad PANTRY_DIR="$env_root" PANTRY_SETUP_SKIP_ROOT=1 PATH="$(dirname "$UPDATE"):$PATH" \
  bash -c 'source "$1"; PANTRY_DIR="'"$env_root"'"; host_flags_read; exit "$HOST_FLAG_ANY_INVALID"' _ "$UPDATE"
rc=$?
set -e
[[ "$rc" -eq 1 ]] || fail "invalid HOST_REFRESH should set HOST_FLAG_ANY_INVALID"
rm -rf "$env_root"

# AUTO_SETUP env beats .env
auto_root=$(mktemp -d)
prepare_root "$auto_root"
printf 'AUTO_SETUP=off\n' >>"$auto_root/.env"
PANTRY_DIR="$auto_root" AUTO_SETUP=on bash -c '
  set -euo pipefail
  # shellcheck disable=SC1091
  source "$1"
  host_flags_read
  [[ "$HOST_FLAG_AUTO_SETUP" == on ]]
' _ "$UPDATE"

# Guard rejects coordinator change in staged bundle
guard_root=$(mktemp -d)
prepare_root "$guard_root"
stage="$guard_root/stage"
mkdir -p "$stage/systemd"
cp "$guard_root/publish-mode.sh" "$stage/"
cp "$guard_root/runtime-setup.sh" "$stage/"
cp "$guard_root/vapid-env.sh" "$stage/"
cp "$guard_root/systemd/pantry-backup.sh" "$stage/systemd/"
printf '\n# changed\n' >>"$stage/systemd/pantry-update.sh"
PANTRY_DIR="$guard_root" bash -c '
  set -euo pipefail
  source "$1"
  host_guard_validate "'"$stage"'" && exit 1 || exit 0
' _ "$UPDATE"
rm -rf "$guard_root"

# Idempotent restore converges
restore_root=$(mktemp -d)
prepare_root "$restore_root"
state="$restore_root/host-setup-state"
mkdir -p "$state/lkg/files"
for id in 1 2 3 4; do
  case "$id" in
    1) cp "$restore_root/publish-mode.sh" "$state/lkg/files/$id" ;;
    2) cp "$restore_root/runtime-setup.sh" "$state/lkg/files/$id" ;;
    3) cp "$restore_root/vapid-env.sh" "$state/lkg/files/$id" ;;
    4) cp "$restore_root/systemd/pantry-backup.sh" "$state/lkg/files/$id" ;;
  esac
done
printf '\n# drift\n' >>"$restore_root/publish-mode.sh"
PANTRY_DIR="$restore_root" HOST_STATE_INITIALIZED=1 bash -c '
  set -euo pipefail
  source "$1"
  host_restore_transaction
  cmp -s "$PANTRY_DIR/host-setup-state/lkg/files/1" "$PANTRY_DIR/publish-mode.sh"
' _ "$UPDATE"
rm -rf "$restore_root"

echo "host_setup_transaction_test ok"
