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
#   sudo ./setup.sh                  # Sync files, containers, and Caddy
#   sudo ./setup.sh apply            # Same command, explicit name
#   sudo ./setup.sh --with-updates   # ...and enable the auto-update timer and deploy hook
#   sudo ./setup.sh publish --tunnel # Publish https://PUBLIC_HOST through Cloudflare Tunnel
#   sudo ./setup.sh unpublish        # Stop the public proxy; LAN pantry keeps running
#   sudo ./setup.sh firewall-off     # Remove leftover LAN firewall and mDNS units
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
source "$SCRIPT_DIR/runtime-setup.sh"

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

# copy_deploy_files refreshes ${PANTRY_DIR} from this script's directory.
# .env is never copied, so a re-run cannot clobber operator settings.
# Copying a file onto itself (running the already-installed script) is skipped.
copy_deploy_files() {
  log_info "Copying deployment files to ${PANTRY_DIR}..."
  mkdir -p "${PANTRY_DIR}"
  local item src dest
  for item in docker-compose.yml docker-compose.tunnel.yml docker-compose.grocy.yml .env.example Caddyfile Caddyfile.tunnel publish-mode.sh runtime-setup.sh vapid-env.sh udev systemd; do
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
  log_success "Deployment files copied"
}

# systemd_unit_dir is where pantry-lan-only.service and pantry-update.service live.
systemd_unit_dir() {
  printf '%s\n' "$PANTRY_SYSTEMD_UNIT_DIR"
}

# valid_hostname is 0 when $1 is a DNS name with no scheme, port, or path.
valid_hostname() {
  local host="$1"
  [[ "$host" =~ ^[A-Za-z0-9.-]+$ && "$host" == *.* && "$host" != .* && "$host" != *. ]]
}

# prepare_public_profile is 0 when compose should include the public profile.
# PUBLIC_HOST, Caddyfile, and (for certificate mode) ACME_EMAIL must be valid.
# A leftover auth.caddy file on the host is ignored and never deleted.
prepare_public_profile() {
  local public_host acme_email
  public_host=$(env_value PUBLIC_HOST)
  if [[ -z "$public_host" ]]; then
    if [[ "${publish_mode:-}" == tunnel ]]; then
      log_warn "CLOUDFLARE_TUNNEL_TOKEN is set but PUBLIC_HOST is empty. The tunnel was not started. Set PUBLIC_HOST in ${PANTRY_DIR}/.env."
    fi
    return 1
  fi
  if ! valid_hostname "$public_host"; then
    log_warn "PUBLIC_HOST must be a hostname without a scheme or path (got: $public_host). The public proxy was left unchanged."
    return 1
  fi
  if [[ -d "$PANTRY_DIR/Caddyfile" ]]; then
    fatal "$PANTRY_DIR/Caddyfile is a directory. Docker creates one when the file is missing on first start. Remove it and re-run 'sudo ./setup.sh'"
  fi
  if [[ ! -f "$PANTRY_DIR/Caddyfile" ]]; then
    fatal "Caddyfile is missing from $PANTRY_DIR"
  fi

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

# report_site prints the public URL. Certificate mode still depends on the
# router hairpinning its own WAN address. Tunnel mode does not.
report_site() {
  local public_host mode
  public_host=$(env_value PUBLIC_HOST)
  mode="${publish_mode:-acme}"
  if [[ -n "$public_host" && "$mode" == acme ]]; then
    log_warn "Home Wi-Fi cannot open https://${public_host} when the router does not hairpin NAT back to itself. That connection hangs. Use Cloudflare Tunnel for this hostname. Cellular data still uses the public name."
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

# verify_public_sign_in asks the loopback listener. A visitor who has not
# signed in gets 401 on the stage-1 session route.
verify_public_sign_in() {
  local host_port body code
  host_port=$(env_value HOST_PORT)
  host_port=${host_port:-8080}
  body=$(mktemp)
  if ! code=$(curl -sS -o "$body" -w '%{http_code}' --max-time 10 \
    "http://127.0.0.1:${host_port}/api/auth/session"); then
    rm -f "$body"
    fatal "Could not check whether the site can sign in."
  fi
  if [[ "$code" != "401" ]]; then
    log_error "GET /api/auth/session returned ${code}: $(tr '\n' ' ' < "$body")"
    rm -f "$body"
    fatal "Sign-in is not enforced on http://127.0.0.1:${host_port}/api/auth/session."
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
  if ! ensure_app_base_url; then
    fatal "Could not set APP_BASE_URL from PUBLIC_HOST"
  fi
  prepare_deploy_trigger_dir
  if ! prepare_setup_dir; then
    fatal "Could not prepare ${PANTRY_DIR}/setup for the setup code"
  fi

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

  if [[ "$use_public" == true ]]; then
    if ! ensure_session_secret; then
      fatal "Could not save the session secret. The running site was left unchanged."
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
    if curl -sf "http://127.0.0.1:$host_port/health" > /dev/null 2>&1; then
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

  verify_public_sign_in
  retire_lan_host_helpers

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

  log_success "Stockbot setup complete"
  report_site
  if [[ "$use_public" == true && "$publish_mode" == tunnel ]]; then
    log_info "Public: https://$(env_value PUBLIC_HOST) through Cloudflare Tunnel. Nothing on this Pi is listening on ports 80 or 443."
    log_info "In Zero Trust, the public hostname service URL is ${PANTRY_TUNNEL_ORIGIN}"
    log_info "Open that https URL on home Wi-Fi and on cellular, then remove the router's forwards for ports 80 and 443."
  elif [[ "$use_public" == true ]]; then
    log_info "Public: https://$(env_value PUBLIC_HOST)"
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
    if curl -sf "http://127.0.0.1:$host_port/health" | grep -q '"status":"ok"'; then
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
    if [[ -z "$public_host" ]]; then
      log_warn "CLOUDFLARE_TUNNEL_TOKEN is set but PUBLIC_HOST is empty, so the tunnel will not start. Set PUBLIC_HOST and run: sudo ./setup.sh publish --tunnel"
    elif docker inspect -f '{{.State.Running}}' "$proxy_container" 2>/dev/null | grep -qx true \
      && docker inspect -f '{{.State.Running}}' "$cloud_container" 2>/dev/null | grep -qx true; then
      log_success "Cloudflare Tunnel is running for https://$public_host. Service URL: ${PANTRY_TUNNEL_ORIGIN}. Ports 80 and 443 are not published."
    else
      log_warn "CLOUDFLARE_TUNNEL_TOKEN is set but the tunnel is not running. Start it with: sudo ./setup.sh publish --tunnel"
    fi
  elif [[ -n "$public_host" ]]; then
    if docker inspect -f '{{.State.Running}}' "$proxy_container" 2>/dev/null | grep -qx true; then
      log_success "Public proxy is running for https://$public_host"
    else
      log_warn "PUBLIC_HOST=$public_host but the proxy container is not running. Start it with: sudo ./setup.sh"
    fi
  else
    log_info "Public internet access is not configured (PUBLIC_HOST is empty)"
  fi

  report_site

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
  log_info "Stopping the public HTTPS proxy and Cloudflare Tunnel. Stockbot keeps running on loopback."
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
  retire_lan_host_helpers
  log_success "Removed leftover LAN firewall and mDNS units."
  log_info "Stockbot listens on 127.0.0.1 only. The next setup does not install those units again."
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
public HTTPS proxy when PUBLIC_HOST and ACME_EMAIL are set, or Cloudflare
Tunnel when CLOUDFLARE_TUNNEL_TOKEN is set), restarts Caddy so the current
Caddyfile is loaded, and removes leftover LAN firewall and mDNS units. It is safe to re-run. When CLOUDFLARE_TUNNEL_TOKEN
is set, the same command publishes through Cloudflare Tunnel instead of
ports 80 and 443.

COMMANDS:
  apply            Same as running setup.sh with no command.
                   Pass --with-updates to enable the automatic-update timer
                   and the deploy-hook path unit (left unchanged otherwise).
                   The timer fires every minute, and a signed hook starts the
                   same update immediately. Leave both off while iterating.
  install          Alias of apply.
  publish          Alias of apply. Pass --tunnel to publish through Cloudflare
                   Tunnel. A token already in .env selects tunnel without the flag.
  firewall         Alias of apply.
  unpublish        Stop the HTTPS proxy. Stockbot keeps running on loopback.
  firewall-off     Remove leftover LAN firewall and mDNS units. Setup does
                   not install them again.
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

  # Publish through Cloudflare Tunnel (set CLOUDFLARE_TUNNEL_TOKEN first)
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
  - Stockbot listens on 127.0.0.1:HOST_PORT. Browsers use https://PUBLIC_HOST.
    The Wi-Fi address does not serve the app.
  - On home Wi-Fi, https://PUBLIC_HOST hangs when the router does not
    hairpin NAT and the site is still on the certificate path. Use
    Cloudflare Tunnel. Cellular data still uses the public name.
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
