#!/usr/bin/env bash
# Restore drill. This script runs only on CJ's own computer.
# It does not run on the pantry Pi, a shared machine, or an agent.
# It does not read the live database and it does not change the Pi.
#
# The age identity is --identity FILE, or --paste-identity, which reads the
# key into a tmpfs scratch file and deletes that copy when the drill ends.
# A private key in an environment variable is refused. Only a variable name
# that is a shell identifier is printed. An identity file outside the scratch
# directory is not deleted.
#
#   deploy/backup/restore-drill.sh --identity /path/to/age-identity.txt \
#       --file snapshot.pantry.db.gz.age --manifest-file snapshot.manifest.json
#
# --bucket-dir reads objects already stored as files (a test fixture or a
# directory CJ downloaded onto this computer). It is not the Pi and it is
# not a place this script copies a key from.
set -euo pipefail

echo "This restore drill runs only on CJ's own computer."
echo "It does not run on the pantry Pi or on a shared agent box."
echo "It does not read the live database and it does not change the Pi."

pi_marker="${PANTRY_DRILL_PI_MARKER:-/opt/pantry/docker-compose.yml}"
if [[ -e "$pi_marker" ]]; then
  echo "refusing to run on the pantry Pi" >&2
  exit 2
fi

env_records() {
  # GNU env -0 keeps a multi-line value on one record. macOS env has no -0.
  if env -0 >/dev/null 2>&1; then
    env -0
    return
  fi
  command -v python3 >/dev/null 2>&1 || return 1
  python3 -c '
import os, sys
try:
    items = list(os.environb.items())
except Exception:
    sys.exit(1)
for name, value in items:
    try:
        name.decode("utf-8")
        value.decode("utf-8")
    except UnicodeDecodeError:
        sys.exit(1)
    sys.stdout.buffer.write(name + b"=" + value + b"\0")
'
}

refuse_identity_env() {
  local line name value label refuse saw pipe_rc
  # Scan the environment in this process. Do not copy it into a file: a
  # refused private key must not land on disk.
  shopt -s lastpipe
  label=""
  saw=0
  set +e
  env_records | while IFS= read -r -d '' line || [[ -n "${line:-}" ]]; do
    [[ "$line" == *=* ]] || continue
    name=${line%%=*}
    value=${line#*=}
    label=""
    if [[ "$name" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
      label=$name
    fi
    refuse=0
    case "$name" in
      AGE_IDENTITY|AGE_IDENTITY_FILE|BACKUP_AGE_IDENTITY|BACKUP_IDENTITY|SOPS_AGE_KEY|SOPS_AGE_KEY_FILE)
        refuse=1
        ;;
    esac
    case "$name$value" in
      *AGE-SECRET-KEY-*)
        refuse=1
        ;;
    esac
    if [[ "$refuse" -eq 1 ]]; then
      saw=1
      break
    fi
  done
  # EOF makes the read loop exit 1. The producer's status is the failure.
  pipe_rc=${PIPESTATUS[0]}
  set -e
  shopt -u lastpipe
  if [[ "$saw" -eq 1 ]]; then
    if [[ -n "$label" ]]; then
      echo "refusing to read a private key from the environment variable $label" >&2
    else
      echo "refusing to read a private key from an environment variable" >&2
    fi
    exit 2
  fi
  if [[ "$pipe_rc" -ne 0 ]]; then
    echo "could not read the environment" >&2
    exit 2
  fi
}
refuse_identity_env

usage() {
  echo "usage: restore-drill.sh (--identity FILE | --paste-identity) (--bucket-dir DIR | --file CIPHER --manifest-file MANIFEST) [--secrets] [--object KEY]" >&2
  exit 2
}

identity=""
paste_identity=0
bucket_dir=""
cipher_file=""
manifest_file=""
secrets=0
object_key=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --identity)
      identity="${2:-}"
      shift 2
      ;;
    --paste-identity)
      paste_identity=1
      shift
      ;;
    --bucket-dir)
      bucket_dir="${2:-}"
      shift 2
      ;;
    --file)
      cipher_file="${2:-}"
      shift 2
      ;;
    --manifest-file)
      manifest_file="${2:-}"
      shift 2
      ;;
    --object)
      object_key="${2:-}"
      shift 2
      ;;
    --secrets)
      secrets=1
      shift
      ;;
    -h|--help)
      usage
      ;;
    *)
      echo "unknown argument" >&2
      usage
      ;;
  esac
done

if [[ -z "$identity" && "$paste_identity" -eq 0 ]]; then
  echo "the age identity file path is required (--identity or --paste-identity)" >&2
  usage
fi
if [[ -n "$identity" && "$paste_identity" -eq 1 ]]; then
  echo "pass either --identity or --paste-identity" >&2
  exit 2
fi

reject_pi_path() {
  local path="$1" label="$2"
  case "$path" in
    /opt/pantry|/opt/pantry/*|/data|/data/*)
      echo "$label must not be on the pantry Pi paths" >&2
      exit 2
      ;;
  esac
}

if [[ -n "$identity" ]]; then
  reject_pi_path "$identity" "the age identity file"
fi
if [[ -n "$bucket_dir" ]]; then
  reject_pi_path "$bucket_dir" "the bucket directory"
fi
if [[ -n "$cipher_file" ]]; then
  reject_pi_path "$cipher_file" "the backup file"
fi

if [[ -z "$cipher_file" && -z "$bucket_dir" ]]; then
  echo "pass --file and --manifest-file, or --bucket-dir" >&2
  usage
fi

started=$SECONDS
scratch_parent="${TMPDIR:-/tmp}"
if [[ -d /dev/shm && -w /dev/shm ]]; then
  scratch_parent=/dev/shm
fi
scratch=$(mktemp -d "$scratch_parent/pantry-restore-drill.XXXXXX")
chmod 0700 "$scratch"
cleanup() {
  if [[ "$paste_identity" -eq 1 && -n "${identity:-}" && -f "$identity" ]]; then
    case "$identity" in
      "$scratch"/*)
        if command -v shred >/dev/null 2>&1; then
          shred -u "$identity" 2>/dev/null || rm -f "$identity"
        else
          rm -f "$identity"
        fi
        ;;
    esac
  fi
  rm -rf "$scratch"
}
trap cleanup EXIT INT TERM

if [[ "$paste_identity" -eq 1 ]]; then
  identity="$scratch/age-identity.txt"
  echo "Paste the age identity, then end input." >&2
  saved_stty=""
  if [[ -t 0 ]]; then
    saved_stty=$(stty -g)
    stty -echo
  fi
  set +e
  cat >"$identity"
  paste_status=$?
  set -e
  if [[ -n "$saved_stty" ]]; then
    stty "$saved_stty"
  fi
  if [[ "$paste_status" -ne 0 ]]; then
    echo "could not read the pasted age identity" >&2
    exit 2
  fi
  chmod 0600 "$identity"
else
  if [[ ! -f "$identity" ]]; then
    echo "the age identity file is missing" >&2
    exit 2
  fi
  identity=$(readlink -f "$identity" 2>/dev/null || realpath "$identity")
  reject_pi_path "$identity" "the age identity file"
  case "$identity" in
    "$scratch"|"$scratch"/*)
      echo "the age identity file must not live in the scratch directory" >&2
      exit 2
      ;;
  esac
fi
if [[ ! -f "$identity" ]]; then
  echo "the age identity file is missing" >&2
  exit 2
fi
# A private key left group- or world-readable is refused. The path itself
# is the only input; nothing is read from the environment.
mode=$(stat -c '%a' "$identity" 2>/dev/null || stat -f '%OLp' "$identity")
perm=$((8#$mode))
if (( (perm & 077) != 0 )); then
  echo "the age identity file must be readable only by its owner" >&2
  exit 2
fi
if ! grep -q 'AGE-SECRET-KEY-' "$identity"; then
  echo "the identity file is not an age identity" >&2
  exit 2
fi

pick_newest() {
  local dir="$1" pattern="$2" newest="" newest_mtime=0 file mtime
  while IFS= read -r file; do
    [[ -n "$file" ]] || continue
    mtime=$(stat -c '%Y' "$file" 2>/dev/null || stat -f '%m' "$file")
    if [[ "$mtime" -ge "$newest_mtime" ]]; then
      newest="$file"
      newest_mtime=$mtime
    fi
  done < <(find "$dir" -type f -name "$pattern")
  printf '%s' "$newest"
}

if [[ -n "$bucket_dir" ]]; then
  if [[ ! -d "$bucket_dir" ]]; then
    echo "the bucket directory is missing" >&2
    exit 2
  fi
  if [[ -n "$object_key" ]]; then
    cipher_file="$bucket_dir/$object_key"
  else
    cipher_file=$(pick_newest "$bucket_dir" '*.pantry.db.gz.age')
  fi
  if [[ -z "$cipher_file" || ! -f "$cipher_file" ]]; then
    echo "no pantry backup object was found" >&2
    exit 1
  fi
  manifest_file="${cipher_file%.pantry.db.gz.age}.manifest.json"
fi

if [[ ! -f "$cipher_file" || ! -f "$manifest_file" ]]; then
  echo "the backup object and its manifest are both required" >&2
  exit 1
fi

command -v age >/dev/null 2>&1 || {
  echo "age is required on this computer" >&2
  exit 1
}

age -d -i "$identity" -o "$scratch/pantry.db.gz" "$cipher_file"
gzip -dc "$scratch/pantry.db.gz" >"$scratch/pantry.db"
rm -f "$scratch/pantry.db.gz"

want_sha=$(python3 - "$manifest_file" plaintext_sha256 <<'PY'
import json, sys
with open(sys.argv[1]) as handle:
    print(json.load(handle)[sys.argv[2]])
PY
)
if command -v sha256sum >/dev/null 2>&1; then
  got_sha=$(sha256sum "$scratch/pantry.db" | awk '{print $1}')
else
  got_sha=$(shasum -a 256 "$scratch/pantry.db" | awk '{print $1}')
fi
if [[ "$got_sha" != "$want_sha" ]]; then
  echo "the restored database sha256 does not match the manifest" >&2
  exit 1
fi

if command -v stockbot-backup >/dev/null 2>&1; then
  stockbot-backup verify -db "$scratch/pantry.db" -manifest "$manifest_file"
else
  python3 - "$scratch/pantry.db" "$manifest_file" <<'PY'
import json, sqlite3, sys
db = sqlite3.connect("file:%s?mode=ro" % sys.argv[1], uri=True)
manifest = json.load(open(sys.argv[2]))
integrity = db.execute("PRAGMA integrity_check").fetchone()[0]
if integrity != "ok":
    sys.exit("the snapshot failed the integrity check")
tables = manifest.get("tables") or {}
for name, want in tables.items():
    quoted = '"' + name.replace('"', '""') + '"'
    got = db.execute("SELECT COUNT(*) FROM %s" % quoted).fetchone()[0]
    if got != want:
        sys.exit("the restored database table counts do not match the manifest")
if len(tables) == 0:
    sys.exit("the restored database table counts do not match the manifest")
print("verify ok integrity=ok tables=%d rows=%s migration=%s" % (
    len(tables), manifest.get("row_total", ""), manifest.get("last_migration", "")))
PY
fi

if [[ "$secrets" -eq 1 ]]; then
  secrets_file=""
  secrets_manifest=""
  if [[ -n "$bucket_dir" ]]; then
    secrets_file=$(pick_newest "$bucket_dir" '*.tar.age')
    if [[ -n "$secrets_file" ]]; then
      secrets_manifest="${secrets_file%.tar.age}.manifest.json"
    fi
  fi
  if [[ -z "$secrets_file" || ! -f "$secrets_manifest" ]]; then
    echo "no secrets archive was found" >&2
    exit 1
  fi
  age -d -i "$identity" -o "$scratch/secrets.tar" "$secrets_file"
  rm -f "$scratch/secrets.tar"
  python3 - "$secrets_manifest" <<'PY'
import json, sys
manifest = json.load(open(sys.argv[1]))
print("secrets ok files=%s" % manifest.get("file_count", ""))
PY
fi

DOCKER="${BACKUP_DRILL_DOCKER:-docker}"
image="${PANTRY_DRILL_IMAGE:-ghcr.io/rhionin/pantry:latest}"
probe_image="${PANTRY_DRILL_PROBE_IMAGE:-curlimages/curl@sha256:c1fe1679c34d9784c1b0d1e5f62ac0a79fca01fb6377cdd33e90473c6f9f9a69}"
mkdir -p "$scratch/data"
cp "$scratch/pantry.db" "$scratch/data/pantry.db"
chmod 0600 "$scratch/data/pantry.db"
if ! "$DOCKER" image inspect "$probe_image" >/dev/null 2>&1; then
  echo "the drill probe image is not available locally" >&2
  exit 1
fi
net="pantry-drill-$$"
# The app stays on an internal network, so published ports would be dropped
# and the copy cannot reach the internet. A second container on that same
# network asks for /api/build and /api/products.
"$DOCKER" network create --internal "$net" >/dev/null
"$DOCKER" run -d --name "$net" \
  --network "$net" \
  --user "$(id -u):$(id -g)" \
  --read-only \
  --tmpfs "/tmp:rw,noexec,nosuid,size=16m,mode=0700,uid=$(id -u),gid=$(id -g)" \
  -e DISABLE_EXTERNAL_PRODUCT_LOOKUP=true \
  -e SCAN_INPUT=stdin \
  -e ADDR=:8080 \
  -e DB_PATH=/data/pantry.db \
  -v "$scratch/data:/data" \
  "$image" >/dev/null
probe_code() {
  "$DOCKER" run --rm \
    --network "$net" \
    --cap-drop ALL \
    --read-only \
    "$probe_image" \
    -sS -o /dev/null -w '%{http_code}' --max-time 2 "$1" 2>/dev/null || true
}
smoke_ok=0
for _ in 1 2 3 4 5 6 7 8 9 10; do
  build_code=$(probe_code "http://${net}:8080/api/build")
  products_code=$(probe_code "http://${net}:8080/api/products")
  if [[ "$build_code" == 200 && "$products_code" == 200 ]]; then
    smoke_ok=1
    break
  fi
  sleep 0.3
done
"$DOCKER" rm -f "$net" >/dev/null 2>&1 || true
"$DOCKER" network rm "$net" >/dev/null 2>&1 || true
if [[ "$smoke_ok" -ne 1 ]]; then
  echo "the offline copy did not answer /api/build and /api/products" >&2
  exit 1
fi

elapsed=$((SECONDS - started))
echo "drill finished in ${elapsed}s"
trap - EXIT INT TERM
rm -rf "$scratch"
if [[ -e "$scratch" ]]; then
  echo "the scratch directory remains" >&2
  exit 1
fi
if [[ "$paste_identity" -eq 1 ]]; then
  if [[ -e "$identity" ]]; then
    echo "the pasted age identity was not removed" >&2
    exit 1
  fi
elif [[ ! -f "$identity" ]]; then
  echo "the age identity file is missing after the drill" >&2
  exit 1
fi
echo "scratch removed"
