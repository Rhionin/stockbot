#!/usr/bin/env bash
# Behavioral checks for the single setup command. Docker, systemd, and
# iptables are stubs so this can run without root or a Pi.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SETUP="$ROOT/deploy/setup.sh"

bash -n "$SETUP"
bash -n "$ROOT/deploy/firewall/pantry-lan-only.sh"
bash -n "$ROOT/deploy/systemd/pantry-update.sh"
bash -n "$ROOT/deploy/mdns/pantry-mdns.sh"
bash -n "$ROOT/deploy/mdns/lan-ipv4.sh"
bash -n "$ROOT/deploy/dns/pantry-split-dns.sh"
bash -n "$ROOT/deploy/publish-mode.sh"
bash -n "$ROOT/deploy/check-publish-modes.sh"
bash -n "$ROOT/deploy/check-deploy-hook.sh"
bash -n "$ROOT/deploy/auth-migrate.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

LAST_RC=0
LAST_OUT=""
LAST_DOCKER=""
LAST_IPTABLES=""
LAST_SYSTEMCTL=""
LAST_AVAHI_SERVICE=""
LAST_DNSMASQ=""
LAST_MDNS_UNIT=""
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

  # enable --now runs the unit's ExecStart. The stub does that itself so the
  # firewall script is what the test observes.
  cat > "$bin/systemctl" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${SYSTEMCTL_LOG:?}"
if [[ "$*" == *enable* && "$*" == *pantry-lan-only.service* && -x "${PANTRY_DIR}/firewall/pantry-lan-only.sh" ]]; then
  "${PANTRY_DIR}/firewall/pantry-lan-only.sh"
fi
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
if [[ "$*" == *"/api/session"* ]]; then
  if [[ -n "$out" ]]; then
    printf '%s' "${CURL_SESSION_BODY:-}" > "$out"
  fi
  if [[ "$write_code" == true ]]; then
    printf '%s' "${CURL_SESSION_CODE:-200}"
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
    CURL_SESSION_CODE="${CURL_SESSION_CODE:-200}" \
    CURL_SESSION_BODY="${CURL_SESSION_BODY:-}" \
    PANTRY_DIR="$pantry" \
    PANTRY_SYSTEMD_UNIT_DIR="$work/systemd" \
    PANTRY_SETUP_SKIP_ROOT=1 \
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
  LAST_AVAHI_SERVICE=""
  if [[ -f "$work/avahi/pantry-http.service" ]]; then
    LAST_AVAHI_SERVICE=$(cat "$work/avahi/pantry-http.service")
  fi
  LAST_DNSMASQ=""
  if [[ -f "$work/dnsmasq/pantry-split-horizon.conf" ]]; then
    LAST_DNSMASQ=$(cat "$work/dnsmasq/pantry-split-horizon.conf")
  fi
  LAST_MDNS_UNIT=""
  if [[ -f "$work/systemd/pantry-mdns.service" ]]; then
    LAST_MDNS_UNIT=$(cat "$work/systemd/pantry-mdns.service")
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
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
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
[[ -d "$lan/auth" ]] || fail "LAN setup did not create the auth directory"
[[ ! -e "$lan/auth/household" ]] || fail "LAN setup invented a password hash"
lack "$LAST_CURL" "/api/session" "LAN setup must not probe public sign-in"
have "$LAST_CHOWN" "65532:65532 $lan/auth" "LAN setup still gives the auth directory to uid 65532"
grep -q '^PANTRY_SESSION_SECRET=$' "$lan/.env" || fail "LAN setup filled a session secret"
lack "$LAST_DOCKER" "restart $(basename "$lan")-caddy" "LAN must not restart Caddy"
have "$LAST_IPTABLES" "--dport 9090" "firewall uses HOST_PORT"
have "$LAST_OUT" "Stockbot setup complete" "LAN success"
have "$LAST_OUT" "Non-LAN clients are blocked on port 9090" "LAN firewall summary"
have "$LAST_OUT" "LAN: http://" "LAN URL is printed"
lack "$LAST_OUT" "Home Wi-Fi cannot open" "LAN-only must not warn about hairpin"
have "$(cat "$lan/Caddyfile.legacy")" "No LEGACY_PUBLIC_HOST" "LAN snippet does not redirect"
lack "$(cat "$lan/Caddyfile.legacy")" "redir" "LAN snippet is not a redirect"
lack "$(cat "$lan/Caddyfile.tunnel.legacy")" "redir" "LAN tunnel snippet is not a redirect"
lack "$LAST_SYSTEMCTL" "pantry-mdns.service" "default test run does not install mDNS"
grep -q '0\.0\.0\.0:.*:8080' "$lan/docker-compose.yml" || fail "compose must still publish the LAN port"

# Existing auth.caddy is kept even when BASIC_AUTH_PASSWORD is empty or different.
# The public profile stays on. The password line in .env is not rewritten.
pub=$(mktemp -d)
write_env "$pub" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$pub/auth.caddy"
cp "$pub/auth.caddy" "$pub/auth.caddy.before"
cp "$pub/.env" "$pub/.env.before"
run_setup "$pub"
[[ "$LAST_RC" -eq 0 ]] || fail "existing auth setup exited $LAST_RC: $LAST_OUT"
cmp "$pub/auth.caddy" "$pub/auth.caddy.before" || fail "auth.caddy was regenerated"
grep -q '^BASIC_AUTH_PASSWORD=$' "$pub/.env" || fail "empty BASIC_AUTH_PASSWORD was rewritten"
have "$LAST_DOCKER" "--profile public" "public profile"
lack "$LAST_DOCKER" "docker-compose.tunnel.yml" "certificate mode must not select the tunnel file"
have "$LAST_DOCKER" "restart $(basename "$pub")-caddy" "Caddyfile restart"
lack "$LAST_DOCKER" "hash-password" "must not hash when auth.caddy exists"
lack "$LAST_DOCKER" "stop caddy" "must not disable public HTTPS"
have "$LAST_OUT" "Keeping existing" "kept auth.caddy"
cmp "$pub/auth.caddy" "$pub/auth/household" || fail "household hash was not copied from auth.caddy"
pub_mode=$(stat -c '%a' "$pub/auth/household")
[[ "$pub_mode" == "600" || "$pub_mode" == "640" ]] || fail "household file mode is $pub_mode, want 600 or 640"
have "$LAST_CHOWN" "65532:65532 $pub/auth" "setup chowns the auth directory to uid 65532"
have "$LAST_CHOWN" "65532:65532 $pub/auth/household" "setup chowns the household file to uid 65532"
have "$LAST_CURL" "X-Pantry-Entry: public" "public setup probes sign-in with the public entry header"
have "$LAST_CURL" "/api/session" "public setup probes /api/session"
pub_secret=$(grep '^PANTRY_SESSION_SECRET=' "$pub/.env" | cut -d= -f2-)
[[ ${#pub_secret} -eq 64 ]] || fail "session secret was not saved (len ${#pub_secret})"
have "$LAST_DOCKER" "pull pantry" "public setup pulls the image before dropping basic auth"
have "$LAST_IPTABLES" "--dport 8080" "public setup still firewalls the LAN port"
have "$LAST_OUT" "Home Wi-Fi cannot open https://pantry.example.com" "hairpin warning names the public host"
have "$LAST_OUT" "shared password" "public summary still mentions the password"
lack "$LAST_DOCKER" "stop caddy" "hairpin warning must not stop the public proxy"

# A second run stays idempotent.
run_setup "$pub"
[[ "$LAST_RC" -eq 0 ]] || fail "second setup exited $LAST_RC: $LAST_OUT"
cmp "$pub/auth.caddy" "$pub/auth.caddy.before" || fail "second run regenerated auth.caddy"
cmp "$pub/auth.caddy" "$pub/auth/household" || fail "second run changed the household hash"
have "$LAST_CHOWN" "65532:65532 $pub/auth/household" "second run chowns an existing household file"
pub_secret_again=$(grep '^PANTRY_SESSION_SECRET=' "$pub/.env" | cut -d= -f2-)
[[ "$pub_secret_again" == "$pub_secret" ]] || fail "second run rotated the session secret"
lack "$LAST_DOCKER" "hash-password" "second run hashed a password"

# A different password in .env does not replace an existing hash.
write_env "$pub" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=correct horse battery staple" \
  "HOST_PORT=8080"
run_setup "$pub"
[[ "$LAST_RC" -eq 0 ]] || fail "password-present re-run exited $LAST_RC: $LAST_OUT"
cmp "$pub/auth.caddy" "$pub/auth.caddy.before" || fail "existing auth.caddy changed when BASIC_AUTH_PASSWORD was set"
grep -q '^BASIC_AUTH_PASSWORD=correct horse battery staple$' "$pub/.env" || fail "BASIC_AUTH_PASSWORD was wiped"
lack "$LAST_DOCKER" "hash-password" "hashed despite existing auth.caddy"

# First public setup writes auth.caddy from the password already in .env.
first=$(mktemp -d)
write_env "$first" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=correct-horse-battery" \
  "HOST_PORT=8080"
run_setup "$first"
[[ "$LAST_RC" -eq 0 ]] || fail "first public setup exited $LAST_RC: $LAST_OUT"
[[ -f "$first/auth.caddy" ]] || fail "auth.caddy was not created"
grep -q 'basic_auth bcrypt Pantry' "$first/auth.caddy" || fail "auth.caddy missing basic_auth block"
grep -q 'abcdefghijklmnopqrstuu' "$first/auth.caddy" || fail "auth.caddy missing the hashed password"
grep -q '^BASIC_AUTH_PASSWORD=correct-horse-battery$' "$first/.env" || fail "password was not kept in .env"
cmp "$first/auth.caddy" "$first/auth/household" || fail "first public setup did not copy the new hash"
first_mode=$(stat -c '%a' "$first/auth/household")
[[ "$first_mode" == "600" || "$first_mode" == "640" ]] || fail "new household file mode is $first_mode"
[[ $(grep '^PANTRY_SESSION_SECRET=' "$first/.env" | cut -d= -f2- | wc -c) -eq 65 ]] || fail "first public setup did not save a session secret"
have "$LAST_DOCKER" "hash-password" "first public setup should hash"
have "$LAST_DOCKER" "--profile public" "first public profile"

# A too-short password fails without inventing auth.caddy or clearing .env.
bad=$(mktemp -d)
write_env "$bad" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=short" \
  "HOST_PORT=8080"
run_setup "$bad"
[[ "$LAST_RC" -ne 0 ]] || fail "short password should fail"
[[ ! -e "$bad/auth.caddy" ]] || fail "short password created auth.caddy"
grep -q '^BASIC_AUTH_PASSWORD=short$' "$bad/.env" || fail "short password was wiped"

# No password and no auth.caddy: LAN still converges, public proxy stays off.
incomplete=$(mktemp -d)
write_env "$incomplete" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080"
run_setup "$incomplete"
[[ "$LAST_RC" -eq 0 ]] || fail "incomplete public setup exited $LAST_RC: $LAST_OUT"
lack "$LAST_DOCKER" "--profile public" "incomplete public config must not start the proxy"
[[ ! -e "$incomplete/auth.caddy" ]] || fail "empty password created auth.caddy"
have "$LAST_OUT" "BASIC_AUTH_PASSWORD is empty" "incomplete public warning"

# Opt-out removes the rule instead of installing it.
opt=$(mktemp -d)
write_env "$opt" \
  "PUBLIC_HOST=" \
  "HOST_PORT=8080" \
  "PANTRY_LAN_FIREWALL=off" \
  "BASIC_AUTH_PASSWORD="
run_setup "$opt"
[[ "$LAST_RC" -eq 0 ]] || fail "opt-out setup exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "PANTRY_LAN_FIREWALL=off" "opt-out notice"
lack "$LAST_OUT" "Non-LAN clients are blocked" "opt-out must not install the firewall"
lack "$LAST_SYSTEMCTL" "enable --now pantry-lan-only.service" "opt-out must not enable the firewall unit"
have "$LAST_IPTABLES" "--dport 8080" "opt-out removes rules for HOST_PORT"

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

# Home Wi-Fi name: publish pantry.local and keep the public proxy on.
mdns=$(mktemp -d)
write_env "$mdns" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080" \
  "PANTRY_LAN_IPV4=192.168.1.203"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$mdns/auth.caddy"
PANTRY_SKIP_MDNS=0 run_setup "$mdns"
[[ "$LAST_RC" -eq 0 ]] || fail "mdns setup exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "LAN: http://192.168.1.203:8080" "pinned LAN URL"
have "$LAST_OUT" "LAN name: http://pantry.local:8080" "mDNS name"
have "$LAST_OUT" "Published http://pantry.local:8080" "mDNS publish success"
have "$LAST_SYSTEMCTL" "enable pantry-mdns.service" "mDNS unit enabled"
have "$LAST_SYSTEMCTL" "restart pantry-mdns.service" "mDNS unit restarted"
have "$LAST_MDNS_UNIT" "/opt/pantry/mdns/pantry-mdns.sh" "mDNS unit runs the publisher"
have "$LAST_AVAHI_SERVICE" "<port>8080</port>" "Avahi advertisement uses HOST_PORT"
have "$LAST_DOCKER" "--profile public" "mDNS setup keeps the public proxy"
lack "$LAST_DOCKER" "stop caddy" "mDNS setup must not stop Caddy"
grep -q 'basic_auth bcrypt Pantry' "$mdns/auth.caddy" || fail "mDNS setup rewrote auth.caddy"

# Split horizon maps only the public name at the LAN address.
split=$(mktemp -d)
write_env "$split" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080" \
  "PANTRY_LAN_IPV4=192.168.1.203" \
  "PANTRY_SPLIT_DNS=on"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$split/auth.caddy"
run_setup "$split"
[[ "$LAST_RC" -eq 0 ]] || fail "split-dns setup exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "split-horizon: pantry.example.com -> 192.168.1.203" "split-horizon mapping"
have "$LAST_DNSMASQ" "address=/pantry.example.com/192.168.1.203" "dnsmasq answer"
have "$LAST_DNSMASQ" "listen-address=192.168.1.203" "dnsmasq binds the LAN address"
have "$LAST_DNSMASQ" "no-dhcp-interface=192.168.1.203" "dnsmasq does not serve DHCP"
lack "$LAST_DNSMASQ" "0.0.0.0" "dnsmasq must not listen on every interface"
have "$LAST_DOCKER" "--profile public" "split-horizon keeps the public proxy"
grep -q 'ORIGINAL-HASH' "$split/auth.caddy" || fail "split-horizon rewrote auth.caddy"
# Turning it off removes the answer so the Pi stops overriding the name.
write_env "$split" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080" \
  "PANTRY_LAN_IPV4=192.168.1.203" \
  "PANTRY_SPLIT_DNS=off"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$split/auth.caddy"
run_setup "$split"
[[ "$LAST_RC" -eq 0 ]] || fail "split-dns off exited $LAST_RC: $LAST_OUT"
[[ ! -e "$split/dns/pantry-split-horizon.conf" ]] || fail "split-horizon config remained after off"
lack "$LAST_DNSMASQ" "address=/pantry.example.com/192.168.1.203" "off run must not reinstall dnsmasq"
have "$LAST_DOCKER" "--profile public" "turning split-horizon off keeps the public proxy"

# A public address must not be published as the LAN workaround.
badip=$(mktemp -d)
write_env "$badip" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080" \
  "PANTRY_LAN_IPV4=8.8.8.8" \
  "PANTRY_SPLIT_DNS=on"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$badip/auth.caddy"
run_setup "$badip"
[[ "$LAST_RC" -eq 0 ]] || fail "bad LAN IP must not fail the rest of setup, exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "Split-horizon DNS was not applied" "bad LAN IP skips split-horizon"
lack "$LAST_DNSMASQ" "8.8.8.8" "public address was written into dnsmasq"
have "$LAST_DOCKER" "--profile public" "bad LAN IP still starts the public proxy"
have "$LAST_OUT" "Home Wi-Fi cannot open https://pantry.example.com" "hairpin warning remains without a detected LAN IP"

# shellcheck disable=SC1091
source "$ROOT/deploy/mdns/lan-ipv4.sh"
unset PANTRY_DIR
got=$(PANTRY_LAN_IPV4=10.1.2.3 lan_ipv4)
[[ "$got" == "10.1.2.3" ]] || fail "pinned 10/8 address, got $got"
got=$(PANTRY_LAN_IPV4=172.16.5.5 lan_ipv4)
[[ "$got" == "172.16.5.5" ]] || fail "pinned 172.16/12 address, got $got"
if PANTRY_LAN_IPV4=8.8.8.8 lan_ipv4 >/dev/null 2>&1; then
  fail "public address accepted as a LAN address"
fi
if PANTRY_LAN_IPV4=192.168.001.203 lan_ipv4 >/dev/null 2>&1; then
  fail "leading-zero address accepted"
fi
if PANTRY_LAN_IPV4=192.168.1.256 lan_ipv4 >/dev/null 2>&1; then
  fail "octet 256 accepted"
fi

mdns_bin=$(mktemp -d)
cat > "$mdns_bin/avahi-publish-address" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" > "${AVAHI_LOG:?}"
exit 0
EOF
chmod +x "$mdns_bin/avahi-publish-address"
mdns_log=$(mktemp)
AVAHI_LOG="$mdns_log" PANTRY_LAN_IPV4=192.168.1.203 PATH="$mdns_bin:$PATH" \
  bash "$ROOT/deploy/mdns/pantry-mdns.sh"
have "$(cat "$mdns_log")" "-R pantry.local 192.168.1.203" "publisher arguments"
set +e
AVAHI_LOG="$mdns_log" PANTRY_LAN_IPV4=1.2.3.4 PATH="$mdns_bin:$PATH" \
  bash "$ROOT/deploy/mdns/pantry-mdns.sh" >/dev/null 2>&1
mdns_rc=$?
set -e
[[ "$mdns_rc" -ne 0 ]] || fail "publisher accepted a public address"

# A token selects the tunnel even when ACME_EMAIL is empty and --tunnel is omitted.
# The certificate profile stays off, and the token is not printed.
tun=$(mktemp -d)
write_env "$tun" \
  "PUBLIC_HOST=stockbot.rhionin.com" \
  "ACME_EMAIL=" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080" \
  "PUBLISH_MODE=" \
  "CLOUDFLARE_TUNNEL_TOKEN=test-tunnel-token"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$tun/auth.caddy"
cp "$tun/auth.caddy" "$tun/auth.caddy.before"
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
cmp "$tun/auth.caddy" "$tun/auth.caddy.before" || fail "tunnel setup regenerated auth.caddy"
lack "$LAST_DOCKER" "hash-password" "tunnel must not hash when auth.caddy exists"
# publish --tunnel is the same switch once the token is already in .env.
run_setup "$tun" publish --tunnel
[[ "$LAST_RC" -eq 0 ]] || fail "publish --tunnel exited $LAST_RC: $LAST_OUT"
have "$LAST_DOCKER" "--profile tunnel" "publish --tunnel"
cmp "$tun/auth.caddy" "$tun/auth.caddy.before" || fail "publish --tunnel regenerated auth.caddy"

# PUBLISH_MODE=acme does not override a token that is still set.
write_env "$tun" \
  "PUBLIC_HOST=stockbot.rhionin.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080" \
  "PUBLISH_MODE=acme" \
  "CLOUDFLARE_TUNNEL_TOKEN=test-tunnel-token"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$tun/auth.caddy"
run_setup "$tun"
[[ "$LAST_RC" -eq 0 ]] || fail "token overrides acme mode, exited $LAST_RC: $LAST_OUT"
have "$LAST_DOCKER" "--profile tunnel" "token wins over PUBLISH_MODE=acme"
lack "$LAST_DOCKER" "--profile public" "token must not open the certificate profile"

# Tunnel without a token fails before containers start, and does not fall through to ACME.
notoken=$(mktemp -d)
write_env "$notoken" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080" \
  "PUBLISH_MODE=tunnel" \
  "CLOUDFLARE_TUNNEL_TOKEN="
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$notoken/auth.caddy"
run_setup "$notoken" publish --tunnel
[[ "$LAST_RC" -ne 0 ]] || fail "tunnel without a token should fail"
lack "$LAST_DOCKER" "up -d" "missing token must not start containers"
have "$LAST_OUT" "CLOUDFLARE_TUNNEL_TOKEN" "missing token names the variable"
lack "$LAST_DOCKER" "--profile public" "missing token must not start the certificate proxy"

# Token set, password not ready: LAN still comes up, tunnel stays off.
unready=$(mktemp -d)
write_env "$unready" \
  "PUBLIC_HOST=stockbot.rhionin.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080" \
  "CLOUDFLARE_TUNNEL_TOKEN=test-tunnel-token"
run_setup "$unready"
[[ "$LAST_RC" -eq 0 ]] || fail "unready tunnel exited $LAST_RC: $LAST_OUT"
lack "$LAST_DOCKER" "--profile tunnel" "unready tunnel must not start cloudflared"
lack "$LAST_DOCKER" "--profile public" "unready tunnel must not start the certificate proxy"
have "$LAST_OUT" "The tunnel was not started" "unready tunnel explains why"
[[ ! -e "$unready/auth.caddy" ]] || fail "empty password created auth.caddy"

# Split-horizon DNS would point the public name at the Pi. Tunnel mode removes it.
tunsplit=$(mktemp -d)
write_env "$tunsplit" \
  "PUBLIC_HOST=stockbot.rhionin.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080" \
  "PANTRY_LAN_IPV4=192.168.1.203" \
  "PANTRY_SPLIT_DNS=on" \
  "CLOUDFLARE_TUNNEL_TOKEN=test-tunnel-token"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$tunsplit/auth.caddy"
run_setup "$tunsplit"
[[ "$LAST_RC" -eq 0 ]] || fail "tunnel split-dns exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "PANTRY_SPLIT_DNS is on" "tunnel warns about split-horizon"
have "$LAST_OUT" "Split-horizon DNS was removed" "tunnel removes split-horizon"
lack "$LAST_DNSMASQ" "address=/stockbot.rhionin.com/192.168.1.203" "tunnel must not install dnsmasq"
have "$LAST_DOCKER" "--profile tunnel" "split-horizon removal keeps the tunnel"

# The previous public name redirects only when it is a different hostname.
legacy=$(mktemp -d)
write_env "$legacy" \
  "PUBLIC_HOST=stockbot.rhionin.com" \
  "LEGACY_PUBLIC_HOST=pantry.rhionin.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080" \
  "CLOUDFLARE_TUNNEL_TOKEN=test-tunnel-token"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$legacy/auth.caddy"
run_setup "$legacy"
[[ "$LAST_RC" -eq 0 ]] || fail "legacy host setup exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "https://pantry.rhionin.com redirects to https://stockbot.rhionin.com" "legacy redirect is logged"
have "$(cat "$legacy/Caddyfile.legacy")" 'pantry.rhionin.com {' "certificate snippet names the previous host"
# shellcheck disable=SC2016 # Caddy expands {$PUBLIC_HOST}; the snippet keeps the placeholder.
have "$(cat "$legacy/Caddyfile.legacy")" 'redir https://{$PUBLIC_HOST}{uri} 308' "certificate snippet redirects"
have "$(cat "$legacy/Caddyfile.tunnel.legacy")" 'http://pantry.rhionin.com {' "tunnel snippet names the previous host"
# shellcheck disable=SC2016 # Caddy expands {$PUBLIC_HOST}; the snippet keeps the placeholder.
have "$(cat "$legacy/Caddyfile.tunnel.legacy")" 'redir https://{$PUBLIC_HOST}{uri} 308' "tunnel snippet redirects"
lack "$(cat "$legacy/Caddyfile.legacy")" "http://pantry.rhionin.com" "certificate snippet is not the tunnel site address"

# The same name twice would make Caddy refuse to start. Leave a comment instead.
write_env "$legacy" \
  "PUBLIC_HOST=stockbot.rhionin.com" \
  "LEGACY_PUBLIC_HOST=stockbot.rhionin.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$legacy/auth.caddy"
run_setup "$legacy"
[[ "$LAST_RC" -eq 0 ]] || fail "duplicate legacy host exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "is not a different hostname" "duplicate legacy host is ignored"
have "$(cat "$legacy/Caddyfile.legacy")" "No LEGACY_PUBLIC_HOST" "duplicate legacy host writes a comment"
lack "$(cat "$legacy/Caddyfile.tunnel.legacy")" "redir" "duplicate legacy host does not redirect"

# A scheme is not a hostname. The primary site still starts.
write_env "$legacy" \
  "PUBLIC_HOST=stockbot.rhionin.com" \
  "LEGACY_PUBLIC_HOST=https://pantry.rhionin.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$legacy/auth.caddy"
run_setup "$legacy"
[[ "$LAST_RC" -eq 0 ]] || fail "invalid legacy host exited $LAST_RC: $LAST_OUT"
have "$LAST_OUT" "without a scheme or path" "invalid legacy host is named"
lack "$(cat "$legacy/Caddyfile.legacy")" "redir" "invalid legacy host does not redirect"
have "$LAST_DOCKER" "--profile public" "invalid legacy host still starts the primary proxy"

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

# A secret the operator set is left alone.
custom=$(mktemp -d)
write_env "$custom" \
  "DEPLOY_HOOK_SECRET=not-generated" \
  "HOST_PORT=8080" \
  "PUBLIC_HOST=pantry.example.com" \
  "BASIC_AUTH_PASSWORD=" \
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
  "PUBLIC_HOST=" \
  "BASIC_AUTH_PASSWORD="
run_setup "$updates" --with-updates
[[ "$LAST_RC" -eq 0 ]] || fail "--with-updates exited $LAST_RC: $LAST_OUT"
have "$LAST_SYSTEMCTL" "enable --now pantry-update.timer" "timer enabled"
have "$LAST_SYSTEMCTL" "enable --now pantry-update.path" "path unit enabled"

# An unreadable password file is 503 from /api/session. Setup must say so
# instead of reporting success.
unavailable=$(mktemp -d)
write_env "$unavailable" \
  "PUBLIC_HOST=pantry.example.com" \
  "ACME_EMAIL=you@example.com" \
  "BASIC_AUTH_USER=pantry" \
  "BASIC_AUTH_PASSWORD=" \
  "HOST_PORT=8080"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$unavailable/auth.caddy"
CURL_SESSION_CODE=503 \
  CURL_SESSION_BODY='{"error":"Sign-in is unavailable right now."}' \
  run_setup "$unavailable"
[[ "$LAST_RC" -ne 0 ]] || fail "unavailable sign-in should fail setup"
have "$LAST_OUT" "Sign-in is unavailable" "unavailable sign-in is a loud error"
have "$LAST_OUT" "uid 65532" "unavailable sign-in names the container user"
have "$LAST_CURL" "X-Pantry-Entry: public" "unavailable probe used the public entry header"

# pantry-update.sh copies the hash, via the same function, before compose up.
updater="$ROOT/deploy/systemd/pantry-update.sh"
sync_line=$(grep -n 'sync_household_credential' "$updater" | head -1 | cut -d: -f1)
up_line=$(grep -n 'up -d' "$updater" | head -1 | cut -d: -f1)
[[ -n "$sync_line" && -n "$up_line" && "$sync_line" -lt "$up_line" ]] || fail "pantry-update.sh must copy the household hash before compose up"

# A file left root-owned by a partial run is chowned again, and tightened to 600.
# A failed chown stops the copy unless the no-sudo test seam is set.
owned=$(mktemp -d)
mkdir -p "$owned/auth"
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry ORIGINAL-HASH' '}' > "$owned/auth.caddy"
printf '%s\n' 'stale' > "$owned/auth/household"
chmod 644 "$owned/auth/household"
own_bin=$(mktemp -d)
own_log=$(mktemp)
cat > "$own_bin/chown" << 'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${CHOWN_LOG:?}"
exit 0
EOF
chmod +x "$own_bin/chown"
# shellcheck disable=SC1091
CHOWN_LOG="$own_log" PANTRY_DIR="$owned" PATH="$own_bin:$PATH" \
  bash -c 'source "$1"; sync_household_credential' _ "$ROOT/deploy/auth-migrate.sh"
cmp "$owned/auth.caddy" "$owned/auth/household" || fail "direct copy did not replace a stale household file"
owned_mode=$(stat -c '%a' "$owned/auth/household")
[[ "$owned_mode" == "600" || "$owned_mode" == "640" ]] || fail "direct copy left mode $owned_mode"
have "$(cat "$own_log")" "65532:65532 $owned/auth/household" "direct copy chowns an existing household file"
have "$(cat "$own_log")" "65532:65532 $owned/auth" "direct copy chowns the auth directory"

fail_bin=$(mktemp -d)
cat > "$fail_bin/chown" << 'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$fail_bin/chown"
set +e
PANTRY_DIR="$owned" PATH="$fail_bin:$PATH" \
  bash -c 'source "$1"; sync_household_credential' _ "$ROOT/deploy/auth-migrate.sh" >/tmp/pantry-chown-fail.out 2>&1
fail_rc=$?
set -e
[[ "$fail_rc" -ne 0 ]] || fail "chown failure should stop the household copy"
have "$(cat /tmp/pantry-chown-fail.out)" "Could not give" "chown failure names the path"
set +e
PANTRY_SETUP_SKIP_ROOT=1 PANTRY_DIR="$owned" PATH="$fail_bin:$PATH" \
  bash -c 'source "$1"; sync_household_credential' _ "$ROOT/deploy/auth-migrate.sh" >/tmp/pantry-chown-skip.out 2>&1
skip_rc=$?
set -e
[[ "$skip_rc" -eq 0 ]] || fail "PANTRY_SETUP_SKIP_ROOT should warn instead of failing chown, rc=$skip_rc"
have "$(cat /tmp/pantry-chown-skip.out)" "[warn] Could not give" "skip-root chown failure is a warning"
unset CURL_SESSION_CODE CURL_SESSION_BODY

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
  "PUBLIC_HOST=" \
  "BASIC_AUTH_PASSWORD="
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
printf '%s\n' 'BASIC_AUTH_USER=pantry' 'SENTRY_DSN=keep-this' > "$sentry_dir/.env"
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

rm -rf "$lan" "$pub" "$first" "$bad" "$incomplete" "$opt" "$mdns" "$split" "$badip" "$mdns_bin" "$tun" "$notoken" "$unready" "$tunsplit" "$mode_dir" "$custom" "$updates" "$unavailable" "$owned" "$own_bin" "$fail_bin" "$rule_fix" "$usb_dir" "$name_dir" "$none_dir" "$sentry_dir"
rm -f /tmp/pantry-chown-fail.out /tmp/pantry-chown-skip.out
echo "setup_apply_test ok"
