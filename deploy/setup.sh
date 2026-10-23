#!/usr/bin/env bash

set -euo pipefail

# Stockbot Deployment Setup Script
# 
# This script configures a Raspberry Pi to run Stockbot in a Docker container
# with hot-pluggable barcode scanner support. It is designed for iteration:
# run it multiple times as you make changes, and it will converge to the
# desired state without losing configuration.
#
# Usage:
#   sudo ./setup.sh                  # Sync files, containers, Caddy, and firewall
#   sudo ./setup.sh apply            # Same command, explicit name
#   sudo ./setup.sh --with-updates   # ...and enable the auto-update timer and deploy hook
#   sudo ./setup.sh publish --tunnel # Publish https://PUBLIC_HOST through Cloudflare Tunnel
#   sudo ./setup.sh unpublish        # Stop the public proxy; LAN pantry keeps running
#   sudo ./setup.sh firewall-off     # Remove the LAN port rule until the next setup
#   sudo ./setup.sh rule             # Regenerate udev rule for a new scanner
#   sudo ./setup.sh status           # Diagnose the full chain from udev to health
#   sudo ./setup.sh logs             # Follow container logs
#   sudo ./setup.sh freeze           # Mask timer and deploy hook during iteration
#   sudo ./setup.sh thaw             # Unmask timer and deploy hook when done
#   sudo ./setup.sh deploy-secret    # Print DEPLOY_HOOK_SECRET
#   sudo ./setup.sh help             # Show this message
#
# install, publish, and firewall are aliases of apply so older scripts keep working.
#
# Default: apply

# Color codes for output (TTY detection)
if [[ -t 1 ]]; then
  RED='\033[0;31m'
  GREEN='\033[0;32m'
  YELLOW='\033[1;33m'
  BLUE='\033[0;36m'
  NC='\033[0m'
else
  RED=''
  GREEN=''
  YELLOW=''
  BLUE=''
  NC=''
fi

# Determine script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/mdns/lan-ipv4.sh"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/auth-migrate.sh"

# Deploy root. Tests point this at a temp directory; the Pi uses /opt/pantry.
PANTRY_DIR="${PANTRY_DIR:-/opt/pantry}"
# Where systemd units are installed. Tests point this at a temp directory.
PANTRY_SYSTEMD_UNIT_DIR="${PANTRY_SYSTEMD_UNIT_DIR:-/etc/systemd/system}"
# Input device table and installed udev rule. Tests point these at temp files
# so rule generation can be checked without a scanner or root.
PANTRY_INPUT_DEVICES="${PANTRY_INPUT_DEVICES:-/proc/bus/input/devices}"
PANTRY_UDEV_RULE_FILE="${PANTRY_UDEV_RULE_FILE:-/etc/udev/rules.d/99-pantry-scanner.rules}"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/publish-mode.sh"

# Helpers
log_info() {
  if [[ -t 1 ]]; then
    echo -e "${BLUE}[info]${NC} $*"
  else
    echo "[info] $*"
  fi
}

log_success() {
  if [[ -t 1 ]]; then
    echo -e "${GREEN}[✓]${NC} $*"
  else
    echo "[✓] $*"
  fi
}

log_warn() {
  if [[ -t 1 ]]; then
    echo -e "${YELLOW}[warn]${NC} $*"
  else
    echo "[warn] $*"
  fi
}

log_error() {
  if [[ -t 1 ]]; then
    echo -e "${RED}[error]${NC} $*" >&2
  else
    echo "[error] $*" >&2
  fi
}

fatal() {
  log_error "$@"
  exit 1
}

# Verify running as root.
# PANTRY_SETUP_SKIP_ROOT is a test seam so behavior tests can run without sudo.
require_root() {
  if [[ "${PANTRY_SETUP_SKIP_ROOT:-}" == 1 ]]; then
    return 0
  fi
  if [[ $EUID -ne 0 ]]; then
    fatal "This script must be run as root (use sudo)"
  fi
}

# Check if a command exists
command_exists() {
  command -v "$1" &> /dev/null
}

# env_value prints KEY's value from ${PANTRY_DIR}/.env, without surrounding whitespace.
# Missing keys print nothing. Callers decide whether an empty value is an error.
env_value() {
  { grep "^${1}=" "${PANTRY_DIR}/.env" || true; } | head -1 | cut -d= -f2- | tr -d '[:space:]'
}

# env_value_keep_spaces is env_value for a passphrase. Internal spaces stay.
# Only a trailing CR and whitespace at the ends are removed.
env_value_keep_spaces() {
  local line
  line=$({ grep "^${1}=" "${PANTRY_DIR}/.env" || true; } | head -1)
  line=${line#*=}
  line=${line%$'\r'}
  line="${line#"${line%%[![:space:]]*}"}"
  line="${line%"${line##*[![:space:]]}"}"
  printf '%s' "$line"
}

# write_auth_caddy stores the bcrypt hash Caddy checks on the public site.
# The plaintext password never goes in this file. A literal bcrypt hash is
# required: doubling the dollar signs makes the password stop matching.
write_auth_caddy() {
  local user="$1" hash="$2" tmp
  if [[ -d ${PANTRY_DIR}/auth.caddy ]]; then
    fatal "${PANTRY_DIR}/auth.caddy is a directory. Docker creates one when the file is missing. Remove it and re-run 'sudo ./setup.sh'"
  fi
  tmp=$(mktemp "${PANTRY_DIR}/auth.caddy.XXXXXX")
  # Realm is a directive argument. Inside the block, a line is a username and hash.
  printf 'basic_auth bcrypt Pantry {\n\t%s %s\n}\n' "$user" "$hash" > "$tmp"
  chmod 600 "$tmp" || fatal "Could not protect the password hash file"
  mv "$tmp" "${PANTRY_DIR}/auth.caddy" || fatal "Could not write the password hash file"
}

# copy_deploy_files refreshes ${PANTRY_DIR} from this script's directory.
# .env is never copied, so a re-run cannot clobber operator settings.
# Copying a file onto itself (running the already-installed script) is skipped.
copy_deploy_files() {
  log_info "Copying deployment files to ${PANTRY_DIR}..."
  mkdir -p "${PANTRY_DIR}"
  local item src dest
  for item in docker-compose.yml docker-compose.tunnel.yml docker-compose.grocy.yml .env.example Caddyfile Caddyfile.tunnel Caddyfile.legacy Caddyfile.tunnel.legacy publish-mode.sh auth-migrate.sh vapid-env.sh udev systemd firewall mdns dns; do
    src="$SCRIPT_DIR/$item"
    dest="${PANTRY_DIR}/$item"
    if [[ ! -e "$src" ]]; then
      continue
    fi
    if [[ "$src" == "$dest" ]]; then
      continue
    fi
    cp -r "$src" "${PANTRY_DIR}/"
  done
  if [[ "$SCRIPT_DIR/setup.sh" != "${PANTRY_DIR}/setup.sh" ]]; then
    cp "$SCRIPT_DIR/setup.sh" "${PANTRY_DIR}/setup.sh"
  fi
  chmod +x "${PANTRY_DIR}/setup.sh"
  if [[ -f "${PANTRY_DIR}/systemd/pantry-update.sh" ]]; then
    chmod +x "${PANTRY_DIR}/systemd/pantry-update.sh"
  fi
  if [[ -f "${PANTRY_DIR}/systemd/pantry-backup.sh" ]]; then
    chmod +x "${PANTRY_DIR}/systemd/pantry-backup.sh"
  fi
  if [[ -f "${PANTRY_DIR}/firewall/pantry-lan-only.sh" ]]; then
    chmod +x "${PANTRY_DIR}/firewall/pantry-lan-only.sh"
  fi
  if [[ -f "${PANTRY_DIR}/mdns/pantry-mdns.sh" ]]; then
    chmod +x "${PANTRY_DIR}/mdns/pantry-mdns.sh"
  fi
  if [[ -f "${PANTRY_DIR}/dns/pantry-split-dns.sh" ]]; then
    chmod +x "${PANTRY_DIR}/dns/pantry-split-dns.sh"
  fi
  log_success "Deployment files copied"
}

# systemd_unit_dir is where pantry-lan-only.service and pantry-update.service live.
systemd_unit_dir() {
  printf '%s\n' "$PANTRY_SYSTEMD_UNIT_DIR"
}

# write_auth_from_env hashes BASIC_AUTH_PASSWORD into auth.caddy.
# Returns 1 when the password is empty so the caller can keep a LAN-only install.
# A password that is present but unusable is a fatal error: the operator set one
# and it cannot protect the public site.
write_auth_from_env() {
  local auth_user auth_password auth_hash
  auth_user=$(env_value BASIC_AUTH_USER)
  auth_password=$(env_value_keep_spaces BASIC_AUTH_PASSWORD)
  if [[ -z "$auth_user" ]]; then
    auth_user=pantry
  fi
  if [[ -z "$auth_password" ]]; then
    return 1
  fi
  if [[ ! "$auth_user" =~ ^[A-Za-z][A-Za-z0-9._-]{0,63}$ ]]; then
    fatal "BASIC_AUTH_USER must be letters, digits, dots, underscores, or hyphens (got: $auth_user)"
  fi
  if [[ ${#auth_password} -lt 12 || ${#auth_password} -gt 72 ]]; then
    fatal "BASIC_AUTH_PASSWORD must be 12 to 72 characters"
  fi

  # Hash on stdin so the password is not a docker argument. Caddy requires
  # the trailing newline as a separator and does not treat it as part of the password.
  log_info "Hashing the shared password"
  if ! auth_hash=$(printf '%s\n' "$auth_password" | docker run --rm -i caddy:2.11.4-alpine caddy hash-password); then
    fatal "Could not hash the shared password. Docker must be able to run caddy:2.11.4-alpine."
  fi
  auth_hash=${auth_hash//$'\r'/}
  auth_hash=${auth_hash//$'\n'/}
  if [[ ! "$auth_hash" =~ ^\$2[aby]\$ ]]; then
    fatal "Caddy did not return a bcrypt password hash. The public proxy was not started."
  fi
  unset auth_password
  write_auth_caddy "$auth_user" "$auth_hash"
  unset auth_hash
  chmod 600 "$PANTRY_DIR/.env" "$PANTRY_DIR/auth.caddy" || fatal "Could not restrict .env and auth.caddy"
  log_success "Wrote $PANTRY_DIR/auth.caddy and restricted .env to the owner"
  return 0
}

# valid_hostname is 0 when $1 is a DNS name with no scheme, port, or path.
valid_hostname() {
  local host="$1"
  [[ "$host" =~ ^[A-Za-z0-9.-]+$ && "$host" == *.* && "$host" != .* && "$host" != *. ]]
}

# write_legacy_snippet DEST SITE writes one Caddy site that redirects to
# PUBLIC_HOST, or a comment when SITE is empty. SITE is a site address
# (a hostname, or http://hostname in tunnel mode) and is already validated.
write_legacy_snippet() {
  local dest="$1" site="$2" tmp
  tmp=$(mktemp "${dest}.XXXXXX")
  if [[ -n "$site" ]]; then
    # {$PUBLIC_HOST} is expanded by Caddy when the proxy starts.
    # shellcheck disable=SC2016
    printf '%s {\n\tredir https://{$PUBLIC_HOST}{uri} 308\n}\n' "$site" > "$tmp"
  else
    printf '%s\n' '# No LEGACY_PUBLIC_HOST. Caddy serves PUBLIC_HOST only.' > "$tmp"
  fi
  mv "$tmp" "$dest"
}

# write_legacy_public_host refreshes the redirect snippets from .env.
# An empty LEGACY_PUBLIC_HOST serves PUBLIC_HOST only. The same value as
# PUBLIC_HOST is ignored so Caddy does not get two blocks for one name.
write_legacy_public_host() {
  local legacy public site=""
  legacy=$(env_value LEGACY_PUBLIC_HOST)
  public=$(env_value PUBLIC_HOST)
  if [[ -n "$legacy" ]]; then
    if ! valid_hostname "$legacy"; then
      log_warn "LEGACY_PUBLIC_HOST must be a hostname such as pantry.rhionin.com, without a scheme or path (got: $legacy). The previous name will not redirect."
    elif [[ -z "$public" || "$legacy" == "$public" ]]; then
      log_warn "LEGACY_PUBLIC_HOST ($legacy) is not a different hostname from PUBLIC_HOST. The previous name will not redirect."
    else
      site="$legacy"
      log_info "https://${legacy} redirects to https://${public}"
    fi
  fi
  if [[ -n "$site" ]]; then
    write_legacy_snippet "${PANTRY_DIR}/Caddyfile.legacy" "$site"
    write_legacy_snippet "${PANTRY_DIR}/Caddyfile.tunnel.legacy" "http://${site}"
  else
    write_legacy_snippet "${PANTRY_DIR}/Caddyfile.legacy" ""
    write_legacy_snippet "${PANTRY_DIR}/Caddyfile.tunnel.legacy" ""
  fi
}

# prepare_public_profile is 0 when compose should include the public profile.
# An existing auth.caddy is never rewritten and the operator is never prompted,
# so a re-run cannot wipe BASIC_AUTH_PASSWORD or force it to be typed again.
# A hash is written only when auth.caddy is absent and the password is already in .env.
prepare_public_profile() {
  local public_host acme_email
  public_host=$(env_value PUBLIC_HOST)
  if [[ -z "$public_host" ]]; then
    if [[ "${publish_mode:-}" == tunnel ]]; then
      log_warn "CLOUDFLARE_TUNNEL_TOKEN is set but PUBLIC_HOST is empty. The tunnel was not started. Set PUBLIC_HOST=stockbot.rhionin.com in ${PANTRY_DIR}/.env."
    fi
    return 1
  fi
  if ! valid_hostname "$public_host"; then
    log_warn "PUBLIC_HOST must be a hostname such as stockbot.rhionin.com, without a scheme or path (got: $public_host). The public proxy was left unchanged."
    return 1
  fi
  if [[ -d "$PANTRY_DIR/Caddyfile" ]]; then
    fatal "$PANTRY_DIR/Caddyfile is a directory. Docker creates one when the file is missing on first start. Remove it and re-run 'sudo ./setup.sh'"
  fi
  if [[ ! -f "$PANTRY_DIR/Caddyfile" ]]; then
    fatal "Caddyfile is missing from $PANTRY_DIR"
  fi
  if [[ -d "$PANTRY_DIR/auth.caddy" ]]; then
    fatal "$PANTRY_DIR/auth.caddy is a directory. Docker creates one when the file is missing. Remove it and re-run 'sudo ./setup.sh'"
  fi

  if [[ -f "$PANTRY_DIR/auth.caddy" ]]; then
    chmod 600 "$PANTRY_DIR/auth.caddy" || fatal "Could not protect $PANTRY_DIR/auth.caddy"
    log_success "Keeping existing $PANTRY_DIR/auth.caddy"
  elif ! write_auth_from_env; then
    if [[ "${publish_mode:-}" == tunnel ]]; then
      log_warn "CLOUDFLARE_TUNNEL_TOKEN is set but $PANTRY_DIR/auth.caddy is missing and BASIC_AUTH_PASSWORD is empty. The tunnel was not started. LAN access is unchanged."
    else
      log_warn "PUBLIC_HOST is set but $PANTRY_DIR/auth.caddy is missing and BASIC_AUTH_PASSWORD is empty. The public proxy was not started. LAN access is unchanged."
    fi
    return 1
  fi

  # Tunnel mode has no certificate request. The email is only for Let's Encrypt.
  if [[ "${publish_mode:-acme}" == acme ]]; then
    acme_email=$(env_value ACME_EMAIL)
    if [[ -z "$acme_email" ]] || [[ ! "$acme_email" =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]]; then
      log_warn "Set ACME_EMAIL in $PANTRY_DIR/.env to an email address for Let's Encrypt expiry notices. The public proxy was not started."
      return 1
    fi
  elif [[ ! -f "$PANTRY_DIR/Caddyfile.tunnel" || ! -f "$PANTRY_DIR/docker-compose.tunnel.yml" ]]; then
    fatal "Tunnel mode files are missing from $PANTRY_DIR. Re-run setup from a current deploy/ directory."
  fi
  return 0
}

# remove_lan_firewall deletes the HOST_PORT source filter and its systemd unit.
remove_lan_firewall() {
  if [[ -f "$PANTRY_DIR/firewall/pantry-lan-only.sh" ]]; then
    "$PANTRY_DIR/firewall/pantry-lan-only.sh" --remove || true
  fi
  local unit_dir
  unit_dir=$(systemd_unit_dir)
  if [[ -f "$unit_dir/pantry-lan-only.service" ]]; then
    systemctl disable --now pantry-lan-only.service || true
    rm -f "$unit_dir/pantry-lan-only.service"
    systemctl daemon-reload || true
  fi
}

# apply_lan_firewall drops non-LAN clients on the published Stockbot port.
# The safe default is on: compose publishes that port, and the public proxy
# does not close it. PANTRY_LAN_FIREWALL=off removes the rule instead.
# Sets firewall_applied=true when the rule is installed.
apply_lan_firewall() {
  local flag
  flag=$(env_value PANTRY_LAN_FIREWALL)
  flag=$(printf '%s' "$flag" | tr '[:upper:]' '[:lower:]')
  case "$flag" in
    off|false|0|no)
      log_info "PANTRY_LAN_FIREWALL=$flag; removing any LAN-only rule so the published port stays reachable from any source"
      remove_lan_firewall
      firewall_applied=false
      return 0
      ;;
  esac

  local published=false
  if [[ -f "$PANTRY_DIR/docker-compose.yml" ]] && grep -q '0\.0\.0\.0:.*:8080' "$PANTRY_DIR/docker-compose.yml"; then
    published=true
  fi
  if [[ "$use_public" != true && "$published" != true ]]; then
    log_info "Compose does not publish the Stockbot port and the public proxy is off; skipping the LAN firewall"
    firewall_applied=false
    return 0
  fi

  if [[ ! -f "$PANTRY_DIR/firewall/pantry-lan-only.sh" ]]; then
    fatal "firewall/pantry-lan-only.sh is missing from $PANTRY_DIR"
  fi
  chmod +x "$PANTRY_DIR/firewall/pantry-lan-only.sh"

  local unit_dir
  unit_dir=$(systemd_unit_dir)
  if [[ -f "$PANTRY_DIR/systemd/pantry-lan-only.service" ]]; then
    mkdir -p "$unit_dir"
    cp "$PANTRY_DIR/systemd/pantry-lan-only.service" "$unit_dir/pantry-lan-only.service"
    systemctl daemon-reload
    systemctl enable --now pantry-lan-only.service
  else
    "$PANTRY_DIR/firewall/pantry-lan-only.sh"
  fi
  firewall_applied=true
  log_success "Published Stockbot port accepts LAN, loopback, and Tailscale sources"
  log_info "Ports 80 and 443 are unchanged. Opt out with PANTRY_LAN_FIREWALL=off in $PANTRY_DIR/.env, then re-run sudo ./setup.sh"
  return 0
}

# report_lan_access prints the address that works on home Wi-Fi.
# The public hostname hangs there when the router does not NAT-hairpin
# its own WAN address back to this Pi. Cellular traffic never takes that path.
report_lan_access() {
  local host_port="${1:-}" ip public_host
  if [[ ! "$host_port" =~ ^[0-9]+$ ]]; then
    host_port=8080
  fi
  if ip=$(lan_ipv4); then
    log_info "LAN: http://${ip}:${host_port}"
    # Advertise pantry.local only when this machine can publish it. A name
    # that does not resolve is worse than the IP, which always works on the LAN.
    if [[ "${PANTRY_SKIP_MDNS:-}" != 1 ]]; then
      if [[ -f "$(systemd_unit_dir)/pantry-mdns.service" ]] || command -v avahi-publish-address >/dev/null 2>&1; then
        log_info "LAN name: http://pantry.local:${host_port}"
      fi
    fi
  else
    log_info "LAN: http://<pi-address>:${host_port}"
    local configured=""
    if [[ -f "${PANTRY_DIR}/.env" ]]; then
      configured=$({ grep '^PANTRY_LAN_IPV4=' "${PANTRY_DIR}/.env" || true; } | head -1 | cut -d= -f2- | tr -d '[:space:]')
    fi
    if [[ -n "${PANTRY_LAN_IPV4:-$configured}" ]]; then
      log_warn "PANTRY_LAN_IPV4 must be a private IPv4 address such as 192.168.1.203"
    else
      log_info "Set PANTRY_LAN_IPV4 in ${PANTRY_DIR}/.env when this Pi's LAN address is not detected"
    fi
  fi
  public_host=$(env_value PUBLIC_HOST)
  # Tunnel mode reaches Cloudflare from inside the house, so the public name
  # does not depend on the router sending its own WAN address back to the Pi.
  if [[ -n "$public_host" && "${publish_mode:-acme}" == acme ]]; then
    log_warn "Home Wi-Fi cannot open https://${public_host} when the router does not hairpin NAT back to itself. That connection hangs. Use the LAN address on this network. Cellular data still uses the public name and the shared password."
  fi
}

# publish_lan_name registers pantry.local via Avahi when it is installed.
# PANTRY_SKIP_MDNS=1 is the test harness; a Pi never sets it.
publish_lan_name() {
  local host_port="$1" unit_dir services_dir
  if [[ "${PANTRY_SKIP_MDNS:-}" == 1 ]]; then
    return 0
  fi
  if [[ ! "$host_port" =~ ^[0-9]+$ ]]; then
    host_port=8080
  fi
  if [[ ! -x "$PANTRY_DIR/mdns/pantry-mdns.sh" ]]; then
    log_warn "mdns/pantry-mdns.sh is missing; pantry.local was not published"
    return 0
  fi
  if ! lan_ipv4 >/dev/null; then
    log_warn "No private LAN address found, so pantry.local was not published. Set PANTRY_LAN_IPV4 in ${PANTRY_DIR}/.env"
    return 0
  fi
  if ! command -v avahi-publish-address >/dev/null 2>&1; then
    log_info "avahi-publish-address is not installed, so pantry.local was not published. On the Pi: sudo apt-get install -y avahi-daemon && sudo ./setup.sh"
    return 0
  fi

  services_dir="${PANTRY_AVAHI_SERVICES_DIR:-}"
  if [[ -z "$services_dir" && -d /etc/avahi/services ]]; then
    services_dir=/etc/avahi/services
  fi
  # A failure to advertise the service must not abort setup. The IP URL
  # still works, and the public proxy is already running by this point.
  if [[ -n "$services_dir" ]] && mkdir -p "$services_dir" && [[ -w "$services_dir" ]]; then
    cat > "${services_dir}/pantry-http.service" << EOF
<?xml version="1.0" standalone='no'?>
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<service-group>
  <name replace-wildcards="yes">Stockbot</name>
  <service>
    <type>_http._tcp</type>
    <port>${host_port}</port>
  </service>
</service-group>
EOF
  elif [[ -n "$services_dir" ]]; then
    log_warn "Could not write ${services_dir}/pantry-http.service. The LAN IP address still works."
  fi

  if [[ ! -f "$PANTRY_DIR/systemd/pantry-mdns.service" ]]; then
    log_warn "systemd/pantry-mdns.service is missing; pantry.local was not published"
    return 0
  fi
  unit_dir=$(systemd_unit_dir)
  mkdir -p "$unit_dir"
  cp "$PANTRY_DIR/systemd/pantry-mdns.service" "$unit_dir/pantry-mdns.service"
  if ! systemctl daemon-reload \
    || ! systemctl enable pantry-mdns.service \
    || ! systemctl restart pantry-mdns.service; then
    log_warn "Could not start pantry.local publishing. The LAN IP address still works."
    return 0
  fi
  log_success "Published http://pantry.local:${host_port} on the LAN"
}

# apply_split_dns installs the optional single-name DNS answer.
# Failure here must not stop the public proxy or the LAN port.
apply_split_dns() {
  if [[ ! -f "$PANTRY_DIR/dns/pantry-split-dns.sh" ]]; then
    log_warn "dns/pantry-split-dns.sh is missing; split-horizon DNS was not configured"
    return 0
  fi
  chmod +x "$PANTRY_DIR/dns/pantry-split-dns.sh"
  if ! "$PANTRY_DIR/dns/pantry-split-dns.sh"; then
    log_warn "Split-horizon DNS was not applied. Public HTTPS and the LAN port are unchanged."
  fi
}

# refresh_update_unit copies the update units when they are already installed
# so a timer pull keeps the public profile, the 1-minute interval, and the
# deploy-hook path unit. The timer is restarted only when its file changed:
# OnBootSec is already in the past on a running Pi, so a restart elapses once.
refresh_update_unit() {
  local unit_dir
  unit_dir=$(systemd_unit_dir)
  if [[ ! -f "$unit_dir/pantry-update.service" && ! -f "$unit_dir/pantry-update.timer" ]]; then
    return 0
  fi
  if [[ ! -f "$PANTRY_DIR/systemd/pantry-update.service" || ! -f "$PANTRY_DIR/systemd/pantry-update.timer" || ! -f "$PANTRY_DIR/systemd/pantry-update.path" ]]; then
    log_warn "systemd unit files not found under $PANTRY_DIR/systemd; leaving automatic updates as they are"
    return 0
  fi
  local timer_changed=false
  if [[ -f "$unit_dir/pantry-update.timer" ]] && ! cmp -s "$PANTRY_DIR/systemd/pantry-update.timer" "$unit_dir/pantry-update.timer"; then
    timer_changed=true
  fi
  cp "$PANTRY_DIR/systemd/pantry-update.service" "$unit_dir/pantry-update.service"
  cp "$PANTRY_DIR/systemd/pantry-update.timer" "$unit_dir/pantry-update.timer"
  cp "$PANTRY_DIR/systemd/pantry-update.path" "$unit_dir/pantry-update.path"
  systemctl daemon-reload
  if systemctl is-enabled pantry-update.timer &>/dev/null; then
    systemctl enable --now pantry-update.path
    if [[ "$timer_changed" == true ]]; then
      systemctl restart pantry-update.timer
      log_info "Update timer now checks every minute. One check may run immediately."
    fi
  fi
  log_success "Refreshed pantry-update units so automatic updates keep the public proxy and the deploy hook"
}

# refresh_backup_unit recopies the backup units only when the timer is
# already installed. A first-time setup does not enable the timer: that
# happens from deploy/backup/install.sh, after the R2 settings exist.
refresh_backup_unit() {
  local unit_dir
  unit_dir=$(systemd_unit_dir)
  if [[ ! -f "$unit_dir/pantry-backup.timer" ]]; then
    return 0
  fi
  if [[ ! -f "$PANTRY_DIR/systemd/pantry-backup.service" || ! -f "$PANTRY_DIR/systemd/pantry-backup.timer" ]]; then
    log_warn "backup units not found under $PANTRY_DIR/systemd; leaving the backup timer as it is"
    return 0
  fi
  cp "$PANTRY_DIR/systemd/pantry-backup.service" "$unit_dir/pantry-backup.service"
  cp "$PANTRY_DIR/systemd/pantry-backup.timer" "$unit_dir/pantry-backup.timer"
  systemctl daemon-reload
  if systemctl is-enabled pantry-backup.timer &>/dev/null; then
    systemctl enable pantry-backup.timer
  fi
  log_success "Refreshed pantry-backup units"
}

# verify_public_sign_in asks Stockbot on the LAN port, with the public-entry
# header Caddy would add. A password file the container cannot read makes
# GET /api/session return 503. A visitor who simply has not signed in gets 401.
verify_public_sign_in() {
  local port="$1"
  local body code
  body=$(mktemp)
  if ! code=$(curl -sS -o "$body" -w '%{http_code}' --max-time 10 \
    -H 'X-Pantry-Entry: public' \
    "http://127.0.0.1:${port}/api/session"); then
    rm -f "$body"
    fatal "Could not check whether the public site can sign in."
  fi
  if [[ "$code" == "503" ]] || grep -q -F 'Sign-in is unavailable' "$body"; then
    log_error "The public site cannot read ${PANTRY_DIR}/auth/household."
    log_error "The pantry container runs as distroless nonroot (uid 65532) and cannot open a root-owned password file."
    log_error "GET /api/session with X-Pantry-Entry: public returned ${code}: $(tr '\n' ' ' < "$body")"
    rm -f "$body"
    fatal "Sign-in is unavailable. The public site is up, but login will not work until that file is readable by uid 65532."
  fi
  rm -f "$body"
}

# prepare_deploy_trigger_dir is the host directory mounted into the container.
# uid 65532 is the distroless nonroot user. If the directory is missing, Docker
# creates it as root and the hook cannot write the trigger file.
prepare_deploy_trigger_dir() {
  local dir="${PANTRY_DIR}/deploy-trigger"
  mkdir -p "$dir"
  chmod 755 "$dir"
  if ! chown 65532:65532 "$dir"; then
    if [[ "${PANTRY_SETUP_SKIP_ROOT:-}" == 1 ]]; then
      log_warn "Could not give ${dir} to uid 65532"
      return 0
    fi
    fatal "Could not prepare ${dir} for the deploy hook"
  fi
}

# ensure_deploy_hook_secret fills DEPLOY_HOOK_SECRET once. A value already
# in .env is kept, including across later setup runs.
ensure_deploy_hook_secret() {
  local current secret tmp
  current=$(env_value DEPLOY_HOOK_SECRET)
  if [[ -n "$current" ]]; then
    log_success "DEPLOY_HOOK_SECRET already set"
    return 0
  fi
  if ! command_exists openssl; then
    fatal "openssl is required to generate DEPLOY_HOOK_SECRET"
  fi
  secret=$(openssl rand -hex 32)
  if [[ ! "$secret" =~ ^[0-9a-f]{64}$ ]]; then
    fatal "Could not generate DEPLOY_HOOK_SECRET"
  fi
  tmp=$(mktemp)
  if [[ -f ${PANTRY_DIR}/.env ]] && grep -q '^DEPLOY_HOOK_SECRET=' "${PANTRY_DIR}/.env"; then
    awk -v secret="$secret" '
      BEGIN { done = 0 }
      /^DEPLOY_HOOK_SECRET=/ && done == 0 {
        print "DEPLOY_HOOK_SECRET=" secret
        done = 1
        next
      }
      { print }
      END { if (done == 0) print "DEPLOY_HOOK_SECRET=" secret }
    ' "${PANTRY_DIR}/.env" > "$tmp"
  else
    if [[ -f ${PANTRY_DIR}/.env ]]; then
      cat "${PANTRY_DIR}/.env" > "$tmp"
    fi
    printf 'DEPLOY_HOOK_SECRET=%s\n' "$secret" >> "$tmp"
  fi
  mv "$tmp" "${PANTRY_DIR}/.env"
  log_success "Generated DEPLOY_HOOK_SECRET (print it with: sudo ./setup.sh deploy-secret)"
}

# ============================================================================
# apply: sync files, containers, Caddy, and the LAN firewall
# ============================================================================
cmd_apply() {
  require_root

  # --with-updates opts into automatic updates. The timer stays as it is
  # unless this flag is passed, so a routine re-run does not disable it.
  # --tunnel selects Cloudflare Tunnel for this run. A token already in .env
  # selects it too, including on a later run without the flag.
  local with_updates=false tunnel_requested=
  if [[ $# -gt 0 ]]; then
    case "$1" in
      apply|install|publish|firewall) shift ;;
    esac
  fi
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --with-updates) with_updates=true ;;
      --tunnel) tunnel_requested=tunnel ;;
      *) log_warn "Ignoring unknown option: $1" ;;
    esac
    shift
  done

  log_info "Applying Stockbot setup..."

  if ! command_exists docker; then
    log_warn "Docker not found, installing..."
    if ! curl -fsSL https://get.docker.com | sh; then
      fatal "Failed to install Docker"
    fi
    log_success "Docker installed"
  else
    log_success "Docker already installed"
  fi

  if ! docker compose version &> /dev/null; then
    fatal "Docker Compose plugin not found after Docker installation"
  fi
  log_success "Docker Compose plugin available"

  local current_user="${SUDO_USER:-$USER}"
  if [[ -z "$current_user" ]]; then
    log_warn "Could not determine current user, skipping docker group add"
  else
    if id -nG "$current_user" | grep -qw docker; then
      log_success "User $current_user already in docker group"
    else
      usermod -aG docker "$current_user"
      log_success "Added $current_user to docker group (you may need to log out and back in)"
    fi
  fi

  if [[ ! -d "$PANTRY_DIR" ]]; then
    mkdir -p "$PANTRY_DIR"
    log_success "Created $PANTRY_DIR"
  fi

  copy_deploy_files
  cmd_reconcile_env
  ensure_deploy_hook_secret
  # shellcheck disable=SC1091
  source "$SCRIPT_DIR/vapid-env.sh"
  if ! ensure_vapid_keys; then
    fatal "Could not generate VAPID keys for scan alerts"
  fi
  if [[ -f "$PANTRY_DIR/.env" ]]; then
    chmod 600 "$PANTRY_DIR/.env"
  fi
  write_legacy_public_host
  prepare_deploy_trigger_dir

  if [[ ! -d /dev/input ]]; then
    log_warn "/dev/input does not exist, creating it (scanner nodes will not appear inside container otherwise)"
    if ! mkdir -p /dev/input; then
      # The scanner is optional. A failed mkdir must not block copying files
      # or starting the containers.
      log_warn "Could not create /dev/input. Stockbot will still start; scanner nodes appear after that directory exists."
    else
      log_success "Created /dev/input"
    fi
  else
    log_success "/dev/input exists"
  fi

  # A missing scanner must not block a file and container update. `rule`
  # still fails when the operator asks to regenerate and nothing matches.
  local rule_mode=optional
  cmd_rule

  local publish_mode mode_rc=0
  publish_mode=$(publish_mode_from_env "$tunnel_requested") || mode_rc=$?
  if [[ "$mode_rc" -eq 2 ]]; then
    fatal "Tunnel mode needs CLOUDFLARE_TUNNEL_TOKEN in ${PANTRY_DIR}/.env. Open the existing tunnel pantry-pi in Cloudflare Zero Trust, paste its token with no quotes, and re-run: sudo ./setup.sh publish --tunnel"
  elif [[ "$mode_rc" -ne 0 ]]; then
    fatal "Could not decide how to publish Stockbot"
  fi

  local use_public=false
  if prepare_public_profile; then
    use_public=true
  fi

  # The hash Caddy already checked becomes the login password. An existing
  # auth.caddy is copied, not regenerated, and a saved session secret stays.
  if ! sync_household_credential; then
    fatal "Could not copy the household password hash. The running site was left unchanged."
  fi
  if [[ -f "${PANTRY_DIR}/auth.caddy" ]]; then
    if ! ensure_session_secret; then
      fatal "Could not save the session secret. The running site was left unchanged."
    fi
    if [[ ! -s "${PANTRY_DIR}/auth/household" ]]; then
      fatal "The public site needs the password hash in ${PANTRY_DIR}/auth/household. The running site was left unchanged."
    fi
  fi

  local -a compose
  local proxy_service=caddy
  compose=(docker compose --project-directory "$PANTRY_DIR" -f "$PANTRY_DIR/docker-compose.yml")
  if [[ "$use_public" == true && "$publish_mode" == tunnel ]]; then
    proxy_service=caddy-tunnel
    compose+=(-f "$PANTRY_DIR/docker-compose.tunnel.yml" --profile tunnel)
    log_info "Starting Stockbot and Cloudflare Tunnel for https://$(env_value PUBLIC_HOST)"
  elif [[ "$use_public" == true ]]; then
    compose+=(--profile public)
    log_info "Starting Stockbot and the public HTTPS proxy for https://$(env_value PUBLIC_HOST)"
  else
    log_info "Starting Stockbot on the LAN"
  fi
  while IFS= read -r arg; do
    [[ -n "$arg" ]] && compose+=("$arg")
  done < <(grocy_compose_suffix)
  # Pull is the update script's job. Here, drop a proxy container left over
  # from the other mode before Compose tries to reuse the proxy name.
  local proxy_container cloud_container
  proxy_container=$(publish_container caddy)
  cloud_container=$(publish_container cloudflared)
  if [[ "$use_public" == true || "$publish_mode" != tunnel ]]; then
    drop_other_publish_containers "$publish_mode"
  fi
  if [[ "$use_public" == true && "$publish_mode" == tunnel ]]; then
    drop_stale_cloudflared
  fi
  # Pull before recreating so the image that checks the password is what
  # starts when Caddy stops showing its own prompt. A failed pull leaves
  # the running containers alone.
  if [[ "$use_public" == true ]]; then
    log_info "Pulling the Stockbot image before reloading the public site"
    if ! "${compose[@]}" pull pantry; then
      fatal "Could not pull the Stockbot image. The running site was left unchanged."
    fi
  fi
  if ! "${compose[@]}" up -d; then
    fatal "Failed to start Stockbot"
  fi

  if [[ "$use_public" == true ]]; then
    # admin is off in the Caddyfile, so `caddy reload` cannot apply a new
    # file. Restarting the proxy re-reads the bind-mounted Caddyfile.
    local proxy_deadline=$((SECONDS + 20))
    local running=false
    while [[ $SECONDS -lt $proxy_deadline ]]; do
      if docker inspect -f '{{.State.Running}}' "$proxy_container" 2>/dev/null | grep -qx true; then
        running=true
        break
      fi
      sleep 1
    done
    if [[ "$running" != true ]]; then
      log_error "The proxy container exited. Recent logs:"
      "${compose[@]}" logs --tail=80 "$proxy_service" || true
      fatal "Public proxy did not stay running"
    fi
    log_info "Restarting the public proxy so the current Caddyfile is loaded"
    if ! docker restart "$proxy_container" >/dev/null; then
      "${compose[@]}" logs --tail=80 "$proxy_service" || true
      fatal "Could not restart the public proxy"
    fi
    if [[ "$publish_mode" == tunnel ]]; then
      local tunnel_deadline=$((SECONDS + 20))
      running=false
      while [[ $SECONDS -lt $tunnel_deadline ]]; do
        if docker inspect -f '{{.State.Running}}' "$cloud_container" 2>/dev/null | grep -qx true; then
          running=true
          break
        fi
        sleep 1
      done
      if [[ "$running" != true ]]; then
        log_error "Cloudflare Tunnel exited. Recent logs:"
        "${compose[@]}" logs --tail=80 cloudflared || true
        fatal "Cloudflare Tunnel did not stay running. Check CLOUDFLARE_TUNNEL_TOKEN in ${PANTRY_DIR}/.env."
      fi
    fi
  fi

  # The unit's ExecStart points at the script copy_deploy_files just refreshed.
  # Recopy the unit itself when it is already installed so a timer keeps the
  # public profile without enabling the timer on a LAN-only box.
  refresh_update_unit
  refresh_backup_unit

  log_info "Waiting for Stockbot to become healthy..."
  local deadline=$((SECONDS + 60))
  local host_port
  host_port=$(env_value HOST_PORT)
  host_port=${host_port:-8080}
  local healthy=false
  while [[ $SECONDS -lt $deadline ]]; do
    if curl -sf "http://localhost:$host_port/health" > /dev/null 2>&1; then
      healthy=true
      break
    fi
    sleep 2
  done
  if [[ "$healthy" != true ]]; then
    log_error "Stockbot failed to become healthy within 60 seconds"
    log_error "Recent logs:"
    docker compose -f "$PANTRY_DIR/docker-compose.yml" logs --tail=50 pantry || true
    fatal "Health check timeout"
  fi
  log_success "Stockbot is healthy"

  if [[ "$use_public" == true ]]; then
    verify_public_sign_in "$host_port"
  fi

  local firewall_applied=false
  apply_lan_firewall

  if [[ "$with_updates" == true ]]; then
    log_info "Installing automatic-update systemd units (--with-updates)..."
    local unit_dir
    unit_dir=$(systemd_unit_dir)
    if [[ -f "$PANTRY_DIR/systemd/pantry-update.service" && -f "$PANTRY_DIR/systemd/pantry-update.timer" && -f "$PANTRY_DIR/systemd/pantry-update.path" ]]; then
      mkdir -p "$unit_dir"
      cp "$PANTRY_DIR/systemd/pantry-update.service" "$unit_dir/"
      cp "$PANTRY_DIR/systemd/pantry-update.timer" "$unit_dir/"
      cp "$PANTRY_DIR/systemd/pantry-update.path" "$unit_dir/"
      systemctl daemon-reload
      systemctl enable --now pantry-update.timer
      systemctl enable --now pantry-update.path
      log_success "pantry-update.timer and pantry-update.path enabled (updates will run automatically)"
    else
      log_warn "systemd unit files not found under $PANTRY_DIR/systemd; skipping update timer"
    fi
  else
    log_info "Automatic updates were not changed (pass --with-updates to enable the timer)"
  fi

  publish_lan_name "$host_port"
  if [[ "$use_public" == true && "$publish_mode" == tunnel ]]; then
    # An answer that points the public name at the Pi bypasses Cloudflare,
    # and Caddy is not listening on the LAN address in this mode.
    local split_flag
    split_flag=$(env_value PANTRY_SPLIT_DNS)
    split_flag=$(printf '%s' "$split_flag" | tr '[:upper:]' '[:lower:]')
    case "$split_flag" in
      on|true|yes|1)
        log_warn "PANTRY_SPLIT_DNS is on. Tunnel mode needs $(env_value PUBLIC_HOST) to resolve to Cloudflare on home Wi-Fi too. Split-horizon DNS was removed. Set PANTRY_SPLIT_DNS=off in ${PANTRY_DIR}/.env."
        ;;
    esac
    if [[ -f "$PANTRY_DIR/dns/pantry-split-dns.sh" ]]; then
      "$PANTRY_DIR/dns/pantry-split-dns.sh" --remove || log_warn "Could not remove split-horizon DNS. Clients that use this Pi for DNS may still miss the tunnel."
    fi
  else
    apply_split_dns
  fi

  log_success "Stockbot setup complete"
  report_lan_access "$host_port"
  if [[ "$use_public" == true && "$publish_mode" == tunnel ]]; then
    log_info "Public: https://$(env_value PUBLIC_HOST) through Cloudflare Tunnel (shared password; the LAN address does not ask for it). Nothing on this Pi is listening on ports 80 or 443."
    log_info "In Zero Trust, the public hostname service URL is ${PANTRY_TUNNEL_ORIGIN}"
    log_info "Open that https URL on home Wi-Fi and on cellular, then remove the router's forwards for ports 80 and 443."
  elif [[ "$use_public" == true ]]; then
    log_info "Public: https://$(env_value PUBLIC_HOST) (shared password; the LAN address does not ask for it)"
  fi
  if [[ "$firewall_applied" == true ]]; then
    log_info "Non-LAN clients are blocked on port $host_port"
  fi
  log_info "Check: sudo ./setup.sh status"
}

cmd_install() {
  log_info "install runs the full setup (same as sudo ./setup.sh)"
  cmd_apply "$@"
}

cmd_publish() {
  log_info "publish runs the full setup (same as sudo ./setup.sh)"
  cmd_apply "$@"
}

cmd_firewall() {
  log_info "firewall runs the full setup (same as sudo ./setup.sh)"
  cmd_apply "$@"
}

# ============================================================================
# reconcile_env: Idempotent .env management
# ============================================================================
cmd_reconcile_env() {
  log_info "Reconciling ${PANTRY_DIR}/.env..."

  # Create from example if absent
  if [[ ! -f ${PANTRY_DIR}/.env ]]; then
    cp "${PANTRY_DIR}/.env.example" "${PANTRY_DIR}/.env"
    log_success "Created ${PANTRY_DIR}/.env from .env.example"
    return
  fi

  # Migrate the obsolete default SCANNER_DEVICE path. A .env written by a
  # pre-hotplug version pinned SCANNER_DEVICE=/dev/pantry-scanner. That was
  # never an operator choice — it was the old default — and the container can
  # no longer open it, since the symlink now lives under /dev/input. Rewrite
  # only that exact obsolete default; a custom path an operator set on purpose
  # (anything other than the old default) is left untouched.
  if grep -q '^SCANNER_DEVICE=/dev/pantry-scanner$' "${PANTRY_DIR}/.env"; then
    sed -i 's|^SCANNER_DEVICE=/dev/pantry-scanner$|SCANNER_DEVICE=/dev/input/pantry-scanner|' "${PANTRY_DIR}/.env"
    log_warn "Migrated obsolete SCANNER_DEVICE=/dev/pantry-scanner -> /dev/input/pantry-scanner"
  fi

  # Append missing keys from .env.example
  local added=0
  while IFS='=' read -r key value; do
    # Skip comments and empty lines
    [[ "$key" =~ ^# ]] && continue
    [[ -z "$key" ]] && continue

    # An `export KEY=` line is already set. Do not add a second KEY=.
    if ! env_has_key "$key"; then
      echo "${key}=${value}" >> "${PANTRY_DIR}/.env"
      log_info "Added missing key: $key"
      added=$((added + 1))
    fi
  done < "${PANTRY_DIR}/.env.example"

  append_empty_env_keys SENTRY_DSN SENTRY_ENVIRONMENT SENTRY_TUNNEL_PROJECT_IDS SENTRY_TUNNEL_PUBLIC_KEYS

  if [[ $added -gt 0 ]]; then
    log_success "Added $added missing keys to .env"
  else
    log_success ".env is up to date"
  fi
}

# env_has_key is true when KEY= or export KEY= is already in .env.
env_has_key() {
  grep -qE "^(export[[:space:]]+)?${1}=" "${PANTRY_DIR}/.env"
}

# append_empty_env_keys adds KEY= lines that are missing. An existing value,
# including one written as `export KEY=`, is left as it is, and the file mode
# is not changed.
append_empty_env_keys() {
  local key
  # cmd_reconcile_env owns added. A direct call starts from zero.
  if [[ -z ${added+x} ]]; then
    added=0
  fi
  [[ -f ${PANTRY_DIR}/.env ]] || return 0
  # wc -l counts the trailing newline. A file that already ends with one
  # stays as it is; appending a key never rewrites earlier lines.
  if [[ -s ${PANTRY_DIR}/.env ]] && [[ $(tail -c 1 "${PANTRY_DIR}/.env" | wc -l) -eq 0 ]]; then
    printf '\n' >> "${PANTRY_DIR}/.env"
  fi
  for key in "$@"; do
    if ! env_has_key "$key"; then
      printf '%s=\n' "$key" >> "${PANTRY_DIR}/.env"
      log_info "Added missing key: $key"
      added=$((added + 1))
    fi
  done
}

# ============================================================================
# rule: Generate and install udev rule
# ============================================================================
cmd_rule() {
  require_root
  log_info "Generating udev rule for barcode scanner..."

  # Find candidates from the input device table. Each device block has an
  # "I:" line (Bus/Vendor/Product) and an "N: Name=..." line. Vendor and
  # Product are the input device's ids, for USB HID and Bluetooth HID.
  local candidate_names=() candidate_vids=() candidate_pids=()
  if [[ -f "$PANTRY_INPUT_DEVICES" ]]; then
    local cur_vid="" cur_pid=""
    while IFS= read -r line; do
      if [[ "$line" =~ ^I:\ Bus= ]]; then
        # e.g. "I: Bus=0003 Vendor=05e0 Product=1200 Version=0100"
        cur_vid=$(printf '%s\n' "$line" | sed -n 's/.*Vendor=\([0-9a-fA-F]*\).*/\1/p')
        cur_pid=$(printf '%s\n' "$line" | sed -n 's/.*Product=\([0-9a-fA-F]*\).*/\1/p')
      elif [[ "$line" =~ ^N:\ Name= ]]; then
        local name="${line#*Name=\"}"
        name="${name%\"*}"
        # Filter for likely scanner devices (avoid generic HID keyboards).
        # nocasematch makes the =~ test case-insensitive so "Scanner",
        # "BARCODE", etc. all match; it is restored immediately after.
        shopt -s nocasematch
        if [[ "$name" =~ (scanner|barcode) ]]; then
          candidate_names+=("$name")
          candidate_vids+=("$cur_vid")
          candidate_pids+=("$cur_pid")
        fi
        shopt -u nocasematch
      fi
    done < "$PANTRY_INPUT_DEVICES"
  fi

  # Show candidates or use first
  if [[ ${#candidate_names[@]} -eq 0 ]]; then
    if [[ "${rule_mode:-required}" == "optional" ]]; then
      log_warn "No scanner candidates found; leaving udev rules unchanged. Plug in a scanner and run 'sudo ./setup.sh rule' to generate one."
      return 0
    fi
    log_warn "No scanner candidates found in $PANTRY_INPUT_DEVICES"
    log_info "Devices available:"
    grep "^N: " "$PANTRY_INPUT_DEVICES" || true
    fatal "Please manually create the udev rule at $PANTRY_UDEV_RULE_FILE"
  fi

  if [[ ${#candidate_names[@]} -gt 1 ]]; then
    log_warn "Multiple scanner candidates found. Using the first:"
    log_info "${candidate_names[0]}"
  else
    log_success "Found scanner: ${candidate_names[0]}"
  fi

  local scanner_name="${candidate_names[0]}"
  local scanner_vid="${candidate_vids[0]}"
  local scanner_pid="${candidate_pids[0]}"

  # Match the input device parent. The kernel exposes id/vendor and
  # id/product there for USB HID and Bluetooth HID (uhid), and both keys
  # sit on that same parent. udev imports ENV{ID_VENDOR_ID} and
  # ENV{ID_MODEL_ID} only when a USB ancestor exists, so a Bluetooth
  # scanner never matches a rule written with those properties. Fall
  # back to the device name only when the IDs are absent.
  local rule_content
  if [[ -n "$scanner_vid" && -n "$scanner_pid" ]]; then
    local vid_lc pid_lc
    vid_lc=$(printf '%s' "$scanner_vid" | tr '[:upper:]' '[:lower:]')
    pid_lc=$(printf '%s' "$scanner_pid" | tr '[:upper:]' '[:lower:]')
    log_info "Matching on vendor=$vid_lc product=$pid_lc"
    rule_content="# Stockbot Scanner udev rule - auto-generated for \"$scanner_name\"
SUBSYSTEM==\"input\", KERNEL==\"event*\", ATTRS{id/vendor}==\"$vid_lc\", ATTRS{id/product}==\"$pid_lc\", \\
  SYMLINK+=\"input/pantry-scanner\", GROUP=\"65532\", MODE=\"0640\"
"
  else
    log_info "No vendor/product IDs found; matching on device name"
    rule_content="# Stockbot Scanner udev rule - auto-generated
SUBSYSTEM==\"input\", KERNEL==\"event*\", ATTRS{name}==\"$scanner_name\", \\
  SYMLINK+=\"input/pantry-scanner\", GROUP=\"65532\", MODE=\"0640\"
"
  fi

  local rule_file="$PANTRY_UDEV_RULE_FILE"

  # Only write if the installed content differs from what we would generate.
  # Comparing the whole content (not just the match key) means an outdated
  # rule — e.g. one from a pre-hotplug version whose SYMLINK was still
  # "pantry-scanner" instead of "input/pantry-scanner", or an ENV match that
  # misses Bluetooth HID — is rewritten on re-run. Command substitution
  # drops the file's trailing newline, so the generated text is compared
  # the same way.
  local installed=""
  if [[ -f "$rule_file" ]]; then
    installed=$(cat "$rule_file")
  fi
  if [[ ! -f "$rule_file" ]] || [[ "$installed" != "${rule_content%$'\n'}" ]]; then
    printf '%s' "$rule_content" > "$rule_file"
    log_success "Wrote $rule_file"
  else
    log_success "$rule_file already up to date"
  fi

  # Reload and trigger udev
  udevadm control --reload
  udevadm trigger --subsystem-match=input --action=add
  log_success "udev rules reloaded and triggered"
}

# ============================================================================
# status: Diagnostic chain
# ============================================================================
cmd_status() {
  require_root
  log_info "Diagnosing Stockbot deployment chain..."
  echo ""

  local failed=0

  # 1. Deployment files
  if [[ -d ${PANTRY_DIR} ]]; then
    log_success "Deployment directory exists"
  else
    log_error "Deployment directory ${PANTRY_DIR} missing"
    failed=$((failed + 1))
  fi

  # 2. .env file
  local image_tag="" host_port="" scanner_device="" public_host=""
  if [[ -f ${PANTRY_DIR}/.env ]]; then
    image_tag=$(grep "^PANTRY_IMAGE_TAG=" "${PANTRY_DIR}/.env" | cut -d= -f2 || echo "latest")
    host_port=$(grep "^HOST_PORT=" "${PANTRY_DIR}/.env" | cut -d= -f2 || echo "8080")
    scanner_device=$(grep "^SCANNER_DEVICE=" "${PANTRY_DIR}/.env" | cut -d= -f2 || echo "/dev/input/pantry-scanner")
    public_host=$(env_value PUBLIC_HOST)
    log_success ".env present: PANTRY_IMAGE_TAG=$image_tag, HOST_PORT=$host_port, SCANNER_DEVICE=$scanner_device"
  else
    log_error ".env not found at ${PANTRY_DIR}/.env"
    failed=$((failed + 1))
  fi

  # 3. /dev/input exists
  if [[ -d /dev/input ]]; then
    local fs_type
    fs_type=$(stat -f /dev/input -c "%T" 2>/dev/null || echo "unknown")
    if [[ "$fs_type" == "tmpfs" ]] || [[ "$fs_type" == "devtmpfs" ]]; then
      log_success "/dev/input exists on devtmpfs"
    else
      log_warn "/dev/input exists but is on $fs_type (expect devtmpfs)"
    fi
  else
    log_error "/dev/input does not exist"
    failed=$((failed + 1))
  fi

  # 4. udev rule exists
  if [[ -f "$PANTRY_UDEV_RULE_FILE" ]]; then
    if grep -q "SYMLINK.*input/pantry-scanner" "$PANTRY_UDEV_RULE_FILE"; then
      log_success "udev rule installed with symlink at /dev/input/pantry-scanner"
    else
      log_error "udev rule exists but does not set symlink to /dev/input/pantry-scanner"
      failed=$((failed + 1))
    fi
  else
    log_warn "udev rule not installed at $PANTRY_UDEV_RULE_FILE"
  fi

  # 5. Scanner device resolves to a real node, and 6. that node is group
  #    65532 / mode 0640. A missing scanner is a SUPPORTED state, not a
  #    failure — the container still runs and /health still returns ok — so
  #    it is reported as PASS-with-note. What operators care about is the
  #    other case: a scanner PRESENT but unreadable.
  if [[ -n "${scanner_device:-}" ]]; then
    # readlink -f succeeds on a non-existent path (it just echoes it back),
    # so test for actual existence with -e, not readlink's exit status.
    if [[ -e "$scanner_device" ]]; then
      local resolved
      resolved=$(readlink -f "$scanner_device")
      log_success "Scanner device resolves: $scanner_device -> $resolved"

      # 6. Group and mode on the resolved node.
      local node_group node_mode
      node_group=$(stat -c "%g" "$resolved" 2>/dev/null || echo "?")
      node_mode=$(stat -c "%a" "$resolved" 2>/dev/null || echo "?")
      if [[ "$node_group" == "65532" && "$node_mode" == "640" ]]; then
        log_success "Scanner node is group 65532 mode 0640 (readable by the container)"
      else
        log_error "Scanner node is group $node_group mode $node_mode, expected group 65532 mode 0640 — scanner is PRESENT but the container cannot read it (EACCES). Fix GROUP in the udev rule or SCANNER_GID in .env, then: sudo ./setup.sh rule"
        failed=$((failed + 1))
      fi
    else
      log_warn "Scanner device $scanner_device does not exist — scanner absent (SUPPORTED: container runs and /health is ok). If a scanner IS attached, the udev rule did not match (ENOENT): fix its match keys and run 'sudo ./setup.sh rule'"
    fi
  fi

  # 6. Container running
  if docker compose -f "${PANTRY_DIR}/docker-compose.yml" ps 2>/dev/null | grep -q "pantry.*Up"; then
    log_success "Stockbot container is running"
  else
    log_error "Stockbot container is not running"
    failed=$((failed + 1))
  fi

  # 7. Health check
  if [[ -n "${host_port:-}" ]]; then
    if curl -sf "http://localhost:$host_port/health" | grep -q '"status":"ok"'; then
      log_success "GET /health returns ok"
    else
      log_error "GET /health failed or timed out"
      failed=$((failed + 1))
    fi
  fi

  # 8. Public proxy. Empty PUBLIC_HOST is the LAN-only default, not a failure.
  local publish_mode mode_rc=0
  if [[ -f ${PANTRY_DIR}/.env ]]; then
    publish_mode=$(publish_mode_from_env) || mode_rc=$?
  else
    publish_mode=none
  fi
  if [[ "$mode_rc" -eq 2 ]]; then
    log_error "Tunnel mode is selected but CLOUDFLARE_TUNNEL_TOKEN is empty. Put the token in ${PANTRY_DIR}/.env and run: sudo ./setup.sh publish --tunnel"
    failed=$((failed + 1))
    publish_mode=tunnel
  elif [[ "$mode_rc" -ne 0 ]]; then
    log_error "PUBLISH_MODE must be acme or tunnel"
    failed=$((failed + 1))
    publish_mode=none
  fi
  local proxy_container cloud_container
  proxy_container=$(publish_container caddy)
  cloud_container=$(publish_container cloudflared)
  if [[ "$publish_mode" == tunnel ]]; then
    if [[ ! -f ${PANTRY_DIR}/auth.caddy ]]; then
      log_warn "CLOUDFLARE_TUNNEL_TOKEN is set but ${PANTRY_DIR}/auth.caddy is missing, so the tunnel will not start. Set BASIC_AUTH_PASSWORD and run: sudo ./setup.sh publish --tunnel"
    elif docker inspect -f '{{.State.Running}}' "$proxy_container" 2>/dev/null | grep -qx true \
      && docker inspect -f '{{.State.Running}}' "$cloud_container" 2>/dev/null | grep -qx true; then
      log_success "Cloudflare Tunnel is running for https://$public_host (shared password required). Service URL: ${PANTRY_TUNNEL_ORIGIN}. Ports 80 and 443 are not published."
    else
      log_warn "CLOUDFLARE_TUNNEL_TOKEN is set but the tunnel is not running. Start it with: sudo ./setup.sh publish --tunnel"
    fi
  elif [[ -n "$public_host" ]]; then
    if [[ ! -f ${PANTRY_DIR}/auth.caddy ]]; then
      log_warn "PUBLIC_HOST=$public_host but ${PANTRY_DIR}/auth.caddy is missing, so the proxy will not start. Set BASIC_AUTH_PASSWORD and run: sudo ./setup.sh"
    elif docker inspect -f '{{.State.Running}}' "$proxy_container" 2>/dev/null | grep -qx true; then
      log_success "Public proxy is running for https://$public_host (shared password required)"
    else
      log_warn "PUBLIC_HOST=$public_host but the proxy container is not running. Start it with: sudo ./setup.sh"
    fi
  else
    log_info "Public internet access is not configured (PUBLIC_HOST is empty)"
  fi

  report_lan_access "${host_port:-}"

  echo ""
  if [[ $failed -eq 0 ]]; then
    log_success "All checks passed"
  else
    log_error "$failed check(s) failed"
    exit 1
  fi
}

# ============================================================================
# unpublish: stop HTTPS proxy, leave the LAN service running
# ============================================================================
cmd_unpublish() {
  require_root
  if [[ ! -f ${PANTRY_DIR}/docker-compose.yml ]]; then
    fatal "Nothing installed at ${PANTRY_DIR}. Run 'sudo ./setup.sh' first"
  fi
  log_info "Stopping the public HTTPS proxy and Cloudflare Tunnel. Stockbot keeps running on the LAN."
  docker compose --project-directory "${PANTRY_DIR}" -f "${PANTRY_DIR}/docker-compose.yml" --profile public stop caddy || true
  docker compose --project-directory "${PANTRY_DIR}" -f "${PANTRY_DIR}/docker-compose.yml" --profile public rm -f caddy || true
  if [[ -f "${PANTRY_DIR}/docker-compose.tunnel.yml" ]]; then
    docker compose --project-directory "${PANTRY_DIR}" -f "${PANTRY_DIR}/docker-compose.yml" -f "${PANTRY_DIR}/docker-compose.tunnel.yml" --profile tunnel stop caddy-tunnel cloudflared || true
    docker compose --project-directory "${PANTRY_DIR}" -f "${PANTRY_DIR}/docker-compose.yml" -f "${PANTRY_DIR}/docker-compose.tunnel.yml" --profile tunnel rm -f caddy-tunnel cloudflared || true
  fi
  remove_project_container "$(publish_container cloudflared)" >/dev/null 2>&1 || true
  log_success "Public proxy stopped. The certificate volume was kept so a later setup can reuse it."
  log_info "Clear PUBLIC_HOST and CLOUDFLARE_TUNNEL_TOKEN in ${PANTRY_DIR}/.env if automatic updates should not start the proxy again."
}

cmd_firewall_off() {
  require_root
  remove_lan_firewall
  log_success "Removed the LAN-only rule. The published port is reachable from any source again."
  log_info "The next 'sudo ./setup.sh' installs that rule again unless PANTRY_LAN_FIREWALL=off is set in $PANTRY_DIR/.env."
}

# ============================================================================
# logs: Follow container logs
# ============================================================================
cmd_logs() {
  require_root
  log_info "Following Stockbot container logs (Ctrl+C to stop)..."
  docker compose -f "${PANTRY_DIR}/docker-compose.yml" logs --tail=100 -f pantry || true
}

# ============================================================================
# freeze: Mask the update timer
# ============================================================================
# freeze_unit masks and stops one update unit. A running path unit would
# still deploy when a hook arrives, so freeze has to stop it, not only mask it.
freeze_unit() {
  local unit="$1"
  if [[ ! -f "/etc/systemd/system/${unit}" ]]; then
    return 1
  fi
  if systemctl is-enabled "$unit" &>/dev/null; then
    systemctl mask "$unit"
    systemctl stop "$unit" || true
    log_success "Masked ${unit} (updates frozen during iteration)"
  else
    log_success "${unit} already masked or disabled"
  fi
  return 0
}

cmd_freeze() {
  require_root
  local found=false
  if freeze_unit pantry-update.timer; then
    found=true
  fi
  if freeze_unit pantry-update.path; then
    found=true
  fi
  if [[ "$found" == false ]]; then
    log_warn "pantry-update.timer not installed"
  fi
}

# ============================================================================
# thaw: Unmask the update timer
# ============================================================================
# thaw_unit undoes freeze. Only a masked unit is started again, so a timer
# that was never enabled stays off.
thaw_unit() {
  local unit="$1" state
  if [[ ! -f "/etc/systemd/system/${unit}" ]]; then
    return 1
  fi
  state=$(systemctl is-enabled "$unit" 2>/dev/null || true)
  if [[ "$state" == enabled ]]; then
    log_success "${unit} already enabled"
  elif [[ "$state" == masked ]]; then
    systemctl unmask "$unit"
    systemctl start "$unit"
    log_success "Unmasked ${unit} (updates will resume)"
  else
    log_success "${unit} is not enabled"
  fi
  return 0
}

cmd_thaw() {
  require_root
  local found=false
  if thaw_unit pantry-update.timer; then
    found=true
  fi
  if thaw_unit pantry-update.path; then
    found=true
  fi
  if [[ "$found" == false ]]; then
    log_warn "pantry-update.timer not installed"
  fi
}

# cmd_deploy_secret prints the hook secret and nothing else, so it can be
# copied into the DEPLOY_HOOK_SECRET GitHub Actions secret.
cmd_deploy_secret() {
  require_root
  if [[ ! -f ${PANTRY_DIR}/.env ]]; then
    fatal "No ${PANTRY_DIR}/.env. Run sudo ./setup.sh first."
  fi
  local secret
  secret=$(env_value DEPLOY_HOOK_SECRET)
  if [[ -z "$secret" ]]; then
    fatal "DEPLOY_HOOK_SECRET is empty. Run sudo ./setup.sh to generate it."
  fi
  printf '%s\n' "$secret"
}

# ============================================================================
# help: Show usage
# ============================================================================
cmd_help() {
  cat << 'EOF'
Stockbot Deployment Setup Script

USAGE:
  sudo ./setup.sh [COMMAND] [OPTIONS]

After git pull, from deploy/:

  sudo ./setup.sh

That syncs this folder to /opt/pantry, starts the containers (including the
public HTTPS proxy when PUBLIC_HOST is set and auth.caddy already exists),
restarts Caddy so the current Caddyfile is loaded, and applies the LAN
firewall on the published Stockbot port. It is safe to re-run. When
CLOUDFLARE_TUNNEL_TOKEN is set, the same command publishes through
Cloudflare Tunnel instead of ports 80 and 443.

COMMANDS:
  apply            Same as running setup.sh with no command.
                   Pass --with-updates to enable the automatic-update timer
                   and the deploy-hook path unit (left unchanged otherwise).
                   The timer fires every minute, and a signed hook starts the
                   same update immediately. Leave both off while iterating.
  install          Alias of apply.
  publish          Alias of apply. Does not rewrite /opt/pantry/auth.caddy when
                   that file already exists, and does not prompt for a password.
                   Pass --tunnel to publish through Cloudflare Tunnel. A token
                   already in .env selects tunnel without the flag.
  firewall         Alias of apply. The LAN port rule is part of apply.
  unpublish        Stop the HTTPS proxy. Stockbot keeps running on the LAN.
  firewall-off     Remove the LAN port rule until the next setup. To leave it
                   off, set PANTRY_LAN_FIREWALL=off in /opt/pantry/.env.
  rule             Regenerate and install udev rule for current scanner
  status           Diagnose the deployment chain and report issues
  logs             Follow container logs (Ctrl+C to stop)
  freeze           Mask the update timer and the deploy hook during iteration
  thaw             Unmask them to resume automatic updates
  deploy-secret    Print DEPLOY_HOOK_SECRET for the GitHub Actions secret
  help             Show this message

EXAMPLES:
  # First install, and every update after git pull
  sudo ./setup.sh

  # Check status after setup or troubleshooting
  sudo ./setup.sh status

  # Change the shared password: set BASIC_AUTH_PASSWORD, delete auth.caddy, re-run
  sudo rm /opt/pantry/auth.caddy
  sudo ./setup.sh

  # Publish https://stockbot.rhionin.com through Cloudflare Tunnel.
  # Put CLOUDFLARE_TUNNEL_TOKEN in /opt/pantry/.env first. The service URL
  # saved on the tunnel hostname is http://caddy:80
  sudo ./setup.sh publish --tunnel

  # Iterate on configuration
  sudo ./setup.sh freeze          # Pause auto-updates
  # ... make changes to /opt/pantry/.env ...
  sudo ./setup.sh
  sudo ./setup.sh thaw            # Resume auto-updates

NOTES:
  - The container is distroless, so 'docker exec pantry sh' does not work.
  - Use 'sudo ./setup.sh logs' and GET /health for runtime introspection.
  - Existing .env values are preserved. PANTRY_IMAGE_TAG and HOST_PORT are
    never overwritten if already set.
  - LAN http://<pi-address>:8080 keeps working. The firewall allows LAN,
    loopback, and Tailscale, and does not change ports 80 or 443.
  - On home Wi-Fi, https://PUBLIC_HOST hangs when the router does not
    hairpin NAT. Use the LAN address or http://pantry.local:8080.
    Cellular data still uses the public name and the shared password.
  - `sudo ./setup.sh publish --tunnel` avoids that hang. Caddy listens only
    for cloudflared, at http://caddy:80, and ports 80 and 443 stay closed.
    Empty CLOUDFLARE_TUNNEL_TOKEN to return to the certificate path. An empty
    token does not stop the Dynu update client or change the router forwards.
EOF
}

# ============================================================================
# Main dispatch
# ============================================================================
main() {
  local cmd="${1:-apply}"

  case "$cmd" in
    help|--help|-h) cmd_help ;;
    --*)      cmd_apply apply "$@" ;;
    apply)    cmd_apply "$@" ;;
    install)  cmd_install "$@" ;;
    publish)  cmd_publish "$@" ;;
    unpublish) cmd_unpublish ;;
    firewall) cmd_firewall "$@" ;;
    firewall-off) cmd_firewall_off ;;
    rule)     cmd_rule ;;
    status)   cmd_status ;;
    logs)     cmd_logs ;;
    freeze)   cmd_freeze ;;
    thaw)     cmd_thaw ;;
    deploy-secret) cmd_deploy_secret ;;
    *)        fatal "Unknown command: $cmd (try 'help')" ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
