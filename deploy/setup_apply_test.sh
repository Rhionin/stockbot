#!/usr/bin/env bash
# Behavioral checks for the single setup command. Docker, systemd, and
# iptables are stubs so this can run without root or a Pi.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SETUP="$ROOT/deploy/setup.sh"

bash -n "$SETUP"
bash -n "$ROOT/deploy/systemd/pantry-update.sh"
bash -n "$ROOT/deploy/publish-mode.sh"
bash -n "$ROOT/deploy/check-publish-modes.sh"
bash -n "$ROOT/deploy/check-deploy-hook.sh"
bash -n "$ROOT/deploy/runtime-setup.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

LAST_RC=0
LAST_OUT=""
LAST_DOCKER=""
LAST_IPTABLES=""
LAST_SYSTEMCTL=""
LAST_DNSMASQ=""
LAST_CHOWN=""
LAST_CURL=""

make_bin() {
  local bin="$1"
  mkdir -p "$bin"

  cat > "$bin/docker" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${DOCKER_LOG:?}"
if [[ "$1" == "inspect" ]]; then
  printf '%s\n' true
  exit 0
fi
if [[ "$*" == *hash-password* ]]; then
  # docker run -i reads the password. Exit first and the writer dies with
  # SIGPIPE, so pipefail reports that the hash failed.
  cat >/dev/null
  printf '%s\n' '$2a$14$abcdefghijklmnopqrstuuabcdefghijklmnopqrstuvwxyz012'
  exit 0
fi
exit 0
EOF

  cat > "$bin/systemctl" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${SYSTEMCTL_LOG:?}"
exit 0
EOF

  cat > "$bin/iptables" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${IPTABLES_LOG:?}"
exit 0
EOF

  # Records the public-entry session probe. -o writes the body the way
  # curl does, and -w '%{http_code}' is printed on stdout.
  cat > "$bin/curl" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${CURL_LOG:?}"
out=""
prev=""
write_code=false
for arg in "$@"; do
  if [[ "$prev" == "-o" ]]; then
    out=$arg
  fi
  if [[ "$arg" == *'%{http_code}'* ]]; then
    write_code=true
  fi
  prev=$arg
done
if [[ "$*" == *"/api/auth/session"* ]]; then
  if [[ -n "$out" ]]; then
    printf '%s' "${CURL_SESSION_BODY:-}" > "$out"
  fi
  if [[ "$write_code" == true ]]; then
    printf '%s' "${CURL_SESSION_CODE:-401}"
  fi
fi
exit 0
EOF

  cat > "$bin/chown" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${CHOWN_LOG:?}"
exit 0
EOF

  cat > "$bin/usermod" << 'EOF'
#!/usr/bin/env bash
exit 0
EOF

  cat > "$bin/udevadm" << 'EOF'
#!/usr/bin/env bash
exit 0
EOF

  # Present so a machine that also has Avahi still resolves this stub first.
  # publish_lan_name only runs when a test sets PANTRY_SKIP_MDNS=0.
  cat > "$bin/avahi-publish-address" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${AVAHI_LOG:?}"
exit 0
EOF

  chmod +x "$bin/docker" "$bin/systemctl" "$bin/iptables" "$bin/curl" "$bin/chown" "$bin/usermod" "$bin/udevadm" "$bin/avahi-publish-address"
}

run_setup() {
  local pantry="$1"
  shift
  local work bin out err
  work=$(mktemp -d)
  bin="$work/bin"
  out="$work/out"
  err="$work/err"
  make_bin "$bin"
  : > "$work/docker.log"
  : > "$work/systemctl.log"
  : > "$work/iptables.log"
  : > "$work/avahi.log"
  : > "$work/chown.log"
  : > "$work/curl.log"
  mkdir -p "$work/systemd" "$work/avahi" "$work/dnsmasq"

  set +e
  DOCKER_LOG="$work/docker.log" \
    SYSTEMCTL_LOG="$work/systemctl.log" \
    IPTABLES_LOG="$work/iptables.log" \
    AVAHI_LOG="$work/avahi.log" \
    CHOWN_LOG="$work/chown.log" \
    CURL_LOG="$work/curl.log" \
    CURL_SESSION_CODE="${CURL_SESSION_CODE:-401}" \
    CURL_SESSION_BODY="${CURL_SESSION_BODY:-}" \
    PANTRY_DIR="$pantry" \
    PANTRY_SYSTEMD_UNIT_DIR="$work/systemd" \
    PANTRY_SETUP_SKIP_ROOT=1 \
    PANTRY_SETUP_GROUP='' \
    SUDO_USER='' \
    PANTRY_SKIP_MDNS="${PANTRY_SKIP_MDNS:-1}" \
    PANTRY_AVAHI_SERVICES_DIR="${PANTRY_AVAHI_SERVICES_DIR:-$work/avahi}" \
    PANTRY_DNSMASQ_DIR="${PANTRY_DNSMASQ_DIR:-$work/dnsmasq}" \
    PATH="$bin:$PATH" \
    bash "$SETUP" "$@" >"$out" 2>"$err"
  LAST_RC=$?
  set -e

  LAST_OUT=$(cat "$out" "$err")
  LAST_DOCKER=$(cat "$work/docker.log")
  LAST_IPTABLES=$(cat "$work/iptables.log")
  LAST_SYSTEMCTL=$(cat "$work/systemctl.log")
  LAST_DNSMASQ=""
  if [[ -f "$work/dnsmasq/pantry-split-horizon.conf" ]]; then
    LAST_DNSMASQ=$(cat "$work/dnsmasq/pantry-split-horizon.conf")
  fi
  LAST_CHOWN=$(cat "$work/chown.log")
  LAST_CURL=$(cat "$work/curl.log")
  rm -rf "$work"
}

# grep -q stops at the first match. Under pipefail a pipe then dies with
# SIGPIPE, so have fails and lack passes even when the text is there.
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

write_env() {
  local pantry="$1"
  shift
  mkdir -p "$pantry"
  printf '%s\n' "$@" > "$pantry/.env"
}

# LAN-only: containers come up without the public profile, and the published
# port is firewalled because compose publishes it.
lan=$(mktemp -d)
write_env "$lan" \
  "PANTRY_IMAGE_TAG=latest" \
  "HOST_PORT=9090" \
  "PUBLIC_HOST=" \
  "ACME_EMAIL=" \
  "SCANNER_DEVICE=/dev/input/pantry-scanner"
run_setup "$lan"
[[ "$LAST_RC" -eq 0 ]] || fail "LAN setup exited $LAST_RC: $LAST_OUT"
have "$LAST_DOCKER" "up -d" "LAN compose up"
lack "$LAST_DOCKER" "--profile public" "LAN must not start the public proxy"
secret1=$(grep '^DEPLOY_HOOK_SECRET=' "$lan/.env" | cut -d= -f2-)
[[ "$secret1" =~ ^[0-9a-f]{64}$ ]] || fail "setup did not generate a 64-hex deploy hook secret (got $secret1)"
[[ -d "$lan/deploy-trigger" ]] || fail "setup did not create the deploy trigger directory"
run_setup "$lan"
[[ "$LAST_RC" -eq 0 ]] || fail "second LAN setup exited $LAST_RC: $LAST_OUT"
secret2=$(grep '^DEPLOY_HOOK_SECRET=' "$lan/.env" | cut -d= -f2-)
[[ "$secret1" == "$secret2" ]] || fail "setup rotated DEPLOY_HOOK_SECRET"
grep -q '^SENTRY_DSN=$' "$lan/.env" || fail "setup did not append an empty SENTRY_DSN"
grep -q '^SENTRY_ENVIRONMENT=$' "$lan/.env" || fail "setup did not append an empty SENTRY_ENVIRONMENT"
grep -q '^SENTRY_TUNNEL_PROJECT_IDS=$' "$lan/.env" || fail "setup did not append an empty SENTRY_TUNNEL_PROJECT_IDS"
grep -q '^SENTRY_TUNNEL_PUBLIC_KEYS=$' "$lan/.env" || fail "setup did not append an empty SENTRY_TUNNEL_PUBLIC_KEYS"
[[ $(grep -c '^SENTRY_DSN=' "$lan/.env") -eq 1 ]] || fail "setup duplicated SENTRY_DSN"
vapid_pub1=$(grep '^VAPID_PUBLIC_KEY=' "$lan/.env" | cut -d= -f2-)
vapid_priv1=$(grep '^VAPID_PRIVATE_KEY=' "$lan/.env" | cut -d= -f2-)
vapid_sub1=$(grep '^VAPID_SUBJECT=' "$lan/.env" | cut -d= -f2-)
[[ $(printf '%s' "$vapid_pub1" | wc -c) -eq 87 ]] || fail "setup did not generate a VAPID public key (got $vapid_pub1)"
[[ $(printf '%s' "$vapid_priv1" | wc -c) -eq 43 ]] || fail "setup did not generate a VAPID private key"
[[ "$vapid_sub1" == "mailto:pantry@localhost" ]] || fail "LAN VAPID subject was $vapid_sub1"
run_setup "$lan"
[[ "$LAST_RC" -eq 0 ]] || fail "third LAN setup exited $LAST_RC: $LAST_OUT"
vapid_pub2=$(grep '^VAPID_PUBLIC_KEY=' "$lan/.env" | cut -d= -f2-)
vapid_priv2=$(grep '^VAPID_PRIVATE_KEY=' "$lan/.env" | cut -d= -f2-)
[[ "$vapid_pub1" == "$vapid_pub2" && "$vapid_priv1" == "$vapid_priv2" ]] || fail "setup rotated VAPID keys"
printed=$(PANTRY_DIR="$lan" PANTRY_SETUP_SKIP_ROOT=1 bash "$SETUP" deploy-secret)
[[ "$printed" == "$secret1" ]] || fail "deploy-secret printed [$printed], want [$secret1]"
lack "$LAST_DOCKER" "docker-compose.tunnel.yml" "LAN must not select the tunnel file"
lack "$LAST_DOCKER" "hash-password" "LAN must not hash a password"
lack "$LAST_DOCKER" "pull pantry" "LAN must not pull before the public site exists"
have "$LAST_CURL" "http://127.0.0.1:9090/api/auth/session" "setup probes sign-in on loopback"
have "$LAST_CURL" "http://127.0.0.1:9090/health" "setup probes health on loopback"
grep -q '^PANTRY_SESSION_SECRET=$' "$lan/.env" || fail "LAN setup filled a session secret"
lack "$LAST_DOCKER" "restart $(basename "$lan")-caddy" "LAN must not restart Caddy"
lack "$LAST_IPTABLES" "--dport" "setup does not install a port filter"
have "$LAST_OUT" "Stockbot setup complete" "LAN success"
lack "$LAST_OUT" "Home Wi-Fi cannot open" "LAN-only must not warn about hairpin"
have "$LAST_SYSTEMCTL" "disable --now pantry-lan-only.service pantry-mdns.service" "setup removes leftover LAN units"
grep -q '127\.0\.0\.1:.*:8080' "$lan/docker-compose.yml" || fail "compose must publish the listener on loopback"
lan_setup_mode=$(stat -c '%a' "$lan/setup")
[[ "$lan_setup_mode" == "2750" ]] || fail "LAN setup dir mode is $lan_setup_mode, want 2750"
lan_group=$(stat -c '%G' "$lan/.env")
[[ "$lan_group" != root ]] || fail "test .env group is root; the operator-group chown cannot be checked"
have "$LAST_CHOWN" "65532:${lan_group} $lan/setup" "LAN setup chowns the setup directory to uid 65532 and the operator group"
grep -q '^APP_BASE_URL=$' "$lan/.env" || fail "LAN setup must leave APP_BASE_URL empty"

# Public HTTPS starts without auth.caddy when PUBLIC_HOST and ACME_EMAIL are set.
pub=$(mktemp -d)
write_env "$pub" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "HOST_PORT=8080"
run_setup "$pub"
[[ "$LAST_RC" -eq 0 ]] || fail "public setup without auth.caddy exited $LAST_RC: $LAST_OUT"
have "$LAST_DOCKER" "--profile public" "public profile without auth.caddy"
lack "$LAST_DOCKER" "hash-password" "must not hash a shared password"
[[ ! -e "$pub/auth.caddy" ]] || fail "setup created auth.caddy"
grep -q '^APP_BASE_URL=https://pantry.example.com$' "$pub/.env" || fail "public setup did not set APP_BASE_URL from PUBLIC_HOST"
pub_secret=$(grep '^PANTRY_SESSION_SECRET=' "$pub/.env" | cut -d= -f2-)
[[ ${#pub_secret} -eq 64 ]] || fail "session secret was not saved (len ${#pub_secret})"
have "$LAST_DOCKER" "pull pantry" "public setup pulls before reload"

# A leftover auth.caddy file is ignored and never deleted.
legacy=$(mktemp -d)
write_env "$legacy" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "HOST_PORT=8080"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$legacy/auth.caddy"
cp "$legacy/auth.caddy" "$legacy/auth.caddy.before"
run_setup "$legacy"
[[ "$LAST_RC" -eq 0 ]] || fail "legacy auth.caddy setup exited $LAST_RC: $LAST_OUT"
cmp "$legacy/auth.caddy" "$legacy/auth.caddy.before" || fail "setup modified leftover auth.caddy"
have "$LAST_DOCKER" "--profile public" "public profile with leftover auth.caddy"

# Missing ACME_EMAIL keeps the public proxy off even when PUBLIC_HOST is set.
incomplete=$(mktemp -d)
write_env "$incomplete" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=" \
  "HOST_PORT=8080"
run_setup "$incomplete"
[[ "$LAST_RC" -eq 0 ]] || fail "incomplete public setup exited $LAST_RC: $LAST_OUT"
lack "$LAST_DOCKER" "--profile public" "missing ACME_EMAIL must not start the proxy"
have "$LAST_OUT" "ACME_EMAIL" "incomplete public warning names ACME_EMAIL"

# A leftover LAN firewall setting does not install a port filter.
opt=$(mktemp -d)
write_env "$opt" \
  "PUBLIC_HOST=" \
  "HOST_PORT=8080" \
  "PANTRY_LAN_FIREWALL=off"
run_setup "$opt"
[[ "$LAST_RC" -eq 0 ]] || fail "opt-out setup exited $LAST_RC: $LAST_OUT"
lack "$LAST_OUT" "Non-LAN clients are blocked" "setup must not install the firewall"
lack "$LAST_SYSTEMCTL" "enable --now pantry-lan-only.service" "setup must not enable the firewall unit"
have "$LAST_SYSTEMCTL" "disable --now pantry-lan-only.service pantry-mdns.service" "setup disables leftover LAN units"
lack "$LAST_IPTABLES" "--dport" "setup does not touch iptables"

# No command, and the old subcommands, all run the same setup.
for args in "" "apply" "install" "publish" "firewall"; do
  # shellcheck disable=SC2086
  run_setup "$lan" $args
  [[ "$LAST_RC" -eq 0 ]] || fail "command [$args] exited $LAST_RC: $LAST_OUT"
  have "$LAST_OUT" "Stockbot setup complete" "command [$args]"
done

if [[ -e /opt/pantry ]]; then
  fail "setup wrote /opt/pantry; tests must stay on PANTRY_DIR"
fi

# Old LAN address settings do not publish a name or a resolver.
mdns=$(mktemp -d)
write_env "$mdns" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "HOST_PORT=8080" \
  "PANTRY_LAN_IPV4=192.168.1.203"
PANTRY_SKIP_MDNS=0 run_setup "$mdns"
[[ "$LAST_RC" -eq 0 ]] || fail "mdns setup exited $LAST_RC: $LAST_OUT"
lack "$LAST_OUT" "pantry.local" "setup does not publish a LAN name"
lack "$LAST_SYSTEMCTL" "enable pantry-mdns.service" "mDNS unit is not enabled"
have "$LAST_SYSTEMCTL" "disable --now pantry-lan-only.service pantry-mdns.service" "mDNS unit is removed"
have "$LAST_DOCKER" "--profile public" "public proxy stays on"
lack "$LAST_DOCKER" "stop caddy" "setup must not stop Caddy"

split=$(mktemp -d)
write_env "$split" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "HOST_PORT=8080" \
  "PANTRY_LAN_IPV4=192.168.1.203" \
  "PANTRY_SPLIT_DNS=on"
run_setup "$split"
[[ "$LAST_RC" -eq 0 ]] || fail "split-dns setup exited $LAST_RC: $LAST_OUT"
[[ -z "$LAST_DNSMASQ" ]] || fail "setup wrote a dnsmasq answer"
have "$LAST_DOCKER" "--profile public" "split-horizon setting keeps the public proxy"

badip=$(mktemp -d)
write_env "$badip" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "HOST_PORT=8080" \
  "PANTRY_LAN_IPV4=8.8.8.8" \
  "PANTRY_SPLIT_DNS=on"
run_setup "$badip"
[[ "$LAST_RC" -eq 0 ]] || fail "old LAN settings must not fail setup, exited $LAST_RC: $LAST_OUT"
have "$LAST_DOCKER" "--profile public" "old LAN settings still start the public proxy"
have "$LAST_OUT" "Home Wi-Fi cannot open https://pantry.example.com" "hairpin warning remains"
mdns_bin=$(mktemp -d)

# A token selects the tunnel even when ACME_EMAIL is empty and --tunnel is omitted.
# The certificate profile stays off, and the token is not printed.
tun=$(mktemp -d)
write_env "$tun" \
  "PUBLIC_HOST=stockbot.rhionin.com" \
  "ACME_EMAIL=" \
  "HOST_PORT=8080" \
  "PUBLISH_MODE=" \
  "CLOUDFLARE_TUNNEL_TOKEN=test-tunnel-token"
run_setup "$tun"
[[ "$LAST_RC" -eq 0 ]] || fail "tunnel setup exited $LAST_RC: $LAST_OUT"
have "$LAST_DOCKER" "docker-compose.tunnel.yml" "tunnel compose file"
have "$LAST_DOCKER" "--profile tunnel" "tunnel profile"
lack "$LAST_DOCKER" "--profile public" "tunnel must not publish the certificate profile"
have "$LAST_DOCKER" "restart $(basename "$tun")-caddy" "tunnel restarts Caddy"
have "$LAST_OUT" "http://caddy:80" "service URL is printed"
have "$LAST_OUT" "Nothing on this Pi is listening on ports 80 or 443" "tunnel closes host 80/443"
lack "$LAST_OUT" "Home Wi-Fi cannot open" "tunnel must not warn about hairpin"
lack "$LAST_OUT" "test-tunnel-token" "token must not be logged"
lack "$LAST_DOCKER" "test-tunnel-token" "token must not be a docker argument"
lack "$LAST_DOCKER" "hash-password" "tunnel must not hash a shared password"
# publish --tunnel is the same switch once the token is already in .env.
run_setup "$tun" publish --tunnel
[[ "$LAST_RC" -eq 0 ]] || fail "publish --tunnel exited $LAST_RC: $LAST_OUT"
have "$LAST_DOCKER" "--profile tunnel" "publish --tunnel"

# PUBLISH_MODE=acme does not override a token that is still set.
write_env "$tun" \
  "PUBLIC_HOST=stockbot.rhionin.com" \
  "ACME_EMAIL=you@example.com" \
  "HOST_PORT=8080" \
  "PUBLISH_MODE=acme" \
  "CLOUDFLARE_TUNNEL_TOKEN=test-tunnel-token"
run_setup "$tun"
[[ "$LAST_RC" -eq 0 ]] || fail "token overrides acme mode, exited $LAST_RC: $LAST_OUT"
have "$LAST_DOCKER" "--profile tunnel" "token wins over PUBLISH_MODE=acme"
lack "$LAST_DOCKER" "--profile public" "token must not open the certificate profile"

# Tunnel without a token fails before containers start, and does not fall through to ACME.
notoken=$(mktemp -d)
write_env "$notoken" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "HOST_PORT=8080" \
  "PUBLISH_MODE=tunnel" \
  "CLOUDFLARE_TUNNEL_TOKEN="
run_setup "$notoken" publish --tunnel
[[ "$LAST_RC" -ne 0 ]] || fail "tunnel without a token should fail"
lack "$LAST_DOCKER" "up -d" "missing token must not start containers"
have "$LAST_OUT" "CLOUDFLARE_TUNNEL_TOKEN" "missing token names the variable"
lack "$LAST_DOCKER" "--profile public" "missing token must not start the certificate proxy"

# Tunnel starts without auth.caddy when the token and PUBLIC_HOST are set.
unready=$(mktemp -d)
write_env "$unready" \
  "PUBLIC_HOST=stockbot.rhionin.com" \
  "HOST_PORT=8080" \
  "CLOUDFLARE_TUNNEL_TOKEN=test-tunnel-token"
run_setup "$unready"
[[ "$LAST_RC" -eq 0 ]] || fail "tunnel without auth.caddy exited $LAST_RC: $LAST_OUT"
have "$LAST_DOCKER" "--profile tunnel" "tunnel starts without auth.caddy"
[[ ! -e "$unready/auth.caddy" ]] || fail "setup created auth.caddy for tunnel"

# Split-horizon DNS would point the public name at the Pi. Tunnel mode removes it.
tunsplit=$(mktemp -d)
write_env "$tunsplit" \
  "PUBLIC_HOST=stockbot.rhionin.com" \
  "HOST_PORT=8080" \
  "PANTRY_LAN_IPV4=192.168.1.203" \
  "PANTRY_SPLIT_DNS=on" \
  "CLOUDFLARE_TUNNEL_TOKEN=test-tunnel-token"
run_setup "$tunsplit"
[[ "$LAST_RC" -eq 0 ]] || fail "tunnel split-dns exited $LAST_RC: $LAST_OUT"
lack "$LAST_OUT" "PANTRY_SPLIT_DNS is on" "tunnel does not configure split-horizon"
[[ -z "$LAST_DNSMASQ" ]] || fail "tunnel setup wrote a dnsmasq answer"
have "$LAST_DOCKER" "--profile tunnel" "split-horizon removal keeps the tunnel"

# publish_mode_from_env
# shellcheck disable=SC1091
source "$ROOT/deploy/publish-mode.sh"
mode_dir=$(mktemp -d)
write_env "$mode_dir" "PUBLIC_HOST=" "CLOUDFLARE_TUNNEL_TOKEN=" "PUBLISH_MODE="
# publish_mode_from_env reads PANTRY_DIR. shellcheck does not follow the source above.
# shellcheck disable=SC2034
PANTRY_DIR="$mode_dir"
got=$(publish_mode_from_env)
[[ "$got" == none ]] || fail "empty env mode, got $got"
write_env "$mode_dir" "PUBLIC_HOST=pantry.example.com" "CLOUDFLARE_TUNNEL_TOKEN=" "PUBLISH_MODE="
got=$(publish_mode_from_env)
[[ "$got" == acme ]] || fail "host without token should be acme, got $got"
write_env "$mode_dir" "PUBLIC_HOST=pantry.example.com" "CLOUDFLARE_TUNNEL_TOKEN=tok" "PUBLISH_MODE=acme"
got=$(publish_mode_from_env)
[[ "$got" == tunnel ]] || fail "token should win, got $got"
write_env "$mode_dir" "PUBLIC_HOST=pantry.example.com" "CLOUDFLARE_TUNNEL_TOKEN=" "PUBLISH_MODE=tunnel"
set +e
got=$(publish_mode_from_env)
mode_rc=$?
set -e
[[ "$mode_rc" -eq 2 ]] || fail "tunnel mode without token rc=$mode_rc"
write_env "$mode_dir" "PUBLIC_HOST=" "CLOUDFLARE_TUNNEL_TOKEN=" "PUBLISH_MODE=sideways"
set +e
got=$(publish_mode_from_env)
mode_rc=$?
set -e
[[ "$mode_rc" -eq 1 ]] || fail "bad PUBLISH_MODE rc=$mode_rc"
write_env "$mode_dir" "PUBLIC_HOST=pantry.example.com" "CLOUDFLARE_TUNNEL_TOKEN=tok" "PUBLISH_MODE="
got=$(publish_mode_from_env tunnel)
[[ "$got" == tunnel ]] || fail "--tunnel request, got $got"

# Filled Grocy credentials must not attach the sidecar. Profile grocy does
# not include grocy-sync, and setup must not select profile grocy-sync.
write_env "$mode_dir" \
  "GROCY_URL=http://grocy:80" \
  "GROCY_API_KEY=secret" \
  "PANTRY_SYNC_TOKEN=tok"
suffix=$(grocy_compose_suffix)
[[ -z "$suffix" ]] || fail "grocy credentials must leave grocy-sync stopped, got: $suffix"

# A secret the operator set is left alone.
custom=$(mktemp -d)
write_env "$custom" \
  "DEPLOY_HOOK_SECRET=not-generated" \
  "HOST_PORT=8080" \
  "PUBLIC_HOST=pantry.example.com" \
  "VAPID_PUBLIC_KEY=operator-public" \
  "VAPID_PRIVATE_KEY=operator-private" \
  "VAPID_SUBJECT="
run_setup "$custom"
[[ "$LAST_RC" -eq 0 ]] || fail "custom secret setup exited $LAST_RC: $LAST_OUT"
grep -q '^DEPLOY_HOOK_SECRET=not-generated$' "$custom/.env" || fail "setup replaced a custom DEPLOY_HOOK_SECRET"
grep -q '^VAPID_PUBLIC_KEY=operator-public$' "$custom/.env" || fail "setup replaced a custom VAPID public key"
grep -q '^VAPID_PRIVATE_KEY=operator-private$' "$custom/.env" || fail "setup replaced a custom VAPID private key"
grep -q '^VAPID_SUBJECT=mailto:pantry@pantry.example.com$' "$custom/.env" || fail "setup did not fill an empty VAPID subject"

# --with-updates enables the timer and the path unit that the hook trips.
updates=$(mktemp -d)
write_env "$updates" \
  "HOST_PORT=8080" \
  "PUBLIC_HOST="
run_setup "$updates" --with-updates
[[ "$LAST_RC" -eq 0 ]] || fail "--with-updates exited $LAST_RC: $LAST_OUT"
have "$LAST_SYSTEMCTL" "enable --now pantry-update.timer" "timer enabled"
have "$LAST_SYSTEMCTL" "enable --now pantry-update.path" "path unit enabled"

# pantry-update.sh prepares the setup directory before compose up.
updater="$ROOT/deploy/systemd/pantry-update.sh"
prep_line=$(grep -n 'prepare_setup_dir' "$updater" | head -1 | cut -d: -f1)
up_line=$(grep -n 'up -d' "$updater" | head -1 | cut -d: -f1)
[[ -n "$prep_line" && -n "$up_line" && "$prep_line" -lt "$up_line" ]] || fail "pantry-update.sh must prepare /run/setup before compose up"

setup_dir=$(mktemp -d)
mkdir -p "$setup_dir/setup"
own_bin=$(mktemp -d)
own_log=$(mktemp)
cat > "$own_bin/chown" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${CHOWN_LOG:?}"
exit 0
EOF
chmod +x "$own_bin/chown"
# shellcheck disable=SC1091
CHOWN_LOG="$own_log" PANTRY_DIR="$setup_dir" PANTRY_SETUP_GROUP=testgroup PATH="$own_bin:$PATH" \
  bash -c 'source "$1"; prepare_setup_dir' _ "$ROOT/deploy/runtime-setup.sh"
setup_mode=$(stat -c '%a' "$setup_dir/setup")
[[ "$setup_mode" == "2750" ]] || fail "prepare_setup_dir left mode $setup_mode"
have "$(cat "$own_log")" "65532:testgroup $setup_dir/setup" "prepare_setup_dir chowns the setup directory"

# An origin the operator already chose is kept. A hostname with a scheme
# is not a PUBLIC_HOST, so the origin stays empty.
kept_origin=$(mktemp -d)
write_env "$kept_origin" \
  "PUBLIC_HOST=pantry.example.com" \
  "APP_BASE_URL=https://operator.example" \
  "ACME_EMAIL=you@example.com" \
  "HOST_PORT=8080"
chmod 600 "$kept_origin/.env"
run_setup "$kept_origin"
[[ "$LAST_RC" -eq 0 ]] || fail "custom origin setup exited $LAST_RC: $LAST_OUT"
grep -q '^APP_BASE_URL=https://operator.example$' "$kept_origin/.env" || fail "setup replaced a custom APP_BASE_URL"
[[ $(stat -c '%a' "$kept_origin/.env") == "600" ]] || fail "custom origin .env mode changed"

bad_host=$(mktemp -d)
write_env "$bad_host" \
  "PUBLIC_HOST=https://stockbot.example.com" \
  "ACME_EMAIL=you@example.com" \
  "HOST_PORT=8080"
run_setup "$bad_host"
[[ "$LAST_RC" -eq 0 ]] || fail "invalid PUBLIC_HOST setup exited $LAST_RC: $LAST_OUT"
grep -q '^APP_BASE_URL=$' "$bad_host/.env" || fail "invalid PUBLIC_HOST must leave APP_BASE_URL empty"

# chown clears setgid, so the mode is applied after the owner. Group
# resolution: an explicit group, the sudo user, then rhionin, then uid 65532.
sg_dir=$(mktemp -d)
sg_log=$(mktemp)
sg_bin=$(mktemp -d)
cat > "$sg_bin/chown" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${CHOWN_LOG:?}"
target=${*: -1}
if [[ -d "$target" ]]; then
  chmod 0750 "$target"
fi
exit 0
EOF
chmod +x "$sg_bin/chown"
printf 'PUBLIC_HOST=\n' > "$sg_dir/.env"
CHOWN_LOG="$sg_log" PANTRY_DIR="$sg_dir" PANTRY_SETUP_GROUP=operators \
  PANTRY_SETUP_SKIP_ROOT=1 SUDO_USER='' PATH="$sg_bin:$PATH" \
  bash -c 'source "$1"; prepare_setup_dir' _ "$ROOT/deploy/runtime-setup.sh"
sg_mode=$(stat -c '%a' "$sg_dir/setup")
[[ "$sg_mode" == "2750" ]] || fail "setup dir mode after chown is $sg_mode, want 2750"
have "$(cat "$sg_log")" "65532:operators $sg_dir/setup" "setup group override is the chown group"

sudo_dir=$(mktemp -d)
sudo_log=$(mktemp)
sudo_bin=$(mktemp -d)
cat > "$sudo_bin/chown" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${CHOWN_LOG:?}"
exit 0
EOF
cat > "$sudo_bin/id" << 'EOF'
#!/usr/bin/env bash
if [[ "$1" == "-gn" && "$2" == "cj" ]]; then
  printf '%s\n' rhionin
  exit 0
fi
exit 1
EOF
chmod +x "$sudo_bin/chown" "$sudo_bin/id"
printf 'PUBLIC_HOST=\n' > "$sudo_dir/.env"
CHOWN_LOG="$sudo_log" PANTRY_DIR="$sudo_dir" PANTRY_SETUP_GROUP='' \
  PANTRY_SETUP_SKIP_ROOT=1 SUDO_USER=cj PATH="$sudo_bin:$PATH" \
  bash -c 'source "$1"; prepare_setup_dir' _ "$ROOT/deploy/runtime-setup.sh"
have "$(cat "$sudo_log")" "65532:rhionin $sudo_dir/setup" "setup dir uses the sudo user's group"

fallback_dir=$(mktemp -d)
fallback_log=$(mktemp)
fallback_bin=$(mktemp -d)
cat > "$fallback_bin/chown" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${CHOWN_LOG:?}"
exit 0
EOF
cat > "$fallback_bin/stat" << 'EOF'
#!/usr/bin/env bash
if [[ "$1" == "-c" && "$2" == "%g" ]]; then
  printf '0\n'
  exit 0
fi
if [[ "$1" == "-c" && "$2" == "%G" ]]; then
  printf 'root\n'
  exit 0
fi
exec /usr/bin/stat "$@"
EOF
cat > "$fallback_bin/getent" << 'EOF'
#!/usr/bin/env bash
if [[ "$1" == "group" && "$2" == "rhionin" ]]; then
  exit 0
fi
exit 1
EOF
chmod +x "$fallback_bin/chown" "$fallback_bin/stat" "$fallback_bin/getent"
printf 'PUBLIC_HOST=\n' > "$fallback_dir/.env"
CHOWN_LOG="$fallback_log" PANTRY_DIR="$fallback_dir" PANTRY_SETUP_GROUP='' \
  PANTRY_SETUP_SKIP_ROOT=1 SUDO_USER='' PATH="$fallback_bin:$PATH" \
  bash -c 'source "$1"; prepare_setup_dir' _ "$ROOT/deploy/runtime-setup.sh"
have "$(cat "$fallback_log")" "65532:rhionin $fallback_dir/setup" "setup dir falls back to group rhionin"

numeric_log=$(mktemp)
cat > "$fallback_bin/getent" << 'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$fallback_bin/getent"
CHOWN_LOG="$numeric_log" PANTRY_DIR="$fallback_dir" PANTRY_SETUP_GROUP='' \
  PANTRY_SETUP_SKIP_ROOT=1 SUDO_USER='' PATH="$fallback_bin:$PATH" \
  bash -c 'source "$1"; prepare_setup_dir' _ "$ROOT/deploy/runtime-setup.sh"
have "$(cat "$numeric_log")" "65532:65532 $fallback_dir/setup" "setup dir falls back to uid 65532"

mode_dir=$(mktemp -d)
printf 'PUBLIC_HOST=stockbot.example\nAPP_BASE_URL=\n' > "$mode_dir/.env"
chmod 640 "$mode_dir/.env"
PANTRY_DIR="$mode_dir" bash -c 'source "$1"; ensure_app_base_url' _ "$ROOT/deploy/runtime-setup.sh"
grep -q '^APP_BASE_URL=https://stockbot.example$' "$mode_dir/.env" || fail "empty APP_BASE_URL was not filled from PUBLIC_HOST"
[[ $(stat -c '%a' "$mode_dir/.env") == "640" ]] || fail "filling APP_BASE_URL changed .env mode"
printf 'PUBLIC_HOST=stockbot.example\nAPP_BASE_URL=https://keep.example\n' > "$mode_dir/.env"
chmod 600 "$mode_dir/.env"
PANTRY_DIR="$mode_dir" bash -c 'source "$1"; ensure_app_base_url' _ "$ROOT/deploy/runtime-setup.sh"
grep -q '^APP_BASE_URL=https://keep.example$' "$mode_dir/.env" || fail "ensure_app_base_url replaced a set origin"
[[ $(stat -c '%a' "$mode_dir/.env") == "600" ]] || fail "keeping APP_BASE_URL changed .env mode"
printf 'PUBLIC_HOST=\n' > "$mode_dir/.env"
chmod 600 "$mode_dir/.env"
PANTRY_DIR="$mode_dir" bash -c 'source "$1"; ensure_app_base_url' _ "$ROOT/deploy/runtime-setup.sh"
grep -q '^APP_BASE_URL=' "$mode_dir/.env" && fail "LAN ensure_app_base_url invented an origin"

# Scanner udev rules match the input parent's id/vendor and id/product.
# udev imports ID_VENDOR_ID / ID_MODEL_ID only for a device with a USB
# ancestor, so a Bluetooth HID (uhid) scanner never matches ENV keys.
# The same ATTRS form is what a USB scanner gets.
assert_rule() {
  local file="$1"
  local label="$2"
  local want="$3"
  [[ -f "$file" ]] || fail "$label (rule file missing)"
  local got
  got=$(cat "$file")
  want="${want%$'\n'}"
  if [[ "$got" != "$want" ]]; then
    printf 'FAIL: %s\n--- got ---\n%s\n--- want ---\n%s\n' "$label" "$got" "$want" >&2
    exit 1
  fi
}

rule_fix=$(mktemp -d)
bt_devices="$rule_fix/bt-devices"
bt_rule="$rule_fix/rules/99-pantry-scanner.rules"
mkdir -p "$rule_fix/rules"
cat > "$bt_devices" << 'EOF'
I: Bus=0003 Vendor=046d Product=c31c Version=0111
N: Name="Logitech USB Keyboard"

I: Bus=0005 Vendor=04E8 Product=7021 Version=0110
N: Name="Netum Barcode Scanner"
EOF
# The broken rule a previous setup.sh would have installed.
cat > "$bt_rule" << 'EOF'
# Stockbot Scanner udev rule - auto-generated for "Netum Barcode Scanner"
SUBSYSTEM=="input", KERNEL=="event*", ENV{ID_VENDOR_ID}=="04e8", ENV{ID_MODEL_ID}=="7021", \
  SYMLINK+="input/pantry-scanner", GROUP="65532", MODE="0640"
EOF
write_env "$rule_fix" \
  "HOST_PORT=8080" \
  "PUBLIC_HOST="
PANTRY_INPUT_DEVICES="$bt_devices" PANTRY_UDEV_RULE_FILE="$bt_rule" \
  run_setup "$rule_fix"
[[ "$LAST_RC" -eq 0 ]] || fail "setup with bluetooth scanner exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "Matching on vendor=04e8 product=7021" "setup logs the scanner ids"
bt_want='# Stockbot Scanner udev rule - auto-generated for "Netum Barcode Scanner"
SUBSYSTEM=="input", KERNEL=="event*", ATTRS{id/vendor}=="04e8", ATTRS{id/product}=="7021", \
  SYMLINK+="input/pantry-scanner", GROUP="65532", MODE="0640"
'
assert_rule "$bt_rule" "setup replaces ENV match for a bluetooth scanner" "$bt_want"
lack "$(cat "$bt_rule")" "ENV{ID_VENDOR_ID}" "installed rule no longer matches ENV vendor id"
lack "$(cat "$bt_rule")" "ENV{ID_MODEL_ID}" "installed rule no longer matches ENV model id"

# `rule` writes the same form for a USB scanner, and leaves a keyboard alone.
usb_dir=$(mktemp -d)
usb_devices="$usb_dir/devices"
usb_rule="$usb_dir/99-pantry-scanner.rules"
cat > "$usb_devices" << 'EOF'
I: Bus=0003 Vendor=05e0 Product=1200 Version=0110
N: Name="Symbol Bar Code Scanner"
EOF
PANTRY_INPUT_DEVICES="$usb_devices" PANTRY_UDEV_RULE_FILE="$usb_rule" \
  run_setup "$usb_dir" rule
[[ "$LAST_RC" -eq 0 ]] || fail "usb rule exited $LAST_RC: $LAST_OUT"
usb_want='# Stockbot Scanner udev rule - auto-generated for "Symbol Bar Code Scanner"
SUBSYSTEM=="input", KERNEL=="event*", ATTRS{id/vendor}=="05e0", ATTRS{id/product}=="1200", \
  SYMLINK+="input/pantry-scanner", GROUP="65532", MODE="0640"
'
assert_rule "$usb_rule" "usb scanner uses input parent ids" "$usb_want"
usb_before=$(cat "$usb_rule")
PANTRY_INPUT_DEVICES="$usb_devices" PANTRY_UDEV_RULE_FILE="$usb_rule" \
  run_setup "$usb_dir" rule
[[ "$LAST_RC" -eq 0 ]] || fail "second usb rule exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "already up to date" "second rule run leaves a matching rule in place"
[[ "$(cat "$usb_rule")" == "$usb_before" ]] || fail "second rule run changed the usb rule"

# No vendor/product: keep the name match.
name_dir=$(mktemp -d)
name_devices="$name_dir/devices"
name_rule="$name_dir/99-pantry-scanner.rules"
cat > "$name_devices" << 'EOF'
N: Name="Handheld Scanner"
EOF
PANTRY_INPUT_DEVICES="$name_devices" PANTRY_UDEV_RULE_FILE="$name_rule" \
  run_setup "$name_dir" rule
[[ "$LAST_RC" -eq 0 ]] || fail "name fallback exited $LAST_RC: $LAST_OUT"
name_want='# Stockbot Scanner udev rule - auto-generated
SUBSYSTEM=="input", KERNEL=="event*", ATTRS{name}=="Handheld Scanner", \
  SYMLINK+="input/pantry-scanner", GROUP="65532", MODE="0640"
'
assert_rule "$name_rule" "missing ids fall back to device name" "$name_want"

# Nothing that looks like a scanner: `rule` fails and does not invent a match.
none_dir=$(mktemp -d)
none_devices="$none_dir/devices"
none_rule="$none_dir/99-pantry-scanner.rules"
cat > "$none_devices" << 'EOF'
I: Bus=0003 Vendor=046d Product=c31c Version=0111
N: Name="Logitech USB Keyboard"
EOF
PANTRY_INPUT_DEVICES="$none_devices" PANTRY_UDEV_RULE_FILE="$none_rule" \
  run_setup "$none_dir" rule
[[ "$LAST_RC" -ne 0 ]] || fail "rule with no scanner should fail"
have "$LAST_OUT" "$none_rule" "failure names the rule path"
[[ ! -e "$none_rule" ]] || fail "rule with no scanner wrote $none_rule"

# Appending the empty Sentry lines must not rewrite a value that is already
# set and must not change the file mode. This calls the helper directly so
# the rest of setup, which restricts .env to its owner, is not involved.
sentry_dir=$(mktemp -d)
printf '%s\n' 'HOST_PORT=8080' 'SENTRY_DSN=keep-this' > "$sentry_dir/.env"
chmod 640 "$sentry_dir/.env"
sentry_mode=$(stat -c '%a' "$sentry_dir/.env")
PANTRY_DIR="$sentry_dir" bash -c 'set -euo pipefail; source "$1"; added=0; append_empty_env_keys SENTRY_DSN SENTRY_ENVIRONMENT SENTRY_TUNNEL_PROJECT_IDS SENTRY_TUNNEL_PUBLIC_KEYS' bash "$SETUP"
[[ $(stat -c '%a' "$sentry_dir/.env") == "$sentry_mode" ]] || fail "append_empty_env_keys changed the .env mode"
grep -q '^SENTRY_DSN=keep-this$' "$sentry_dir/.env" || fail "append_empty_env_keys rewrote SENTRY_DSN"
grep -q '^SENTRY_ENVIRONMENT=$' "$sentry_dir/.env" || fail "append_empty_env_keys missed SENTRY_ENVIRONMENT"
grep -q '^SENTRY_TUNNEL_PROJECT_IDS=$' "$sentry_dir/.env" || fail "append_empty_env_keys missed SENTRY_TUNNEL_PROJECT_IDS"
grep -q '^SENTRY_TUNNEL_PUBLIC_KEYS=$' "$sentry_dir/.env" || fail "append_empty_env_keys missed SENTRY_TUNNEL_PUBLIC_KEYS"
PANTRY_DIR="$sentry_dir" bash -c 'set -euo pipefail; source "$1"; added=0; append_empty_env_keys SENTRY_DSN SENTRY_ENVIRONMENT SENTRY_TUNNEL_PROJECT_IDS SENTRY_TUNNEL_PUBLIC_KEYS' bash "$SETUP"
[[ $(grep -c '^SENTRY_ENVIRONMENT=' "$sentry_dir/.env") -eq 1 ]] || fail "append_empty_env_keys duplicated SENTRY_ENVIRONMENT"
[[ $(stat -c '%a' "$sentry_dir/.env") == "$sentry_mode" ]] || fail "second append changed the .env mode"

# An exported assignment is already present. Neither the empty-key helper nor
# reconcile may append a second KEY= line, and neither may rewrite the value.
export_dir=$(mktemp -d)
printf '%s\n' 'export SENTRY_DSN=keep-export' > "$export_dir/.env"
chmod 640 "$export_dir/.env"
export_mode=$(stat -c '%a' "$export_dir/.env")
PANTRY_DIR="$export_dir" bash -c 'set -euo pipefail; source "$1"; added=0; append_empty_env_keys SENTRY_DSN SENTRY_ENVIRONMENT SENTRY_TUNNEL_PROJECT_IDS SENTRY_TUNNEL_PUBLIC_KEYS' bash "$SETUP"
[[ $(stat -c '%a' "$export_dir/.env") == "$export_mode" ]] || fail "append_empty_env_keys changed the exported .env mode"
grep -q '^export SENTRY_DSN=keep-export$' "$export_dir/.env" || fail "append_empty_env_keys rewrote export SENTRY_DSN"
[[ $(grep -cE '^(export[[:space:]]+)?SENTRY_DSN=' "$export_dir/.env") -eq 1 ]] || fail "append_empty_env_keys duplicated export SENTRY_DSN"
printf '%s\n' 'HOST_PORT=8080' 'SENTRY_DSN=' 'SENTRY_ENVIRONMENT=' > "$export_dir/.env.example"
PANTRY_DIR="$export_dir" bash -c 'set -euo pipefail; source "$1"; cmd_reconcile_env' bash "$SETUP"
grep -q '^export SENTRY_DSN=keep-export$' "$export_dir/.env" || fail "reconcile rewrote export SENTRY_DSN"
[[ $(grep -cE '^(export[[:space:]]+)?SENTRY_DSN=' "$export_dir/.env") -eq 1 ]] || fail "reconcile duplicated export SENTRY_DSN"
[[ $(stat -c '%a' "$export_dir/.env") == "$export_mode" ]] || fail "reconcile changed the .env mode"

# Grocy credentials must not change what setup.sh asks docker to do.
# With GROCY_URL, GROCY_API_KEY, and PANTRY_SYNC_TOKEN filled, setup must
# issue the exact docker argv it issues without them: no
# docker-compose.grocy.yml, no grocy profile, and never an orphan or volume cleanup.
grocy_mode_env() {
  local dir="$1" mode="$2"
  if [[ "$mode" == tunnel ]]; then
    write_env "$dir" "PUBLIC_HOST=stockbot.example" "ACME_EMAIL=" \
      "HOST_PORT=8080" "PUBLISH_MODE=" "CLOUDFLARE_TUNNEL_TOKEN=test-tunnel-token"
  else
    write_env "$dir" "PANTRY_IMAGE_TAG=latest" "HOST_PORT=9090" "PUBLIC_HOST=" "ACME_EMAIL=" \
      "SCANNER_DEVICE=/dev/input/pantry-scanner"
  fi
}
for grocy_mode in lan tunnel; do
  grocy_off=$(mktemp -d); grocy_on=$(mktemp -d)
  grocy_mode_env "$grocy_off" "$grocy_mode"
  grocy_mode_env "$grocy_on" "$grocy_mode"
  printf '%s\n' GROCY_URL=http://grocy:80 GROCY_API_KEY=invented-key PANTRY_SYNC_TOKEN=invented-token >> "$grocy_on/.env"
  run_setup "$grocy_off"
  [[ "$LAST_RC" -eq 0 ]] || fail "$grocy_mode setup without Grocy failed: $LAST_OUT"
  grocy_off_docker=${LAST_DOCKER//$grocy_off/<pantry>}
  grocy_off_docker=${grocy_off_docker//$(basename "$grocy_off")/<proj>}
  run_setup "$grocy_on"
  [[ "$LAST_RC" -eq 0 ]] || fail "$grocy_mode setup with Grocy failed: $LAST_OUT"
  grocy_on_docker=${LAST_DOCKER//$grocy_on/<pantry>}
  grocy_on_docker=${grocy_on_docker//$(basename "$grocy_on")/<proj>}
  have "$grocy_on_docker" " up -d" "$grocy_mode setup with Grocy still starts the stack"
  lack "$grocy_on_docker" "docker-compose.grocy.yml" "$grocy_mode setup with Grocy must not load the sidecar file"
  lack "$grocy_on_docker" "--profile grocy" "$grocy_mode setup with Grocy must not select a grocy profile"
  lack "$grocy_on_docker" "--remove-orphans" "$grocy_mode setup must not remove orphans (pantry-grocy-sync)"
  if grep -qE '(^| )(down|volume rm|volume prune)( |$)' <<<"$grocy_on_docker"; then
    fail "$grocy_mode setup with Grocy must not take the stack down or remove volumes"
  fi
  [[ "$grocy_on_docker" == "$grocy_off_docker" ]] \
    || fail "$grocy_mode setup docker argv changed with Grocy credentials:"$'\n'"$(diff <(printf '%s\n' "$grocy_off_docker") <(printf '%s\n' "$grocy_on_docker"))"
  rm -rf "$grocy_off" "$grocy_on"
done

rm -rf "$lan" "$pub" "$legacy" "$incomplete" "$opt" "$mdns" "$split" "$badip" "$mdns_bin" "$tun" "$notoken" "$unready" "$tunsplit" "$mode_dir" "$custom" "$updates" "$setup_dir" "$own_bin" "$rule_fix" "$usb_dir" "$name_dir" "$none_dir" "$sentry_dir" "$export_dir" "$kept_origin" "$bad_host" "$sg_dir"
echo "setup_apply_test ok"
