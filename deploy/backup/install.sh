#!/usr/bin/env bash
# Install the nightly pantry backup on this Pi.
#
# Run as root: sudo bash deploy/backup/install.sh
# Prompts for the age public key and the R2 settings. The secret access key
# is read with `read -rs`, so it is not saved in shell history.
# The settings file is mode 0400 and owned by uid 65532 when this runs as root.
#
# The script proves the running pantry image contains stockbot-backup before
# it asks for anything. It enables the 03:30 America/Denver timer and then
# starts one manual backup.
#
# PANTRY_SETUP_SKIP_ROOT=1 is the same test seam the rest of deploy/ uses.
# PANTRY_BACKUP_INSTALL_KEEP=1 leaves the script in place. Otherwise a copy
# invoked from /tmp is shredded after a successful install.
set -euo pipefail

PANTRY_DIR="${PANTRY_DIR:-/opt/pantry}"
PANTRY_SYSTEMD_UNIT_DIR="${PANTRY_SYSTEMD_UNIT_DIR:-/etc/systemd/system}"
PANTRY_CONTAINER="${PANTRY_CONTAINER:-pantry}"
DOCKER="${DOCKER:-docker}"
reason_file="${PANTRY_BACKUP_REASON_FILE:-/run/pantry-backup.env}"

if [[ "$(id -u)" -ne 0 && "${PANTRY_SETUP_SKIP_ROOT:-}" != 1 ]]; then
  echo "Run this as root: sudo bash $0" >&2
  exit 1
fi

script_dir=$(cd "$(dirname "$0")" && pwd)
unit_src="${PANTRY_BACKUP_UNIT_DIR:-}"
if [[ -z "$unit_src" ]]; then
  if [[ -f "$script_dir/../systemd/pantry-backup.service" ]]; then
    unit_src="$script_dir/../systemd"
  elif [[ -f "$PANTRY_DIR/systemd/pantry-backup.service" ]]; then
    unit_src="$PANTRY_DIR/systemd"
  else
    echo "could not find the backup units" >&2
    exit 1
  fi
fi
if [[ ! -f "$unit_src/pantry-backup.service" || ! -f "$unit_src/pantry-backup.timer" || ! -f "$unit_src/pantry-backup.sh" ]]; then
  echo "could not find the backup units" >&2
  exit 1
fi

image=$("$DOCKER" inspect "$PANTRY_CONTAINER" --format '{{.Image}}')
if ! "$DOCKER" run --rm --entrypoint /usr/local/bin/stockbot-backup "$image" version; then
  echo "the running pantry image does not include stockbot-backup" >&2
  exit 1
fi

read_value() {
  local silent="$1" label="$2" value=""
  if [[ "$silent" == silent ]]; then
    read -rs -r -p "$label: " value
    printf '\n' >&2
  else
    read -r -p "$label: " value
  fi
  printf '%s' "$value"
}

age_recipient=$(read_value "" "age public key (age1...)")
account_id=$(read_value "" "R2 account id")
access_key=$(read_value "" "R2 access key id")
secret_key=$(read_value silent "R2 secret access key")
bucket=$(read_value "" "R2 bucket [${PANTRY_BACKUP_DEFAULT_BUCKET:-pantry-pi-backups}]")
health_url=$(read_value "" "Healthchecks URL (optional)")

if [[ "$age_recipient" == *AGE-SECRET-KEY-* ]]; then
  echo "that is a private key; the Pi stores only the age public key" >&2
  exit 1
fi
if [[ "$age_recipient" != age1* ]]; then
  echo "the age recipient must be an age1 public key" >&2
  exit 1
fi
if [[ ! "$account_id" =~ ^[A-Za-z0-9]{1,64}$ ]]; then
  echo "the R2 account id is not valid" >&2
  exit 1
fi
if [[ -z "$bucket" ]]; then
  bucket="${PANTRY_BACKUP_DEFAULT_BUCKET:-pantry-pi-backups}"
fi
if [[ ! "$bucket" =~ ^[a-z0-9.-]{3,63}$ ]]; then
  echo "the bucket name is not valid" >&2
  exit 1
fi
if [[ -z "$access_key" || -z "$secret_key" ]]; then
  echo "the R2 access key is missing" >&2
  exit 1
fi

install_private() {
  local dest="$1" tmp
  tmp=$(mktemp "${dest}.XXXXXX")
  cat >"$tmp"
  chmod 0400 "$tmp"
  if ! chown 65532:65532 "$tmp"; then
    if [[ "${PANTRY_SETUP_SKIP_ROOT:-}" != 1 ]]; then
      rm -f "$tmp"
      echo "could not give the backup settings file to the pantry user" >&2
      exit 1
    fi
  fi
  mv "$tmp" "$dest"
  chmod 0400 "$dest"
}

mkdir -p "$PANTRY_DIR/secrets/backup" "$PANTRY_DIR/backup" "$PANTRY_DIR/systemd"
{
  printf 'R2_ACCOUNT_ID=%s\n' "$account_id"
  printf 'R2_ACCESS_KEY_ID=%s\n' "$access_key"
  printf 'R2_SECRET_ACCESS_KEY=%s\n' "$secret_key"
  printf 'R2_BUCKET=%s\n' "$bucket"
  if [[ -n "$health_url" ]]; then
    printf 'HC_BACKUP_RUN_URL=%s\n' "$health_url"
  fi
} | install_private "$PANTRY_DIR/secrets/backup/r2.env"
recipient_tmp=$(mktemp "$PANTRY_DIR/backup/recipients.XXXXXX")
printf '%s\n' "$age_recipient" >"$recipient_tmp"
chmod 0444 "$recipient_tmp"
mv "$recipient_tmp" "$PANTRY_DIR/backup/recipients"
unset secret_key access_key

cp "$unit_src/pantry-backup.service" "$PANTRY_DIR/systemd/pantry-backup.service"
cp "$unit_src/pantry-backup.timer" "$PANTRY_DIR/systemd/pantry-backup.timer"
cp "$unit_src/pantry-backup.sh" "$PANTRY_DIR/systemd/pantry-backup.sh"
chmod 0755 "$PANTRY_DIR/systemd/pantry-backup.sh"

mkdir -p "$PANTRY_SYSTEMD_UNIT_DIR"
cp "$PANTRY_DIR/systemd/pantry-backup.service" "$PANTRY_SYSTEMD_UNIT_DIR/pantry-backup.service"
cp "$PANTRY_DIR/systemd/pantry-backup.timer" "$PANTRY_SYSTEMD_UNIT_DIR/pantry-backup.timer"
systemctl daemon-reload
systemctl enable --now pantry-backup.timer

mkdir -p "$(dirname "$reason_file")"
printf 'BACKUP_REASON=manual\n' >"$reason_file"
chmod 0600 "$reason_file"
systemctl start pantry-backup.service
rm -f "$reason_file"

journal=$(journalctl -u pantry-backup.service -n 80 --no-pager || true)
line=$(printf '%s\n' "$journal" | grep 'backup ok key=' | tail -n 1 || true)
if [[ -z "$line" ]]; then
  echo "the manual backup did not report success" >&2
  exit 1
fi
key=$(printf '%s\n' "$line" | sed -n 's/.*key=\([^ ]*\).*/\1/p')
integrity=$(printf '%s\n' "$line" | sed -n 's/.*integrity=\([^ ]*\).*/\1/p')
rows=$(printf '%s\n' "$line" | sed -n 's/.*rows=\([^ ]*\).*/\1/p')
printf 'key=%s\nintegrity=%s\nrows=%s\n' "$key" "$integrity" "$rows"

if [[ "${PANTRY_BACKUP_INSTALL_KEEP:-}" == 1 ]]; then
  exit 0
fi
self=$(readlink -f "$0" 2>/dev/null || realpath "$0")
case "$self" in
  /tmp/*)
    if command -v shred >/dev/null 2>&1; then
      shred -u "$self" || rm -f "$self"
    else
      rm -f "$self"
    fi
    ;;
esac
