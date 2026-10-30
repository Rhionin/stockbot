#!/usr/bin/env bash
# Behavioral checks for pantry-update.sh. Docker and curl are stubs so this
# can run without root, a Pi, or a registry.

set -euo pipefail

unset BACKUP_BEFORE_DEPLOY STUB_GITHUB_COMPARE_STATUS

ROOT=$(cd "$(dirname "$0")/.." && pwd)
UPDATE="$ROOT/deploy/systemd/pantry-update.sh"

bash -n "$UPDATE"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# File probes follow PANTRY_DIR. A literal /opt/pantry/ or /run/ path is a
# file the fixture cannot move. The production default (PANTRY_DIR=${PANTRY_DIR:-/opt/pantry})
# and publish-mode.sh's refusal to docker rm when PANTRY_DIR is not that
# directory stay literal on purpose: they do not open a path under it.
assert_no_literal_probe_paths() {
  local file line code
  for file in \
    "$UPDATE" \
    "$ROOT/deploy/publish-mode.sh" \
    "$ROOT/deploy/runtime-setup.sh" \
    "$ROOT/deploy/vapid-env.sh"
  do
    while IFS= read -r line || [[ -n "$line" ]]; do
      [[ "$line" =~ ^[[:space:]]*# ]] && continue
      code=${line%%#*}
      if [[ "$code" == *"/opt/pantry/"* || "$code" == *"/run/"* ]]; then
        fail "${file} probes a literal path a fixture cannot redirect: ${line}"
      fi
    done < "$file"
  done
}
assert_no_literal_probe_paths

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
# The updater holds the deploy lock on fd 9 and closes it before starting
# the backup. A child of that backup must not inherit the lock.
if [[ "${PANTRY_BACKUP_LOCK_HELD:-}" == 1 ]]; then
  [ ! -e /proc/self/fd/9 ] || { echo "fd 9 leaked into backup child" >&2; exit 99; }
fi

tsv_col() {
  local key="$1" col="$2" svc ref run_id run_dig pull_id pull_dig revision run_revision
  while IFS=$'\t' read -r svc ref run_id run_dig pull_id pull_dig revision run_revision || [[ -n "$svc" ]]; do
    [[ -z "$svc" || "$svc" == \#* ]] && continue
    if [[ "$key" == "$svc" || "$key" == "cid-$svc" || "$key" == "$ref" || "$key" == "$run_id" || "$key" == "$pull_id" || "$key" == "$run_dig" || "$key" == "$pull_dig" ]]; then
      case "$col" in
        ref) printf '%s' "$ref" ;;
        run_id) printf '%s' "$run_id" ;;
        run_dig) printf '%s' "$run_dig" ;;
        pull_id) printf '%s' "$pull_id" ;;
        pull_dig) printf '%s' "$pull_dig" ;;
        revision)
          if [[ -n "$run_revision" && ( "$key" == "$run_id" || "$key" == "$run_dig" || "$key" == "cid-$svc" ) ]]; then
            printf '%s' "$run_revision"
          else
            printf '%s' "$revision"
          fi
          ;;
        run_revision) printf '%s' "$run_revision" ;;
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

if [[ "$1" == logs ]]; then
  printf '%s\n' "${STUB_CONTAINER_LOGS:-wrote backup: /data/pantry-pre-targets-20261017.db}"
  exit 0
fi

if [[ "$1" == info ]]; then
  exit 0
fi

if [[ "$1" == inspect ]]; then
  format=$(format_arg "$@")
  target=${*: -1}
  if [[ "$format" == *com.docker.compose.project* ]]; then
    if [[ -n "${STUB_PROJECT_LABEL+x}" ]]; then
      printf '%s\n' "$STUB_PROJECT_LABEL"
    elif [[ -n "${PANTRY_TEST_PROJECT_PREFIX:-}" ]]; then
      printf '%s\n' "$PANTRY_TEST_PROJECT_PREFIX"
    else
      basename "${PANTRY_DIR:-pantry}"
    fi
    exit 0
  fi
  if [[ "$format" == *com.docker.compose.service* ]]; then
    if [[ -n "${STUB_COMPOSE_SERVICE:-}" ]]; then
      printf '%s\n' "$STUB_COMPOSE_SERVICE"
      exit 0
    fi
    exit 1
  fi
  if [[ "$format" == *State.Status* ]]; then
    name=${target#cid-}
    status=${STUB_CONTAINER_STATUS:-running}
    restarts=${STUB_RESTART_COUNT:-0}
    if [[ -n "${STUB_STATUS_TSV:-}" && -f "$STUB_STATUS_TSV" ]]; then
      while IFS=$'\t' read -r svc st rc || [[ -n "${svc:-}" ]]; do
        [[ -z "${svc:-}" || "$svc" == \#* ]] && continue
        [[ "$svc" == "$name" ]] || continue
        status=$st
        restarts=${rc:-0}
        break
      done < "$STUB_STATUS_TSV"
    fi
    if [[ -n "${STUB_BUMP_SERVICE:-}" && "$name" == "$STUB_BUMP_SERVICE" && -n "${STUB_INSPECT_STATE:-}" ]]; then
      n=0
      if [[ -f "$STUB_INSPECT_STATE" ]]; then
        n=$(awk -F '\t' -v s="$name" '$1 == s { print $2; exit }' "$STUB_INSPECT_STATE")
        n=${n:-0}
      fi
      n=$((n + 1))
      tmp=$(mktemp)
      if [[ -f "$STUB_INSPECT_STATE" ]]; then
        awk -F '\t' -v s="$name" '$1 != s { print }' "$STUB_INSPECT_STATE" > "$tmp"
      fi
      printf '%s\t%s\n' "$name" "$n" >> "$tmp"
      mv "$tmp" "$STUB_INSPECT_STATE"
      bump_on=${STUB_BUMP_ON_CALL:-2}
      if [[ "$n" -ge "$bump_on" ]]; then
        restarts=${STUB_BUMP_RESTARTS:-1}
        if [[ -n "${STUB_BUMP_STATUS:-}" ]]; then
          status=$STUB_BUMP_STATUS
        fi
      fi
    fi
    printf '%s %s\n' "$status" "$restarts"
    exit 0
  fi
  if [[ "$format" == *StartedAt* ]]; then
    name=${target#cid-}
    name=${name%-new}
    started=${STUB_STARTED_AT:-2020-01-01T00:00:00.000000000Z}
    if [[ "${STUB_SHIFT_STARTED_SERVICE:-}" == "$name" && -f "${DOCKER_LOG:-}" ]] && grep -q -F -- ' up' "$DOCKER_LOG"; then
      started=${STUB_STARTED_AT_AFTER:-2026-10-17T00:00:01.000000000Z}
    fi
    printf '%s\n' "$started"
    exit 0
  fi
  if [[ "$format" == '{{.Image}}' ]]; then
    name=${target#cid-}
    name=${name%-new}
    img=$(tsv_col "$name" run_id || true)
    if [[ -f "${DOCKER_LOG:-}" ]] && grep -q -F -- 'pending-before-up' "$DOCKER_LOG" \
        && [[ "${STUB_UP_RC:-0}" -eq 0 ]]; then
      pulled=$(tsv_col "$name" pull_id || true)
      [[ -n "$pulled" ]] && img=$pulled
    fi
    [[ -n "$img" ]] || img="${STUB_CONTAINER_IMAGE:-sha256:running-pantry}"
    printf '%s\n' "$img"
    exit 0
  fi
  if [[ "$format" == *State.Running* ]]; then
    name=${target#cid-}
    id=$(tsv_col "$name" run_id || true)
    if [[ -f "${DOCKER_LOG:-}" ]] && grep -q -F -- 'pending-before-up' "$DOCKER_LOG" \
        && [[ "${STUB_UP_RC:-0}" -eq 0 ]]; then
      pulled=$(tsv_col "$name" pull_id || true)
      [[ -n "$pulled" ]] && id=$pulled
    fi
    if [[ -n "${STUB_LIVE_IMAGE_TSV:-}" && -f "$STUB_LIVE_IMAGE_TSV" && -f "${DOCKER_LOG:-}" ]] && grep -q -F -- ' up' "$DOCKER_LOG"; then
      live=$(awk -F '\t' -v s="$name" '$1 == s { print $2; exit }' "$STUB_LIVE_IMAGE_TSV")
      if [[ -n "$live" ]]; then
        id=$live
      fi
    fi
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
    if [[ "$src" == sha256:* || "$src" == *@sha256:* ]]; then
      found_id=$src
      found_dig=$src
    else
      echo "stub docker tag: unknown source ${src}" >&2
      exit 1
    fi
  fi
  set_tag "$dest" "$found_id" "$found_dig"
  exit 0
fi

if [[ "$1" == image && "$2" == inspect ]]; then
  format=$(format_arg "$@")
  target=${*: -1}
  if [[ -n "${STUB_ABSENT_REFS:-}" ]]; then
    IFS=',' read -ra absent_refs <<< "$STUB_ABSENT_REFS"
    for absent in "${absent_refs[@]}"; do
      if [[ -n "$absent" && "$target" == "$absent" ]]; then
        echo "stub docker image inspect: absent ${target}" >&2
        exit 1
      fi
    done
  fi
  tagged_id=""
  tagged_dig=""
  if [[ "$target" != sha256:* ]]; then
    tagged_id=$(tag_field "$target" id || true)
    tagged_dig=$(tag_field "$target" dig || true)
  fi
  if [[ "$format" == *RepoDigests* ]]; then
    if [[ -n "${STUB_EMPTY_REPO_DIGEST_FOR:-}" ]]; then
      IFS=',' read -ra empty_digest_targets <<< "$STUB_EMPTY_REPO_DIGEST_FOR"
      for empty_digest_target in "${empty_digest_targets[@]}"; do
        [[ -n "$empty_digest_target" && "$target" == *"$empty_digest_target"* ]] || continue
        printf '\n'
        exit 0
      done
    fi
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
    if [[ -n "${STUB_EMPTY_IMAGE_ID_FOR:-}" ]]; then
      IFS=',' read -ra empty_id_targets <<< "$STUB_EMPTY_IMAGE_ID_FOR"
      for empty_id_target in "${empty_id_targets[@]}"; do
        [[ -n "$empty_id_target" && "$target" == *"$empty_id_target"* ]] || continue
        printf '\n'
        exit 0
      done
    fi
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
  if [[ "$joined" == *" config"* ]]; then
    printf 'services:\n'
    while IFS=$'\t' read -r svc ref run_id run_dig pull_id pull_dig revision run_revision || [[ -n "${svc:-}" ]]; do
      [[ -z "${svc:-}" || "$svc" == \#* ]] && continue
      printf '  %s:\n    image: %s\n' "$svc" "$ref"
    done < "${STUB_IMAGES:?}"
    exit 0
  fi
  if [[ "$joined" == *" pull" || "$joined" == *" pull "* ]]; then
    if [[ "${STUB_REWRITE_TRIGGER:-}" == 1 && -n "${PANTRY_DIR:-}" && ! -f "$PANTRY_DIR/deploy-trigger/.rewritten" ]]; then
      mkdir -p "$PANTRY_DIR/deploy-trigger"
      printf 'again\n' > "$PANTRY_DIR/deploy-trigger/request"
      : > "$PANTRY_DIR/deploy-trigger/.rewritten"
    fi
    apply_pull_tags
    exit 0
  fi
  if [[ "$joined" == *" ps "* ]]; then
    svc=${*: -1}
    id=$(tsv_col "$svc" run_id || true)
    if [[ -n "$id" ]]; then
      if [[ "${STUB_NEW_CID_SERVICE:-}" == "$svc" && -f "${DOCKER_LOG:-}" ]] && grep -q -F -- ' up' "$DOCKER_LOG"; then
        printf 'cid-%s-new\n' "$svc"
      else
        printf 'cid-%s\n' "$svc"
      fi
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
    exit "${STUB_ROLLBACK_RC:-0}"
  fi
  if [[ "$joined" == *" up "* || "$joined" == *" up" ]]; then
    if [[ -n "${PANTRY_DIR:-}" && -f "$PANTRY_DIR/.update-pending" ]]; then
      printf '%s\n' pending-before-up >> "${DOCKER_LOG:?}"
    else
      printf '%s\n' pending-missing-before-up >> "${DOCKER_LOG:?}"
    fi
    exit "${STUB_UP_RC:-0}"
  fi
  exit 0
fi

if [[ "$1" == cp ]]; then
  container_path=$2
  dest=$3
  if [[ "$container_path" == *"/usr/share/stockbot/host-bundle"* ]]; then
    if [[ -n "${STUB_HOST_BUNDLE:-}" && -d "$STUB_HOST_BUNDLE" ]]; then
      mkdir -p "$dest"
      cp -a "$STUB_HOST_BUNDLE/." "$dest/"
      exit 0
    fi
  fi
  echo "stub docker cp: unsupported source ${container_path}" >&2
  exit 1
fi

if [[ "$1" == run && "$*" == *"--reason"* ]]; then
  exit "${STUB_BACKUP_RC:-0}"
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
resolve=""
outfile=""
write_code=0
prev=""
for arg in "$@"; do
  if [[ "$prev" == --resolve ]]; then
    resolve=$arg
  elif [[ "$prev" == -o ]]; then
    outfile=$arg
  elif [[ "$prev" == -w && "$arg" == *http_code* ]]; then
    write_code=1
  fi
  prev=$arg
  case "$arg" in
    http://*|https://*) url=$arg ;;
  esac
done
emit_body() {
  local body="$1" status="$2"
  if [[ -n "$outfile" ]]; then
    printf '%s' "$body" > "$outfile"
  fi
  if [[ "$write_code" -eq 1 ]]; then
    printf '%s' "$status"
  elif [[ -z "$outfile" ]]; then
    printf '%s' "$body"
  fi
}
if [[ "$url" == *api.github.com/repos/Rhionin/stockbot/compare/* ]]; then
  if [[ "${STUB_GITHUB_COMPARE_STATUS:-}" == unreachable ]]; then
    exit 22
  fi
  compare_tail=${url#*compare/}
  compare_base=${compare_tail%%...*}
  compare_head=${compare_tail#*...}
  compare_head=${compare_head%%/*}
  compare_head=${compare_head%%\?*}
  if [[ "$compare_head" != master ]]; then
    emit_body '{"status":"diverged","files":[{"filename":"x","status":"modified"}]}' 200
    exit 0
  fi
  if [[ "$compare_base" == master ]]; then
    emit_body '{"status":"diverged","files":[{"filename":"x","status":"modified"}]}' 200
    exit 0
  fi
  gh_status=${STUB_GITHUB_COMPARE_STATUS:-ahead}
  if [[ "${PANTRY_TEST_GITHUB_COMPARE_PRETTY:-}" == 1 && -n "${STUB_FIXTURE_ROOT:-}" ]]; then
    case "$gh_status" in
      ahead|identical|behind|diverged)
        if [[ -f "${STUB_FIXTURE_ROOT}/github_compare_${gh_status}_pretty.json" ]]; then
          emit_body "$(cat "${STUB_FIXTURE_ROOT}/github_compare_${gh_status}_pretty.json")" 200
          exit 0
        fi
        ;;
    esac
  fi
  case "$gh_status" in
    identical)
      emit_body '{"status":"identical","ahead_by":0,"behind_by":0,"files":[]}' 200
      ;;
    behind)
      emit_body '{"status":"behind","ahead_by":0,"behind_by":3,"files":[{"filename":"README.md","status":"modified"}]}' 200
      ;;
    diverged)
      emit_body '{"status":"diverged","ahead_by":1,"behind_by":2,"files":[{"filename":"README.md","status":"added"}]}' 200
      ;;
    malformed)
      emit_body $'{\n  "files": [\n    {\n      "filename": "README.md",\n      "status": "modified"\n    }\n  ]\n}' 200
      ;;
    empty)
      emit_body '' 200
      ;;
    *)
      emit_body "{\"status\":\"ahead\",\"ahead_by\":9,\"behind_by\":0,\"files\":[{\"filename\":\"deploy/setup.sh\",\"status\":\"modified\"},{\"filename\":\"firmware/x\",\"status\":\"added\"}]}" 200
      ;;
  esac
  exit 0
fi
rollback_phase=0
if [[ -n "${DOCKER_LOG:-}" && -f "$DOCKER_LOG" ]] && grep -q -- '--pull never' "$DOCKER_LOG"; then
  rollback_phase=1
fi
if [[ "$resolve" == *:80:10.77.77.3 ]]; then
  count_file="${CURL_LOG:?}.tunnel-n"
  n=0
  if [[ -f "$count_file" ]]; then
    n=$(cat "$count_file")
  fi
  n=$((n + 1))
  printf '%s\n' "$n" > "$count_file"
  if [[ "$n" -eq 1 ]]; then
    rc=${STUB_TUNNEL_OLD_RC:-0}
    status=${STUB_TUNNEL_OLD_STATUS:-200}
    if [[ -n "${STUB_TUNNEL_OLD_BODY+x}" ]]; then
      body=$STUB_TUNNEL_OLD_BODY
    else
      body=${STUB_BUILD_BODY:-}
    fi
  elif [[ -n "${STUB_TUNNEL_LATER_FROM:-}" && "$n" -ge "$STUB_TUNNEL_LATER_FROM" ]]; then
    rc=${STUB_TUNNEL_LATER_RC:-22}
    status=${STUB_TUNNEL_LATER_STATUS:-500}
    body=""
  else
    rc=${STUB_TUNNEL_RC:-0}
    status=${STUB_TUNNEL_STATUS:-200}
    if [[ -n "${STUB_TUNNEL_BODY+x}" ]]; then
      body=$STUB_TUNNEL_BODY
    else
      body=${STUB_BUILD_BODY:-}
    fi
  fi
  if [[ "$rc" -ne 0 ]]; then
    if [[ "$write_code" -eq 1 ]]; then
      printf '%s' "000"
    fi
    exit "$rc"
  fi
  emit_body "$body" "$status"
  exit 0
fi
if [[ "$url" == https://* ]]; then
  count_file="${CURL_LOG:?}.caddy-n"
  n=0
  if [[ -f "$count_file" ]]; then
    n=$(cat "$count_file")
  fi
  n=$((n + 1))
  printf '%s\n' "$n" > "$count_file"
  if [[ "$n" -eq 1 ]]; then
    rc=${STUB_CADDY_OLD_RC:-${STUB_CADDY_RC:-0}}
  elif [[ "$n" -ge 3 && -n "${STUB_CADDY_RETRY_RC:-}" ]]; then
    rc=$STUB_CADDY_RETRY_RC
  else
    rc=${STUB_CADDY_RC:-0}
  fi
  [[ "$rc" -eq 0 ]] || exit "$rc"
fi
if [[ "$url" == *"/health"* ]]; then
  if [[ "$rollback_phase" -eq 1 ]]; then
    [[ "${STUB_ROLLBACK_HEALTH_RC:-0}" -eq 0 ]] || exit "$STUB_ROLLBACK_HEALTH_RC"
    if [[ -n "${STUB_ROLLBACK_HEALTH_BODY:-}" ]]; then
      printf '%s' "$STUB_ROLLBACK_HEALTH_BODY"
    else
      printf '%s' '{"status":"ok"}'
    fi
    exit 0
  fi
  if [[ -n "${STUB_HEALTH_QUEUE:-}" && -s "$STUB_HEALTH_QUEUE" ]]; then
    body=$(head -n 1 "$STUB_HEALTH_QUEUE")
    tail -n +2 "$STUB_HEALTH_QUEUE" > "${STUB_HEALTH_QUEUE}.next"
    mv "${STUB_HEALTH_QUEUE}.next" "$STUB_HEALTH_QUEUE"
    printf '%s' "$body"
    exit 0
  fi
  [[ "${STUB_HEALTH_RC:-0}" -eq 0 ]] || exit "$STUB_HEALTH_RC"
  printf '%s' "${STUB_HEALTH_BODY-}"
  exit 0
fi
if [[ "$url" == *"/api/build"* ]]; then
  if [[ "$rollback_phase" -eq 1 ]]; then
    [[ "${STUB_ROLLBACK_BUILD_RC:-0}" -eq 0 ]] || exit "$STUB_ROLLBACK_BUILD_RC"
    if [[ -n "${STUB_ROLLBACK_BUILD_BODY:-}" ]]; then
      printf '%s' "$STUB_ROLLBACK_BUILD_BODY"
    else
      rev=$(awk -F '\t' '$1 == "pantry" { print ($8 != "" ? $8 : $7); exit }' "${STUB_IMAGES:?}")
      printf '{"commit":"%s"}' "$rev"
    fi
    exit 0
  fi
  if [[ -n "${STUB_BUILD_QUEUE:-}" && -s "$STUB_BUILD_QUEUE" ]]; then
    body=$(head -n 1 "$STUB_BUILD_QUEUE")
    tail -n +2 "$STUB_BUILD_QUEUE" > "${STUB_BUILD_QUEUE}.next"
    mv "${STUB_BUILD_QUEUE}.next" "$STUB_BUILD_QUEUE"
    printf '%s' "$body"
    exit 0
  fi
  [[ "${STUB_BUILD_RC:-0}" -eq 0 ]] || exit "$STUB_BUILD_RC"
  printf '%s' "${STUB_BUILD_BODY-}"
  exit 0
fi
exit 0
EOF

  cat > "$bin/chown" << 'EOF'
#!/usr/bin/env bash
if [[ -n "${CHOWN_LOG:-}" ]]; then
  printf '%s\n' "$*" >> "$CHOWN_LOG"
fi
exit 0
EOF

  cat > "$bin/mv" << 'EOF'
#!/usr/bin/env bash
if [[ -n "${PANTRY_TEST_MV_FAIL_DEST:-}" ]]; then
  dest=${!#}
  if [[ "$dest" == *"$PANTRY_TEST_MV_FAIL_DEST"* ]] || [[ "$(basename "$dest")" == "$PANTRY_TEST_MV_FAIL_DEST" ]]; then
    echo "stub mv: refusing to install ${dest}" >&2
    exit 1
  fi
fi
exec /bin/mv "$@"
EOF

  cat > "$bin/cp" << 'EOF'
#!/usr/bin/env bash
if [[ -n "${PANTRY_TEST_SNAPSHOT_CP_FAIL_REL:-}" ]]; then
  dest=${!#}
  if [[ "$dest" == *host-scripts-last-known-good* && "$dest" == *"$PANTRY_TEST_SNAPSHOT_CP_FAIL_REL"* ]]; then
    echo "stub cp: refusing snapshot for ${PANTRY_TEST_SNAPSHOT_CP_FAIL_REL}" >&2
    exit 1
  fi
fi
exec /bin/cp "$@"
EOF

  chmod +x "$bin/docker" "$bin/curl" "$bin/chown" "$bin/mv" "$bin/cp"
  if [[ -n "${PANTRY_TEST_PATH_WITHOUT_JQ:-}" ]]; then
    local cmd p d
    for d in /usr/bin /bin; do
      [[ -d "$d" ]] || continue
      for p in "$d"/*; do
        [[ -x "$p" ]] || continue
        cmd=$(basename "$p")
        [[ "$cmd" == jq ]] && continue
        [[ -e "$bin/$cmd" ]] && continue
        ln -sf "$p" "$bin/$cmd"
      done
    done
  fi
}

prepare_pantry() {
  local pantry="$1"
  mkdir -p "$pantry/systemd"
  cp "$ROOT/deploy/publish-mode.sh" "$ROOT/deploy/runtime-setup.sh" "$ROOT/deploy/vapid-env.sh" "$pantry/"
  cp "$ROOT/deploy/systemd/pantry-update.sh" \
    "$ROOT/deploy/systemd/pantry-backup.sh" \
    "$pantry/systemd/"
  chmod +x "$pantry/systemd/pantry-update.sh" "$pantry/systemd/pantry-backup.sh"
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
pantry	ghcr.io/rhionin/pantry:latest	sha256:runpantry	ghcr.io/rhionin/pantry@sha256:oldpantry	$2	$3	${NEW_COMMIT}	${OLD_COMMIT}
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
  : > "$work/chown.log"

  local -a extra_env=()
  if [[ -n "${STUB_ROLLBACK_HEALTH_BODY:-}" ]]; then
    extra_env+=(STUB_ROLLBACK_HEALTH_BODY="$STUB_ROLLBACK_HEALTH_BODY")
  fi
  if [[ -n "${STUB_ROLLBACK_BUILD_BODY:-}" ]]; then
    extra_env+=(STUB_ROLLBACK_BUILD_BODY="$STUB_ROLLBACK_BUILD_BODY")
  fi
  if [[ -n "${STUB_PROJECT_LABEL+x}" ]]; then
    extra_env+=(STUB_PROJECT_LABEL="$STUB_PROJECT_LABEL")
  fi
  if [[ -n "${STUB_TUNNEL_BODY+x}" ]]; then
    extra_env+=(STUB_TUNNEL_BODY="$STUB_TUNNEL_BODY")
  fi
  if [[ -n "${STUB_TUNNEL_OLD_BODY+x}" ]]; then
    extra_env+=(STUB_TUNNEL_OLD_BODY="$STUB_TUNNEL_OLD_BODY")
  fi

  if [[ -n "${STUB_GITHUB_COMPARE_STATUS+x}" ]]; then
    extra_env+=(STUB_GITHUB_COMPARE_STATUS="$STUB_GITHUB_COMPARE_STATUS")
  fi
  if [[ -n "${BACKUP_BEFORE_DEPLOY+x}" ]]; then
    extra_env+=(BACKUP_BEFORE_DEPLOY="$BACKUP_BEFORE_DEPLOY")
  fi

  local -a deadline_cmd=()
  if [[ -n "${PANTRY_TEST_DEADLINE:-}" ]]; then
    deadline_cmd=(timeout "$PANTRY_TEST_DEADLINE")
  fi

  local path_setting="$bin:$PATH"
  if [[ -n "${PANTRY_TEST_PATH_WITHOUT_JQ:-}" ]]; then
    path_setting="$bin"
  fi

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
    STUB_HEALTH_QUEUE="${STUB_HEALTH_QUEUE:-}" \
    STUB_BUILD_QUEUE="${STUB_BUILD_QUEUE:-}" \
    STUB_UP_RC="${STUB_UP_RC:-0}" \
    STUB_ROLLBACK_RC="${STUB_ROLLBACK_RC:-0}" \
    STUB_ROLLBACK_HEALTH_RC="${STUB_ROLLBACK_HEALTH_RC:-0}" \
    STUB_CADDY_RC="${STUB_CADDY_RC:-0}" \
    STUB_CADDY_OLD_RC="${STUB_CADDY_OLD_RC:-}" \
    STUB_CADDY_RETRY_RC="${STUB_CADDY_RETRY_RC:-}" \
    STUB_SHIFT_STARTED_SERVICE="${STUB_SHIFT_STARTED_SERVICE:-}" \
    STUB_STARTED_AT="${STUB_STARTED_AT:-}" \
    STUB_STARTED_AT_AFTER="${STUB_STARTED_AT_AFTER:-}" \
    STUB_REWRITE_TRIGGER="${STUB_REWRITE_TRIGGER:-}" \
    STUB_TUNNEL_RC="${STUB_TUNNEL_RC:-0}" \
    STUB_TUNNEL_OLD_RC="${STUB_TUNNEL_OLD_RC:-0}" \
    STUB_TUNNEL_OLD_STATUS="${STUB_TUNNEL_OLD_STATUS:-200}" \
    STUB_TUNNEL_STATUS="${STUB_TUNNEL_STATUS:-200}" \
    STUB_TUNNEL_LATER_FROM="${STUB_TUNNEL_LATER_FROM:-}" \
    STUB_TUNNEL_LATER_RC="${STUB_TUNNEL_LATER_RC:-22}" \
    STUB_LIVE_IMAGE_TSV="${STUB_LIVE_IMAGE_TSV:-}" \
    STUB_NEW_CID_SERVICE="${STUB_NEW_CID_SERVICE:-}" \
    STUB_CONTAINER_STATUS="${STUB_CONTAINER_STATUS:-running}" \
    STUB_RESTART_COUNT="${STUB_RESTART_COUNT:-0}" \
    STUB_CONTAINER_LOGS="${STUB_CONTAINER_LOGS:-}" \
    STUB_STATUS_TSV="${STUB_STATUS_TSV:-}" \
    STUB_COMPOSE_SERVICE="${STUB_COMPOSE_SERVICE:-}" \
    STUB_BUMP_SERVICE="${STUB_BUMP_SERVICE:-}" \
    STUB_BUMP_ON_CALL="${STUB_BUMP_ON_CALL:-2}" \
    STUB_BUMP_RESTARTS="${STUB_BUMP_RESTARTS:-1}" \
    STUB_BUMP_STATUS="${STUB_BUMP_STATUS:-}" \
    STUB_INSPECT_STATE="$work/inspect-counts.tsv" \
    STUB_ABSENT_REFS="${STUB_ABSENT_REFS:-}" \
    PANTRY_DIR="$pantry" \
    PANTRY_SETUP_SKIP_ROOT=1 \
    PANTRY_SETUP_GROUP='' \
    SUDO_USER='' \
    CHOWN_LOG="$work/chown.log" \
    PANTRY_TEST_PROJECT_PREFIX="${PANTRY_TEST_PROJECT_PREFIX:-}" \
    PANTRY_UPDATE_HEALTH_TIMEOUT="${PANTRY_UPDATE_HEALTH_TIMEOUT:-0}" \
    PANTRY_UPDATE_HEALTH_INTERVAL="${PANTRY_UPDATE_HEALTH_INTERVAL:-1}" \
    PANTRY_UPDATE_MIGRATION_TIMEOUT="${PANTRY_UPDATE_MIGRATION_TIMEOUT:-600}" \
    PANTRY_UPDATE_SETTLE_SECONDS="${PANTRY_UPDATE_SETTLE_SECONDS:-0}" \
    PANTRY_UPDATE_PROBE_RETRY_SECONDS="${PANTRY_UPDATE_PROBE_RETRY_SECONDS:-0}" \
    PANTRY_UPDATE_MIN_FREE_KIB="${PANTRY_UPDATE_MIN_FREE_KIB:-65536}" \
    PANTRY_BACKUP_LOCK_WAIT="${PANTRY_BACKUP_LOCK_WAIT:-1}" \
    STUB_BACKUP_RC="${STUB_BACKUP_RC:-0}" \
    STUB_HOST_BUNDLE="${STUB_HOST_BUNDLE:-}" \
    STUB_EMPTY_REPO_DIGEST_FOR="${STUB_EMPTY_REPO_DIGEST_FOR:-}" \
    STUB_EMPTY_IMAGE_ID_FOR="${STUB_EMPTY_IMAGE_ID_FOR:-}" \
    PANTRY_TEST_MV_FAIL_DEST="${PANTRY_TEST_MV_FAIL_DEST:-}" \
    PANTRY_TEST_PATH_WITHOUT_JQ="${PANTRY_TEST_PATH_WITHOUT_JQ:-}" \
    PANTRY_TEST_GITHUB_COMPARE_PRETTY="${PANTRY_TEST_GITHUB_COMPARE_PRETTY:-}" \
    PANTRY_TEST_SNAPSHOT_CP_FAIL_REL="${PANTRY_TEST_SNAPSHOT_CP_FAIL_REL:-}" \
    STUB_FIXTURE_ROOT="$ROOT/deploy/fixtures" \
    PANTRY_SYSTEMD_UNIT_DIR="${PANTRY_SYSTEMD_UNIT_DIR:-}" \
    PATH="$path_setting" \
    "${deadline_cmd[@]}" env -u BACKUP_BEFORE_DEPLOY "${extra_env[@]}" bash "$pantry/systemd/pantry-update.sh" >"$out" 2>"$err"
  LAST_RC=$?
  set -e

  LAST_OUT=$(cat "$out" "$err")
  LAST_DOCKER=$(cat "$work/docker.log")
  LAST_CURL=$(cat "$work/curl.log")
  LAST_SEQUENCE=$(cat "$work/sequence.log")
  LAST_CHOWN=$(cat "$work/chown.log")
  rm -rf "$work"
}

# grep -q stops at the first match. Under pipefail a pipe then dies with
# SIGPIPE, so have fails and lack passes even when the text is there (#272).
have() {
  local haystack="$1" needle="$2" label="$3"
  if ! grep -q -F -- "$needle" <<<"$haystack"; then
    fail "$label (missing $needle)"
  fi
}

lack() {
  local haystack="$1" needle="$2" label="$3"
  if grep -q -F -- "$needle" <<<"$haystack"; then
    fail "$label (unexpected $needle)"
  fi
}

assert_github_compare_curl_max_time() {
  local log="$1" label="$2" line
  line=$(grep -F 'api.github.com/repos/Rhionin/stockbot/compare/' <<<"$log" | head -n1)
  [[ -n "$line" ]] || fail "$label (missing GitHub compare curl line)"
  have "$line" --max-time "$label"
}

assert_deploy_status_success() {
  local status_body="$1" label="$2"
  have "$status_body" result=success "$label records success result"
}

# The match is the first of tens of thousands of lines, which is when the old
# pipe helper lost the writer. Fifty tries is enough for that helper to fail
# on every run. pipefail is already on for this file.
assert_have_lack_large_input() {
  local large n
  large=$(awk 'BEGIN { print "NEEDLE-PRESENT"; for (i = 1; i <= 40000; i++) print "padding" }')
  for ((n = 1; n <= 50; n++)); do
    have "$large" "NEEDLE-PRESENT" "large input contains the needle"
    if (lack "$large" "NEEDLE-PRESENT" "large input must report the needle") 2>/dev/null; then
      fail "lack passed on large input that contains the needle (try $n)"
    fi
  done
}
assert_have_lack_large_input

# --- github_compare_top_status (jq and no-jq fallback) ---
eval "$(sed -n '/^github_compare_top_status() {/,/^}$/p' "$UPDATE")"

unit_test_bin=$(mktemp -d)
for cmd in grep sed tr head bash; do
  ln -sf "$(command -v "$cmd")" "$unit_test_bin/$cmd"
done
no_jq_path=$unit_test_bin

compare_top_status_with_path() {
  local path="$1" body="$2"
  PATH="$path" /bin/bash -c "$(declare -f github_compare_top_status); github_compare_top_status $(printf '%q' "$body")"
}

FIXTURE_DIR="$ROOT/deploy/fixtures"
pretty_ahead=$(cat "$FIXTURE_DIR/github_compare_ahead_pretty.json")
[[ "$(github_compare_top_status "$pretty_ahead")" == ahead ]] || fail "compare unit: jq pretty ahead"
[[ "$(compare_top_status_with_path "$no_jq_path" "$pretty_ahead")" == ahead ]] \
  || fail "compare unit: no-jq pretty ahead (old fallback would yield modified)"

for gh_case in identical:identical behind:behind diverged:diverged; do
  name=${gh_case%%:*}
  want=${gh_case#*:}
  body=$(cat "$FIXTURE_DIR/github_compare_${name}_pretty.json")
  [[ "$(github_compare_top_status "$body")" == "$want" ]] || fail "compare unit: jq $name"
  [[ "$(compare_top_status_with_path "$no_jq_path" "$body")" == "$want" ]] \
    || fail "compare unit: no-jq $name"
done

malformed_body=$'{\n  "files": [\n    {\n      "filename": "README.md",\n      "status": "modified"\n    }\n  ]\n}'
[[ -z "$(github_compare_top_status "$malformed_body")" ]] \
  || fail "compare unit: jq missing top-level status should be empty"
malformed_no_jq=$(compare_top_status_with_path "$no_jq_path" "$malformed_body")
[[ "$malformed_no_jq" != ahead && "$malformed_no_jq" != identical ]] \
  || fail "compare unit: no-jq malformed body must not look ahead or identical"
[[ -z "$(github_compare_top_status '')" ]] || fail "compare unit: empty body"
[[ -z "$(compare_top_status_with_path "$no_jq_path" '')" ]] || fail "compare unit: no-jq empty body"

rm -rf "$unit_test_bin"

line_of() {
  local haystack="$1" needle="$2"
  grep -n -F -m1 -- "$needle" <<< "$haystack" | cut -d: -f1 || true
}

env_unchanged() {
  local pantry="$1"
  cmp -s "$pantry/.env" "$pantry/.env.orig" || fail "update rewrote .env"
}

# expected_setup_group follows setup_dir_group. run_update clears
# PANTRY_SETUP_GROUP and SUDO_USER. The decoy namespace maps the runner
# to root, so the .env group is root and the fallback applies.
expected_setup_group() {
  local pantry="$1" group gid path
  for path in "$pantry/.env" "$pantry"; do
    if [[ -e "$path" ]]; then
      gid=$(stat -c '%g' "$path" 2>/dev/null || true)
      group=$(stat -c '%G' "$path" 2>/dev/null || true)
      if [[ -n "$group" && "$group" != root && "$gid" != 0 ]]; then
        printf '%s' "$group"
        return 0
      fi
    fi
  done
  if getent group rhionin >/dev/null 2>&1; then
    printf '%s' rhionin
    return 0
  fi
  printf '%s' 65532
}

# normalize_update_docker strips fixture paths from a docker log so two
# runs can be compared. The pre-deploy backup copies config secrets into
# a fresh directory; that name is not part of the compose argv.
normalize_update_docker() {
  local text="$1" dir="$2"
  text=${text//$dir/<pantry>}
  text=${text//$(basename "$dir")/<proj>}
  printf '%s\n' "$text" | sed -E \
    -e 's#/(tmp|run)/stockbot-backup-secrets\.[^ :]+#/\1/stockbot-backup-secrets.<id>#g' \
    -e '/^cp cid-pantry:\/usr\/share\/stockbot\/host-bundle\//d'
}

NEW_COMMIT=0123456789abcdef0123456789abcdef01234567
OLD_COMMIT=ffffffffffffffffffffffffffffffffffffffff
MID_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa

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
have "$LAST_DOCKER" "pending-before-up" "pending marker is written before compose up"
lack "$LAST_DOCKER" "pending-missing-before-up" "compose up does not run without the pending marker"
lack "$LAST_CURL" "stockbot.rhionin.com" "healthy run does not use the public hostname"
lack "$LAST_DOCKER" "rm -f pantry-caddy" "a fixture pantry dir does not remove pantry-caddy"
lack "$LAST_DOCKER" "rm -f pantry-cloudflared" "a fixture pantry dir does not remove pantry-cloudflared"
env_unchanged "$healthy"
healthy_setup_mode=$(stat -c '%a' "$healthy/setup")
[[ "$healthy_setup_mode" == "2750" ]] || fail "LAN update setup dir mode is $healthy_setup_mode, want 2750"
have "$LAST_CHOWN" "65532:$(expected_setup_group "$healthy") $healthy/setup" "LAN update chowns the setup directory to uid 65532 and the operator group"
grep -q '^APP_BASE_URL=' "$healthy/.env" && fail "LAN update must leave APP_BASE_URL unset"
[[ ! -e "$healthy/.update-rejected-digest" ]] || fail "healthy run recorded a rejected digest"
record_line=$(line_of "$LAST_DOCKER" "sha256:runpantry")
pull_line=$(line_of "$LAST_DOCKER" " pull")
up_line=$(line_of "$LAST_SEQUENCE" " up -d")
curl_line=$(line_of "$LAST_SEQUENCE" "curl ")
[[ -n "$record_line" && -n "$pull_line" && "$record_line" -lt "$pull_line" ]] || fail "healthy run inspected the running image after pull (record=$record_line pull=$pull_line)"
[[ -n "$up_line" && -n "$curl_line" && "$up_line" -lt "$curl_line" ]] || fail "healthy run polled health before up -d"
backup_line=$(line_of "$LAST_DOCKER" "--reason pre-deploy")
if [[ -z "$backup_line" ]]; then
  backup_line=$(line_of "$LAST_SEQUENCE" "--reason pre-deploy")
fi
[[ -n "$backup_line" && -n "$up_line" && "$backup_line" -lt "$up_line" ]] || fail "healthy run did not back up before recreate (backup=$backup_line up=$up_line)"
have "$LAST_OUT" "pantry update: pre-deploy backup (required)" "a pantry image change takes a required backup by default"
have "$LAST_OUT" "backup: deploy lock already held" "the updater tells the backup it already holds the lock"
have "$LAST_OUT" "pantry update: pre-deploy backup finished" "a successful pre-deploy backup finishes"
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
lack "$LAST_OUT" "pre-deploy backup" "an unchanged pantry image does not take a backup"
lack "$LAST_DOCKER" "--reason pre-deploy" "an unchanged pantry image does not start the backup container"
[[ -z "$LAST_CURL" ]] || fail "unchanged pull polled health: $LAST_CURL"
lack "$LAST_OUT" "PANTRY UPDATE FAILED" "unchanged pull does not roll back"
env_unchanged "$noop"
[[ ! -e "$noop/.update-rejected-digest" ]] || fail "unchanged pull recorded a rejected digest"
unset STUB_HEALTH_BODY STUB_BUILD_BODY

# An origin the operator already set is left alone, including its mode.
kept_origin=$(mktemp -d)
prepare_pantry "$kept_origin"
cat > "$kept_origin/.env" << 'EOF'
HOST_PORT=8080
PUBLIC_HOST=stockbot.example
APP_BASE_URL=https://operator.example
PANTRY_IMAGE_TAG=latest
VAPID_PUBLIC_KEY=test-public
VAPID_PRIVATE_KEY=test-private
VAPID_SUBJECT=mailto:pantry@localhost
EOF
cp "$kept_origin/.env" "$kept_origin/.env.orig"
chmod 600 "$kept_origin/.env"
write_images "$kept_origin/images.tsv" \
  sha256:runpantry ghcr.io/rhionin/pantry@sha256:oldpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
run_update "$kept_origin"
[[ "$LAST_RC" -eq 0 ]] || fail "custom origin update exited $LAST_RC: $LAST_OUT"
grep -q '^APP_BASE_URL=https://operator.example$' "$kept_origin/.env" || fail "update replaced a custom APP_BASE_URL"
[[ $(stat -c '%a' "$kept_origin/.env") == "600" ]] || fail "custom origin update changed .env mode from 0600"
env_unchanged "$kept_origin"

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
lack "$LAST_OUT" "pantry update: finished" "mismatch does not report success"
lack "$LAST_OUT" "healthy, running commit" "mismatch does not report the new image healthy"
lack "$LAST_OUT" "BOTH IMAGES DOWN" "a healthy rollback is not both images down"
have "$LAST_OUT" "previous image healthy, commit ${OLD_COMMIT}" "rollback health check accepts the previous image"
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
checked_again=$(printf '%s\n' "$LAST_SEQUENCE" | awk -v n="$rollback_up" 'NR > n && /curl / { print NR; exit }')
[[ -n "$checked_again" ]] || fail "rollback was not health-checked"

# The next minute pulls the same bad remote digest, does not start it, and
# leaves the local tag on the good digest. The restored tag must not make
# this look like "images unchanged". A digest that was already rejected
# exits 0 with one line instead of failing every minute.
run_update "$mismatch"
[[ "$LAST_RC" -eq 0 ]] || fail "rejected digest should exit 0: $LAST_OUT"
have "$LAST_OUT" "skipping rejected ghcr.io/rhionin/pantry@sha256:newpantry" "second run skips the rejected digest"
have "$LAST_OUT" "<4>pantry update: warning: skipping rejected ghcr.io/rhionin/pantry@sha256:newpantry" "the first skip of a digest is a warning"
lack "$LAST_OUT" "PANTRY UPDATE FAILED" "second run does not repeat the failure banner"
lack "$LAST_OUT" "was already rolled back" "second run does not use the old failure banner"
have "$LAST_DOCKER" " pull" "second run still pulls the remote digest"
lack "$LAST_OUT" "images unchanged" "retagged local tag is not treated as the remote image"
lack "$LAST_DOCKER" " up " "second run does not recreate containers"
lack "$LAST_DOCKER" "--pull never" "second run does not roll back again"
[[ -z "$LAST_CURL" ]] || fail "second run polled health: $LAST_CURL"
have "$(cat "$mismatch/tag-state.tsv")" "ghcr.io/rhionin/pantry:latest	sha256:runpantry	ghcr.io/rhionin/pantry@sha256:oldpantry" "second run puts the local tag back on the good digest"
env_unchanged "$mismatch"

# The following minute repeats the skip and does not warn again.
run_update "$mismatch"
[[ "$LAST_RC" -eq 0 ]] || fail "third skip should exit 0: $LAST_OUT"
have "$LAST_OUT" "skipping rejected ghcr.io/rhionin/pantry@sha256:newpantry" "third run still skips the rejected digest"
lack "$LAST_OUT" "<4>pantry update: warning: skipping rejected" "a digest is warned only once"
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
lack "$LAST_OUT" "BOTH IMAGES DOWN" "a healthy rollback of an unhealthy image is not both images down"
have "$LAST_OUT" "previous image healthy, commit ${OLD_COMMIT}" "unhealthy image rolls back onto a healthy previous image"
env_unchanged "$unhealthy"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_HEALTH_RC STUB_BUILD_RC

# A caddy-only change that fails to start must not reject the pantry digest
# that was already running.
caddy_only=$(mktemp -d)
prepare_pantry "$caddy_only"
write_images "$caddy_only/images.tsv" \
  sha256:runpantry ghcr.io/rhionin/pantry@sha256:oldpantry \
  sha256:newcaddy caddy@sha256:newcaddy
printf '%s\n' $'caddy\texited\t0' > "$caddy_only/status.tsv"
STUB_STATUS_TSV="$caddy_only/status.tsv"
STUB_UP_RC=1
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${OLD_COMMIT}\"}"
run_update "$caddy_only"
[[ "$LAST_RC" -ne 0 ]] || fail "caddy-only up failure should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "healthy, running commit ${OLD_COMMIT}" "caddy-only failure leaves pantry healthy"
have "$(cat "$caddy_only/.update-rejected-digest")" "caddy@sha256:newcaddy" "caddy-only failure rejects the caddy digest"
lack "$(cat "$caddy_only/.update-rejected-digest")" "oldpantry" "caddy-only failure does not reject the running pantry digest"
lack "$(cat "$caddy_only/.update-rejected-digest")" "newpantry" "caddy-only failure does not invent a pantry digest"
lack "$LAST_OUT" "BOTH IMAGES DOWN" "caddy-only rollback reaches the previous pantry image"
lack "$LAST_OUT" "pre-deploy backup" "a caddy-only image change does not back up"
lack "$LAST_DOCKER" "--reason pre-deploy" "a caddy-only image change does not start the backup container"
unset STUB_STATUS_TSV STUB_UP_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# Rollback that does not start records nothing and says both images are down.
rollback_fail=$(mktemp -d)
prepare_pantry "$rollback_fail"
write_images "$rollback_fail/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:newcaddy caddy@sha256:newcaddy
STUB_HEALTH_RC=1
STUB_BUILD_RC=1
STUB_ROLLBACK_RC=1
run_update "$rollback_fail"
[[ "$LAST_RC" -ne 0 ]] || fail "failed rollback should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "BOTH IMAGES DOWN" "failed rollback names both images"
[[ ! -e "$rollback_fail/.update-rejected-digest" ]] || fail "failed rollback recorded a digest: $(cat "$rollback_fail/.update-rejected-digest")"
unset STUB_HEALTH_RC STUB_BUILD_RC STUB_ROLLBACK_RC

# A pantry image with no revision label is never started.
unlabeled=$(mktemp -d)
prepare_pantry "$unlabeled"
write_images "$unlabeled/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
awk -F '\t' 'BEGIN { OFS = "\t" } $1 == "pantry" { $7 = ""; $8 = "" } { print }' \
  "$unlabeled/images.tsv" > "$unlabeled/images.tsv.next"
mv "$unlabeled/images.tsv.next" "$unlabeled/images.tsv"
run_update "$unlabeled"
[[ "$LAST_RC" -ne 0 ]] || fail "missing label should exit non-zero: $LAST_OUT"
lack "$LAST_SEQUENCE" " up -d" "missing label must not run compose up"
lack "$LAST_SEQUENCE" "--pull never" "missing label must not start a rollback"
have "$LAST_OUT" "not starting it" "missing label says the image was not started"
have "$(cat "$unlabeled/.update-rejected-digest")" "ghcr.io/rhionin/pantry@sha256:newpantry" "missing label rejects the new pantry digest"

# Health becomes ok on the third poll. Timeout 0 would stop after the first.
polled=$(mktemp -d)
prepare_pantry "$polled"
write_images "$polled/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:newcaddy caddy@sha256:newcaddy
printf '%s\n' '{"status":"starting"}' '{"status":"starting"}' > "$polled/health.queue"
STUB_HEALTH_QUEUE="$polled/health.queue"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
PANTRY_UPDATE_HEALTH_TIMEOUT=30
PANTRY_UPDATE_HEALTH_INTERVAL=0
PANTRY_TEST_PROJECT_PREFIX=fixture
run_update "$polled"
[[ "$LAST_RC" -eq 0 ]] || fail "health that recovers should exit 0: $LAST_OUT"
have "$LAST_OUT" "pantry update: healthy, running commit ${NEW_COMMIT}" "recovered health is kept"
lack "$LAST_OUT" "PANTRY UPDATE FAILED" "recovered health does not roll back"
health_polls=$(printf '%s\n' "$LAST_CURL" | grep -c '/health' || true)
[[ "$health_polls" -ge 3 ]] || fail "health loop ran ${health_polls} polls, want at least 3"
have "$LAST_DOCKER" "rm -f fixture-cloudflared" "test project prefix derives the cloudflared name"
lack "$LAST_DOCKER" "rm -f pantry-cloudflared" "test project prefix does not remove pantry-cloudflared"
unset STUB_HEALTH_QUEUE STUB_HEALTH_BODY STUB_BUILD_BODY PANTRY_UPDATE_HEALTH_TIMEOUT PANTRY_UPDATE_HEALTH_INTERVAL PANTRY_TEST_PROJECT_PREFIX

# /health reports migrating twice, then ok. Timeout 0 must not give up on the first 503.
migrating=$(mktemp -d)
prepare_pantry "$migrating"
write_images "$migrating/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' '{"status":"migrating","error":"Updating. Try again in a minute.","code":"updating"}' '{"status":"migrating","error":"Updating. Try again in a minute.","code":"updating"}' > "$migrating/health.queue"
STUB_HEALTH_QUEUE="$migrating/health.queue"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
PANTRY_UPDATE_HEALTH_TIMEOUT=0
PANTRY_UPDATE_HEALTH_INTERVAL=0
run_update "$migrating"
[[ "$LAST_RC" -eq 0 ]] || fail "migrating health should exit 0: $LAST_OUT"
have "$LAST_OUT" "migrations still running" "migrating status keeps the wait going"
have "$LAST_OUT" "pantry update: healthy, running commit ${NEW_COMMIT}" "migrating wait then succeeds"
lack "$LAST_DOCKER" "--pull never" "migrating wait does not roll back"
unset STUB_HEALTH_QUEUE STUB_HEALTH_BODY STUB_BUILD_BODY PANTRY_UPDATE_HEALTH_TIMEOUT PANTRY_UPDATE_HEALTH_INTERVAL

# The migration cap is what ends a check that stays on migrating. A tiny
# override must fail at once; ignoring the cap would sleep until the deadline.
migr_cap=$(mktemp -d)
prepare_pantry "$migr_cap"
write_images "$migr_cap/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"migrating","error":"Updating. Try again in a minute.","code":"updating"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
PANTRY_UPDATE_MIGRATION_TIMEOUT=0
PANTRY_UPDATE_HEALTH_INTERVAL=1
PANTRY_TEST_DEADLINE=60
run_update "$migr_cap"
[[ "$LAST_RC" -ne 0 ]] || fail "migration cap should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "still migrating after 0s" "migration cap stops a check that stays migrating"
have "$(cat "$migr_cap/.update-rejected-digest")" "newpantry" "migration cap rejects the unverified pantry digest"
unset STUB_HEALTH_BODY STUB_BUILD_BODY PANTRY_UPDATE_MIGRATION_TIMEOUT PANTRY_UPDATE_HEALTH_INTERVAL PANTRY_TEST_DEADLINE

# A pending marker means the previous run died after up. Images look unchanged,
# but the health check still runs and a failure rolls back to the saved pin.
pending=$(mktemp -d)
prepare_pantry "$pending"
write_images "$pending/images.tsv" \
  sha256:runpantry ghcr.io/rhionin/pantry@sha256:oldpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'pantry\tghcr.io/rhionin/pantry@sha256:previous\tghcr.io/rhionin/pantry@sha256:oldpantry' > "$pending/.update-pending"
STUB_HEALTH_RC=1
STUB_BUILD_RC=1
run_update "$pending"
[[ "$LAST_RC" -ne 0 ]] || fail "pending unhealthy deploy should exit non-zero: $LAST_OUT"
lack "$LAST_OUT" "images unchanged" "pending marker is not treated as a no-op"
have "$LAST_DOCKER" "image: ghcr.io/rhionin/pantry@sha256:previous" "pending failure re-pins the saved digest"
have "$LAST_OUT" "previous deploy was not confirmed" "pending marker is resumed"
unset STUB_HEALTH_RC STUB_BUILD_RC

# An exited container fails the forward wait without sitting out the timeout.
exited=$(mktemp -d)
prepare_pantry "$exited"
write_images "$exited/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_CONTAINER_STATUS=exited
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
PANTRY_UPDATE_HEALTH_TIMEOUT=30
PANTRY_UPDATE_HEALTH_INTERVAL=0
run_update "$exited"
[[ "$LAST_RC" -ne 0 ]] || fail "exited container should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "pantry container is exited" "exited container fails fast"
have "$LAST_DOCKER" "--pull never" "exited container rolls back"
unset STUB_CONTAINER_STATUS STUB_HEALTH_BODY STUB_BUILD_BODY PANTRY_UPDATE_HEALTH_TIMEOUT PANTRY_UPDATE_HEALTH_INTERVAL

# The public proxy is checked through Caddy after the LAN health check.
proxied=$(mktemp -d)
prepare_pantry "$proxied"
cat > "$proxied/.env" << 'EOF'
HOST_PORT=8080
PUBLIC_HOST=stockbot.example
PANTRY_IMAGE_TAG=latest
PANTRY_SESSION_SECRET=test-secret
VAPID_PUBLIC_KEY=test-public
VAPID_PRIVATE_KEY=test-private
VAPID_SUBJECT=mailto:pantry@localhost
EOF
cp "$proxied/.env" "$proxied/.env.orig"
chmod 600 "$proxied/.env"
write_images "$proxied/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:newcaddy caddy@sha256:newcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$proxied"
[[ "$LAST_RC" -eq 0 ]] || fail "caddy probe should exit 0: $LAST_OUT"
have "$LAST_CURL" "--resolve stockbot.example:443:127.0.0.1" "caddy probe uses --resolve"
have "$LAST_CURL" "https://stockbot.example/api/build" "caddy probe requests /api/build"
grep -q '^APP_BASE_URL=https://stockbot.example$' "$proxied/.env" || fail "update did not set APP_BASE_URL from PUBLIC_HOST"
[[ $(stat -c '%a' "$proxied/.env") == "600" ]] || fail "update changed .env mode from 0600"
proxied_setup_mode=$(stat -c '%a' "$proxied/setup")
[[ "$proxied_setup_mode" == "2750" ]] || fail "public update setup dir mode is $proxied_setup_mode, want 2750"
have "$LAST_CHOWN" "65532:$(expected_setup_group "$proxied") $proxied/setup" "public update chowns the setup directory"
grep -v '^APP_BASE_URL=' "$proxied/.env" > "$proxied/.env.without"
cmp -s "$proxied/.env.without" "$proxied/.env.orig" || fail "update rewrote .env beyond APP_BASE_URL"
unset STUB_HEALTH_BODY STUB_BUILD_BODY

# Not enough free space skips the deploy before any container is recreated.
full_disk=$(mktemp -d)
prepare_pantry "$full_disk"
write_images "$full_disk/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:newcaddy caddy@sha256:newcaddy
PANTRY_UPDATE_MIN_FREE_KIB=999999999999
run_update "$full_disk"
[[ "$LAST_RC" -ne 0 ]] || fail "low disk should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "KiB free" "low disk names the free space"
lack "$LAST_SEQUENCE" " up -d" "low disk does not start containers"
unset PANTRY_UPDATE_MIN_FREE_KIB

# pantry-grocy-sync is no longer a compose service. A stopped container, and
# a restarting one, stay where they are: the deploy does not name them.
orphan=$(mktemp -d)
prepare_pantry "$orphan"
cp "$ROOT/deploy/docker-compose.yml" "$ROOT/deploy/docker-compose.grocy.yml" "$orphan/"
printf '%s\n' GROCY_URL=http://grocy:80 GROCY_API_KEY=invented-key PANTRY_SYNC_TOKEN=invented-token >> "$orphan/.env"
write_images "$orphan/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'grocy-sync\texited\t0' $'grocy\trestarting\t2' > "$orphan/status.tsv"
STUB_STATUS_TSV="$orphan/status.tsv"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$orphan"
[[ "$LAST_RC" -eq 0 ]] || fail "orphaned grocy-sync should be ignored: $LAST_OUT"
have "$LAST_OUT" "healthy, running commit ${NEW_COMMIT}" "an orphaned sidecar still deploys pantry"
lack "$LAST_OUT" "grocy-sync" "an orphaned grocy-sync is not reported"
lack "$LAST_OUT" "grocy is" "an orphaned grocy container is not reported"
lack "$LAST_DOCKER" "grocy" "an orphaned grocy container is not named in a docker command"
lack "$LAST_DOCKER" "--remove-orphans" "an orphaned grocy-sync is not removed"
if grep -qE '(^| )(down|volume rm|volume prune)( |$)' <<<"$LAST_DOCKER"; then
  fail "an orphaned grocy-sync must not take the stack down or remove volumes"
fi
[[ ! -e "$orphan/.update-rejected-digest" ]] || fail "an orphaned grocy-sync recorded a digest: $(cat "$orphan/.update-rejected-digest")"
unset STUB_STATUS_TSV STUB_HEALTH_BODY STUB_BUILD_BODY

# A changed cloudflared that is down after up fails the deploy. Pantry's
# image did not change and stayed healthy, so only the cloudflared digest
# is rejected. This is the shared confirm_recreated path.
cloud_down=$(mktemp -d)
prepare_pantry "$cloud_down"
write_images "$cloud_down/images.tsv" \
  sha256:runpantry ghcr.io/rhionin/pantry@sha256:oldpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'cloudflared\tcloudflare/cloudflared:2026.10.3\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\tsha256:newcloud\tcloudflare/cloudflared@sha256:newcloud\t' >> "$cloud_down/images.tsv"
printf '%s\n' $'cloudflared\texited\t0' > "$cloud_down/status.tsv"
STUB_STATUS_TSV="$cloud_down/status.tsv"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${OLD_COMMIT}\"}"
PANTRY_UPDATE_SETTLE_SECONDS=0
run_update "$cloud_down"
[[ "$LAST_RC" -ne 0 ]] || fail "changed cloudflared exit should fail the deploy: $LAST_OUT"
have "$LAST_OUT" "cloudflared is not running" "a changed cloudflared that exited is reported"
have "$(cat "$cloud_down/.update-rejected-digest")" $'cloudflared\tcloudflare/cloudflared@sha256:newcloud' "a changed cloudflared exit rejects only that digest"
lack "$(cat "$cloud_down/.update-rejected-digest")" "oldpantry" "a changed cloudflared exit does not reject pantry"
lack "$(cat "$cloud_down/.update-rejected-digest")" "newpantry" "a changed cloudflared exit does not invent a pantry digest"
unset STUB_STATUS_TSV STUB_HEALTH_BODY STUB_BUILD_BODY PANTRY_UPDATE_SETTLE_SECONDS

# compose up fails and the changed cloudflared is still on its previous
# image. That digest is rejected. The new pantry image, which is running,
# is not.
cloud_stale=$(mktemp -d)
prepare_pantry "$cloud_stale"
write_images "$cloud_stale/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'cloudflared\tcloudflare/cloudflared:2026.10.3\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\tsha256:newcloud\tcloudflare/cloudflared@sha256:newcloud\t' >> "$cloud_stale/images.tsv"
printf '%s\n' $'pantry\tsha256:newpantry' > "$cloud_stale/live.tsv"
STUB_LIVE_IMAGE_TSV="$cloud_stale/live.tsv"
STUB_UP_RC=1
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$cloud_stale"
[[ "$LAST_RC" -ne 0 ]] || fail "stale cloudflared should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "cloudflared is still running the previous image" "a stale changed cloudflared is not healthy"
have "$LAST_DOCKER" "--pull never" "a stale changed cloudflared rolls back"
have "$(cat "$cloud_stale/.update-rejected-digest")" "newcloud" "a stale cloudflared is rejected"
lack "$(cat "$cloud_stale/.update-rejected-digest")" "newpantry" "a stale cloudflared does not reject the new pantry"
unset STUB_LIVE_IMAGE_TSV STUB_UP_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# An unchanged cloudflared that was already restarting is a warning.
# Three runs keep the new pantry image and record no digest.
cloud_warn=$(mktemp -d)
prepare_pantry "$cloud_warn"
write_images "$cloud_warn/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'cloudflared\tcloudflare/cloudflared:2026.10.3\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\t' >> "$cloud_warn/images.tsv"
printf '%s\n' $'cloudflared\trestarting\t4' > "$cloud_warn/status.tsv"
STUB_STATUS_TSV="$cloud_warn/status.tsv"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
PANTRY_UPDATE_SETTLE_SECONDS=0
cloud_warn_try=0
while [[ "$cloud_warn_try" -lt 3 ]]; do
  cloud_warn_try=$((cloud_warn_try + 1))
  run_update "$cloud_warn"
  [[ "$LAST_RC" -eq 0 ]] || fail "already-restarting cloudflared run ${cloud_warn_try} exited $LAST_RC: $LAST_OUT"
  have "$LAST_OUT" "cloudflared was already restarting and its image did not change" "run ${cloud_warn_try} warns about cloudflared"
  lack "$LAST_DOCKER" "--pull never" "run ${cloud_warn_try} does not roll back"
  [[ ! -e "$cloud_warn/.update-rejected-digest" ]] || fail "run ${cloud_warn_try} recorded a digest: $(cat "$cloud_warn/.update-rejected-digest")"
done
unset STUB_STATUS_TSV STUB_HEALTH_BODY STUB_BUILD_BODY PANTRY_UPDATE_SETTLE_SECONDS

# A sidecar that is running at first and then crash-loops is caught on the
# settle re-check. Pantry already passed health, so only caddy is rejected.
crash=$(mktemp -d)
prepare_pantry "$crash"
write_images "$crash/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:newcaddy caddy@sha256:newcaddy
STUB_BUMP_SERVICE=caddy
STUB_BUMP_ON_CALL=3
STUB_BUMP_RESTARTS=1
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$crash"
[[ "$LAST_RC" -ne 0 ]] || fail "crash-loop should fail the deploy: $LAST_OUT"
have "$LAST_OUT" "caddy restarted (0 -> 1)" "settle re-check notices the restart"
have "$(cat "$crash/.update-rejected-digest")" $'caddy\tcaddy@sha256:newcaddy' "crash-loop rejects the caddy digest"
lack "$(cat "$crash/.update-rejected-digest")" "newpantry" "crash-loop does not reject the healthy pantry digest"
unset STUB_BUMP_SERVICE STUB_BUMP_ON_CALL STUB_BUMP_RESTARTS STUB_HEALTH_BODY STUB_BUILD_BODY

# A newer release is pulled before the pending deploy is resumed. Health is
# compared to the pending digest's label, not the newest image's label.
resumed=$(mktemp -d)
prepare_pantry "$resumed"
cat > "$resumed/images.tsv" << EOF
pantry	ghcr.io/rhionin/pantry:latest	sha256:runmid	ghcr.io/rhionin/pantry@sha256:midpantry	sha256:newpantry	ghcr.io/rhionin/pantry@sha256:newpantry	${NEW_COMMIT}	${MID_COMMIT}
caddy	caddy:2.11.7-alpine	sha256:runcaddy	caddy@sha256:oldcaddy	sha256:runcaddy	caddy@sha256:oldcaddy	
EOF
printf '%s\n' $'pantry\tghcr.io/rhionin/pantry@sha256:oldpantry\tghcr.io/rhionin/pantry@sha256:midpantry' > "$resumed/.update-pending"
printf '%s\n' "{\"commit\":\"${MID_COMMIT}\"}" "{\"commit\":\"${NEW_COMMIT}\"}" > "$resumed/build.queue"
STUB_BUILD_QUEUE="$resumed/build.queue"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$resumed"
[[ "$LAST_RC" -eq 0 ]] || fail "resume against the pending digest exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "previous deploy was not confirmed" "resume notices the pending marker"
have "$LAST_OUT" "waiting for /health and /api/build commit ${MID_COMMIT}" "resume waits for the pending digest's label"
have "$LAST_OUT" "pending deploy is healthy" "resume keeps the pending digest when its build matches"
have "$LAST_OUT" "waiting for /health and /api/build commit ${NEW_COMMIT}" "the newer release is deployed after the resume"
lack "$LAST_OUT" "re-pinning pantry to ghcr.io/rhionin/pantry@sha256:oldpantry" "resume does not roll back to the pre-pending image"
lack "$LAST_DOCKER" "--pull never" "resume does not start a rollback"
first_wait=$(printf '%s\n' "$LAST_OUT" | grep -m1 'waiting for /health')
have "$first_wait" "$MID_COMMIT" "the first health check is the pending digest"
lack "$first_wait" "$NEW_COMMIT" "the first health check is not the newer release"
unset STUB_BUILD_QUEUE STUB_HEALTH_BODY STUB_BUILD_BODY

# The pending digest is gone locally. Skip the resume instead of rolling back.
gone=$(mktemp -d)
prepare_pantry "$gone"
cat > "$gone/images.tsv" << EOF
pantry	ghcr.io/rhionin/pantry:latest	sha256:runmid	ghcr.io/rhionin/pantry@sha256:midpantry	sha256:runmid	ghcr.io/rhionin/pantry@sha256:midpantry	${MID_COMMIT}
caddy	caddy:2.11.7-alpine	sha256:runcaddy	caddy@sha256:oldcaddy	sha256:runcaddy	caddy@sha256:oldcaddy	
EOF
printf '%s\n' $'pantry\tghcr.io/rhionin/pantry@sha256:oldpantry\tghcr.io/rhionin/pantry@sha256:midpantry' > "$gone/.update-pending"
STUB_ABSENT_REFS='ghcr.io/rhionin/pantry@sha256:midpantry'
run_update "$gone"
[[ "$LAST_RC" -eq 0 ]] || fail "missing pending digest exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "not on this machine; skipping the resume check" "a missing pending digest is skipped"
have "$LAST_OUT" "images unchanged" "a missing pending digest leaves an unchanged stack alone"
lack "$LAST_OUT" "PANTRY UPDATE FAILED" "a missing pending digest does not fail the run"
lack "$LAST_OUT" "rolling back" "a missing pending digest does not roll back"
lack "$LAST_DOCKER" "--pull never" "a missing pending digest does not start a rollback"
unset STUB_ABSENT_REFS

# /health degraded, even with the expected build commit, is not healthy.
degraded=$(mktemp -d)
prepare_pantry "$degraded"
write_images "$degraded/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"degraded"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$degraded"
[[ "$LAST_RC" -ne 0 ]] || fail "degraded health should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "PANTRY UPDATE FAILED: rolling back to the previous image." "degraded health rolls back"
have "$(cat "$degraded/.update-rejected-digest")" "ghcr.io/rhionin/pantry@sha256:newpantry" "degraded health rejects the new pantry digest"
lack "$LAST_OUT" "healthy, running commit" "degraded health is not treated as ok"
unset STUB_HEALTH_BODY STUB_BUILD_BODY

# The banner names a snapshot this deploy wrote, not an older "already exists" path.
snap=$(mktemp -d)
prepare_pantry "$snap"
write_images "$snap/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_RC=1
STUB_BUILD_RC=1
STUB_CONTAINER_LOGS=$'shopping backup already exists: /data/pantry-pre-groups-20200101T000000Z.db\nwrote shopping backup: /data/pantry-pre-groups-20261017T150405Z.db'
run_update "$snap"
[[ "$LAST_RC" -ne 0 ]] || fail "snapshot rollback should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "Pre-migration snapshot written by this deploy: /data/pantry-pre-groups-20261017T150405Z.db" "banner names the snapshot this deploy wrote"
lack "$LAST_OUT" "pantry-pre-groups-20200101T000000Z.db" "banner does not name the already-exists snapshot"
lack "$LAST_OUT" "no new snapshot found" "a written snapshot is not reported missing"

# Only an "already exists" line: say so, and do not print that older path.
snap_old=$(mktemp -d)
prepare_pantry "$snap_old"
write_images "$snap_old/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_CONTAINER_LOGS='shopping backup already exists: /data/pantry-pre-groups-20200101T000000Z.db'
run_update "$snap_old"
[[ "$LAST_RC" -ne 0 ]] || fail "old snapshot rollback should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "no new snapshot found" "banner says when this deploy wrote no snapshot"
lack "$LAST_OUT" "pantry-pre-groups-20200101T000000Z.db" "banner does not name an already-exists snapshot"
unset STUB_HEALTH_RC STUB_BUILD_RC STUB_CONTAINER_LOGS

# Rollback starts, then its health check stays failed.
rollback_unhealthy=$(mktemp -d)
prepare_pantry "$rollback_unhealthy"
write_images "$rollback_unhealthy/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_RC=1
STUB_BUILD_RC=1
STUB_ROLLBACK_HEALTH_RC=1
run_update "$rollback_unhealthy"
[[ "$LAST_RC" -ne 0 ]] || fail "unhealthy rollback health should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "BOTH IMAGES DOWN" "unhealthy rollback health names both images"
have "$LAST_DOCKER" "--pull never" "unhealthy rollback health still starts the previous image"
have "$(cat "$rollback_unhealthy/.update-rejected-digest")" "ghcr.io/rhionin/pantry@sha256:newpantry" "unhealthy rollback health still records the digest"
unset STUB_HEALTH_RC STUB_BUILD_RC STUB_ROLLBACK_HEALTH_RC

# compose up fails and the new pantry never becomes healthy. The forward
# health check is one poll, not the configured timeout, and every changed
# digest is rejected because pantry is unverified.
unpinned=$(mktemp -d)
prepare_pantry "$unpinned"
write_images "$unpinned/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:newcaddy caddy@sha256:newcaddy
STUB_UP_RC=1
STUB_HEALTH_RC=1
STUB_BUILD_RC=1
PANTRY_UPDATE_HEALTH_TIMEOUT=5
PANTRY_UPDATE_HEALTH_INTERVAL=1
run_update "$unpinned"
[[ "$LAST_RC" -ne 0 ]] || fail "unpinned up failure should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "health check timed out after 0s" "up failure does not wait out the health timeout"
have "$(cat "$unpinned/.update-rejected-digest")" "newpantry" "unpinned up failure rejects pantry"
lack "$(cat "$unpinned/.update-rejected-digest")" "grocy" "unpinned up failure does not reject an orphaned sidecar"
rollback_line=$(line_of "$LAST_SEQUENCE" "--pull never")
forward_health=$(printf '%s\n' "$LAST_SEQUENCE" | awk -v n="$rollback_line" 'NR < n && /\/health/ { c++ } END { print c+0 }')
[[ "$forward_health" -eq 1 ]] || fail "up failure waited through ${forward_health} health polls"
unset STUB_UP_RC STUB_HEALTH_RC STUB_BUILD_RC PANTRY_UPDATE_HEALTH_TIMEOUT PANTRY_UPDATE_HEALTH_INTERVAL

# compose up fails while every container is running and pantry is healthy.
# Changed services verified, so the new images stay.
spill=$(mktemp -d)
prepare_pantry "$spill"
write_images "$spill/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:newcaddy caddy@sha256:newcaddy
printf '%s\n' $'pantry\tsha256:newpantry' $'caddy\tsha256:newcaddy' > "$spill/live.tsv"
STUB_LIVE_IMAGE_TSV="$spill/live.tsv"
STUB_UP_RC=1
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$spill"
[[ "$LAST_RC" -eq 0 ]] || fail "healthy up failure should keep the deploy: $LAST_OUT"
have "$LAST_OUT" "every changed service is healthy; not rolling back" "a healthy up failure does not roll back"
lack "$LAST_DOCKER" "--pull never" "a healthy up failure does not re-pin"
[[ ! -e "$spill/.update-rejected-digest" ]] || fail "healthy up failure recorded a digest: $(cat "$spill/.update-rejected-digest")"
unset STUB_UP_RC STUB_HEALTH_BODY STUB_BUILD_BODY STUB_LIVE_IMAGE_TSV

# An unchanged cloudflared restart during settle does not roll back.
flap=$(mktemp -d)
prepare_pantry "$flap"
write_images "$flap/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'cloudflared\tcloudflare/cloudflared:2026.10.0\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\t' >> "$flap/images.tsv"
STUB_BUMP_SERVICE=cloudflared
STUB_BUMP_ON_CALL=3
STUB_BUMP_RESTARTS=2
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$flap"
[[ "$LAST_RC" -eq 0 ]] || fail "unchanged cloudflared restart exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "cloudflared restarted during settle" "settle warns about an unchanged restart"
have "$LAST_OUT" "its image did not change; not rolling back" "unchanged restart does not roll back"
lack "$LAST_DOCKER" "--pull never" "unchanged cloudflared restart does not re-pin"
[[ ! -e "$flap/.update-rejected-digest" ]] || fail "unchanged cloudflared restart recorded a digest"
unset STUB_BUMP_SERVICE STUB_BUMP_ON_CALL STUB_BUMP_RESTARTS STUB_HEALTH_BODY STUB_BUILD_BODY

# Pantry's RestartCount rising during the health wait fails the deploy.
pantry_restart=$(mktemp -d)
prepare_pantry "$pantry_restart"
write_images "$pantry_restart/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_BUMP_SERVICE=pantry
STUB_BUMP_ON_CALL=3
STUB_BUMP_RESTARTS=1
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
PANTRY_UPDATE_HEALTH_TIMEOUT=30
PANTRY_UPDATE_HEALTH_INTERVAL=0
run_update "$pantry_restart"
[[ "$LAST_RC" -ne 0 ]] || fail "pantry restart should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "pantry container restarted" "a rising pantry RestartCount fails fast"
have "$(cat "$pantry_restart/.update-rejected-digest")" "newpantry" "pantry restart rejects the new digest"
lack "$LAST_OUT" "healthy, running commit" "pantry restart is not reported healthy"
unset STUB_BUMP_SERVICE STUB_BUMP_ON_CALL STUB_BUMP_RESTARTS STUB_HEALTH_BODY STUB_BUILD_BODY PANTRY_UPDATE_HEALTH_TIMEOUT PANTRY_UPDATE_HEALTH_INTERVAL

# A failed Caddy probe rolls the deploy back.
caddy_fail=$(mktemp -d)
prepare_pantry "$caddy_fail"
cat > "$caddy_fail/.env" << 'EOF'
HOST_PORT=8080
PUBLIC_HOST=stockbot.example
PANTRY_IMAGE_TAG=latest
PANTRY_SESSION_SECRET=test-secret
VAPID_PUBLIC_KEY=test-public
VAPID_PRIVATE_KEY=test-private
VAPID_SUBJECT=mailto:pantry@localhost
EOF
cp "$caddy_fail/.env" "$caddy_fail/.env.orig"
write_images "$caddy_fail/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:newcaddy caddy@sha256:newcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_CADDY_RC=22
run_update "$caddy_fail"
[[ "$LAST_RC" -ne 0 ]] || fail "caddy probe failure should exit non-zero: $LAST_OUT"
have "$LAST_CURL" "https://stockbot.example/api/build" "caddy probe failure still requests Caddy"
have "$(cat "$caddy_fail/.update-rejected-digest")" "newcaddy" "caddy probe failure rejects caddy"
lack "$(cat "$caddy_fail/.update-rejected-digest")" "newpantry" "caddy probe failure does not reject a healthy pantry"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_CADDY_RC

# Tunnel mode: reachable internal listener, unreachable listener, and a
# listener that answers and fails.
public_env() {
  local pantry="$1" token="${2:-}"
  cat > "$pantry/.env" << EOF
HOST_PORT=8080
PUBLIC_HOST=stockbot.example
PANTRY_IMAGE_TAG=latest
PANTRY_SESSION_SECRET=test-secret
CLOUDFLARE_TUNNEL_TOKEN=${token}
VAPID_PUBLIC_KEY=test-public
VAPID_PRIVATE_KEY=test-private
VAPID_SUBJECT=mailto:pantry@localhost
EOF
  cp "$pantry/.env" "$pantry/.env.orig"
}

tunnel_env() {
  public_env "$1" test-tunnel-token
}

tunnel_ok=$(mktemp -d)
prepare_pantry "$tunnel_ok"
tunnel_env "$tunnel_ok"
write_images "$tunnel_ok/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_TUNNEL_RC=0
run_update "$tunnel_ok"
[[ "$LAST_RC" -eq 0 ]] || fail "reachable tunnel probe exited $LAST_RC: $LAST_OUT"
have "$LAST_CURL" "--resolve stockbot.example:80:10.77.77.3" "tunnel probe resolves PUBLIC_HOST to caddy-tunnel"
have "$LAST_CURL" "http://stockbot.example/api/build" "tunnel probe requests the site Caddyfile.tunnel serves"
lack "$LAST_CURL" "http://10.77.77.3/api/build" "tunnel probe does not send the pinned address as Host"
have "$LAST_OUT" "reachable before recreate" "tunnel reachability is decided on the old stack"
grep -q '^APP_BASE_URL=https://stockbot.example$' "$tunnel_ok/.env" || fail "tunnel update did not set APP_BASE_URL from PUBLIC_HOST"
grep -v '^APP_BASE_URL=' "$tunnel_ok/.env" > "$tunnel_ok/.env.without"
cmp -s "$tunnel_ok/.env.without" "$tunnel_ok/.env.orig" || fail "tunnel update rewrote .env beyond APP_BASE_URL"

tunnel_skip=$(mktemp -d)
prepare_pantry "$tunnel_skip"
tunnel_env "$tunnel_skip"
write_images "$tunnel_skip/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_TUNNEL_OLD_RC=7
STUB_TUNNEL_RC=7
run_update "$tunnel_skip"
[[ "$LAST_RC" -eq 0 ]] || fail "unreachable tunnel probe exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "not reachable from this host; skipped" "an unreachable tunnel listener is documented and skipped"
have "$LAST_OUT" "pre-deploy probe could not reach it either" "skip requires the old stack to be unreachable too"
lack "$LAST_DOCKER" "--pull never" "an unreachable tunnel listener does not roll back"

tunnel_bad=$(mktemp -d)
prepare_pantry "$tunnel_bad"
tunnel_env "$tunnel_bad"
write_images "$tunnel_bad/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:newtunnel\tcaddy@sha256:newtunnel\t' >> "$tunnel_bad/images.tsv"
STUB_TUNNEL_OLD_RC=0
STUB_TUNNEL_RC=22
run_update "$tunnel_bad"
[[ "$LAST_RC" -ne 0 ]] || fail "failed tunnel probe should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "caddy-tunnel probe" "a reachable tunnel probe failure is reported"
have "$LAST_DOCKER" "--pull never" "a failed tunnel probe rolls back"
have "$(cat "$tunnel_bad/.update-rejected-digest")" "newtunnel" "a changed caddy-tunnel probe failure rejects that digest"
lack "$(cat "$tunnel_bad/.update-rejected-digest")" "newpantry" "a changed caddy-tunnel probe failure does not reject a healthy pantry"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_TUNNEL_RC STUB_TUNNEL_OLD_RC

# Certificate mode, caddy unchanged, probe error. Pantry is new and healthy.
acme_same=$(mktemp -d)
prepare_pantry "$acme_same"
public_env "$acme_same"
write_images "$acme_same/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_CADDY_RC=22
run_update "$acme_same"
[[ "$LAST_RC" -eq 0 ]] || fail "unchanged caddy probe exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "caddy probe failed and its image did not change; not rolling back" "unchanged caddy probe is a warning"
lack "$LAST_DOCKER" "--pull never" "unchanged caddy probe does not roll back"
[[ ! -e "$acme_same/.update-rejected-digest" ]] || fail "unchanged caddy probe recorded a digest: $(cat "$acme_same/.update-rejected-digest")"
[[ "$(grep -c -x caddy "$acme_same/.update-sidecar-warn")" -eq 1 ]] || fail "a running caddy probe failure was not remembered: $(cat "$acme_same/.update-sidecar-warn" 2>/dev/null)"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_CADDY_RC

# Tunnel mode, caddy-tunnel unchanged, and the old listener was already down.
tunnel_same=$(mktemp -d)
prepare_pantry "$tunnel_same"
tunnel_env "$tunnel_same"
write_images "$tunnel_same/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\t' >> "$tunnel_same/images.tsv"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_TUNNEL_OLD_RC=7
STUB_TUNNEL_RC=22
run_update "$tunnel_same"
[[ "$LAST_RC" -eq 0 ]] || fail "already-failing caddy-tunnel probe exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "caddy-tunnel probe failed and its image did not change; not rolling back" "an already-failing unchanged proxy is a warning"
lack "$LAST_DOCKER" "--pull never" "an already-failing unchanged proxy does not roll back"
[[ ! -e "$tunnel_same/.update-rejected-digest" ]] || fail "already-failing caddy-tunnel probe recorded a digest: $(cat "$tunnel_same/.update-rejected-digest")"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_TUNNEL_RC STUB_TUNNEL_OLD_RC

# The old stack answered, and the new listener refuses the connection.
tunnel_refused=$(mktemp -d)
prepare_pantry "$tunnel_refused"
tunnel_env "$tunnel_refused"
write_images "$tunnel_refused/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:newtunnel\tcaddy@sha256:newtunnel\t' >> "$tunnel_refused/images.tsv"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_TUNNEL_OLD_RC=0
STUB_TUNNEL_RC=7
run_update "$tunnel_refused"
[[ "$LAST_RC" -ne 0 ]] || fail "refused tunnel probe should exit non-zero: $LAST_OUT"
have "$LAST_DOCKER" "--pull never" "a refusal after a reachable old stack rolls back"
have "$LAST_OUT" "caddy-tunnel probe of http://stockbot.example/api/build failed (curl 7)" "a refusal reports curl's exit code"
lack "$LAST_OUT" "wanted commit" "a refusal is not reported as a commit mismatch"
lack "$LAST_OUT" "got <none>" "a refusal is not reported as an empty body"
lack "$LAST_OUT" "not reachable from this host; skipped" "a refusal after a reachable old stack is not skipped"
have "$LAST_OUT" "proxy probe failed; retrying in 0s" "a failed proxy probe is tried once more"
tunnel_probes=$(grep -c -F -- '10.77.77.3' <<< "$LAST_CURL" || true)
[[ "$tunnel_probes" -eq 3 ]] || fail "a refused tunnel probe ran ${tunnel_probes} curls, wanted the pre-check and one retry"
have "$(cat "$tunnel_refused/.update-rejected-digest")" "newtunnel" "a refused changed caddy-tunnel is rejected"
lack "$(cat "$tunnel_refused/.update-rejected-digest")" "newpantry" "a refused caddy-tunnel does not reject a healthy pantry"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_TUNNEL_RC STUB_TUNNEL_OLD_RC

# The listener answers, but not with the new commit. An empty 200 used to pass.
tunnel_commit=$(mktemp -d)
prepare_pantry "$tunnel_commit"
tunnel_env "$tunnel_commit"
write_images "$tunnel_commit/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:newtunnel\tcaddy@sha256:newtunnel\t' >> "$tunnel_commit/images.tsv"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_TUNNEL_BODY='{"status":"ok"}'
run_update "$tunnel_commit"
[[ "$LAST_RC" -ne 0 ]] || fail "tunnel commit mismatch should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "wanted commit ${NEW_COMMIT}" "tunnel probe checks the commit"
have "$(cat "$tunnel_commit/.update-rejected-digest")" "newtunnel" "tunnel commit mismatch rejects caddy-tunnel"
lack "$(cat "$tunnel_commit/.update-rejected-digest")" "newpantry" "tunnel commit mismatch does not reject a healthy pantry"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_TUNNEL_BODY

# Deleting a rejected digest also drops its skip warning, so the next skip warns.
warned=$(mktemp -d)
prepare_pantry "$warned"
write_images "$warned/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_RC=1
STUB_BUILD_RC=1
run_update "$warned"
[[ "$LAST_RC" -ne 0 ]] || fail "warning-mark setup should exit non-zero: $LAST_OUT"
unset STUB_HEALTH_RC STUB_BUILD_RC
run_update "$warned"
[[ "$LAST_RC" -eq 0 ]] || fail "warning-mark skip exited $LAST_RC: $LAST_OUT"
have "$(cat "$warned/.update-skip-warned")" "newpantry" "the first skip records the digest"
printf '%s\n' '# Image digests rolled back by pantry-update.sh, one service per line. Not a setup last-known-good sha.' > "$warned/.update-rejected-digest"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$warned"
[[ "$LAST_RC" -eq 0 ]] || fail "cleared rejection should deploy: $LAST_OUT"
lack "$(cat "$warned/.update-skip-warned")" "newpantry" "a digest that is no longer rejected is dropped from the warning file"
unset STUB_HEALTH_BODY STUB_BUILD_BODY

# up fails because an unchanged caddy has exited. The probe was already failing.
caddy_keep=$(mktemp -d)
prepare_pantry "$caddy_keep"
public_env "$caddy_keep"
write_images "$caddy_keep/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy\texited\t0' > "$caddy_keep/status.tsv"
printf '%s\n' $'pantry\tsha256:newpantry' > "$caddy_keep/live.tsv"
STUB_STATUS_TSV="$caddy_keep/status.tsv"
STUB_LIVE_IMAGE_TSV="$caddy_keep/live.tsv"
STUB_UP_RC=1
STUB_CADDY_OLD_RC=22
STUB_CADDY_RC=22
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$caddy_keep"
[[ "$LAST_RC" -eq 0 ]] || fail "unchanged exited caddy exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "<4>pantry update: warning: caddy is exited and its image did not change; not rolling back" "unchanged exited caddy is a warning"
lack "$LAST_DOCKER" "--pull never" "unchanged exited caddy does not roll back"
[[ ! -e "$caddy_keep/.update-rejected-digest" ]] || fail "unchanged exited caddy recorded a digest: $(cat "$caddy_keep/.update-rejected-digest")"
unset STUB_STATUS_TSV STUB_UP_RC STUB_CADDY_OLD_RC STUB_CADDY_RC STUB_HEALTH_BODY STUB_BUILD_BODY STUB_LIVE_IMAGE_TSV

# An unlabeled proxy container keeps the name, so up fails. Pantry is new
# and healthy. The fixture project is "fixture", the same removal path as
# pantry-caddy on the Pi.
name_conflict=$(mktemp -d)
prepare_pantry "$name_conflict"
write_images "$name_conflict/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
printf '%s\n' $'pantry\tsha256:newpantry' > "$name_conflict/live.tsv"
STUB_LIVE_IMAGE_TSV="$name_conflict/live.tsv"
STUB_PROJECT_LABEL=
STUB_COMPOSE_SERVICE=caddy-tunnel
PANTRY_TEST_PROJECT_PREFIX=fixture
STUB_UP_RC=1
run_update "$name_conflict"
[[ "$LAST_RC" -eq 0 ]] || fail "unlabeled name conflict exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "Not removing fixture-caddy; it has no compose project label" "the name conflict names the unlabeled proxy"
have "$LAST_OUT" "every changed service is healthy; not rolling back" "a name conflict does not roll back a healthy pantry"
lack "$LAST_DOCKER" "--pull never" "a name conflict does not re-pin"
[[ ! -e "$name_conflict/.update-rejected-digest" ]] || fail "a name conflict recorded a digest: $(cat "$name_conflict/.update-rejected-digest")"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_PROJECT_LABEL STUB_COMPOSE_SERVICE PANTRY_TEST_PROJECT_PREFIX STUB_UP_RC STUB_LIVE_IMAGE_TSV

# The old proxy answered, and the unchanged proxy fails after a new pantry.
acme_regress=$(mktemp -d)
prepare_pantry "$acme_regress"
public_env "$acme_regress"
write_images "$acme_regress/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_CADDY_OLD_RC=0
STUB_CADDY_RC=22
run_update "$acme_regress"
[[ "$LAST_RC" -ne 0 ]] || fail "regressed caddy probe should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "caddy was reachable before recreate and failed after; rejecting pantry" "a proxy regression is charged to pantry"
have "$LAST_DOCKER" "--pull never" "a proxy regression rolls back"
have "$(cat "$acme_regress/.update-rejected-digest")" "newpantry" "a proxy regression rejects the new pantry"
lack "$(cat "$acme_regress/.update-rejected-digest")" "oldcaddy" "a proxy regression does not reject the unchanged caddy"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_CADDY_OLD_RC STUB_CADDY_RC

tunnel_regress=$(mktemp -d)
prepare_pantry "$tunnel_regress"
tunnel_env "$tunnel_regress"
write_images "$tunnel_regress/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\t' >> "$tunnel_regress/images.tsv"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_TUNNEL_OLD_RC=0
STUB_TUNNEL_OLD_BODY="{\"commit\":\"${OLD_COMMIT}\"}"
STUB_TUNNEL_RC=22
run_update "$tunnel_regress"
[[ "$LAST_RC" -ne 0 ]] || fail "regressed tunnel probe should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "caddy-tunnel was reachable before recreate and failed after; rejecting pantry" "a tunnel regression is charged to pantry"
have "$(cat "$tunnel_regress/.update-rejected-digest")" "newpantry" "a tunnel regression rejects the new pantry"
lack "$(cat "$tunnel_regress/.update-rejected-digest")" "oldtunnel" "a tunnel regression does not reject the unchanged proxy"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_TUNNEL_RC STUB_TUNNEL_OLD_RC STUB_TUNNEL_OLD_BODY

# The new listener hangs after the old one answered.
tunnel_hang=$(mktemp -d)
prepare_pantry "$tunnel_hang"
tunnel_env "$tunnel_hang"
write_images "$tunnel_hang/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:newtunnel\tcaddy@sha256:newtunnel\t' >> "$tunnel_hang/images.tsv"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_TUNNEL_OLD_RC=0
STUB_TUNNEL_RC=28
run_update "$tunnel_hang"
[[ "$LAST_RC" -ne 0 ]] || fail "hung tunnel probe should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "failed (curl 28)" "a hang reports curl's exit code"
lack "$LAST_OUT" "wanted commit" "a hang is not reported as a commit mismatch"
lack "$LAST_OUT" "got <none>" "a hang is not reported as an empty body"
have "$(cat "$tunnel_hang/.update-rejected-digest")" "newtunnel" "a hung changed caddy-tunnel is rejected"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_TUNNEL_RC STUB_TUNNEL_OLD_RC

# The old listener was unreachable. The new one connects and returns the wrong commit.
tunnel_late=$(mktemp -d)
prepare_pantry "$tunnel_late"
tunnel_env "$tunnel_late"
write_images "$tunnel_late/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:newtunnel\tcaddy@sha256:newtunnel\t' >> "$tunnel_late/images.tsv"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_TUNNEL_OLD_RC=7
STUB_TUNNEL_RC=0
STUB_TUNNEL_BODY='{"commit":"wrong"}'
run_update "$tunnel_late"
[[ "$LAST_RC" -ne 0 ]] || fail "late tunnel commit mismatch should exit non-zero: $LAST_OUT"
lack "$LAST_OUT" "not reachable from this host; skipped" "a connected probe is not skipped because the old one was down"
have "$LAST_OUT" "wanted commit ${NEW_COMMIT}" "a connected probe still checks the commit"
have "$(cat "$tunnel_late/.update-rejected-digest")" "newtunnel" "a late commit mismatch rejects caddy-tunnel"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_TUNNEL_RC STUB_TUNNEL_OLD_RC STUB_TUNNEL_BODY

# Curl 56 on the old stack is not "unreachable". A healthy new listener passes.
tunnel_reset=$(mktemp -d)
prepare_pantry "$tunnel_reset"
tunnel_env "$tunnel_reset"
write_images "$tunnel_reset/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:newtunnel\tcaddy@sha256:newtunnel\t' >> "$tunnel_reset/images.tsv"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_TUNNEL_OLD_RC=56
STUB_TUNNEL_RC=0
run_update "$tunnel_reset"
[[ "$LAST_RC" -eq 0 ]] || fail "curl 56 then a healthy tunnel exited $LAST_RC: $LAST_OUT"
lack "$LAST_DOCKER" "--pull never" "curl 56 on the old stack does not roll back a healthy listener"
[[ ! -e "$tunnel_reset/.update-rejected-digest" ]] || fail "curl 56 on the old stack recorded a digest: $(cat "$tunnel_reset/.update-rejected-digest")"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_TUNNEL_RC STUB_TUNNEL_OLD_RC

# Unchanged caddy was running, then exits after up. The old probe passed.
# That is caddy's outage, not pantry's.
caddy_exit=$(mktemp -d)
prepare_pantry "$caddy_exit"
public_env "$caddy_exit"
write_images "$caddy_exit/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'pantry\tsha256:newpantry' > "$caddy_exit/live.tsv"
STUB_LIVE_IMAGE_TSV="$caddy_exit/live.tsv"
STUB_BUMP_SERVICE=caddy
STUB_BUMP_ON_CALL=2
STUB_BUMP_STATUS=exited
STUB_CADDY_OLD_RC=0
STUB_CADDY_RC=7
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$caddy_exit"
[[ "$LAST_RC" -eq 0 ]] || fail "caddy exiting after up exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "caddy is exited and its image did not change; not rolling back" "caddy exiting after up is a warning"
lack "$LAST_OUT" "rejecting pantry" "a down caddy does not reject pantry"
lack "$LAST_DOCKER" "--pull never" "a down caddy does not roll back"
[[ ! -e "$caddy_exit/.update-rejected-digest" ]] || fail "caddy exiting after up recorded a digest: $(cat "$caddy_exit/.update-rejected-digest")"
[[ "$(grep -c -x caddy "$caddy_exit/.update-sidecar-warn")" -eq 1 ]] || fail "caddy was not remembered once: $(cat "$caddy_exit/.update-sidecar-warn")"
unset STUB_LIVE_IMAGE_TSV STUB_BUMP_SERVICE STUB_BUMP_ON_CALL STUB_BUMP_STATUS STUB_CADDY_OLD_RC STUB_CADDY_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# up itself fails while the unchanged caddy is already exited. The old probe
# passed. Keep the new pantry and remember caddy.
caddy_up_down=$(mktemp -d)
prepare_pantry "$caddy_up_down"
public_env "$caddy_up_down"
write_images "$caddy_up_down/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy\texited\t0' > "$caddy_up_down/status.tsv"
printf '%s\n' $'pantry\tsha256:newpantry' > "$caddy_up_down/live.tsv"
STUB_STATUS_TSV="$caddy_up_down/status.tsv"
STUB_LIVE_IMAGE_TSV="$caddy_up_down/live.tsv"
STUB_UP_RC=1
STUB_CADDY_OLD_RC=0
STUB_CADDY_RC=7
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$caddy_up_down"
[[ "$LAST_RC" -eq 0 ]] || fail "up failure with exited caddy exited $LAST_RC: $LAST_OUT"
lack "$LAST_OUT" "rejecting pantry" "an up failure with a down caddy does not reject pantry"
lack "$LAST_DOCKER" "--pull never" "an up failure with a down caddy does not roll back"
have "$(cat "$caddy_up_down/.update-sidecar-warn")" "caddy" "an up failure remembers the down caddy"
unset STUB_STATUS_TSV STUB_LIVE_IMAGE_TSV STUB_UP_RC STUB_CADDY_OLD_RC STUB_CADDY_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# Unchanged caddy-tunnel is restarting after up. Do not charge pantry.
tunnel_restart=$(mktemp -d)
prepare_pantry "$tunnel_restart"
tunnel_env "$tunnel_restart"
write_images "$tunnel_restart/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\t' >> "$tunnel_restart/images.tsv"
STUB_BUMP_SERVICE=caddy-tunnel
STUB_BUMP_ON_CALL=2
STUB_BUMP_STATUS=restarting
STUB_TUNNEL_OLD_RC=0
STUB_TUNNEL_OLD_BODY="{\"commit\":\"${OLD_COMMIT}\"}"
STUB_TUNNEL_RC=22
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$tunnel_restart"
[[ "$LAST_RC" -eq 0 ]] || fail "restarting caddy-tunnel exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "caddy-tunnel is restarting and its image did not change; not rolling back" "a restarting caddy-tunnel is a warning"
lack "$LAST_OUT" "rejecting pantry" "a restarting caddy-tunnel does not reject pantry"
[[ "$(grep -c -x caddy-tunnel "$tunnel_restart/.update-sidecar-warn")" -eq 1 ]] || fail "caddy-tunnel was not remembered once: $(cat "$tunnel_restart/.update-sidecar-warn")"
unset STUB_BUMP_SERVICE STUB_BUMP_ON_CALL STUB_BUMP_STATUS STUB_TUNNEL_OLD_RC STUB_TUNNEL_OLD_BODY STUB_TUNNEL_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# The old tunnel answered 502 with the old commit in the body. That is not
# a pass, so a failing new probe does not reject the new pantry.
tunnel_502=$(mktemp -d)
prepare_pantry "$tunnel_502"
tunnel_env "$tunnel_502"
write_images "$tunnel_502/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\t' >> "$tunnel_502/images.tsv"
STUB_TUNNEL_OLD_RC=0
STUB_TUNNEL_OLD_STATUS=502
STUB_TUNNEL_OLD_BODY="{\"commit\":\"${OLD_COMMIT}\"}"
STUB_TUNNEL_RC=0
STUB_TUNNEL_STATUS=502
STUB_TUNNEL_BODY='{"commit":"nope"}'
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$tunnel_502"
[[ "$LAST_RC" -eq 0 ]] || fail "tunnel 502 before exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "failed (HTTP 502)" "a 502 probe names the status"
lack "$LAST_OUT" "rejecting pantry" "a 502 before does not reject the new pantry"
[[ ! -e "$tunnel_502/.update-rejected-digest" ]] || fail "tunnel 502 before recorded a digest: $(cat "$tunnel_502/.update-rejected-digest")"
unset STUB_TUNNEL_OLD_RC STUB_TUNNEL_OLD_STATUS STUB_TUNNEL_OLD_BODY STUB_TUNNEL_RC STUB_TUNNEL_STATUS STUB_TUNNEL_BODY STUB_HEALTH_BODY STUB_BUILD_BODY

# An empty 200 from the old stack is not a pass either.
tunnel_empty=$(mktemp -d)
prepare_pantry "$tunnel_empty"
tunnel_env "$tunnel_empty"
write_images "$tunnel_empty/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\t' >> "$tunnel_empty/images.tsv"
STUB_TUNNEL_OLD_RC=0
STUB_TUNNEL_OLD_STATUS=200
STUB_TUNNEL_OLD_BODY=
STUB_TUNNEL_RC=0
STUB_TUNNEL_STATUS=200
STUB_TUNNEL_BODY=
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$tunnel_empty"
[[ "$LAST_RC" -eq 0 ]] || fail "empty tunnel 200 exited $LAST_RC: $LAST_OUT"
lack "$LAST_OUT" "rejecting pantry" "an empty 200 before does not reject the new pantry"
[[ ! -e "$tunnel_empty/.update-rejected-digest" ]] || fail "empty tunnel 200 recorded a digest: $(cat "$tunnel_empty/.update-rejected-digest")"
unset STUB_TUNNEL_OLD_RC STUB_TUNNEL_OLD_STATUS STUB_TUNNEL_OLD_BODY STUB_TUNNEL_RC STUB_TUNNEL_STATUS STUB_TUNNEL_BODY STUB_HEALTH_BODY STUB_BUILD_BODY

# A marker for a service that is no longer in compose is dropped.
marker_off=$(mktemp -d)
prepare_pantry "$marker_off"
write_images "$marker_off/images.tsv" \
  sha256:runpantry ghcr.io/rhionin/pantry@sha256:oldpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' grocy-sync > "$marker_off/.update-sidecar-warn"
run_update "$marker_off"
[[ "$LAST_RC" -eq 0 ]] || fail "absent sidecar marker exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "images unchanged" "an absent sidecar is checked on an unchanged pull"
lack "$LAST_OUT" "grocy-sync is unhealthy" "a service that left compose is not warned"
[[ ! -e "$marker_off/.update-sidecar-warn" ]] || fail "a service that left compose kept a marker: $(cat "$marker_off/.update-sidecar-warn")"

# A CR stuck on a marker line still matches the running container.
marker_cr=$(mktemp -d)
prepare_pantry "$marker_cr"
write_images "$marker_cr/images.tsv" \
  sha256:runpantry ghcr.io/rhionin/pantry@sha256:oldpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'cloudflared\tcloudflare/cloudflared:2026.10.3\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\t' >> "$marker_cr/images.tsv"
printf '%s\r\n' cloudflared > "$marker_cr/.update-sidecar-warn"
run_update "$marker_cr"
[[ "$LAST_RC" -eq 0 ]] || fail "crlf marker exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "cloudflared is running again" "a CR-terminated marker matches the running container"
[[ ! -e "$marker_cr/.update-sidecar-warn" ]] || fail "a recovered CR marker stayed: $(cat "$marker_cr/.update-sidecar-warn")"

# A deploy clears a sidecar marker once that container is running.
marker_clear=$(mktemp -d)
prepare_pantry "$marker_clear"
write_images "$marker_clear/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'cloudflared\tcloudflare/cloudflared:2026.10.3\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\t' >> "$marker_clear/images.tsv"
printf '%s\n' cloudflared > "$marker_clear/.update-sidecar-warn"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$marker_clear"
[[ "$LAST_RC" -eq 0 ]] || fail "deploy marker clear exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "cloudflared is running again" "a deploy clears a sidecar that is running"
[[ ! -e "$marker_clear/.update-sidecar-warn" ]] || fail "a running sidecar kept its marker: $(cat "$marker_clear/.update-sidecar-warn")"
unset STUB_HEALTH_BODY STUB_BUILD_BODY

# Parking a rejected digest still rechecks the sidecar marker.
marker_park=$(mktemp -d)
prepare_pantry "$marker_park"
write_images "$marker_park/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'cloudflared\tcloudflare/cloudflared:2026.10.3\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\t' >> "$marker_park/images.tsv"
printf '%s\n' $'pantry\tghcr.io/rhionin/pantry@sha256:newpantry' > "$marker_park/.update-rejected-digest"
printf '%s\n' cloudflared > "$marker_park/.update-sidecar-warn"
run_update "$marker_park"
[[ "$LAST_RC" -eq 0 ]] || fail "park-path recheck exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "cloudflared is running again" "the park path rechecks a sidecar marker"
[[ ! -e "$marker_park/.update-sidecar-warn" ]] || fail "the park path left a sidecar marker: $(cat "$marker_park/.update-sidecar-warn")"

# Pantry's image did not change. A regressed proxy must not reject it.
proxy_grocy=$(mktemp -d)
prepare_pantry "$proxy_grocy"
public_env "$proxy_grocy"
write_images "$proxy_grocy/images.tsv" \
  sha256:runpantry ghcr.io/rhionin/pantry@sha256:oldpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'cloudflared\tcloudflare/cloudflared:2026.10.3\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\tsha256:newcloud\tcloudflare/cloudflared@sha256:newcloud\t' >> "$proxy_grocy/images.tsv"
printf '%s\n' $'cloudflared\tsha256:newcloud' > "$proxy_grocy/live.tsv"
STUB_LIVE_IMAGE_TSV="$proxy_grocy/live.tsv"
STUB_CADDY_OLD_RC=0
STUB_CADDY_RC=22
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${OLD_COMMIT}\"}"
run_update "$proxy_grocy"
[[ "$LAST_RC" -eq 0 ]] || fail "unchanged pantry proxy regression exited $LAST_RC: $LAST_OUT"
lack "$LAST_OUT" "rejecting pantry" "an unchanged pantry is not charged for the proxy"
lack "$LAST_DOCKER" "--pull never" "an unchanged pantry is not rolled back for the proxy"
[[ ! -e "$proxy_grocy/.update-rejected-digest" ]] || fail "an unchanged pantry was rejected: $(cat "$proxy_grocy/.update-rejected-digest")"
unset STUB_LIVE_IMAGE_TSV STUB_CADDY_OLD_RC STUB_CADDY_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# A second pull must not reuse the first pull's "proxy passed" result.
reach_reset=$(mktemp -d)
prepare_pantry "$reach_reset"
tunnel_env "$reach_reset"
write_images "$reach_reset/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\t' >> "$reach_reset/images.tsv"
STUB_REWRITE_TRIGGER=1
STUB_TUNNEL_OLD_RC=0
STUB_TUNNEL_OLD_BODY="{\"commit\":\"${OLD_COMMIT}\"}"
STUB_TUNNEL_RC=0
STUB_TUNNEL_LATER_FROM=3
STUB_TUNNEL_LATER_RC=22
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$reach_reset"
[[ "$LAST_RC" -eq 0 ]] || fail "second-round probe exited $LAST_RC: $LAST_OUT"
lack "$LAST_OUT" "rejecting pantry" "a later probe does not reuse the first round's reachability"
[[ ! -e "$reach_reset/.update-rejected-digest" ]] || fail "a later probe recorded a digest: $(cat "$reach_reset/.update-rejected-digest")"
unset STUB_REWRITE_TRIGGER STUB_TUNNEL_OLD_RC STUB_TUNNEL_OLD_BODY STUB_TUNNEL_RC STUB_TUNNEL_LATER_FROM STUB_TUNNEL_LATER_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# The unchanged proxy is still running, but its container id changed.
# That is a new container, so a failed probe does not reject pantry.
caddy_newid=$(mktemp -d)
prepare_pantry "$caddy_newid"
public_env "$caddy_newid"
write_images "$caddy_newid/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_NEW_CID_SERVICE=caddy
STUB_CADDY_OLD_RC=0
STUB_CADDY_RC=22
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$caddy_newid"
[[ "$LAST_RC" -eq 0 ]] || fail "new caddy id exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "caddy probe failed and its image did not change; not rolling back" "a new caddy id is a warning"
lack "$LAST_OUT" "rejecting pantry" "a new caddy id does not reject pantry"
lack "$LAST_DOCKER" "--pull never" "a new caddy id does not roll back"
[[ ! -e "$caddy_newid/.update-rejected-digest" ]] || fail "a new caddy id recorded a digest: $(cat "$caddy_newid/.update-rejected-digest")"
unset STUB_NEW_CID_SERVICE STUB_CADDY_OLD_RC STUB_CADDY_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# RestartCount went up by 1 and the container is still running. Same id.
# A higher count is not the container that passed the old probe.
caddy_restarts=$(mktemp -d)
prepare_pantry "$caddy_restarts"
public_env "$caddy_restarts"
write_images "$caddy_restarts/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_BUMP_SERVICE=caddy
STUB_BUMP_ON_CALL=2
STUB_BUMP_RESTARTS=1
STUB_CADDY_OLD_RC=0
STUB_CADDY_RC=22
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$caddy_restarts"
[[ "$LAST_RC" -eq 0 ]] || fail "caddy restart count exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "caddy probe failed and its image did not change; not rolling back" "a higher caddy restart count is a warning"
lack "$LAST_OUT" "rejecting pantry" "a higher caddy restart count does not reject pantry"
lack "$LAST_DOCKER" "--pull never" "a higher caddy restart count does not roll back"
[[ ! -e "$caddy_restarts/.update-rejected-digest" ]] || fail "a higher caddy restart count recorded a digest: $(cat "$caddy_restarts/.update-rejected-digest")"
unset STUB_BUMP_SERVICE STUB_BUMP_ON_CALL STUB_BUMP_RESTARTS STUB_CADDY_OLD_RC STUB_CADDY_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# Same id and restart count, but StartedAt moved. Compose before 2.35
# restarts Caddy that way. Do not charge pantry.
caddy_started=$(mktemp -d)
prepare_pantry "$caddy_started"
public_env "$caddy_started"
write_images "$caddy_started/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_SHIFT_STARTED_SERVICE=caddy
STUB_CADDY_OLD_RC=0
STUB_CADDY_RC=22
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$caddy_started"
[[ "$LAST_RC" -eq 0 ]] || fail "caddy StartedAt change exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "caddy probe failed and its image did not change; not rolling back" "a new caddy StartedAt is a warning"
lack "$LAST_OUT" "rejecting pantry" "a new caddy StartedAt does not reject pantry"
lack "$LAST_DOCKER" "--pull never" "a new caddy StartedAt does not roll back"
[[ ! -e "$caddy_started/.update-rejected-digest" ]] || fail "a new caddy StartedAt recorded a digest: $(cat "$caddy_started/.update-rejected-digest")"
unset STUB_SHIFT_STARTED_SERVICE STUB_CADDY_OLD_RC STUB_CADDY_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# The first probe after recreate fails. The retry passes. Keep pantry.
caddy_retry=$(mktemp -d)
prepare_pantry "$caddy_retry"
public_env "$caddy_retry"
write_images "$caddy_retry/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_CADDY_OLD_RC=0
STUB_CADDY_RC=22
STUB_CADDY_RETRY_RC=0
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$caddy_retry"
[[ "$LAST_RC" -eq 0 ]] || fail "caddy probe retry exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "proxy probe failed; retrying in 0s" "a failed proxy probe is retried"
lack "$LAST_OUT" "rejecting pantry" "a proxy probe that passes on retry does not reject pantry"
lack "$LAST_DOCKER" "--pull never" "a proxy probe that passes on retry does not roll back"
[[ ! -e "$caddy_retry/.update-rejected-digest" ]] || fail "a passing proxy retry recorded a digest: $(cat "$caddy_retry/.update-rejected-digest")"
[[ ! -e "$caddy_retry/.update-sidecar-warn" ]] || fail "a passing proxy retry left a marker: $(cat "$caddy_retry/.update-sidecar-warn")"
unset STUB_CADDY_OLD_RC STUB_CADDY_RC STUB_CADDY_RETRY_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# A huge retry delay is capped at 15s, the same ceiling as the settle window.
probe_cap=$(mktemp -d)
prepare_pantry "$probe_cap"
public_env "$probe_cap"
write_images "$probe_cap/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_CADDY_OLD_RC=0
STUB_CADDY_RC=22
STUB_CADDY_RETRY_RC=0
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
PANTRY_UPDATE_PROBE_RETRY_SECONDS=99999
PANTRY_TEST_DEADLINE=30
run_update "$probe_cap"
[[ "$LAST_RC" -eq 0 ]] || fail "capped probe retry exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "proxy probe failed; retrying in 15s" "a huge probe retry delay is capped at 15s"
lack "$LAST_OUT" "retrying in 99999s" "a huge probe retry delay is not slept as given"
lack "$LAST_OUT" "rejecting pantry" "a capped retry that passes does not reject pantry"
unset STUB_CADDY_OLD_RC STUB_CADDY_RC STUB_CADDY_RETRY_RC STUB_HEALTH_BODY STUB_BUILD_BODY PANTRY_UPDATE_PROBE_RETRY_SECONDS PANTRY_TEST_DEADLINE

# A running caddy whose probe fails stays on the marker. The next unchanged
# minute warns again. A later probe that passes removes the line.
proxy_mark=$(mktemp -d)
prepare_pantry "$proxy_mark"
public_env "$proxy_mark"
write_images "$proxy_mark/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_CADDY_RC=22
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$proxy_mark"
[[ "$LAST_RC" -eq 0 ]] || fail "proxy marker write exited $LAST_RC: $LAST_OUT"
lack "$LAST_OUT" "caddy is running again" "a failed caddy probe is not cleared for being running"
[[ "$(grep -c -x caddy "$proxy_mark/.update-sidecar-warn")" -eq 1 ]] || fail "a running caddy probe failure was not remembered: $(cat "$proxy_mark/.update-sidecar-warn" 2>/dev/null)"
cat > "$proxy_mark/images.tsv" << EOF
pantry	ghcr.io/rhionin/pantry:latest	sha256:newpantry	ghcr.io/rhionin/pantry@sha256:newpantry	sha256:newpantry	ghcr.io/rhionin/pantry@sha256:newpantry	${NEW_COMMIT}	${OLD_COMMIT}
caddy	caddy:2.11.7-alpine	sha256:runcaddy	caddy@sha256:oldcaddy	sha256:runcaddy	caddy@sha256:oldcaddy		
EOF
STUB_CADDY_RC=22
run_update "$proxy_mark"
[[ "$LAST_RC" -eq 0 ]] || fail "proxy marker recheck exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "images unchanged" "a proxy marker is rechecked when images do not change"
have "$LAST_OUT" "<4>pantry update: warning: caddy probe failed and its image did not change; not rolling back" "a running caddy with a failed probe warns again"
lack "$LAST_OUT" "caddy is running again" "a running caddy with a failed probe stays on the marker"
[[ "$(grep -c -x caddy "$proxy_mark/.update-sidecar-warn")" -eq 1 ]] || fail "a failed caddy probe dropped its marker: $(cat "$proxy_mark/.update-sidecar-warn" 2>/dev/null)"
STUB_CADDY_RC=0
run_update "$proxy_mark"
[[ "$LAST_RC" -eq 0 ]] || fail "proxy marker clear exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "caddy probe passed" "a passing caddy probe clears its marker"
[[ ! -e "$proxy_mark/.update-sidecar-warn" ]] || fail "a passing caddy probe left a marker: $(cat "$proxy_mark/.update-sidecar-warn")"
unset STUB_CADDY_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# A deploy whose probe passes drops the proxy line itself. The next minute
# is not what clears it. A sidecar on the same list stays.
marker_deploy_clear=$(mktemp -d)
prepare_pantry "$marker_deploy_clear"
public_env "$marker_deploy_clear"
write_images "$marker_deploy_clear/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'cloudflared\tcloudflare/cloudflared:2026.10.3\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\tsha256:runcloud\tcloudflare/cloudflared@sha256:oldcloud\t' >> "$marker_deploy_clear/images.tsv"
printf '%s\n' caddy cloudflared > "$marker_deploy_clear/.update-sidecar-warn"
printf '%s\n' $'cloudflared\texited\t0' > "$marker_deploy_clear/status.tsv"
STUB_STATUS_TSV="$marker_deploy_clear/status.tsv"
STUB_CADDY_RC=0
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$marker_deploy_clear"
[[ "$LAST_RC" -eq 0 ]] || fail "deploy proxy clear exited $LAST_RC: $LAST_OUT"
lack "$LAST_OUT" "images unchanged" "a passing deploy probe clears the proxy without waiting for the next minute"
lack "$LAST_OUT" "caddy is running again" "a passing deploy probe does not clear caddy for being running"
lack "$LAST_OUT" "caddy probe passed" "the deploy clear is not the unchanged-minute probe"
have "$(cat "$marker_deploy_clear/.update-sidecar-warn")" "cloudflared" "a deploy that clears the proxy keeps the down sidecar"
lack "$(cat "$marker_deploy_clear/.update-sidecar-warn")" "caddy" "a passing deploy probe drops the proxy marker"
unset STUB_STATUS_TSV STUB_CADDY_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# Tunnel mode: a marked caddy-tunnel stays while the probe fails, and the
# next unchanged minute clears it once the listener returns the running commit.
tunnel_mark=$(mktemp -d)
prepare_pantry "$tunnel_mark"
tunnel_env "$tunnel_mark"
write_images "$tunnel_mark/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'caddy-tunnel\tcaddy:tunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\tsha256:runtunnel\tcaddy@sha256:oldtunnel\t' >> "$tunnel_mark/images.tsv"
STUB_TUNNEL_OLD_RC=7
STUB_TUNNEL_RC=0
STUB_TUNNEL_STATUS=502
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$tunnel_mark"
[[ "$LAST_RC" -eq 0 ]] || fail "tunnel marker write exited $LAST_RC: $LAST_OUT"
lack "$LAST_OUT" "rejecting pantry" "an already-failing tunnel does not reject pantry"
[[ "$(grep -c -x caddy-tunnel "$tunnel_mark/.update-sidecar-warn")" -eq 1 ]] || fail "a tunnel probe failure was not remembered: $(cat "$tunnel_mark/.update-sidecar-warn" 2>/dev/null)"
cat > "$tunnel_mark/images.tsv" << EOF
pantry	ghcr.io/rhionin/pantry:latest	sha256:newpantry	ghcr.io/rhionin/pantry@sha256:newpantry	sha256:newpantry	ghcr.io/rhionin/pantry@sha256:newpantry	${NEW_COMMIT}	
caddy	caddy:2.11.7-alpine	sha256:runcaddy	caddy@sha256:oldcaddy	sha256:runcaddy	caddy@sha256:oldcaddy		
caddy-tunnel	caddy:tunnel	sha256:runtunnel	caddy@sha256:oldtunnel	sha256:runtunnel	caddy@sha256:oldtunnel		
EOF
STUB_TUNNEL_OLD_RC=0
STUB_TUNNEL_OLD_STATUS=502
STUB_TUNNEL_RC=0
STUB_TUNNEL_STATUS=502
run_update "$tunnel_mark"
[[ "$LAST_RC" -eq 0 ]] || fail "tunnel marker recheck exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "images unchanged" "a tunnel marker is rechecked when images do not change"
have "$LAST_OUT" "<4>pantry update: warning: caddy-tunnel probe failed and its image did not change; not rolling back" "a running tunnel with a failed probe warns again"
lack "$LAST_OUT" "caddy-tunnel probe passed" "a failed tunnel probe does not clear its marker"
[[ "$(grep -c -x caddy-tunnel "$tunnel_mark/.update-sidecar-warn")" -eq 1 ]] || fail "a failed tunnel probe dropped its marker: $(cat "$tunnel_mark/.update-sidecar-warn" 2>/dev/null)"
unset STUB_TUNNEL_OLD_RC STUB_TUNNEL_OLD_STATUS
STUB_TUNNEL_RC=0
STUB_TUNNEL_STATUS=200
run_update "$tunnel_mark"
[[ "$LAST_RC" -eq 0 ]] || fail "tunnel marker clear exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "caddy-tunnel probe passed" "a healthy tunnel clears its marker"
lack "$LAST_OUT" "wanted commit <missing>" "a healthy tunnel is checked against the recorded pantry revision"
[[ ! -e "$tunnel_mark/.update-sidecar-warn" ]] || fail "a healthy tunnel left a marker: $(cat "$tunnel_mark/.update-sidecar-warn")"
unset STUB_TUNNEL_RC STUB_TUNNEL_STATUS STUB_HEALTH_BODY STUB_BUILD_BODY

# PUBLIC_HOST without auth.caddy: sidecar marker recheck still probes Caddy.
marker_no_auth=$(mktemp -d)
prepare_pantry "$marker_no_auth"
public_env "$marker_no_auth"
[[ ! -e "$marker_no_auth/auth.caddy" ]] || fail "fixture must not ship auth.caddy"
cat > "$marker_no_auth/images.tsv" << EOF
pantry	ghcr.io/rhionin/pantry:latest	sha256:runpantry	ghcr.io/rhionin/pantry@sha256:oldpantry	sha256:runpantry	ghcr.io/rhionin/pantry@sha256:oldpantry	${NEW_COMMIT}	${OLD_COMMIT}
caddy	caddy:2.11.7-alpine	sha256:runcaddy	caddy@sha256:oldcaddy	sha256:runcaddy	caddy@sha256:oldcaddy		
EOF
printf '%s\n' caddy > "$marker_no_auth/.update-sidecar-warn"
STUB_CADDY_RC=22
run_update "$marker_no_auth"
[[ "$LAST_RC" -eq 0 ]] || fail "marker without auth.caddy exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "images unchanged" "a marker is rechecked without auth.caddy"
have "$LAST_CURL" "https://stockbot.example/api/build" "PUBLIC_HOST enables the caddy probe without auth.caddy"
have "$LAST_OUT" "<4>pantry update: warning: caddy probe failed and its image did not change; not rolling back" "failed probe keeps warning without auth.caddy"
[[ "$(grep -c -x caddy "$marker_no_auth/.update-sidecar-warn")" -eq 1 ]] || fail "failed probe dropped the marker: $(cat "$marker_no_auth/.update-sidecar-warn" 2>/dev/null)"
unset STUB_CADDY_RC

# A trigger that lands during the pull is pulled again instead of dropped.
repull=$(mktemp -d)
prepare_pantry "$repull"
write_images "$repull/images.tsv" \
  sha256:runpantry ghcr.io/rhionin/pantry@sha256:oldpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_REWRITE_TRIGGER=1
run_update "$repull"
[[ "$LAST_RC" -eq 0 ]] || fail "trigger re-pull exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "trigger changed during the pull; pulling again" "a trigger during the pull starts another pull"
pulls=$(printf '%s\n' "$LAST_DOCKER" | grep -c ' pull' || true)
[[ "$pulls" -ge 2 ]] || fail "trigger re-pull ran ${pulls} pulls"
lack "$LAST_OUT" "stopping after 3 pulls" "a single extra trigger does not hit the cap"
unset STUB_REWRITE_TRIGGER

# A container whose compose project label is someone else's is not removed.
wrong_project=$(mktemp -d)
prepare_pantry "$wrong_project"
write_images "$wrong_project/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:newcaddy caddy@sha256:newcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_PROJECT_LABEL=other
PANTRY_TEST_PROJECT_PREFIX=fixture
run_update "$wrong_project"
[[ "$LAST_RC" -eq 0 ]] || fail "foreign project label exited $LAST_RC: $LAST_OUT"
lack "$LAST_DOCKER" "rm -f fixture-cloudflared" "rm skips a container from another compose project"
have "$LAST_OUT" "Not removing fixture-cloudflared" "rm names the container it refused"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_PROJECT_LABEL PANTRY_TEST_PROJECT_PREFIX

# A container with the proxy's name and no compose project label is named, not treated as absent.
blank_project=$(mktemp -d)
prepare_pantry "$blank_project"
write_images "$blank_project/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:newcaddy caddy@sha256:newcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_PROJECT_LABEL=
PANTRY_TEST_PROJECT_PREFIX=fixture
run_update "$blank_project"
[[ "$LAST_RC" -eq 0 ]] || fail "unlabeled proxy container exited $LAST_RC: $LAST_OUT"
lack "$LAST_DOCKER" "rm -f fixture-cloudflared" "rm skips an unlabeled container"
have "$LAST_OUT" "Not removing fixture-cloudflared; it has no compose project label" "an unlabeled proxy container is named"
unset STUB_HEALTH_BODY STUB_BUILD_BODY STUB_PROJECT_LABEL PANTRY_TEST_PROJECT_PREFIX

# Overlapping runs take the lock instead of pulling twice.
locked=$(mktemp -d)
prepare_pantry "$locked"
write_images "$locked/images.tsv" \
  sha256:runpantry ghcr.io/rhionin/pantry@sha256:oldpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
mkdir -p "$locked/deploy-trigger"
flock "$locked/deploy-trigger/.lock" sleep 20 &
lock_holder=$!
slept=0
while [[ "$slept" -lt 20 ]]; do
  if ! flock -n "$locked/deploy-trigger/.lock" true; then
    break
  fi
  sleep 0.1
  slept=$((slept + 1))
done
lock_work=$(mktemp -d)
make_bin "$lock_work/bin"
: > "$lock_work/docker.log"
: > "$lock_work/curl.log"
set +e
timeout 3 env \
  DOCKER_LOG="$lock_work/docker.log" \
  CURL_LOG="$lock_work/curl.log" \
  STUB_IMAGES="$locked/images.tsv" \
  STUB_TAG_STATE="$locked/tag-state.tsv" \
  PANTRY_DIR="$locked" \
  PANTRY_SETUP_SKIP_ROOT=1 \
  PANTRY_UPDATE_HEALTH_TIMEOUT=0 \
  PATH="$lock_work/bin:$PATH" \
  bash "$UPDATE" >"$lock_work/out" 2>"$lock_work/err"
lock_rc=$?
set -e
kill "$lock_holder" 2>/dev/null || true
wait "$lock_holder" 2>/dev/null || true
lock_out=$(cat "$lock_work/out" "$lock_work/err")
[[ "$lock_rc" -eq 124 ]] || fail "held lock should block the update (rc $lock_rc): $lock_out"
have "$lock_out" "another update is running" "a held lock is waited on"
lack "$lock_out" "pantry update: lock acquired" "the waiter does not pass the lock while it is held"
rm -rf "$lock_work"

# Grocy credentials must not change what the updater asks docker to do.
# A deploy with GROCY_* + PANTRY_SYNC_TOKEN set (and docker-compose.grocy.yml
# present, as setup.sh leaves it) must issue the same docker argv as one
# without them: no pull/up of grocy-sync, no orphan removal, no volume removal.
for grocy_mode in lan tunnel; do
  grocy_off=$(mktemp -d); grocy_on=$(mktemp -d)
  for grocy_dir in "$grocy_off" "$grocy_on"; do
    prepare_pantry "$grocy_dir"
    [[ "$grocy_mode" == tunnel ]] && public_env "$grocy_dir" test-tunnel-token
    cp "$ROOT/deploy/docker-compose.yml" "$ROOT/deploy/docker-compose.tunnel.yml" "$ROOT/deploy/docker-compose.grocy.yml" "$grocy_dir/"
    write_images "$grocy_dir/images.tsv" sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry sha256:runcaddy caddy@sha256:oldcaddy
  done
  printf '%s\n' GROCY_URL=http://grocy:80 GROCY_API_KEY=invented-key PANTRY_SYNC_TOKEN=invented-token >> "$grocy_on/.env"
  cp "$grocy_on/.env" "$grocy_on/.env.orig"
  STUB_HEALTH_BODY='{"status":"ok"}' STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}" run_update "$grocy_off"
  [[ "$LAST_RC" -eq 0 ]] || fail "$grocy_mode update without Grocy failed: $LAST_OUT"
  grocy_off_docker=$(normalize_update_docker "$LAST_DOCKER" "$grocy_off")
  STUB_HEALTH_BODY='{"status":"ok"}' STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}" run_update "$grocy_on"
  [[ "$LAST_RC" -eq 0 ]] || fail "$grocy_mode update with Grocy failed: $LAST_OUT"
  grocy_on_docker=$(normalize_update_docker "$LAST_DOCKER" "$grocy_on")
  have "$grocy_on_docker" " up -d" "$grocy_mode update with Grocy still recreates the stack"
  lack "$grocy_on_docker" "docker-compose.grocy.yml" "$grocy_mode update with Grocy must not load the sidecar file"
  lack "$grocy_on_docker" "--profile grocy" "$grocy_mode update with Grocy must not select a grocy profile"
  lack "$grocy_on_docker" "--remove-orphans" "$grocy_mode update must not remove orphans (pantry-grocy-sync)"
  if grep -qE '(^| )(down|volume rm|volume prune)( |$)' <<<"$grocy_on_docker"; then
    fail "$grocy_mode update with Grocy must not take the stack down or remove volumes"
  fi
  [[ "$grocy_on_docker" == "$grocy_off_docker" ]] \
    || fail "$grocy_mode update docker argv changed with Grocy credentials:"$'\n'"$(diff <(printf '%s\n' "$grocy_off_docker") <(printf '%s\n' "$grocy_on_docker"))"
  rm -rf "$grocy_off" "$grocy_on"
done
unset STUB_HEALTH_BODY STUB_BUILD_BODY

# A failed pre-deploy backup warns and the new image is still recreated.
backup_warn=$(mktemp -d)
prepare_pantry "$backup_warn"
write_images "$backup_warn/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_BACKUP_RC=1
BACKUP_BEFORE_DEPLOY=warn
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$backup_warn"
[[ "$LAST_RC" -eq 0 ]] || fail "warn backup failure exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "<4>pantry update: warning: pre-deploy backup failed; continuing because BACKUP_BEFORE_DEPLOY=warn" "a warn backup failure is a warning"
have "$LAST_OUT" "pantry update: healthy, running commit ${NEW_COMMIT}" "a warn backup failure still deploys"
have "$LAST_DOCKER" " up -d" "a warn backup failure still recreates"
warn_backup=$(line_of "$LAST_SEQUENCE" "--reason pre-deploy")
warn_up=$(line_of "$LAST_SEQUENCE" " up -d")
[[ -n "$warn_backup" && -n "$warn_up" && "$warn_backup" -lt "$warn_up" ]] || fail "warn backup did not run before recreate"
lack "$LAST_OUT" "not recreating pantry" "warn does not abort before recreate"
[[ ! -e "$backup_warn/.update-rejected-digest" ]] || fail "warn backup failure rejected a digest"
unset STUB_BACKUP_RC BACKUP_BEFORE_DEPLOY STUB_HEALTH_BODY STUB_BUILD_BODY

# required (default) refuses to recreate pantry when the backup fails.
backup_required=$(mktemp -d)
prepare_pantry "$backup_required"
write_images "$backup_required/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_BACKUP_RC=1
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$backup_required"
[[ "$LAST_RC" -ne 0 ]] || fail "required backup failure should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "pantry update: pre-deploy backup (required)" "required mode runs the backup"
have "$LAST_OUT" "PANTRY UPDATE BLOCKED: pre-deploy backup failed" "required mode stops before recreate"
have "$LAST_OUT" "backup: deploy lock already held" "required mode still shares the deploy lock"
lack "$LAST_DOCKER" " up -d" "required mode does not recreate pantry"
lack "$LAST_DOCKER" "pending-before-up" "required mode does not write the pending marker before up"
lack "$LAST_DOCKER" "--pull never" "required mode does not roll back"
lack "$LAST_OUT" "PANTRY UPDATE FAILED: rolling back" "required mode does not roll back an image that was not started"
[[ ! -e "$backup_required/.update-pending" ]] || fail "required mode left a pending marker"
[[ ! -e "$backup_required/.update-rejected-digest" ]] || fail "required mode recorded a rejected digest"
status_body=$(cat "$backup_required/last-deploy-status")
have "$status_body" "result=blocked" "required mode records blocked status"
have "$status_body" "reason=pre_deploy_backup_failed" "required mode records backup reason"
have "$status_body" "image_revision=${NEW_COMMIT}" "required mode records the target revision"
have "$LAST_DOCKER" "tag ghcr.io/rhionin/pantry@sha256:oldpantry ghcr.io/rhionin/pantry:latest" "blocked required backup retags pantry to the previous digest"
unset STUB_BACKUP_RC STUB_HEALTH_BODY STUB_BUILD_BODY

# required blocks when the backup script is missing.
backup_missing=$(mktemp -d)
prepare_pantry "$backup_missing"
write_images "$backup_missing/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
rm -f "$backup_missing/systemd/pantry-backup.sh"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$backup_missing"
[[ "$LAST_RC" -ne 0 ]] || fail "missing backup script should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "pre-deploy backup script is missing or not executable" "missing backup script is reported"
have "$LAST_OUT" "PANTRY UPDATE BLOCKED" "missing backup script blocks deploy"
lack "$LAST_DOCKER" " up -d" "missing backup script does not recreate"
unset STUB_HEALTH_BODY STUB_BUILD_BODY

# required blocks when the backup script is not executable.
backup_noexec=$(mktemp -d)
prepare_pantry "$backup_noexec"
write_images "$backup_noexec/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
chmod -x "$backup_noexec/systemd/pantry-backup.sh"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$backup_noexec"
[[ "$LAST_RC" -ne 0 ]] || fail "non-executable backup script should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "pre-deploy backup script is missing or not executable" "non-executable backup script is reported"
lack "$LAST_DOCKER" " up -d" "non-executable backup script does not recreate"
unset STUB_HEALTH_BODY STUB_BUILD_BODY

# optional is an alias for warn.
backup_optional=$(mktemp -d)
prepare_pantry "$backup_optional"
write_images "$backup_optional/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_BACKUP_RC=1
BACKUP_BEFORE_DEPLOY=optional
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$backup_optional"
[[ "$LAST_RC" -eq 0 ]] || fail "optional backup failure exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "pre-deploy backup (warn)" "optional normalizes to warn"
have "$LAST_OUT" "continuing because BACKUP_BEFORE_DEPLOY=warn" "optional continues with a warning"
unset STUB_BACKUP_RC BACKUP_BEFORE_DEPLOY STUB_HEALTH_BODY STUB_BUILD_BODY

# off skips the backup even when the pantry digest changes.
backup_off=$(mktemp -d)
prepare_pantry "$backup_off"
write_images "$backup_off/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
BACKUP_BEFORE_DEPLOY=off
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$backup_off"
[[ "$LAST_RC" -eq 0 ]] || fail "off backup mode exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "pantry update: healthy, running commit ${NEW_COMMIT}" "off mode still deploys"
lack "$LAST_OUT" "pre-deploy backup" "off mode does not take a backup"
lack "$LAST_DOCKER" "--reason pre-deploy" "off mode does not start the backup container"
have "$LAST_DOCKER" " up -d" "off mode still recreates"
unset BACKUP_BEFORE_DEPLOY STUB_HEALTH_BODY STUB_BUILD_BODY

# .env is the fallback when the process environment does not set the mode.
backup_dotenv=$(mktemp -d)
prepare_pantry "$backup_dotenv"
printf '\nBACKUP_BEFORE_DEPLOY=off\n' >> "$backup_dotenv/.env"
write_images "$backup_dotenv/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
unset BACKUP_BEFORE_DEPLOY
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$backup_dotenv"
[[ "$LAST_RC" -eq 0 ]] || fail "off in .env exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "pantry update: healthy, running commit ${NEW_COMMIT}" "off in .env still deploys"
lack "$LAST_OUT" "pre-deploy backup" "off in .env does not take a backup"
lack "$LAST_DOCKER" "--reason pre-deploy" "off in .env does not start the backup container"
have "$LAST_DOCKER" " up -d" "off in .env still recreates"
unset STUB_HEALTH_BODY STUB_BUILD_BODY

# An unknown mode stops before recreate. The default must not be substituted.
backup_invalid=$(mktemp -d)
prepare_pantry "$backup_invalid"
write_images "$backup_invalid/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
BACKUP_BEFORE_DEPLOY=invalid
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$backup_invalid"
[[ "$LAST_RC" -ne 0 ]] || fail "invalid BACKUP_BEFORE_DEPLOY should exit non-zero: $LAST_OUT"
have "$LAST_OUT" "BACKUP_BEFORE_DEPLOY must be required, warn, optional, or off" "invalid mode names the allowed values"
lack "$LAST_DOCKER" " up -d" "invalid mode does not recreate pantry"
lack "$LAST_DOCKER" "--reason pre-deploy" "invalid mode does not start the backup container"
lack "$LAST_OUT" "PANTRY UPDATE BLOCKED" "invalid mode fails before the backup runs"
[[ ! -e "$backup_invalid/.update-pending" ]] || fail "invalid mode left a pending marker"
unset BACKUP_BEFORE_DEPLOY STUB_HEALTH_BODY STUB_BUILD_BODY

stage_host_bundle() {
  local dest="$1"
  mkdir -p "$dest/systemd"
  cp "$ROOT/deploy/publish-mode.sh" "$ROOT/deploy/runtime-setup.sh" "$ROOT/deploy/vapid-env.sh" "$dest/"
  cp "$ROOT/deploy/systemd/pantry-update.sh" \
    "$ROOT/deploy/systemd/pantry-backup.sh" \
    "$ROOT/deploy/systemd/pantry-update.service" \
    "$ROOT/deploy/systemd/pantry-update.timer" \
    "$ROOT/deploy/systemd/pantry-update.path" \
    "$ROOT/deploy/systemd/pantry-backup.service" \
    "$ROOT/deploy/systemd/pantry-backup.timer" \
    "$dest/systemd/"
}

# A successful deploy refreshes host scripts from the image bundle.
host_refresh=$(mktemp -d)
prepare_pantry "$host_refresh"
stage_host_bundle "$host_refresh/host-bundle-stub"
printf '\n# host-bundle-marker\n' >> "$host_refresh/host-bundle-stub/publish-mode.sh"
STUB_HOST_BUNDLE="$host_refresh/host-bundle-stub"
write_images "$host_refresh/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$host_refresh"
[[ "$LAST_RC" -eq 0 ]] || fail "host refresh update exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "refreshed host deploy scripts from the running image" "successful deploy refreshes host scripts"
have "$LAST_DOCKER" "cp cid-pantry:/usr/share/stockbot/host-bundle" "host refresh copies the bundle out of the container"
grep -q 'host-bundle-marker' "$host_refresh/publish-mode.sh" || fail "host refresh did not install the bundled publish-mode.sh"
find "$host_refresh" -name 'publish-mode.sh.refresh.*' -print -quit | grep -q . && fail "host refresh left a partial publish-mode temp file"
status_body=$(cat "$host_refresh/last-deploy-status")
have "$status_body" "result=success" "host refresh records success result"
have "$status_body" "reason=deploy_ok" "host refresh records deploy_ok after refresh"
[[ -d "$host_refresh/host-scripts-last-known-good" ]] || fail "host refresh did not snapshot last-known-good host scripts"
[[ -d "$host_refresh/host-scripts-last-known-good/systemd-units" ]] || fail "host refresh did not snapshot systemd unit copies separately"
assert_github_compare_curl_max_time "$LAST_CURL" "GitHub compare curl uses --max-time"
unset STUB_HOST_BUNDLE STUB_HEALTH_BODY STUB_BUILD_BODY

run_provenance_compare_case() {
  local label="$1" gh_status="$2" expect_refresh="$3" no_jq="$4"
  local dir marker
  dir=$(mktemp -d)
  prepare_pantry "$dir"
  stage_host_bundle "$dir/host-bundle-stub"
  marker="# prov-${label}-${gh_status}-marker"
  printf '\n%s\n' "$marker" >> "$dir/host-bundle-stub/publish-mode.sh"
  STUB_HOST_BUNDLE="$dir/host-bundle-stub"
  write_images "$dir/images.tsv" \
    sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
    sha256:runcaddy caddy@sha256:oldcaddy
  STUB_HEALTH_BODY='{"status":"ok"}'
  STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
  PANTRY_TEST_GITHUB_COMPARE_PRETTY=1
  if [[ "$no_jq" == 1 ]]; then
    PANTRY_TEST_PATH_WITHOUT_JQ=1
  else
    unset PANTRY_TEST_PATH_WITHOUT_JQ
  fi
  if [[ "$gh_status" == ahead || "$gh_status" == identical ]]; then
    unset STUB_GITHUB_COMPARE_STATUS
  else
    STUB_GITHUB_COMPARE_STATUS=$gh_status
  fi
  run_update "$dir"
  [[ "$LAST_RC" -eq 0 ]] || fail "provenance $label/$gh_status exited $LAST_RC: $LAST_OUT"
  if [[ "$expect_refresh" == 1 ]]; then
    grep -q "$marker" "$dir/publish-mode.sh" \
      || fail "provenance $label/$gh_status should refresh (no-jq=${no_jq})"
    assert_github_compare_curl_max_time "$LAST_CURL" "provenance $label/$gh_status compare uses --max-time"
  else
    have "$LAST_OUT" "host script refresh skipped: image revision is not on origin/master" \
      "provenance $label/$gh_status skips refresh"
    grep -q "$marker" "$dir/publish-mode.sh" \
      && fail "provenance $label/$gh_status should not install bundle ($label)"
    status_body=$(cat "$dir/last-deploy-status")
    have "$status_body" "reason=host_refresh_failed" "provenance $label/$gh_status records host_refresh_failed"
  fi
  rm -rf "$dir"
  unset STUB_HOST_BUNDLE STUB_GITHUB_COMPARE_STATUS STUB_HEALTH_BODY STUB_BUILD_BODY \
    PANTRY_TEST_GITHUB_COMPARE_PRETTY PANTRY_TEST_PATH_WITHOUT_JQ
}

for jq_mode in jq no_jq; do
  no_jq_flag=0
  [[ "$jq_mode" == no_jq ]] && no_jq_flag=1
  run_provenance_compare_case "$jq_mode" ahead 1 "$no_jq_flag"
  run_provenance_compare_case "$jq_mode" identical 1 "$no_jq_flag"
  run_provenance_compare_case "$jq_mode" behind 0 "$no_jq_flag"
  run_provenance_compare_case "$jq_mode" diverged 0 "$no_jq_flag"
  run_provenance_compare_case "$jq_mode" malformed 0 "$no_jq_flag"
  run_provenance_compare_case "$jq_mode" empty 0 "$no_jq_flag"
done

# Realistic GitHub compare bodies list file statuses; top-level ahead still refreshes.
host_prov_realistic=$(mktemp -d)
prepare_pantry "$host_prov_realistic"
stage_host_bundle "$host_prov_realistic/host-bundle-stub"
printf '\n# realistic-compare-marker\n' >> "$host_prov_realistic/host-bundle-stub/publish-mode.sh"
STUB_HOST_BUNDLE="$host_prov_realistic/host-bundle-stub"
write_images "$host_prov_realistic/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
PANTRY_TEST_GITHUB_COMPARE_PRETTY=1
PANTRY_TEST_PATH_WITHOUT_JQ=1
unset STUB_GITHUB_COMPARE_STATUS
run_update "$host_prov_realistic"
[[ "$LAST_RC" -eq 0 ]] || fail "realistic ahead compare (no jq) exited $LAST_RC: $LAST_OUT"
grep -q 'realistic-compare-marker' "$host_prov_realistic/publish-mode.sh" \
  || fail "realistic pretty ahead compare without jq did not refresh host scripts"
unset STUB_HOST_BUNDLE STUB_HEALTH_BODY STUB_BUILD_BODY PANTRY_TEST_GITHUB_COMPARE_PRETTY PANTRY_TEST_PATH_WITHOUT_JQ

# diverged compare rejects refresh.
host_prov_diverged=$(mktemp -d)
prepare_pantry "$host_prov_diverged"
stage_host_bundle "$host_prov_diverged/host-bundle-stub"
printf '\n# diverged-marker\n' >> "$host_prov_diverged/host-bundle-stub/publish-mode.sh"
STUB_HOST_BUNDLE="$host_prov_diverged/host-bundle-stub"
write_images "$host_prov_diverged/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_GITHUB_COMPARE_STATUS=diverged
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$host_prov_diverged"
[[ "$LAST_RC" -eq 0 ]] || fail "diverged compare should not fail deploy: $LAST_OUT"
have "$LAST_OUT" "host script refresh skipped: image revision is not on origin/master" "diverged compare skips refresh"
grep -q 'diverged-marker' "$host_prov_diverged/publish-mode.sh" && fail "diverged compare installed host bundle files"
unset STUB_HOST_BUNDLE STUB_GITHUB_COMPARE_STATUS STUB_HEALTH_BODY STUB_BUILD_BODY

# GitHub compare failure skips host refresh but the deploy still succeeds.
host_prov_fail=$(mktemp -d)
prepare_pantry "$host_prov_fail"
stage_host_bundle "$host_prov_fail/host-bundle-stub"
printf '\n# host-bundle-marker\n' >> "$host_prov_fail/host-bundle-stub/publish-mode.sh"
STUB_HOST_BUNDLE="$host_prov_fail/host-bundle-stub"
write_images "$host_prov_fail/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_GITHUB_COMPARE_STATUS=behind
run_update "$host_prov_fail"
[[ "$LAST_RC" -eq 0 ]] || fail "provenance failure should not fail deploy: $LAST_OUT"
have "$LAST_OUT" "host script refresh skipped: image revision is not on origin/master" "compare failure skips refresh"
grep -q 'host-bundle-marker' "$host_prov_fail/publish-mode.sh" && fail "compare failure still installed bundled host files"
status_body=$(cat "$host_prov_fail/last-deploy-status")
have "$status_body" "reason=host_refresh_failed" "compare failure records host_refresh_failed"
unset STUB_HOST_BUNDLE STUB_GITHUB_COMPARE_STATUS STUB_HEALTH_BODY STUB_BUILD_BODY

# Running pantry digest mismatch skips refresh.
host_prov_digest=$(mktemp -d)
prepare_pantry "$host_prov_digest"
stage_host_bundle "$host_prov_digest/host-bundle-stub"
STUB_HOST_BUNDLE="$host_prov_digest/host-bundle-stub"
write_images "$host_prov_digest/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
printf '%s\n' $'pantry\tsha256:runpantry' > "$host_prov_digest/live.tsv"
STUB_LIVE_IMAGE_TSV="$host_prov_digest/live.tsv"
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$host_prov_digest"
[[ "$LAST_RC" -eq 0 ]] || fail "digest mismatch should not fail deploy: $LAST_OUT"
have "$LAST_OUT" "host script refresh skipped: running pantry digest does not match the deployed image" "digest mismatch skips refresh"
status_body=$(cat "$host_prov_digest/last-deploy-status")
have "$status_body" "reason=host_refresh_failed" "digest mismatch records host_refresh_failed"
unset STUB_HOST_BUNDLE STUB_LIVE_IMAGE_TSV STUB_HEALTH_BODY STUB_BUILD_BODY

# Empty running and deployed digests fail the provenance gate closed (G4).
host_prov_empty_dig=$(mktemp -d)
prepare_pantry "$host_prov_empty_dig"
stage_host_bundle "$host_prov_empty_dig/host-bundle-stub"
STUB_HOST_BUNDLE="$host_prov_empty_dig/host-bundle-stub"
write_images "$host_prov_empty_dig/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_EMPTY_REPO_DIGEST_FOR=ghcr.io/rhionin/pantry,sha256:newpantry,sha256:runpantry
STUB_EMPTY_IMAGE_ID_FOR=ghcr.io/rhionin/pantry:latest,sha256:runpantry,sha256:newpantry
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$host_prov_empty_dig"
[[ "$LAST_RC" -eq 0 ]] || fail "empty digests should not fail deploy: $LAST_OUT"
have "$LAST_OUT" "host script refresh skipped: running pantry digest does not match the deployed image" "empty digests skip refresh"
status_body=$(cat "$host_prov_empty_dig/last-deploy-status")
assert_deploy_status_success "$status_body" "empty digests"
have "$status_body" "reason=host_refresh_failed" "empty digests record host_refresh_failed"
unset STUB_HOST_BUNDLE STUB_EMPTY_REPO_DIGEST_FOR STUB_EMPTY_IMAGE_ID_FOR STUB_HEALTH_BODY STUB_BUILD_BODY

# A mid-install mv failure restores last-known-good host scripts (N6).
host_lkg_mv=$(mktemp -d)
prepare_pantry "$host_lkg_mv"
stage_host_bundle "$host_lkg_mv/host-bundle-stub"
old_line='# lkg-keep-this-publish-line'
new_line='# lkg-new-publish-line'
printf '\n%s\n' "$old_line" >> "$host_lkg_mv/publish-mode.sh"
printf '\n%s\n' "$new_line" >> "$host_lkg_mv/host-bundle-stub/publish-mode.sh"
printf '\n# lkg-new-runtime-line\n' >> "$host_lkg_mv/host-bundle-stub/runtime-setup.sh"
STUB_HOST_BUNDLE="$host_lkg_mv/host-bundle-stub"
write_images "$host_lkg_mv/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
PANTRY_TEST_MV_FAIL_DEST=runtime-setup.sh
run_update "$host_lkg_mv"
[[ "$LAST_RC" -eq 0 ]] || fail "mv failure should not fail deploy: $LAST_OUT"
have "$LAST_OUT" "host script refresh failed" "mv failure fails refresh"
grep -q "$old_line" "$host_lkg_mv/publish-mode.sh" || fail "mv failure did not restore publish-mode.sh from last-known-good"
grep -q 'lkg-new-runtime-line' "$host_lkg_mv/runtime-setup.sh" && fail "mv failure left a partially installed runtime-setup.sh"
find "$host_lkg_mv" -name '*.refresh.*' -print -quit | grep -q . && fail "mv failure left refresh temp files under PANTRY_DIR"
status_body=$(cat "$host_lkg_mv/last-deploy-status")
have "$status_body" "reason=host_refresh_failed" "mv failure records host_refresh_failed"
unset STUB_HOST_BUNDLE PANTRY_TEST_MV_FAIL_DEST STUB_HEALTH_BODY STUB_BUILD_BODY

# Snapshot cp failure aborts refresh with no partial host state (S1).
host_lkg_snapshot=$(mktemp -d)
prepare_pantry "$host_lkg_snapshot"
stage_host_bundle "$host_lkg_snapshot/host-bundle-stub"
keep_line='# snapshot-keep-publish-line'
printf '\n%s\n' "$keep_line" >> "$host_lkg_snapshot/publish-mode.sh"
printf '\n# snapshot-bundle-line\n' >> "$host_lkg_snapshot/host-bundle-stub/publish-mode.sh"
STUB_HOST_BUNDLE="$host_lkg_snapshot/host-bundle-stub"
write_images "$host_lkg_snapshot/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
PANTRY_TEST_SNAPSHOT_CP_FAIL_REL=publish-mode.sh
run_update "$host_lkg_snapshot"
[[ "$LAST_RC" -eq 0 ]] || fail "snapshot cp failure should not fail deploy: $LAST_OUT"
have "$LAST_OUT" "host script refresh failed" "snapshot cp failure fails refresh"
grep -q "$keep_line" "$host_lkg_snapshot/publish-mode.sh" || fail "snapshot cp failure changed publish-mode.sh"
grep -q 'snapshot-bundle-line' "$host_lkg_snapshot/publish-mode.sh" && fail "snapshot cp failure partially installed publish-mode.sh"
find "$host_lkg_snapshot" -name '*.refresh.*' -print -quit | grep -q . && fail "snapshot cp failure left refresh temp files"
status_body=$(cat "$host_lkg_snapshot/last-deploy-status")
have "$status_body" "reason=host_refresh_failed" "snapshot cp failure records host_refresh_failed"
unset STUB_HOST_BUNDLE PANTRY_TEST_SNAPSHOT_CP_FAIL_REL STUB_HEALTH_BODY STUB_BUILD_BODY

# Restore drops host files that were new before refresh (R1/R2).
host_lkg_new=$(mktemp -d)
prepare_pantry "$host_lkg_new"
stage_host_bundle "$host_lkg_new/host-bundle-stub"
unit_dir="$host_lkg_new/fake-systemd"
mkdir -p "$unit_dir" "$host_lkg_new/systemd"
cp "$host_lkg_new/host-bundle-stub/systemd/pantry-update.service" "$unit_dir/"
l1_unit_marker='# l1-restore-from-systemd-units-marker'
l1_wrong_marker='# l1-wrong-pantry-systemd-marker'
printf '\n%s\n' "$l1_unit_marker" >> "$unit_dir/pantry-update.service"
cp "$host_lkg_new/host-bundle-stub/systemd/pantry-update.service" "$host_lkg_new/systemd/"
printf '\n%s\n' "$l1_wrong_marker" >> "$host_lkg_new/systemd/pantry-update.service"
STUB_HOST_BUNDLE="$host_lkg_new/host-bundle-stub"
write_images "$host_lkg_new/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
PANTRY_SYSTEMD_UNIT_DIR="$unit_dir"
PANTRY_TEST_MV_FAIL_DEST="${unit_dir}/pantry-update.path"
run_update "$host_lkg_new"
[[ "$LAST_RC" -eq 0 ]] || fail "mv failure after new unit should not fail deploy: $LAST_OUT"
have "$LAST_OUT" "host script refresh failed" "mv failure after new unit fails refresh"
[[ ! -f "$unit_dir/pantry-update.timer" ]] \
  || fail "restore did not remove newly installed unit-dir pantry-update.timer (R1)"
[[ ! -f "$host_lkg_new/systemd/pantry-update.timer" ]] \
  || fail "restore did not remove newly installed pantry-dir pantry-update.timer (R2)"
grep -q "$l1_wrong_marker" "$unit_dir/pantry-update.service" \
  && fail "unit restore copied from host-scripts-last-known-good/systemd instead of systemd-units (L1)"
grep -q "$l1_unit_marker" "$unit_dir/pantry-update.service" \
  || fail "unit restore did not copy from host-scripts-last-known-good/systemd-units (L1)"
lkg_unit="$host_lkg_new/host-scripts-last-known-good/systemd-units/pantry-update.service"
[[ -f "$lkg_unit" ]] || fail "snapshot did not record systemd-units/pantry-update.service (L1)"
grep -q "$l1_unit_marker" "$lkg_unit" \
  || fail "systemd-units snapshot does not hold the pre-refresh unit copy (L1)"
find "$host_lkg_new" "$unit_dir" -name '*.refresh.*' -print -quit | grep -q . \
  && fail "mv failure after new unit left refresh temp files"
status_body=$(cat "$host_lkg_new/last-deploy-status")
assert_deploy_status_success "$status_body" "mv failure after new unit"
have "$status_body" "reason=host_refresh_failed" "mv failure after new unit records host_refresh_failed"
unset STUB_HOST_BUNDLE PANTRY_TEST_MV_FAIL_DEST PANTRY_SYSTEMD_UNIT_DIR STUB_HEALTH_BODY STUB_BUILD_BODY

# Unreachable GitHub skips host refresh without failing deploy.
host_prov_net=$(mktemp -d)
prepare_pantry "$host_prov_net"
stage_host_bundle "$host_prov_net/host-bundle-stub"
STUB_HOST_BUNDLE="$host_prov_net/host-bundle-stub"
write_images "$host_prov_net/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
STUB_GITHUB_COMPARE_STATUS=unreachable
run_update "$host_prov_net"
[[ "$LAST_RC" -eq 0 ]] || fail "unreachable GitHub should not fail deploy: $LAST_OUT"
have "$LAST_OUT" "host script refresh skipped: could not reach GitHub" "unreachable GitHub skips refresh"
status_body=$(cat "$host_prov_net/last-deploy-status")
have "$status_body" "reason=host_refresh_failed" "unreachable GitHub records host_refresh_failed"
unset STUB_HOST_BUNDLE STUB_GITHUB_COMPARE_STATUS STUB_HEALTH_BODY STUB_BUILD_BODY

# An incomplete bundle fails refresh atomically and leaves host files unchanged.
host_bundle_bad=$(mktemp -d)
prepare_pantry "$host_bundle_bad"
stage_host_bundle "$host_bundle_bad/host-bundle-stub"
rm -f "$host_bundle_bad/host-bundle-stub/runtime-setup.sh"
marker_line='# keep-me-on-host'
printf '\n%s\n' "$marker_line" >> "$host_bundle_bad/runtime-setup.sh"
STUB_HOST_BUNDLE="$host_bundle_bad/host-bundle-stub"
write_images "$host_bundle_bad/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$host_bundle_bad"
[[ "$LAST_RC" -eq 0 ]] || fail "bundle verify failure should not fail deploy: $LAST_OUT"
have "$LAST_OUT" "host script refresh failed" "incomplete bundle fails refresh"
grep -q "$marker_line" "$host_bundle_bad/runtime-setup.sh" || fail "incomplete bundle changed host runtime-setup.sh"
status_body=$(cat "$host_bundle_bad/last-deploy-status")
have "$status_body" "reason=host_refresh_failed" "incomplete bundle records host_refresh_failed"
unset STUB_HOST_BUNDLE STUB_HEALTH_BODY STUB_BUILD_BODY

# Rollback does not install the new image's host files.
host_no_refresh=$(mktemp -d)
prepare_pantry "$host_no_refresh"
stage_host_bundle "$host_no_refresh/host-bundle-stub"
printf '\n# host-bundle-marker\n' >> "$host_no_refresh/host-bundle-stub/publish-mode.sh"
STUB_HOST_BUNDLE="$host_no_refresh/host-bundle-stub"
write_images "$host_no_refresh/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY='{"commit":"wrong-commit-wrong-commit-wrong-commit-wrong"}'
run_update "$host_no_refresh"
[[ "$LAST_RC" -ne 0 ]] || fail "rollback run should fail: $LAST_OUT"
lack "$LAST_OUT" "refreshed host deploy scripts" "rollback does not refresh host scripts"
grep -q 'host-bundle-marker' "$host_no_refresh/publish-mode.sh" && fail "rollback installed bundled host files"
unset STUB_HOST_BUNDLE STUB_HEALTH_BODY STUB_BUILD_BODY

# Idempotent when bundled files already match the host copy.
host_idempotent=$(mktemp -d)
prepare_pantry "$host_idempotent"
stage_host_bundle "$host_idempotent/host-bundle-stub"
STUB_HOST_BUNDLE="$host_idempotent/host-bundle-stub"
mkdir -p "$host_idempotent/systemd"
cp "$host_idempotent/host-bundle-stub/publish-mode.sh" "$host_idempotent/publish-mode.sh"
cp "$host_idempotent/host-bundle-stub/runtime-setup.sh" "$host_idempotent/runtime-setup.sh"
cp "$host_idempotent/host-bundle-stub/vapid-env.sh" "$host_idempotent/vapid-env.sh"
cp "$host_idempotent/host-bundle-stub/systemd/"* "$host_idempotent/systemd/"
publish_before=$(stat -c '%Y' "$host_idempotent/publish-mode.sh")
write_images "$host_idempotent/images.tsv" \
  sha256:newpantry ghcr.io/rhionin/pantry@sha256:newpantry \
  sha256:runcaddy caddy@sha256:oldcaddy
STUB_HEALTH_BODY='{"status":"ok"}'
STUB_BUILD_BODY="{\"commit\":\"${NEW_COMMIT}\"}"
run_update "$host_idempotent"
[[ "$LAST_RC" -eq 0 ]] || fail "idempotent host refresh exited $LAST_RC: $LAST_OUT"
publish_after=$(stat -c '%Y' "$host_idempotent/publish-mode.sh")
[[ "$publish_before" -eq "$publish_after" ]] || fail "unchanged bundle rewrote publish-mode.sh"
have "$LAST_OUT" "refreshed host deploy scripts from the running image" "unchanged bundle still completes refresh"
status_body=$(cat "$host_idempotent/last-deploy-status")
assert_deploy_status_success "$status_body" "idempotent host refresh"
have "$status_body" "reason=deploy_ok" "idempotent host refresh records deploy_ok"
unset STUB_HOST_BUNDLE STUB_HEALTH_BODY STUB_BUILD_BODY

rm -rf "$backup_warn" "$backup_required" "$backup_missing" "$backup_noexec" "$backup_optional" "$backup_off" "$backup_dotenv" "$backup_invalid" "$host_refresh" "$host_prov_realistic" "$host_prov_diverged" "$host_prov_fail" "$host_prov_digest" "$host_prov_empty_dig" "$host_lkg_mv" "$host_lkg_snapshot" "$host_lkg_new" "$host_prov_net" "$host_bundle_bad" "$host_no_refresh" "$host_idempotent"

rm -rf "$healthy" "$noop" "$mismatch" "$unhealthy" "$caddy_only" "$rollback_fail" "$unlabeled" "$polled" "$migrating" "$migr_cap" "$pending" "$exited" "$proxied" "$full_disk" "$orphan" "$cloud_down" "$cloud_stale" "$cloud_warn" "$crash" "$resumed" "$gone" "$degraded" "$snap" "$snap_old" "$rollback_unhealthy" "$unpinned" "$spill" "$flap" "$pantry_restart" "$caddy_fail" "$tunnel_ok" "$tunnel_skip" "$tunnel_bad" "$acme_same" "$tunnel_same" "$tunnel_refused" "$tunnel_commit" "$warned" "$caddy_keep" "$name_conflict" "$acme_regress" "$tunnel_regress" "$tunnel_hang" "$tunnel_late" "$tunnel_reset" "$caddy_exit" "$caddy_up_down" "$tunnel_restart" "$tunnel_502" "$tunnel_empty" "$marker_off" "$marker_cr" "$marker_clear" "$marker_park" "$proxy_grocy" "$reach_reset" "$caddy_newid" "$caddy_restarts" "$caddy_started" "$caddy_retry" "$probe_cap" "$proxy_mark" "$marker_deploy_clear" "$tunnel_mark" "$marker_no_auth" "$repull" "$wrong_project" "$blank_project" "$locked"
echo "pantry_update_test ok"
