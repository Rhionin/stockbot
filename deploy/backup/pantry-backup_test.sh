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

cat >"$bin/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${DOCKER_LOG:?}"
if [[ "$1" == inspect ]]; then
  printf '%s\n' "sha256:abc"
  exit 0
fi
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
    exit 1
  fi
  printf '%s\n' "backup ok key=nightly/x.pantry.db.gz.age integrity=ok rows=1 staging=tmpfs at=now"
  exit 0
fi
exit 0
EOF
chmod +x "$bin/docker"

run_backup() {
  local rc
  set +e
  DOCKER_LOG="$work/docker.log" \
    DOCKER="$bin/docker" \
    PATH="$bin:$PATH" \
    PANTRY_DIR="$work/pantry" \
    PANTRY_DATA_VOLUME="pantry_pantry-data" \
    PANTRY_SETUP_SKIP_ROOT=1 \
    PANTRY_CONTAINER=pantry \
    FAIL_RUN="${FAIL_RUN:-}" \
    SLEEP_RUN="${SLEEP_RUN:-}" \
    SLEEP_PID="${SLEEP_PID:-}" \
    CURL_LOG="${CURL_LOG:-}" \
    PANTRY_BACKUP_LOCK_WAIT="${PANTRY_BACKUP_LOCK_WAIT:-2}" \
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
  "--image-digest sha256:abc"
do
  [[ "$log" == *"$needle"* ]] || fail "docker args missing $needle"
done
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
