#!/usr/bin/env bash
# Behavioral checks for pantry-update.sh. Docker and curl are stubs so this
# can run without root, a Pi, or a registry.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
UPDATE="$ROOT/deploy/systemd/pantry-update.sh"

bash -n "$UPDATE"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

LAST_RC=0
LAST_OUT=""
LAST_DOCKER=""
LAST_CURL=""
LAST_SEQUENCE=""

make_bin() {
  local bin="$1"
  mkdir -p "$bin"

  cat > "$bin/docker" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${DOCKER_LOG:?}"
if [[ -n "${SEQUENCE_LOG:-}" ]]; then
  printf 'docker %s\n' "$*" >> "$SEQUENCE_LOG"
fi

tsv_col() {
  local key="$1" col="$2" svc ref run_id run_dig pull_id pull_dig revision
  while IFS=$'\t' read -r svc ref run_id run_dig pull_id pull_dig revision || [[ -n "$svc" ]]; do
    [[ -z "$svc" || "$svc" == \#* ]] && continue
    if [[ "$key" == "$svc" || "$key" == "cid-$svc" || "$key" == "$ref" || "$key" == "$run_id" || "$key" == "$pull_id" ]]; then
      case "$col" in
        ref) printf '%s' "$ref" ;;
        run_id) printf '%s' "$run_id" ;;
        run_dig) printf '%s' "$run_dig" ;;
        pull_id) printf '%s' "$pull_id" ;;
        pull_dig) printf '%s' "$pull_dig" ;;
        revision) printf '%s' "$revision" ;;
        *) return 1 ;;
      esac
      return 0
    fi
  done < "${STUB_IMAGES:?}"
  return 1
}

# Local tags survive across runs. Pull moves each ref onto the remote
# image. docker tag records a switch back to an older digest.
set_tag() {
  local ref="$1" id="$2" dig="$3" tmp tref tid tdig
  [[ -n "${STUB_TAG_STATE:-}" ]] || return 0
  tmp=$(mktemp)
  if [[ -f "$STUB_TAG_STATE" ]]; then
    while IFS=$'\t' read -r tref tid tdig || [[ -n "$tref" ]]; do
      [[ -z "$tref" || "$tref" == "$ref" ]] && continue
      printf '%s\t%s\t%s\n' "$tref" "$tid" "$tdig" >> "$tmp"
    done < "$STUB_TAG_STATE"
  fi
  printf '%s\t%s\t%s\n' "$ref" "$id" "$dig" >> "$tmp"
  mv "$tmp" "$STUB_TAG_STATE"
}

tag_field() {
  local ref="$1" col="$2" tref tid tdig
  [[ -n "${STUB_TAG_STATE:-}" && -f "$STUB_TAG_STATE" ]] || return 1
  while IFS=$'\t' read -r tref tid tdig || [[ -n "$tref" ]]; do
    [[ "$tref" == "$ref" ]] || continue
    case "$col" in
      id) printf '%s' "$tid" ;;
      dig) printf '%s' "$tdig" ;;
      *) return 1 ;;
    esac
    return 0
  done < "$STUB_TAG_STATE"
  return 1
}

apply_pull_tags() {
  local svc ref run_id run_dig pull_id pull_dig revision
  while IFS=$'\t' read -r svc ref run_id run_dig pull_id pull_dig revision || [[ -n "$svc" ]]; do
    [[ -z "$svc" || "$svc" == \#* ]] && continue
    set_tag "$ref" "$pull_id" "$pull_dig"
  done < "${STUB_IMAGES:?}"
}

format_arg() {
  local prev="" arg
  for arg in "$@"; do
    if [[ "$prev" == -f || "$prev" == --format ]]; then
      printf '%s' "$arg"
      return 0
    fi
    prev=$arg
  done
}

if [[ "$1" == rm ]]; then
  exit 0
fi

if [[ "$1" == inspect ]]; then
  format=$(format_arg "$@")
  target=${*: -1}
  if [[ "$format" == *com.docker.compose.service* ]]; then
    exit 1
  fi
  if [[ "$format" == *State.Running* ]]; then
    name=${target#cid-}
    id=$(tsv_col "$name" run_id || true)
    if [[ -n "$id" ]]; then
      printf 'true %s\n' "$id"
      exit 0
    fi
    exit 1
  fi
  exit 1
fi

if [[ "$1" == tag ]]; then
  src=$2
  dest=$3
  found_id=""
  found_dig=""
  while IFS=$'\t' read -r svc ref run_id run_dig pull_id pull_dig revision || [[ -n "${svc:-}" ]]; do
    [[ -z "${svc:-}" || "$svc" == \#* ]] && continue
    if [[ "$src" == "$run_dig" || "$src" == "$run_id" ]]; then
      found_id=$run_id
      found_dig=$run_dig
      break
    fi
    if [[ "$src" == "$pull_dig" || "$src" == "$pull_id" ]]; then
      found_id=$pull_id
      found_dig=$pull_dig
      break
    fi
  done < "${STUB_IMAGES:?}"
  if [[ -z "$found_id" ]]; then
    echo "stub docker tag: unknown source ${src}" >&2
    exit 1
  fi
  set_tag "$dest" "$found_id" "$found_dig"
  exit 0
fi

if [[ "$1" == image && "$2" == inspect ]]; then
  format=$(format_arg "$@")
  target=${*: -1}
  tagged_id=""
  tagged_dig=""
  if [[ "$target" != sha256:* ]]; then
    tagged_id=$(tag_field "$target" id || true)
    tagged_dig=$(tag_field "$target" dig || true)
  fi
  if [[ "$format" == *RepoDigests* ]]; then
    if [[ -n "$tagged_dig" ]]; then
      printf '%s\n' "$tagged_dig"
      exit 0
    fi
    run_id=$(tsv_col "$target" run_id || true)
    if [[ -n "$run_id" && "$run_id" == "$target" ]]; then
      tsv_col "$target" run_dig || true
      printf '\n'
      exit 0
    fi
    tsv_col "$target" pull_dig || true
    printf '\n'
    exit 0
  fi
  if [[ "$format" == *org.opencontainers.image.revision* ]]; then
    if [[ -n "$tagged_id" ]]; then
      rev=$(tsv_col "$tagged_id" revision || true)
      printf '%s\n' "$rev"
      exit 0
    fi
    tsv_col "$target" revision || true
    printf '\n'
    exit 0
  fi
  if [[ "$format" == *'.Id'* ]]; then
    if [[ -n "$tagged_id" ]]; then
      printf '%s\n' "$tagged_id"
      exit 0
    fi
    tsv_col "$target" pull_id || true
    printf '\n'
    exit 0
  fi
  exit 0
fi

if [[ "$1" == compose ]]; then
  joined="$*"
  if [[ "$joined" == *" config "* && "$joined" == *"--format"* ]]; then
    python3 - "${STUB_IMAGES:?}" << 'PY'
import json, sys
services = {}
with open(sys.argv[1]) as fh:
    for line in fh:
        line = line.rstrip("\n")
        if not line or line.startswith("#"):
            continue
        parts = line.split("\t")
        services[parts[0]] = {"image": parts[1]}
json.dump({"services": services}, sys.stdout)
PY
    printf '\n'
    exit 0
  fi
  if [[ "$joined" == *" pull" || "$joined" == *" pull "* ]]; then
    apply_pull_tags
    exit 0
  fi
  if [[ "$joined" == *" ps "* ]]; then
    svc=${*: -1}
    id=$(tsv_col "$svc" run_id || true)
    if [[ -n "$id" ]]; then
      printf 'cid-%s\n' "$svc"
    fi
    exit 0
  fi
  if [[ "$joined" == *"--pull never"* ]]; then
    prev=""
    for arg in "$@"; do
      if [[ "$prev" == -f && -f "$arg" && "$arg" != *docker-compose.yml ]]; then
        printf 'override-file\n' >> "${DOCKER_LOG:?}"
        cat "$arg" >> "${DOCKER_LOG:?}"
      fi
      prev=$arg
    done
    exit 0
  fi
  exit 0
fi

exit 0
EOF

  cat > "$bin/curl" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${CURL_LOG:?}"
if [[ -n "${SEQUENCE_LOG:-}" ]]; then
  printf 'curl %s\n' "$*" >> "$SEQUENCE_LOG"
fi
url=""
for arg in "$@"; do
  case "$arg" in
    http://*|https://*) url=$arg ;;
  esac
done
if [[ "$url" == *"/health"* ]]; then
  [[ "${STUB_HEALTH_RC:-0}" -eq 0 ]] || exit "$STUB_HEALTH_RC"
  printf '%s' "${STUB_HEALTH_BODY-}"
  exit 0
fi
if [[ "$url" == *"/api/build"* ]]; then
  [[ "${STUB_BUILD_RC:-0}" -eq 0 ]] || exit "$STUB_BUILD_RC"
  printf '%s' "${STUB_BUILD_BODY-}"
  exit 0
fi
exit 0
EOF

  cat > "$bin/chown" << 'EOF'
#!/usr/bin/env bash
exit 0
EOF

  chmod +x "$bin/docker" "$bin/curl" "$bin/chown"
}

prepare_pantry() {
  local pantry="$1"
  mkdir -p "$pantry"
  cp "$ROOT/deploy/publish-mode.sh" "$ROOT/deploy/auth-migrate.sh" "$ROOT/deploy/vapid-env.sh" "$pantry/"
  cat > "$pantry/.env" << 'EOF'
HOST_PORT=8080
PUBLIC_HOST=
PANTRY_IMAGE_TAG=latest
VAPID_PUBLIC_KEY=test-public
VAPID_PRIVATE_KEY=test-private
VAPID_SUBJECT=mailto:pantry@localhost
EOF
  cp "$pantry/.env" "$pantry/.env.orig"
}

# write_images DEST PANTRY_PULLED_ID PANTRY_PULLED_DIGEST CADDY_PULLED_ID CADDY_PULLED_DIGEST
write_images() {
  local dest="$1"
  cat > "$dest" << EOF
pantry	ghcr.io/rhionin/pantry:latest	sha256:runpantry	ghcr.io/rhionin/pantry@sha256:oldpantry	$2	$3	0123456789abcdef0123456789abcdef01234567
caddy	caddy:2.11.7-alpine	sha256:runcaddy	caddy@sha256:oldcaddy	$4	$5	
EOF
}

run_update() {
  local pantry="$1"
  local work bin out err
  work=$(mktemp -d)
  bin="$work/bin"
  out="$work/out"
  err="$work/err"
  make_bin "$bin"
  : > "$work/docker.log"
  : > "$work/curl.log"
  : > "$work/sequence.log"

  set +e
  DOCKER_LOG="$work/docker.log" \
    CURL_LOG="$work/curl.log" \
    SEQUENCE_LOG="$work/sequence.log" \
    STUB_IMAGES="$pantry/images.tsv" \
    STUB_TAG_STATE="$pantry/tag-state.tsv" \
    STUB_HEALTH_BODY="${STUB_HEALTH_BODY-}" \
    STUB_HEALTH_RC="${STUB_HEALTH_RC:-0}" \
    STUB_BUILD_BODY="${STUB_BUILD_BODY-}" \
    STUB_BUILD_RC="${STUB_BUILD_RC:-0}" \
    PANTRY_DIR="$pantry" \
    PANTRY_SETUP_SKIP_ROOT=1 \
    PANTRY_UPDATE_HEALTH_TIMEOUT="${PANTRY_UPDATE_HEALTH_TIMEOUT:-0}" \
    PANTRY_UPDATE_HEALTH_INTERVAL="${PANTRY_UPDATE_HEALTH_INTERVAL:-1}" \
    PATH="$bin:$PATH" \
    bash "$UPDATE" >"$out" 2>"$err"
  LAST_RC=$?
  set -e

  LAST_OUT=$(cat "$out" "$err")
  LAST_DOCKER=$(cat "$work/docker.log")
  LAST_CURL=$(cat "$work/curl.log")
  LAST_SEQUENCE=$(cat "$work/sequence.log")
  rm -rf "$work"
}

have() {
  local haystack="$1" needle="$2" label="$3"
  if ! printf '%s\n' "$haystack" | grep -q -F -- "$needle"; then
    fail "$label (missing $needle)"
  fi
}

lack() {
  local haystack="$1" needle="$2" label="$3"
  if printf '%s\n' "$haystack" | grep -q -F -- "$needle"; then
    fail "$label (unexpected $needle)"
  fi
}

line_of() {
  local haystack="$1" needle="$2"
  printf '%s\n' "$haystack" | grep -n -F -m1 -- "$needle" | cut -d: -f1
}

env_unchanged() {
  local pantry="$1"
  cmp -s "$pantry/.env" "$pantry/.env.orig" || fail "update rewrote .env"
}

NEW_COMMIT=0123456789abcdef0123456789abcdef01234567
OLD_COMMIT=ffffffffffffffffffffffffffffffffffffffff

# Healthy new image: digest recorded before pull, health and commit match,
# previous digest is not re-pinned, .env is left alone.
healthy=$(mktemp -d)
prepare_pantry "$healthy"
write_images "$healthy/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:newcaddy caddy@sha256:newcaddy
STUB_HEALTH_RC=0
STUB_BUILD_RC=0
STUB_HEALTH_BODY='{"status":"ok","scanner":{"connected":false}}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\",\"version\":\"vtest\"}"
run_update "$healthy"
[[ "$LAST_RC" -eq 0 ]] || fail "healthy update exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "pantry update: running pantry image ghcr.io/rhionin/pantry@sha256:oldpantry" "healthy run records the pantry digest"
have "$LAST_OUT" "pantry update: running caddy image caddy@sha256:oldcaddy" "healthy run records the caddy digest"
have "$LAST_OUT" "pantry update: healthy, running commit ${NEW_COMMIT}" "healthy run keeps the matching commit"
have "$LAST_OUT" "pantry update: finished" "healthy run finishes"
lack "$LAST_OUT" "PANTRY UPDATE FAILED" "healthy run does not roll back"
lack "$LAST_DOCKER" "--pull never" "healthy run does not re-pin"
have "$LAST_CURL" "http://127.0.0.1:8080/health" "healthy run polls the host port health endpoint"
have "$LAST_CURL" "http://127.0.0.1:8080/api/build" "healthy run polls /api/build"
lack "$LAST_CURL" "stockbot.rhionin.com" "healthy run does not use the public hostname"
env_unchanged "$healthy"
[[ ! -e "$healthy/.update-rejected-digest" ]] || fail "healthy run recorded a rejected digest"
record_line=$(line_of "$LAST_DOCKER" "sha256:runpantry")
pull_line=$(line_of "$LAST_DOCKER" " pull")
up_line=$(line_of "$LAST_SEQUENCE" " up -d")
curl_line=$(line_of "$LAST_SEQUENCE" "curl ")
[[ -n "$record_line" && -n "$pull_line" && "$record_line" -lt "$pull_line" ]] || fail "healthy run inspected the running image after pull (record=$record_line pull=$pull_line)"
[[ -n "$up_line" && -n "$curl_line" && "$up_line" -lt "$curl_line" ]] || fail "healthy run polled health before up -d"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_HEALTH_RC STUB_BUILD_RC

# Nothing changed on pull: no recreate and no health poll.
noop=$(mktemp -d)
prepare_pantry "$noop"
write_images "$noop/images.tsv" \
  sha256:runpantry ghcr.io/rhionin/pantry@sha256:oldpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_RC=0
STUB_BUILD_RC=0
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$noop"
[[ "$LAST_RC" -eq 0 ]] || fail "unchanged update exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "pantry update: images unchanged" "unchanged pull is a no-op"
have "$LAST_OUT" "pantry update: finished" "unchanged pull finishes"
have "$LAST_DOCKER" " pull" "unchanged pull still checks the registry"
lack "$LAST_DOCKER" " up " "unchanged pull does not recreate containers"
[[ -z "$LAST_CURL" ]] || fail "unchanged pull polled health: $LAST_CURL"
lack "$LAST_OUT" "PANTRY UPDATE FAILED" "unchanged pull does not roll back"
env_unchanged "$noop"
[[ ! -e "$noop/.update-rejected-digest" ]] || fail "unchanged pull recorded a rejected digest"
unset STUB_HEALTH_BODY STUB_BUILD_BODY

# Health is ok but /api/build is a different commit: roll back, exit non-zero,
# and do not retry that digest on the next run.
mismatch=$(mktemp -d)
prepare_pantry "$mismatch"
write_images "$mismatch/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:newcaddy caddy@sha256:newcaddy
STUB_HEALTH_RC=0
STUB_BUILD_RC=0
STUB_HEALTH_BODY='{"status":"ok","scanner":{"connected":false}}'
STUB_BUILD_BODY="{\"commit\":\"${OLD_COMMIT}\",\"version\":\"vtest\"}"
run_update "$mismatch"
[[ "$LAST_RC" -ne 0 ]] || fail "mismatched sha should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "PANTRY UPDATE FAILED: rolling back to the previous image." "mismatch rolls back loudly"
have "$LAST_OUT" "does not undo a database migration" "mismatch says an image rollback cannot undo a migration"
have "$LAST_OUT" "not transactional" "mismatch names the migration runner"
have "$LAST_OUT" "pantry-pre-*.db" "mismatch names the snapshot files"
have "$LAST_OUT" "/data" "mismatch names the snapshot directory"
have "$LAST_OUT" "does not restore the database" "mismatch does not imply a database restore"
have "$LAST_OUT" "pantry update: re-pinning pantry to ghcr.io/rhionin/pantry@sha256:oldpantry" "mismatch re-pins the pantry digest"
have "$LAST_OUT" "pantry update: re-pinning caddy to caddy@sha256:oldcaddy" "mismatch re-pins the caddy digest"
have "$LAST_DOCKER" "--pull never" "mismatch starts the previous image without pulling"
have "$LAST_DOCKER" "image: ghcr.io/rhionin/pantry@sha256:oldpantry" "mismatch override pins the pantry digest"
have "$LAST_DOCKER" "image: caddy@sha256:oldcaddy" "mismatch override pins the caddy digest"
lack "$LAST_OUT" "pantry update: healthy, running commit" "mismatch does not keep the new image"
lack "$LAST_OUT" "pantry update: finished" "mismatch does not report success"
have "$LAST_DOCKER" "tag ghcr.io/rhionin/pantry@sha256:oldpantry ghcr.io/rhionin/pantry:latest" "rollback retags the pantry ref to the good digest"
have "$LAST_DOCKER" "tag caddy@sha256:oldcaddy caddy:2.11.7-alpine" "rollback retags a changed sidecar ref"
have "$(cat "$mismatch/tag-state.tsv")" "ghcr.io/rhionin/pantry:latest	sha256:runpantry	ghcr.io/rhionin/pantry@sha256:oldpantry" "local pantry tag resolves to the good digest"
have "$(cat "$mismatch/tag-state.tsv")" "caddy:2.11.7-alpine	sha256:runcaddy	caddy@sha256:oldcaddy" "local caddy tag resolves to the good digest"
env_unchanged "$mismatch"
have "$(cat "$mismatch/.update-rejected-digest")" "ghcr.io/rhionin/pantry@sha256:newpantry" "mismatch remembers the rejected digest"
have "$(cat "$mismatch/.update-rejected-digest")" "Not a setup last-known-good sha" "rejected file is not the setup sha"
lack "$(cat "$mismatch/.update-rejected-digest")" "PANTRY_IMAGE_TAG" "rejected file does not pin the tag"
forward_up=$(line_of "$LAST_SEQUENCE" " up -d")
rollback_up=$(line_of "$LAST_SEQUENCE" "--pull never")
probed=$(line_of "$LAST_SEQUENCE" "curl ")
[[ -n "$forward_up" && -n "$probed" && "$forward_up" -lt "$probed" ]] || fail "mismatch polled health before the new image was started"
[[ -n "$probed" && -n "$rollback_up" && "$probed" -lt "$rollback_up" ]] || fail "mismatch re-pinned before the health check failed"

# The next minute pulls the same bad remote digest, does not start it, and
# leaves the local tag on the good digest. The restored tag must not make
# this look like "images unchanged".
run_update "$mismatch"
[[ "$LAST_RC" -ne 0 ]] || fail "rejected digest should stay failed: $LAST_OUT"
have "$LAST_OUT" "was already rolled back; leaving the previous image running." "second run leaves the previous image"
have "$LAST_OUT" "pantry-pre-*.db" "second run still names the manual snapshot path"
have "$LAST_DOCKER" " pull" "second run still pulls the remote digest"
lack "$LAST_OUT" "images unchanged" "retagged local tag is not treated as the remote image"
lack "$LAST_DOCKER" " up " "second run does not recreate containers"
lack "$LAST_DOCKER" "--pull never" "second run does not roll back again"
[[ -z "$LAST_CURL" ]] || fail "second run polled health: $LAST_CURL"
have "$(cat "$mismatch/tag-state.tsv")" "ghcr.io/rhionin/pantry:latest	sha256:runpantry	ghcr.io/rhionin/pantry@sha256:oldpantry" "second run puts the local tag back on the good digest"
env_unchanged "$mismatch"

# A newer remote digest is not stuck behind the retag.
write_images "$mismatch/images.tsv" \
  sha256:newerpantry ghcr.io/rhionin/pantry@sha256:newerpantry \
  sha256:newercaddy caddy@sha256:newercaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\",\"version\":\"vtest\"}"
run_update "$mismatch"
[[ "$LAST_RC" -eq 0 ]] || fail "new digest after a rollback exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "pantry update: healthy, running commit ${NEW_COMMIT}" "new digest is deployed"
have "$LAST_DOCKER" " up -d" "new digest recreates the stack"
lack "$LAST_OUT" "images unchanged" "new digest is not skipped as unchanged"
lack "$LAST_OUT" "was already rolled back" "new digest is not the rejected one"
have "$(cat "$mismatch/tag-state.tsv")" "ghcr.io/rhionin/pantry:latest	sha256:newerpantry	ghcr.io/rhionin/pantry@sha256:newerpantry" "new digest keeps the local tag"
env_unchanged "$mismatch"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_HEALTH_RC STUB_BUILD_RC

# The new image never answers /health.
unhealthy=$(mktemp -d)
prepare_pantry "$unhealthy"
write_images "$unhealthy/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_RC=1
STUB_BUILD_RC=1
STUB_HEALTH_BODY=""
STUB_BUILD_BODY=""
run_update "$unhealthy"
[[ "$LAST_RC" -ne 0 ]] || fail "unhealthy image should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "PANTRY UPDATE FAILED: rolling back to the previous image." "unhealthy image rolls back loudly"
have "$LAST_OUT" "does not undo a database migration" "unhealthy rollback says a migration stays applied"
have "$LAST_OUT" "not transactional" "unhealthy rollback names the migration runner"
have "$LAST_OUT" "pantry-pre-*.db" "unhealthy rollback names the snapshot files"
have "$LAST_OUT" "does not restore the database" "unhealthy rollback does not restore the database"
have "$LAST_OUT" "pantry update: re-pinning pantry to ghcr.io/rhionin/pantry@sha256:oldpantry" "unhealthy rollback re-pins pantry"
have "$LAST_DOCKER" "tag ghcr.io/rhionin/pantry@sha256:oldpantry ghcr.io/rhionin/pantry:latest" "unhealthy rollback retags pantry"
lack "$LAST_DOCKER" "tag caddy@" "unchanged sidecar tag is left alone"
have "$LAST_DOCKER" "--pull never" "unhealthy rollback starts the previous image"
have "$LAST_DOCKER" "image: ghcr.io/rhionin/pantry@sha256:oldpantry" "unhealthy override pins the pantry digest"
lack "$LAST_OUT" "pantry update: finished" "unhealthy rollback does not report success"
env_unchanged "$unhealthy"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_HEALTH_RC STUB_BUILD_RC

rm -rf "$healthy" "$noop" "$mismatch" "$unhealthy"
echo "pantry_update_test ok"
