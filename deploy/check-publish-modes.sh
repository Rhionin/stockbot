#!/usr/bin/env bash
# Validate both publish modes: compose config, and caddy adapt.
# The certificate path is docker-compose.yml + Caddyfile.
# The tunnel path adds docker-compose.tunnel.yml + Caddyfile.tunnel.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

fail() {
  echo "FAIL: $*" >&2
  exit 1
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

# compose_service_image prints the image of a two-space-indented service.
# The compose files are the source of the pin; callers compare them instead
# of keeping another copy of the tag.
compose_service_image() {
  local file="$1" service="$2" line in_svc=0 image=""
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" =~ ^[[:space:]]{2}${service}:[[:space:]]*$ ]]; then
      in_svc=1
      continue
    fi
    if [[ "$in_svc" -eq 1 && "$line" =~ ^[[:space:]]{2}[A-Za-z0-9_-]+:[[:space:]]*$ ]]; then
      break
    fi
    if [[ "$in_svc" -eq 1 && "$line" =~ ^[[:space:]]+image:[[:space:]]*(.*)$ ]]; then
      image="${BASH_REMATCH[1]}"
      image="${image%%#*}"
      image="${image%"${image##*[![:space:]]}"}"
      image="${image#"${image%%[![:space:]]*}"}"
      if [[ ${#image} -ge 2 ]]; then
        local first="${image:0:1}" last="${image: -1}"
        if [[ "$first" == "$last" && ( "$first" == '"' || "$first" == "'" ) ]]; then
          image="${image:1}"
          image="${image%?}"
        fi
      fi
      break
    fi
  done < "$file"
  if [[ -z "$image" ]]; then
    fail "could not read the ${service} image from ${file}"
  fi
  printf '%s\n' "$image"
}

# require_pinned_caddy rejects a floating tag. latest is not a version bump.
require_pinned_caddy() {
  local image="$1" tag
  tag="${image#caddy:}"
  if [[ "$image" == "$tag" || -z "$tag" || "$tag" == latest || "$tag" == latest-* ]]; then
    fail "Caddy image must be a pinned official tag (caddy:<version>), got ${image}"
  fi
}

# assert_only_cloudflared_on_trusted_ip walks `docker compose config`.
# Service names are indented two spaces. 10.77.77.2 is the address
# Caddyfile.tunnel trusts, so no other cf-tunnel service may be given it.
# caddy-tunnel must keep a fixed address of its own.
assert_only_cloudflared_on_trusted_ip() {
  local cfg="$1" svc="" bad="" saw_cf=0 saw_caddy=0 line
  while IFS= read -r line; do
    if [[ "$line" =~ ^[[:space:]]{2}([A-Za-z0-9_-]+):[[:space:]]*$ ]]; then
      svc="${BASH_REMATCH[1]}"
      continue
    fi
    if [[ "$line" =~ ^[^[:space:]] ]]; then
      svc=""
      continue
    fi
    if [[ -n "$svc" && "$line" == *'ipv4_address: 10.77.77.2'* ]]; then
      if [[ "$svc" != cloudflared ]]; then
        bad="$svc"
      else
        saw_cf=1
      fi
    fi
    if [[ "$svc" == caddy-tunnel && "$line" == *'ipv4_address: 10.77.77.3'* ]]; then
      saw_caddy=1
    fi
  done <<< "$cfg"
  if [[ -n "$bad" ]]; then
    fail "$bad must not use 10.77.77.2; only cloudflared may"
  fi
  if [[ "$saw_cf" -ne 1 ]]; then
    fail "cloudflared must be pinned to 10.77.77.2"
  fi
  if [[ "$saw_caddy" -ne 1 ]]; then
    fail "caddy-tunnel must be pinned to 10.77.77.3"
  fi
}

# Both publish paths must name the same Caddy image. Reading it here means a
# Dependabot bump of the compose tags does not need a matching edit below.
cert_caddy=$(compose_service_image deploy/docker-compose.yml caddy)
tunnel_caddy=$(compose_service_image deploy/docker-compose.tunnel.yml caddy-tunnel)
if [[ "$cert_caddy" != "$tunnel_caddy" ]]; then
  fail "certificate and tunnel compose files pin different Caddy images ($cert_caddy vs $tunnel_caddy)"
fi
require_pinned_caddy "$cert_caddy"

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  compose() { docker compose "$@"; }
elif command -v docker-compose >/dev/null 2>&1; then
  compose() { docker-compose "$@"; }
else
  fail "docker compose is required to validate publish modes"
fi

if command -v caddy >/dev/null 2>&1; then
  caddy_adapt() {
    local cfg="$1" dir="$2"
    (
      cd "$dir"
      PUBLIC_HOST=stockbot.rhionin.com ACME_EMAIL=cj@example.com \
        caddy adapt --config "$cfg" --pretty
    )
  }
elif command -v docker >/dev/null 2>&1; then
  caddy_adapt() {
    local cfg="$1" dir="$2"
    docker run --rm \
      -e PUBLIC_HOST=stockbot.rhionin.com \
      -e ACME_EMAIL=cj@example.com \
      -v "$dir:/etc/caddy:ro" \
      "$cert_caddy" \
      caddy adapt --config "/etc/caddy/$(basename "$cfg")" --pretty
  }
else
  fail "caddy or docker is required to adapt the Caddyfiles"
fi

# Site blocks start at the same comment and must stay byte-for-byte identical
# so a public-path change cannot land in only one mode.
route_body() {
  local file="$1" text
  text=$(cat "$file")
  local marker=$'\n\t# handle is first-match.'
  if [[ "$text" != *"$marker"* ]]; then
    fail "$file is missing the shared route block"
  fi
  printf '%s\n' "${text#*"$marker"}"
}

acme_routes=$(route_body deploy/Caddyfile)
tunnel_routes=$(route_body deploy/Caddyfile.tunnel)
if [[ "$acme_routes" != "$tunnel_routes" ]]; then
  fail "Caddyfile and Caddyfile.tunnel route blocks differ"
fi

# caddy_handle_block prints one handle, from its header through the brace
# that closes it. Comments are left in place; callers skip them.
caddy_handle_block() {
  local file="$1" header="$2"
  awk -v header="$header" '
    !capturing && index($0, header) { capturing = 1 }
    capturing {
      print
      opens = gsub(/{/, "{")
      closes = gsub(/}/, "}")
      depth += opens - closes
      if (depth <= 0 && opens + closes > 0) exit
    }
  ' "$file"
}

# assert_request_body_limits checks the site cap and the device cap in one
# Caddyfile. The 1 MB directive is the site default, so telemetry, the
# build identity, the deploy hook, and the public catch-all (Sentry tunnel
# included) do not set their own. /api/device/* is the only tighter cap.
assert_request_body_limits() {
  local file="$1" site device telemetry deploy
  site=$(awk '
    $0 ~ /^[[:space:]]*request_body[[:space:]]*\{/ && !seen {
      seen = 1
      capturing = 1
    }
    capturing {
      print
      opens = gsub(/{/, "{")
      closes = gsub(/}/, "}")
      depth += opens - closes
      if (depth <= 0 && opens + closes > 0) exit
    }
  ' "$file")
  have "$site" $'request_body {' "$file site request_body"
  have "$site" "max_size 1MB" "$file site cap is 1MB"
  device=$(caddy_handle_block "$file" "handle /api/device/*")
  [[ -n "$device" ]] || fail "$file is missing handle /api/device/*"
  have "$device" "max_size 64KB" "$file device cap is 64KB"
  have "$device" "header_up -X-Pantry-Entry" "$file device handle strips a client entry header"
  lack "$device" "header_up X-Pantry-Entry " "$file device handle must not stamp an entry header"
  have "$device" "header_up X-Pantry-Client-IP {client_ip}" "$file device handle passes the visitor address"
  have "$device" "header_up X-Forwarded-For {client_ip}" "$file device handle forwards the visitor address"
  have "$device" "reverse_proxy pantry:8080" "$file device handle proxies to the listener"
  lack "$device" "max_size 1MB" "$file device handle must not use the site cap"
  telemetry=$(caddy_handle_block "$file" "handle @telemetry")
  lack "$telemetry" "request_body" "$file telemetry handle stays under the 1MB site cap"
  deploy=$(caddy_handle_block "$file" "handle @deploy")
  lack "$deploy" "request_body" "$file deploy hook stays under the 1MB site cap"
  # The site cap is the first request_body. A second one is the device cap.
  if [[ $(grep -c '^[[:space:]]*max_size 1MB$' "$file") -ne 1 ]]; then
    fail "$file must set max_size 1MB once"
  fi
  if [[ $(grep -c '^[[:space:]]*max_size 64KB$' "$file") -ne 1 ]]; then
    fail "$file must set max_size 64KB once"
  fi
  local site_at device_at household_at
  site_at=$(grep -n '^[[:space:]]*max_size 1MB$' "$file" | head -n 1 | cut -d: -f1)
  device_at=$(grep -n 'handle /api/device/\*' "$file" | head -n 1 | cut -d: -f1)
  household_at=$(grep -n '# Everything else is the household site\.' "$file" | head -n 1 | cut -d: -f1 || true)
  [[ -n "$site_at" && -n "$device_at" && "$site_at" -lt "$device_at" ]] || fail "$file site cap must come before the device handle"
  # handle is first-match. The catch-all after this comment would shadow
  # /api/device/* if that handle were moved below it.
  if [[ -z "$household_at" || -z "$device_at" || "$device_at" -ge "$household_at" ]]; then
    fail "$file device handle must come before the household catch-all"
  fi
}

assert_request_body_limits deploy/Caddyfile
assert_request_body_limits deploy/Caddyfile.tunnel

# Every handle proxies to the one listener. A client-supplied entry header
# is stripped. Stamping X-Pantry-Entry would not be the listener's entry.
assert_public_listener() {
  local file="$1" text proxies targets
  text=$(cat "$file")
  have "$text" "reverse_proxy pantry:8080" "$file proxies to the listener"
  lack "$text" "pantry:8081" "$file must not proxy to a second listener"
  have "$text" "header_up -X-Pantry-Entry" "$file strips a client entry header"
  lack "$text" "header_up X-Pantry-Entry " "$file must not stamp an entry header"
  lack "$text" "X-Pantry-Entry public" "$file must not stamp a public entry"
  proxies=$(grep -c 'reverse_proxy ' "$file" || true)
  targets=$(grep -c 'reverse_proxy pantry:8080' "$file" || true)
  [[ "$proxies" -ge 1 && "$proxies" == "$targets" ]] || fail "$file has a reverse_proxy that is not the listener"
}

assert_public_listener deploy/Caddyfile
assert_public_listener deploy/Caddyfile.tunnel

empty_env=$(mktemp)
public_cfg=$(compose --env-file "$empty_env" -f deploy/docker-compose.yml --profile public config)
tunnel_cfg=$(compose --env-file "$empty_env" -f deploy/docker-compose.yml -f deploy/docker-compose.tunnel.yml --profile tunnel config)
# Profile grocy must not include grocy-sync. The sidecar stays out of a
# default config too, and appears only on profile grocy-sync.
grocy_default=$(compose --env-file "$empty_env" -f deploy/docker-compose.yml -f deploy/docker-compose.grocy.yml config)
grocy_profile=$(compose --env-file "$empty_env" -f deploy/docker-compose.yml -f deploy/docker-compose.grocy.yml --profile grocy config)
grocy_sync_profile=$(compose --env-file "$empty_env" -f deploy/docker-compose.yml -f deploy/docker-compose.grocy.yml --profile grocy-sync config)
rm -f "$empty_env"

# pantry publishes the listener on loopback only. Caddy dials pantry:8080
# on the compose network, which is not this published host port.
pantry_ports=$(awk '
  $0 ~ /^  pantry:$/ { in_s = 1; next }
  in_s && /^  [^ ]/ { in_s = 0 }
  in_s && /^    ports:$/ { in_p = 1; next }
  in_s && in_p && /^    [^ ]/ { in_p = 0 }
  in_s && in_p { print }
' <<< "$public_cfg")
have "$pantry_ports" 'target: 8080' "pantry publishes the listener"
have "$pantry_ports" 'host_ip: 127.0.0.1' "pantry publishes on loopback"
lack "$pantry_ports" '0.0.0.0' "pantry must not publish on every interface"
lack "$pantry_ports" '8081' "pantry must not publish a second listener"
have "$public_cfg" 'target: /run/setup' "pantry bind-mounts the setup directory"
host_env=$(mktemp)
printf '%s\n' 'PUBLIC_HOST=stockbot.rhionin.com' 'APP_BASE_URL=' > "$host_env"
host_cfg=$(compose --env-file "$host_env" -f deploy/docker-compose.yml config)
rm -f "$host_env"
have "$host_cfg" 'https://stockbot.rhionin.com' "empty APP_BASE_URL follows PUBLIC_HOST"
kept_env=$(mktemp)
printf '%s\n' 'PUBLIC_HOST=stockbot.rhionin.com' 'APP_BASE_URL=https://operator.example' > "$kept_env"
kept_cfg=$(compose --env-file "$kept_env" -f deploy/docker-compose.yml config)
rm -f "$kept_env"
have "$kept_cfg" 'https://operator.example' "a set APP_BASE_URL is kept"
lack "$kept_cfg" 'https://stockbot.rhionin.com' "a set APP_BASE_URL is not replaced from PUBLIC_HOST"
setup_mount=$(awk '
  $0 ~ /target: \/run\/setup/ { capture = 1 }
  capture { print }
  capture && /target: \/run\/setup/ { next }
  capture && /^        [a-z_]+:/ { next }
  capture && /^      - / { exit }
' <<< "$public_cfg")
lack "$setup_mount" 'read_only: true' "setup mount must be read-write"
lack "$public_cfg" './auth:/etc/pantry/auth' "public compose must not bind-mount legacy auth"
lack "$tunnel_cfg" './auth:/etc/pantry/auth' "tunnel compose must not bind-mount legacy auth"
have "$public_cfg" 'published: "80"' "certificate mode publishes port 80"
have "$public_cfg" 'published: "443"' "certificate mode publishes port 443"
have "$public_cfg" "$cert_caddy" "certificate mode pins Caddy"
lack "$public_cfg" 'cloudflare/cloudflared' "certificate mode must not start cloudflared"
lack "$public_cfg" '10.77.77.2' "certificate mode must not add the tunnel network"
lack "$public_cfg" 'Caddyfile.tunnel' "certificate mode must not mount the tunnel Caddyfile"

lack "$tunnel_cfg" 'published: "80"' "tunnel mode must not publish port 80"
lack "$tunnel_cfg" 'published: "443"' "tunnel mode must not publish port 443"
have "$tunnel_cfg" 'cloudflare/cloudflared:2026.10.3' "tunnel mode pins cloudflared"
lack "$grocy_default" 'container_name: pantry-grocy-sync' "default compose must not start grocy-sync"
lack "$grocy_profile" 'container_name: pantry-grocy-sync' "grocy profile must not start grocy-sync"
have "$grocy_sync_profile" 'container_name: pantry-grocy-sync' "grocy-sync profile names the sidecar"
have "$tunnel_cfg" "$tunnel_caddy" "tunnel mode pins Caddy"
have "$tunnel_cfg" 'ipv4_address: 10.77.77.2' "cloudflared has the trusted address"
have "$tunnel_cfg" 'ipv4_address: 10.77.77.3' "caddy has a fixed address"
have "$tunnel_cfg" 'ip_range: 10.77.77.4/30' "dynamic addresses cannot take .2 or .3"
have "$tunnel_cfg" 'gateway: 10.77.77.1' "cf-tunnel gateway stays .1"
assert_only_cloudflared_on_trusted_ip "$tunnel_cfg"
lack "$public_cfg" 'Caddyfile.legacy' "certificate mode must not mount a legacy snippet"
have "$tunnel_cfg" 'Caddyfile.tunnel' "tunnel mode mounts Caddyfile.tunnel"
lack "$tunnel_cfg" 'Caddyfile.tunnel.legacy' "tunnel mode must not mount a legacy snippet"
have "$tunnel_cfg" 'target: /etc/caddy/Caddyfile' "tunnel Caddyfile is what Caddy loads"
have "$tunnel_cfg" 'container_name: pantry-cloudflared' "cloudflared container name"
lack "$tunnel_cfg" 'CLOUDFLARE_TUNNEL_TOKEN: null' "token env must be the empty string when unset, not null"
# Command is tunnel run. The image entrypoint supplies cloudflared --no-autoupdate.
have "$tunnel_cfg" $'- tunnel\n      - run' "cloudflared runs tunnel run"

adapt_dir=$(mktemp -d)
cp deploy/Caddyfile deploy/Caddyfile.tunnel "$adapt_dir/"

acme_json=$(caddy_adapt "$adapt_dir/Caddyfile" "$adapt_dir")
tunnel_json=$(caddy_adapt "$adapt_dir/Caddyfile.tunnel" "$adapt_dir")

have "$acme_json" 'stockbot.rhionin.com' "certificate adapt expands PUBLIC_HOST"
have "$acme_json" '/api/telemetry' "certificate adapt keeps telemetry public"
have "$acme_json" '/api/build' "certificate adapt keeps the build identity public"
have "$acme_json" '/api/deploy-hook' "certificate adapt keeps the deploy hook public"
have "$acme_json" '/brand/logo.png' "certificate adapt keeps the brand mark public"
have "$acme_json" 'cj@example.com' "certificate adapt keeps the ACME email"
lack "$acme_json" '10.77.77.2' "certificate adapt must not trust the tunnel address"
lack "$acme_json" 'pantry.rhionin.com' "certificate adapt does not claim the previous name by default"

have "$tunnel_json" 'stockbot.rhionin.com' "tunnel adapt expands PUBLIC_HOST"
have "$tunnel_json" '/api/telemetry' "tunnel adapt keeps telemetry public"
have "$tunnel_json" '/api/build' "tunnel adapt keeps the build identity public"
have "$tunnel_json" '/api/deploy-hook' "tunnel adapt keeps the deploy hook public"
have "$tunnel_json" '/api/telemetry/client' "tunnel adapt keeps the client report public"
have "$tunnel_json" '/brand/logo.png' "tunnel adapt keeps the brand mark public"
have "$tunnel_json" '/terms' "tunnel adapt keeps terms public"
have "$tunnel_json" '/privacy' "tunnel adapt keeps privacy public"
have "$tunnel_json" '/api/events' "tunnel adapt keeps the event stream unbuffered"
lack "$tunnel_json" 'pantry.rhionin.com' "tunnel adapt does not claim the previous name by default"
have "$tunnel_json" '10.77.77.2/32' "tunnel adapt trusts only cloudflared"
have "$tunnel_json" 'Cf-Connecting-Ip' "tunnel adapt reads the visitor address"
lack "$tunnel_json" 'cj@example.com' "tunnel adapt must not configure an ACME account"
lack "$tunnel_json" '"module": "acme"' "tunnel adapt must not request a certificate"
lack "$acme_json" "http_basic" "certificate adapt must not use browser basic auth"
lack "$tunnel_json" "http_basic" "tunnel adapt must not use browser basic auth"
have "$acme_json" "X-Pantry-Entry" "certificate adapt strips the entry header"
have "$tunnel_json" "X-Pantry-Entry" "tunnel adapt strips the entry header"
lack "$acme_json" "X-Pantry-Entry public" "certificate adapt must not stamp an entry header"
lack "$tunnel_json" "X-Pantry-Entry public" "tunnel adapt must not stamp an entry header"
have "$acme_json" "pantry:8080" "certificate adapt dials the listener"
have "$tunnel_json" "pantry:8080" "tunnel adapt dials the listener"
lack "$acme_json" "pantry:8081" "certificate adapt must not dial a second listener"
lack "$tunnel_json" "pantry:8081" "tunnel adapt must not dial a second listener"
have "$tunnel_json" "X-Forwarded-Proto" "tunnel adapt sets the public scheme"
# humanize sizes: 1MB is 1000000 bytes, 64KB is 64000. The device route is
# the only place the tighter cap is compiled.
have "$acme_json" '"max_size": 1000000' "certificate adapt enforces the 1MB site cap"
have "$tunnel_json" '"max_size": 1000000' "tunnel adapt enforces the 1MB site cap"
have "$acme_json" '"max_size": 64000' "certificate adapt enforces the 64KB device cap"
have "$tunnel_json" '"max_size": 64000' "tunnel adapt enforces the 64KB device cap"
have "$acme_json" "/api/device/*" "certificate adapt keeps the device route"
have "$tunnel_json" "/api/device/*" "tunnel adapt keeps the device route"
# Caddy's adapt output canonicalizes the header to Client-Ip.
have "$acme_json" "X-Pantry-Client-Ip" "certificate adapt passes the visitor address"
have "$tunnel_json" "X-Pantry-Client-Ip" "tunnel adapt passes the visitor address"
# Stockbot sends these on /sw.js. A second copy is joined with the app's header.
lack "$acme_json" "Service-Worker-Allowed" "certificate adapt must not set Service-Worker-Allowed"
lack "$tunnel_json" "Service-Worker-Allowed" "tunnel adapt must not set Service-Worker-Allowed"
lack "$acme_json" "no-cache, no-transform" "certificate adapt must not set the worker Cache-Control"
lack "$tunnel_json" "no-cache, no-transform" "tunnel adapt must not set the worker Cache-Control"

rm -rf "$adapt_dir"

echo "check-publish-modes ok"
