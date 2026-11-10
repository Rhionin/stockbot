#!/usr/bin/env bash
# Behavior checks for the Pi backup wrapper. Docker is a stub.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
fail() {
  echo "FAIL: $*" >&2
  exit 1
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
bin="$work/bin"
mkdir -p "$bin" "$work/pantry/secrets/backup" "$work/pantry/backup" "$work/pantry/deploy-trigger"
cp "$ROOT/deploy/publish-mode.sh" "$work/pantry/publish-mode.sh"
: >"$work/docker.log"

TEST_IMAGE_SHA="${TEST_IMAGE_SHA:-sha256:00000000000000000000000000000000000000000000000000000000000abc}"

cat >"$bin/docker" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >>"\${DOCKER_LOG:?}"
if [[ "\$1" == inspect ]]; then
  printf '%s\n' "$TEST_IMAGE_SHA"
  exit 0
fi
EOF
cat >>"$bin/docker" <<'EOF'
if [[ "$1" == rm ]]; then
  exit 0
fi
if [[ "$*" == *cleanup-stage* ]]; then
  exit 0
fi
if [[ "$*" == *"/config-secrets:ro"* ]]; then
  src=$(printf '%s\n' "$*" | sed -n 's/.*--volume \([^ ]*\):\/config-secrets:ro.*/\1/p')
  [[ -d "$src" ]] || exit 4
  [[ ! -e "$src/.env.example" ]] || exit 4
  for f in "$src"/*; do
    [[ -f "$f" ]] || exit 4
    mode=$(stat -c '%a' "$f")
    [[ "$mode" == "400" ]] || exit 4
  done
fi
if [[ "${SLEEP_RUN:-}" == 1 && "$*" == *"--stage"* ]]; then
  printf '%s\n' "$$" >"${SLEEP_PID:?}"
  sleep 30
  exit 0
fi
if [[ "$*" == *" run "* ]]; then
  if [[ "${FAIL_RUN:-}" == 1 ]]; then
    if [[ -n "${FAIL_RUN_CLASS:-}" ]]; then
      printf 'backup failed class=%s\n' "${FAIL_RUN_CLASS}" >&2
    fi
    exit "${FAIL_RUN_RC:-20}"
  fi
  if [[ -n "${BACKUP_RESULT_LINE:-}" ]]; then
    printf '%s\n' "${BACKUP_RESULT_LINE}"
  fi
  if [[ "${BACKUP_STDOUT_FALLBACK:-}" == 1 ]]; then
    printf '%s\n' "backup ok key=nightly/2026-11-10T000000Z.pantry.db.gz.age integrity=ok rows=1 staging=tmpfs at=now"
  elif [[ -z "${BACKUP_RESULT_LINE:-}" ]]; then
    printf '%s\n' "backup ok key=nightly/2026-11-10T000000Z.pantry.db.gz.age integrity=ok rows=1 staging=tmpfs at=now"
  fi
  exit 0
fi
exit 0
EOF
chmod +x "$bin/docker"

receipt_dir() {
  printf '%s' "$work/pantry/backup/receipts"
}

assert_receipt_absent() {
  local needle=$1
  local f
  while IFS= read -r -d '' f; do
    if grep -qF -- "$needle" "$f" 2>/dev/null; then
      fail "receipt leak: found $needle in $f"
    fi
  done < <(find "$(receipt_dir)" -type f -print0 2>/dev/null || true)
}

assert_dir_empty() {
  local d=$1
  [[ -d "$d" ]] || fail "assert_dir_empty: not a directory: $d"
  if [[ -n "$(find "$d" -mindepth 1 -maxdepth 1 2>/dev/null | head -n1)" ]]; then
    fail "assert_dir_empty: directory not empty: $d"
  fi
}

assert_file_bytes() {
  local file=$1 expected=$2
  [[ -f "$file" ]] || fail "assert_file_bytes: missing $file"
  [[ "$(cat "$file")" == "$expected" ]] || fail "assert_file_bytes: $file changed"
}

assert_reason_allowed() {
  local file=$1 reason
  reason=$(json_get "$file" reason)
  case "$reason" in
    nightly|manual|pre-deploy|drill|stale|invalid) ;;
    *) fail "receipt reason not allowlisted: ${reason:-<empty>}" ;;
  esac
}

assert_no_receipt_temp_files() {
  local dir=$1 f
  [[ -d "$dir" ]] || return 0
  while IFS= read -r -d '' f; do
    fail "leftover receipt temp file: $f"
  done < <(find "$dir" -maxdepth 1 \( -name '.tmp.*' -o -name 'history.jsonl.tmp.*' \) -print0 2>/dev/null || true)
}

json_get() {
  local file=$1 key=$2
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$file" "$key" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    doc = json.load(f)
key = sys.argv[2]
cur = doc
for part in key.split("."):
    if part not in cur:
        print("", end="")
        sys.exit(0)
    cur = cur[part]
if cur is None:
    print("null", end="")
elif isinstance(cur, bool):
    print("true" if cur else "false", end="")
else:
    print(cur, end="")
PY
    return 0
  fi
  grep -o "\"${key//./\":\"}[^\"]*\"" "$file" 2>/dev/null | head -1 | sed 's/.*:"\(.*\)"/\1/' || true
}

run_backup() {
  local rc
  set +e
  DOCKER_LOG="$work/docker.log" \
    DOCKER="$bin/docker" \
    PATH="$bin:$PATH" \
    PANTRY_DIR="$work/pantry" \
    PANTRY_RECEIPT_DIR="$(receipt_dir)" \
    PANTRY_DATA_VOLUME="pantry_pantry-data" \
    PANTRY_SETUP_SKIP_ROOT=1 \
    PANTRY_CONTAINER=pantry \
    FAIL_RUN="${FAIL_RUN:-}" \
    FAIL_RUN_RC="${FAIL_RUN_RC:-}" \
    FAIL_RUN_CLASS="${FAIL_RUN_CLASS:-}" \
    BACKUP_RESULT_LINE="${BACKUP_RESULT_LINE:-}" \
    BACKUP_STDOUT_FALLBACK="${BACKUP_STDOUT_FALLBACK:-}" \
    SLEEP_RUN="${SLEEP_RUN:-}" \
    SLEEP_PID="${SLEEP_PID:-}" \
    CURL_LOG="${CURL_LOG:-}" \
    PANTRY_BACKUP_LOCK_WAIT="${PANTRY_BACKUP_LOCK_WAIT:-2}" \
    PANTRY_BACKUP_DEPLOY_MODE="${PANTRY_BACKUP_DEPLOY_MODE:-}" \
    PANTRY_BACKUP_TARGET_REVISION="${PANTRY_BACKUP_TARGET_REVISION:-}" \
    PANTRY_BACKUP_TARGET_DIGEST="${PANTRY_BACKUP_TARGET_DIGEST:-}" \
    PANTRY_RECEIPT_KEEP="${PANTRY_RECEIPT_KEEP:-}" \
    SANITIZE_STDERR="${SANITIZE_STDERR:-}" \
    bash "$ROOT/deploy/systemd/pantry-backup.sh" "$@" >"$work/out" 2>"$work/err" 8>&- 9>&-
  rc=$?
  set -e
  printf '%s' "$rc"
}

: >"$work/docker.log"
rc=$(run_backup nightly)
[[ "$rc" == 0 ]] || fail "nightly backup wrapper rc=$rc err=$(cat "$work/err")"
log=$(cat "$work/docker.log")
for needle in \
  "--user 65532:65532" \
  "--read-only" \
  "--cap-drop ALL" \
  "--security-opt no-new-privileges" \
  "--pids-limit 64" \
  "--memory 128m" \
  "--tmpfs /tmp:rw,noexec,nosuid,size=16m,mode=0700,uid=65532,gid=65532" \
  "--tmpfs /run/stockbot-backup:rw,noexec,nosuid,size=64m,mode=0700,uid=65532,gid=65532" \
  "--stage /run/stockbot-backup" \
  "--volume pantry_pantry-data:/data" \
  "--entrypoint /usr/local/bin/stockbot-backup" \
  "cleanup-stage -db /data/pantry.db" \
  "--image-digest $TEST_IMAGE_SHA"
do
  [[ "$log" == *"$needle"* ]] || fail "docker args missing $needle"
done
tmpfs_n=$(grep -Fc -- '--tmpfs /tmp:rw,noexec,nosuid,size=16m,mode=0700,uid=65532,gid=65532' "$work/docker.log" || true)
[[ "$tmpfs_n" -eq 2 ]] || fail "tmpfs /tmp uid 65532 must be on both run_locked and cleanup_stage (got ${tmpfs_n})"
[[ "$log" == *--env-file* ]] && fail "docker was passed --env-file"
[[ "$log" == *--with-secrets* ]] && fail "secrets were archived when no config files exist"
[[ "$log" == *R2_SECRET* ]] && fail "docker args named an R2 secret"
grep -q 'backup ok key=' "$work/out" || fail "success line was not printed"

: >"$work/docker.log"
FAIL_RUN=1
rc=$(FAIL_RUN=1 run_backup manual)
unset FAIL_RUN
[[ "$rc" != 0 ]] || fail "a failed docker run should fail the wrapper"
grep -q 'cleanup-stage' "$work/docker.log" || fail "failed run did not trap-clean the stage"

printf 'auth-line\n' >"$work/pantry/auth.caddy"
printf 'env-line\n' >"$work/pantry/.env"
printf 'bak-line\n' >"$work/pantry/.env.bak"
printf 'example-line\n' >"$work/pantry/.env.example"
: >"$work/docker.log"
rc=$(run_backup nightly)
[[ "$rc" == 0 ]] || fail "config secret backup rc=$rc err=$(cat "$work/err")"
log=$(cat "$work/docker.log")
for needle in \
  "--with-secrets" \
  "--secrets-path /config-secrets/auth.caddy" \
  "--secrets-path /config-secrets/.env.bak" \
  ":/config-secrets:ro"
do
  [[ "$log" == *"$needle"* ]] || fail "docker args missing $needle"
done
if printf '%s\n' "$log" | grep -E -q '(^| )--secrets-path /config-secrets/\.env( |$)'; then
  fail "live .env was archived by default"
fi
[[ "$log" == *".env.example"* ]] && fail "env example was archived"
src=$(sed -n 's/.*--volume \([^ ]*\):\/config-secrets:ro.*/\1/p' <<<"$log")
[[ -n "$src" ]] || fail "config secret mount was not recorded"
[[ ! -d "$src" ]] || fail "config secret copy was left behind"

mkdir -p "$work/pantry/backup"
printf '%s\n' "$work/pantry/.env" >"$work/pantry/backup/secrets-paths"
: >"$work/docker.log"
rc=$(run_backup nightly)
[[ "$rc" == 0 ]] || fail "explicit .env archive rc=$rc err=$(cat "$work/err")"
log=$(cat "$work/docker.log")
if ! printf '%s\n' "$log" | grep -E -q '(^| )--secrets-path /config-secrets/\.env( |$)'; then
  fail "an explicitly configured .env was not archived"
fi
rm -f "$work/pantry/backup/secrets-paths"

printf 'HC_BACKUP_RUN_URL=https://hc.example/backup-run-token\n' >"$work/pantry/secrets/backup/r2.env"
chmod 0400 "$work/pantry/secrets/backup/r2.env"
cat >"$bin/curl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${CURL_LOG:?}"
exit 0
EOF
chmod +x "$bin/curl"

rm -f "$work/pantry/auth.caddy"
ln -s "$work/nowhere" "$work/pantry/auth.caddy"
: >"$work/docker.log"
: >"$work/curl.log"
rc=$(CURL_LOG="$work/curl.log" run_backup nightly)
[[ "$rc" == 40 ]] || fail "symlink config secret rc=$rc"
[[ ! -s "$work/docker.log" ]] || fail "symlink config secret started a backup"
grep -q '/fail' "$work/curl.log" || fail "symlink config secret did not ping healthchecks"
[[ "$(cat "$work/out" "$work/err")" != *backup-run-token* ]] || fail "healthchecks URL was printed"
[[ -f "$(receipt_dir)/latest.json" ]] || fail "exit 40 secret refusal missing receipt"
[[ "$(json_get "$(receipt_dir)/latest.json" exit_code)" == "40" ]] \
  || fail "exit 40 receipt exit_code"
[[ "$(json_get "$(receipt_dir)/latest.json" failed_stage)" == "secrets-preflight" ]] \
  || fail "exit 40 receipt failed_stage"
[[ "$(json_get "$(receipt_dir)/latest.json" status)" == "failed" ]] \
  || fail "exit 40 receipt status"
assert_reason_allowed "$(receipt_dir)/latest.json"
[[ "$(json_get "$(receipt_dir)/latest.json" reason)" == "nightly" ]] \
  || fail "exit 40 receipt reason"
assert_receipt_absent "a config secret is a symlink"
rm -f "$work/pantry/auth.caddy"

mkdir -p "$work/pantry/not-a-file"
printf '%s\n' "$work/pantry/not-a-file" >"$work/pantry/backup/secrets-paths"
: >"$work/docker.log"
: >"$work/curl.log"
rc=$(CURL_LOG="$work/curl.log" run_backup nightly)
[[ "$rc" == 40 ]] || fail "invalid config secret rc=$rc err=$(cat "$work/err")"
[[ ! -s "$work/docker.log" ]] || fail "invalid config secret started a backup"
grep -q '/fail' "$work/curl.log" || fail "invalid config secret did not ping healthchecks"
rm -f "$work/pantry/backup/secrets-paths" "$work/pantry/.env" "$work/pantry/.env.bak" "$work/pantry/.env.example"
rmdir "$work/pantry/not-a-file"

for kind in symlink broken directory; do
  rm -rf "$work/pantry/backup/secrets-paths"
  case "$kind" in
    symlink)
      printf '%s\n' "/opt/pantry/auth.caddy" >"$work/real-list"
      ln -s "$work/real-list" "$work/pantry/backup/secrets-paths"
      ;;
    broken)
      ln -s "$work/missing-list" "$work/pantry/backup/secrets-paths"
      ;;
    directory)
      mkdir "$work/pantry/backup/secrets-paths"
      ;;
  esac
  : >"$work/docker.log"
  : >"$work/curl.log"
  rc=$(CURL_LOG="$work/curl.log" run_backup nightly)
  [[ "$rc" == 40 ]] || fail "$kind secrets list rc=$rc err=$(cat "$work/err")"
  [[ ! -s "$work/docker.log" ]] || fail "$kind secrets list started a backup"
  grep -q '/fail' "$work/curl.log" || fail "$kind secrets list did not ping healthchecks"
done
rm -rf "$work/pantry/backup/secrets-paths" "$work/real-list"

: >"$work/docker.log"
: >"$work/curl.log"
exec 8>"$work/pantry/deploy-trigger/.lock"
flock -n 8 || fail "could not hold the deploy lock in the test"
rc=$(CURL_LOG="$work/curl.log" PANTRY_BACKUP_LOCK_WAIT=1 run_backup nightly)
[[ "$rc" == 30 ]] || fail "lock timeout rc=$rc err=$(cat "$work/err")"
[[ ! -s "$work/docker.log" ]] || fail "backup talked to docker without the lock"
grep -q '/fail' "$work/curl.log" || fail "lock timeout did not ping healthchecks"
[[ "$(cat "$work/out" "$work/err")" != *backup-run-token* ]] || fail "healthchecks URL was printed"
flock -u 8

# The updater already holds this lock. A second flock would wait until
# that process exits, and the updater is waiting on this script.
: >"$work/docker.log"
: >"$work/curl.log"
exec 8>"$work/pantry/deploy-trigger/.lock"
flock -n 8 || fail "could not hold the deploy lock for the nested backup"
rc=$(CURL_LOG="$work/curl.log" PANTRY_BACKUP_LOCK_HELD=1 PANTRY_BACKUP_LOCK_WAIT=1 run_backup pre-deploy)
[[ "$rc" == 0 ]] || fail "backup while the updater holds the lock rc=$rc err=$(cat "$work/err") out=$(cat "$work/out")"
grep -q 'deploy lock already held' "$work/out" || fail "held-lock backup did not say the lock was already held"
held_log=$(cat "$work/docker.log")
[[ "$held_log" == *"--reason pre-deploy"* ]] || fail "held-lock backup did not pass pre-deploy"
[[ "$held_log" == *"--entrypoint /usr/local/bin/stockbot-backup"* ]] || fail "held-lock backup did not start the container"
flock -u 8

: >"$work/docker.log"
rm -f "$work/sleep.pid"
SLEEP_RUN=1 SLEEP_PID="$work/sleep.pid" \
  DOCKER_LOG="$work/docker.log" \
  DOCKER="$bin/docker" \
  PATH="$bin:$PATH" \
  PANTRY_DIR="$work/pantry" \
  PANTRY_DATA_VOLUME="pantry_pantry-data" \
  PANTRY_SETUP_SKIP_ROOT=1 \
  PANTRY_CONTAINER=pantry \
  bash "$ROOT/deploy/systemd/pantry-backup.sh" nightly >"$work/out" 2>"$work/err" 8>&- 9>&- &
backup_pid=$!
ready=0
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  if grep -q -- '--stage /run/stockbot-backup' "$work/docker.log"; then
    ready=1
    break
  fi
  sleep 0.1
done
[[ "$ready" == 1 ]] || fail "backup did not reach docker run"
kill -9 "$backup_pid" 2>/dev/null || true
stopped=0
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
  rms=$(grep -c 'rm -f pantry-backup' "$work/docker.log" || true)
  if [[ "${rms:-0}" -ge 2 ]]; then
    stopped=1
    break
  fi
  sleep 0.1
done
if [[ -f "$work/sleep.pid" ]]; then
  kill "$(cat "$work/sleep.pid")" 2>/dev/null || true
fi
[[ "$stopped" == 1 ]] || fail "container was still running after the lock holder died"

# --- backup receipts ---
receipt_root="$(receipt_dir)"
[[ -d "$receipt_root" ]] || fail "nightly run did not create receipts dir"
mode=$(stat -c '%a' "$receipt_root")
[[ "$mode" == "755" ]] || fail "receipts dir mode is $mode, want 755"
[[ -f "$receipt_root/latest.json" ]] || fail "nightly missing latest.json"
latest_mode=$(stat -c '%a' "$receipt_root/latest.json")
[[ "$latest_mode" == "644" ]] || fail "latest.json mode is $latest_mode"
if command -v python3 >/dev/null 2>&1; then
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$receipt_root/latest.json" \
    || fail "latest.json is not valid JSON"
fi
[[ "$(json_get "$receipt_root/latest.json" status)" == "ok" ]] || fail "latest status not ok"
[[ "$(json_get "$receipt_root/latest.json" exit_code)" == "0" ]] || fail "latest exit_code not 0"
[[ "$(json_get "$receipt_root/latest.json" result_source)" == "stdout-fallback" ]] \
  || fail "latest result_source not stdout-fallback"
[[ "$(json_get "$receipt_root/latest.json" integrity)" == "ok" ]] || fail "latest integrity not ok"
assert_reason_allowed "$receipt_root/latest.json"
assert_no_receipt_temp_files "$receipt_root"

rm -rf "$receipt_root"
mkdir -p "$work/pantry/backup"
BACKUP_RESULT_LINE='backup result {"schema":1,"ok":true,"stored":true,"object_key":"nightly/2026-11-10T120000Z.pantry.db.gz.age","manifest_key":"nightly/2026-11-10T120000Z.manifest.json","integrity":"ok","foreign_key_check":"ok","ciphertext_size":481233,"ciphertext_sha256":"9f2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c","last_migration":"0031_example","staging":"tmpfs","tool_version":"v1.2.3","created_at":"2026-11-10T12:00:00Z"}'
: >"$work/docker.log"
rc=$(run_backup nightly)
[[ "$rc" == 0 ]] || fail "go result nightly rc=$rc"
[[ "$(json_get "$(receipt_dir)/latest.json" result_source)" == "go" ]] || fail "go result_source"
[[ "$(json_get "$(receipt_dir)/latest.json" ciphertext_size)" == "481233" ]] || fail "go ciphertext_size"
[[ "$(json_get "$(receipt_dir)/latest.json" object_key)" == *120000Z* ]] || fail "go object_key"

rm -rf "$(receipt_dir)"
BACKUP_STDOUT_FALLBACK=1
unset BACKUP_RESULT_LINE
: >"$work/docker.log"
rc=$(run_backup nightly)
[[ "$rc" == 0 ]] || fail "fallback rc=$rc"
[[ "$(json_get "$(receipt_dir)/latest.json" result_source)" == "stdout-fallback" ]] || fail "fallback source"
[[ "$(json_get "$(receipt_dir)/latest.json" ciphertext_size)" == "null" ]] || fail "fallback size should be null"

rm -rf "$(receipt_dir)"
FAIL_RUN=1 FAIL_RUN_RC=20 FAIL_RUN_CLASS=upload
: >"$work/docker.log"
rc=$(FAIL_RUN=1 FAIL_RUN_RC=20 FAIL_RUN_CLASS=upload run_backup nightly)
unset FAIL_RUN FAIL_RUN_RC FAIL_RUN_CLASS
[[ "$rc" == 20 ]] || fail "upload failure rc=$rc"
[[ "$(json_get "$(receipt_dir)/latest.json" status)" == "failed" ]] || fail "upload status"
[[ "$(json_get "$(receipt_dir)/latest.json" error_class)" == "upload" ]] || fail "upload error_class"
[[ "$(json_get "$(receipt_dir)/latest.json" failed_stage)" == "backup-run" ]] || fail "upload failed_stage"

rm -rf "$(receipt_dir)"
exec 8>"$work/pantry/deploy-trigger/.lock"
flock -n 8 || fail "lock for receipt lock-timeout test"
rc=$(PANTRY_BACKUP_LOCK_WAIT=1 run_backup nightly)
[[ "$rc" == 30 ]] || fail "lock timeout receipt rc=$rc"
[[ "$(json_get "$(receipt_dir)/latest.json" failed_stage)" == "lock" ]] || fail "lock timeout stage"
flock -u 8

rm -rf "$(receipt_dir)"
: >"$work/docker.log"
rc=$(run_backup not-a-reason 2>/dev/null || true)
[[ "$rc" == 2 ]] || fail "bad reason rc=$rc"
[[ "$(json_get "$(receipt_dir)/latest.json" exit_code)" == "2" ]] || fail "bad reason exit_code"
[[ "$(json_get "$(receipt_dir)/latest.json" reason)" == "invalid" ]] || fail "bad reason receipt reason"
assert_reason_allowed "$(receipt_dir)/latest.json"
assert_receipt_absent "backup reason must be"

rm -rf "$(receipt_dir)"
BACKUP_RESULT_LINE='backup result {"schema":1,"ok":false,"stored":true,"exit_code":30,"error_class":"sweep","object_key":"nightly/2026-11-10T120000Z.pantry.db.gz.age","manifest_key":"nightly/2026-11-10T120000Z.manifest.json","integrity":"ok","ciphertext_size":1,"ciphertext_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","created_at":"2026-11-10T12:00:00Z"}'
cat >"$bin/docker" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >>"\${DOCKER_LOG:?}"
if [[ "\$1" == inspect ]]; then printf '%s\n' "$TEST_IMAGE_SHA"; exit 0; fi
[[ "\$1" == rm ]] && exit 0
[[ "\$*" == *cleanup-stage* ]] && exit 0
[[ "\$*" == *" run "* ]] && {
  printf '%s\n' "\${BACKUP_RESULT_LINE:-}"
  exit 30
}
exit 0
EOF
chmod +x "$bin/docker"
: >"$work/docker.log"
rc=$(run_backup nightly)
[[ "$rc" == 30 ]] || fail "post-verify rc=$rc"
[[ "$(json_get "$(receipt_dir)/latest.json" stored)" == "true" ]] || fail "post-verify stored"
[[ "$(json_get "$(receipt_dir)/latest.json" status)" == "failed" ]] || fail "post-verify status"

# Restore the default docker stub after the exit-30 post-verify harness.
cat >"$bin/docker" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >>"\${DOCKER_LOG:?}"
if [[ "\$1" == inspect ]]; then
  printf '%s\n' "$TEST_IMAGE_SHA"
  exit 0
fi
EOF
cat >>"$bin/docker" <<'EOF'
if [[ "$1" == rm ]]; then
  exit 0
fi
if [[ "$*" == *cleanup-stage* ]]; then
  exit 0
fi
if [[ "$*" == *"/config-secrets:ro"* ]]; then
  src=$(printf '%s\n' "$*" | sed -n 's/.*--volume \([^ ]*\):\/config-secrets:ro.*/\1/p')
  [[ -d "$src" ]] || exit 4
  [[ ! -e "$src/.env.example" ]] || exit 4
  for f in "$src"/*; do
    [[ -f "$f" ]] || exit 4
    mode=$(stat -c '%a' "$f")
    [[ "$mode" == "400" ]] || exit 4
  done
fi
if [[ "${SLEEP_RUN:-}" == 1 && "$*" == *"--stage"* ]]; then
  printf '%s\n' "$$" >"${SLEEP_PID:?}"
  sleep 30
  exit 0
fi
if [[ "$*" == *" run "* ]]; then
  if [[ "${FAIL_RUN:-}" == 1 ]]; then
    if [[ -n "${FAIL_RUN_CLASS:-}" ]]; then
      printf 'backup failed class=%s\n' "${FAIL_RUN_CLASS}" >&2
    fi
    exit "${FAIL_RUN_RC:-20}"
  fi
  if [[ -n "${BACKUP_RESULT_LINE:-}" ]]; then
    printf '%s\n' "${BACKUP_RESULT_LINE}"
  fi
  if [[ "${BACKUP_STDOUT_FALLBACK:-}" == 1 ]]; then
    printf '%s\n' "backup ok key=nightly/2026-11-10T000000Z.pantry.db.gz.age integrity=ok rows=1 staging=tmpfs at=now"
  elif [[ -z "${BACKUP_RESULT_LINE:-}" ]]; then
    printf '%s\n' "backup ok key=nightly/2026-11-10T000000Z.pantry.db.gz.age integrity=ok rows=1 staging=tmpfs at=now"
  fi
  exit 0
fi
exit 0
EOF
chmod +x "$bin/docker"
unset BACKUP_RESULT_LINE BACKUP_STDOUT_FALLBACK FAIL_RUN FAIL_RUN_RC FAIL_RUN_CLASS

rm -rf "$(receipt_dir)"
for _ in $(seq 1 35); do
  : >"$work/docker.log"
  rc=$(PANTRY_RECEIPT_KEEP=30 run_backup nightly)
  [[ "$rc" == 0 ]] || fail "rotation run rc=$rc"
done
lines=$(wc -l <"$(receipt_dir)/history.jsonl")
[[ "$lines" -eq 30 ]] || fail "history has $lines lines, want 30"
last_hist=$(tail -n1 "$(receipt_dir)/history.jsonl")
latest_body=$(cat "$(receipt_dir)/latest.json")
[[ "$last_hist" == "$latest_body" ]] || fail "latest.json does not match last history line"

rm -rf "$(receipt_dir)"
nasty='backup result {"schema":1,"ok":true,"stored":true,"object_key":"nightly/2026-11-10T120000Z.pantry.db.gz.age","manifest_key":"nightly/2026-11-10T120000Z.manifest.json","integrity":"bad\"\\nvalue","ciphertext_size":481233,"ciphertext_sha256":"9f2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c","created_at":"2026-11-10T12:00:00Z"}'
BACKUP_RESULT_LINE=$nasty
export SANITIZE_STDERR='backup failed class=upload
https://bucket.r2.cloudflarestorage.com/secret
AKIAIOSFODNN7EXAMPLE
R2_SECRET_ACCESS_KEY=looks-secret
/opt/pantry/.env
AGE-SECRET-KEY-1ABCDEF'
cat >"$bin/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${DOCKER_LOG:?}"
if [[ "$1" == inspect ]]; then
  printf '%s\n' "sha256:0000000000000000000000000000000000000000000000000000000000abc"
  exit 0
fi
[[ "$1" == rm ]] && exit 0
[[ "$*" == *cleanup-stage* ]] && exit 0
if [[ "$*" == *" run "* ]]; then
  printf '%s\n' "${BACKUP_RESULT_LINE:-}"
  if [[ -n "${SANITIZE_STDERR:-}" ]]; then
    printf '%s\n' "${SANITIZE_STDERR}" >&2
  fi
  exit 0
fi
exit 0
EOF
chmod +x "$bin/docker"
: >"$work/docker.log"
rc=$(SANITIZE_STDERR="$SANITIZE_STDERR" run_backup nightly)
[[ "$rc" == 0 ]] || fail "sanitization rc=$rc"
unset SANITIZE_STDERR
assert_receipt_absent 'AKIAIOSFODNN7EXAMPLE'
assert_receipt_absent 'r2.cloudflarestorage.com'
assert_receipt_absent 'R2_SECRET'
assert_receipt_absent '/opt/pantry/.env'
assert_receipt_absent 'AGE-SECRET-KEY'
[[ "$(json_get "$(receipt_dir)/latest.json" integrity)" == "invalid" ]] || fail "sanitized integrity"

receipt_decoy_dir="$work/receipt-decoy-dir"
receipt_decoy_hist="$work/receipt-decoy-hist.jsonl"
receipt_decoy_latest="$work/receipt-decoy-latest.json"
mkdir -p "$receipt_decoy_dir"
printf 'DECOY_HIST_V1\n' >"$receipt_decoy_hist"
printf '{"decoy_latest":true}\n' >"$receipt_decoy_latest"
hist_decoy_before=$(cat "$receipt_decoy_hist")
latest_decoy_before=$(cat "$receipt_decoy_latest")

rm -rf "$work/pantry/backup/receipts"
mkdir -p "$work/pantry/backup"
ln -s "$receipt_decoy_dir" "$work/pantry/backup/receipts"
: >"$work/docker.log"
: >"$work/err"
rc=$(run_backup nightly)
[[ "$rc" == 0 ]] || fail "symlink receipt dir rc=$rc"
assert_dir_empty "$receipt_decoy_dir"
grep -q 'backup receipt not written' "$work/err" || fail "symlink receipt dir did not warn"
[[ -L "$work/pantry/backup/receipts" ]] || fail "receipts path is not a symlink"
rm -f "$work/pantry/backup/receipts"

mkdir -p "$work/pantry/backup/receipts"
printf 'DECOY_HIST_V1\n' >"$receipt_decoy_hist"
ln -sf "$receipt_decoy_hist" "$work/pantry/backup/receipts/history.jsonl"
: >"$work/docker.log"
: >"$work/err"
rc=$(run_backup nightly)
[[ "$rc" == 0 ]] || fail "symlink history rc=$rc"
assert_file_bytes "$receipt_decoy_hist" "$hist_decoy_before"
grep -q 'backup receipt not written' "$work/err" || fail "symlink history did not warn"
[[ -L "$work/pantry/backup/receipts/history.jsonl" ]] || fail "history.jsonl symlink removed"
rm -f "$work/pantry/backup/receipts/history.jsonl"

mkdir -p "$work/pantry/backup/receipts"
printf '{"decoy_latest":true}\n' >"$receipt_decoy_latest"
ln -sf "$receipt_decoy_latest" "$work/pantry/backup/receipts/latest.json"
: >"$work/docker.log"
: >"$work/err"
rc=$(run_backup nightly)
[[ "$rc" == 0 ]] || fail "symlink latest rc=$rc"
assert_file_bytes "$receipt_decoy_latest" "$latest_decoy_before"
grep -q 'backup receipt not written' "$work/err" || fail "symlink latest did not warn"
[[ -L "$work/pantry/backup/receipts/latest.json" ]] || fail "latest.json symlink removed"
rm -f "$work/pantry/backup/receipts/latest.json"
mkdir -p "$work/pantry/backup/receipts"

PANTRY_DIR="$work/pantry" \
  PANTRY_RECEIPT_DIR="$(receipt_dir)" \
  PANTRY_BACKUP_DEPLOY_MODE=off \
  PANTRY_BACKUP_TARGET_REVISION=0123456789abcdef0123456789abcdef01234567 \
  PANTRY_BACKUP_TARGET_DIGEST="$TEST_IMAGE_SHA" \
  bash "$ROOT/deploy/systemd/pantry-backup.sh" record-skip \
  >"$work/out" 2>"$work/err"
[[ "$(json_get "$(receipt_dir)/latest-pre-deploy.json" status)" == "skipped" ]] || fail "record-skip status"
[[ "$(json_get "$(receipt_dir)/latest-pre-deploy.json" deploy.mode)" == "off" ]] \
  || fail "record-skip deploy.mode"

rm -rf "$(receipt_dir)"
mkdir -p "$(receipt_dir)"
# Restore default stub (sanitization block overwrote it).
cat >"$bin/docker" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >>"\${DOCKER_LOG:?}"
if [[ "\$1" == inspect ]]; then
  printf '%s\n' "$TEST_IMAGE_SHA"
  exit 0
fi
EOF
cat >>"$bin/docker" <<'EOF'
if [[ "$1" == rm ]]; then
  exit 0
fi
if [[ "$*" == *cleanup-stage* ]]; then
  exit 0
fi
if [[ "$*" == *"/config-secrets:ro"* ]]; then
  src=$(printf '%s\n' "$*" | sed -n 's/.*--volume \([^ ]*\):\/config-secrets:ro.*/\1/p')
  [[ -d "$src" ]] || exit 4
  [[ ! -e "$src/.env.example" ]] || exit 4
  for f in "$src"/*; do
    [[ -f "$f" ]] || exit 4
    mode=$(stat -c '%a' "$f")
    [[ "$mode" == "400" ]] || exit 4
  done
fi
if [[ "${SLEEP_RUN:-}" == 1 && "$*" == *"--stage"* ]]; then
  printf '%s\n' "$$" >"${SLEEP_PID:?}"
  sleep 30
  exit 0
fi
if [[ "$*" == *" run "* ]]; then
  if [[ "${FAIL_RUN:-}" == 1 ]]; then
    if [[ -n "${FAIL_RUN_CLASS:-}" ]]; then
      printf 'backup failed class=%s\n' "${FAIL_RUN_CLASS}" >&2
    fi
    exit "${FAIL_RUN_RC:-20}"
  fi
  if [[ -n "${BACKUP_RESULT_LINE:-}" ]]; then
    printf '%s\n' "${BACKUP_RESULT_LINE}"
  fi
  if [[ "${BACKUP_STDOUT_FALLBACK:-}" == 1 ]]; then
    printf '%s\n' "backup ok key=nightly/2026-11-10T000000Z.pantry.db.gz.age integrity=ok rows=1 staging=tmpfs at=now"
  elif [[ -z "${BACKUP_RESULT_LINE:-}" ]]; then
    printf '%s\n' "backup ok key=nightly/2026-11-10T000000Z.pantry.db.gz.age integrity=ok rows=1 staging=tmpfs at=now"
  fi
  exit 0
fi
exit 0
EOF
chmod +x "$bin/docker"
unset BACKUP_RESULT_LINE BACKUP_STDOUT_FALLBACK FAIL_RUN FAIL_RUN_CLASS FAIL_RUN_RC
: >"$work/docker.log"
rm -f "$work/term.pid"
SLEEP_RUN=1 SLEEP_PID="$work/term.pid" \
  DOCKER_LOG="$work/docker.log" DOCKER="$bin/docker" PATH="$bin:$PATH" \
  PANTRY_DIR="$work/pantry" PANTRY_RECEIPT_DIR="$(receipt_dir)" \
  PANTRY_DATA_VOLUME="pantry_pantry-data" PANTRY_SETUP_SKIP_ROOT=1 PANTRY_CONTAINER=pantry \
  bash "$ROOT/deploy/systemd/pantry-backup.sh" nightly >"$work/out" 2>"$work/err" 8>&- 9>&- &
term_pid=$!
for _ in $(seq 1 30); do
  grep -q -- '--stage /run/stockbot-backup' "$work/docker.log" && break
  sleep 0.1
done
kill -TERM "$term_pid" 2>/dev/null || true
wait "$term_pid" 2>/dev/null || true
[[ "$(json_get "$(receipt_dir)/latest.json" status)" == "failed" ]] || fail "SIGTERM receipt status"
if [[ -f "$work/term.pid" ]]; then
  kill "$(cat "$work/term.pid")" 2>/dev/null || true
fi

echo "pantry-backup_test ok"
