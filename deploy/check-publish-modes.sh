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

empty_env=$(mktemp)
public_cfg=$(compose --env-file "$empty_env" -f deploy/docker-compose.yml --profile public config)
tunnel_cfg=$(compose --env-file "$empty_env" -f deploy/docker-compose.yml -f deploy/docker-compose.tunnel.yml --profile tunnel config)
# Profile grocy must not include grocy-sync. The sidecar stays out of a
# default config too, and appears only on profile grocy-sync.
grocy_default=$(compose --env-file "$empty_env" -f deploy/docker-compose.yml -f deploy/docker-compose.grocy.yml config)
grocy_profile=$(compose --env-file "$empty_env" -f deploy/docker-compose.yml -f deploy/docker-compose.grocy.yml --profile grocy config)
grocy_sync_profile=$(compose --env-file "$empty_env" -f deploy/docker-compose.yml -f deploy/docker-compose.grocy.yml --profile grocy-sync config)
rm -f "$empty_env"

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
have "$public_cfg" 'Caddyfile.legacy' "certificate mode mounts the legacy snippet"
have "$tunnel_cfg" 'Caddyfile.tunnel' "tunnel mode mounts Caddyfile.tunnel"
have "$tunnel_cfg" 'Caddyfile.tunnel.legacy' "tunnel mode mounts the legacy snippet"
have "$tunnel_cfg" 'target: /etc/caddy/Caddyfile' "tunnel Caddyfile is what Caddy loads"
have "$tunnel_cfg" 'container_name: pantry-cloudflared' "cloudflared container name"
lack "$tunnel_cfg" 'CLOUDFLARE_TUNNEL_TOKEN: null' "token env must be the empty string when unset, not null"
# Command is tunnel run. The image entrypoint supplies cloudflared --no-autoupdate.
have "$tunnel_cfg" $'- tunnel\n      - run' "cloudflared runs tunnel run"

adapt_dir=$(mktemp -d)
cp deploy/Caddyfile deploy/Caddyfile.tunnel deploy/Caddyfile.legacy "$adapt_dir/"
# shellcheck disable=SC2016 # bcrypt hashes start with $2
printf '%s\n' 'basic_auth bcrypt Pantry {' '	pantry $2a$14$abcdefghijklmnopqrstuuabcdefghijklmnopqrstuvwxyz012' '}' > "$adapt_dir/auth.caddy"

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
have "$acme_json" "X-Pantry-Entry" "certificate adapt marks the public site for the login page"
have "$tunnel_json" "X-Pantry-Entry" "tunnel adapt marks the public site for the login page"
# Caddy's adapt output canonicalizes the header to Client-Ip.
have "$acme_json" "X-Pantry-Client-Ip" "certificate adapt passes the visitor address"
have "$tunnel_json" "X-Pantry-Client-Ip" "tunnel adapt passes the visitor address"
# Stockbot sends these on /sw.js. A second copy is joined with the app's header.
lack "$acme_json" "Service-Worker-Allowed" "certificate adapt must not set Service-Worker-Allowed"
lack "$tunnel_json" "Service-Worker-Allowed" "tunnel adapt must not set Service-Worker-Allowed"
lack "$acme_json" "no-cache, no-transform" "certificate adapt must not set the worker Cache-Control"
lack "$tunnel_json" "no-cache, no-transform" "tunnel adapt must not set the worker Cache-Control"

# The previous name is a separate site that redirects. It is not in the
# committed snippet; setup.sh writes it from LEGACY_PUBLIC_HOST.
# shellcheck disable=SC2016 # Caddy expands {$PUBLIC_HOST}; the snippet keeps the placeholder.
printf '%s {\n\tredir https://{$PUBLIC_HOST}{uri} 308\n}\n' 'pantry.rhionin.com' > "$adapt_dir/Caddyfile.legacy"
acme_legacy=$(caddy_adapt "$adapt_dir/Caddyfile" "$adapt_dir")
have "$acme_legacy" 'pantry.rhionin.com' "certificate adapt redirects the previous name"
have "$acme_legacy" '308' "certificate adapt uses a permanent redirect"
have "$acme_legacy" 'stockbot.rhionin.com' "certificate redirect target is PUBLIC_HOST"

# shellcheck disable=SC2016 # Caddy expands {$PUBLIC_HOST}; the snippet keeps the placeholder.
printf '%s {\n\tredir https://{$PUBLIC_HOST}{uri} 308\n}\n' 'http://pantry.rhionin.com' > "$adapt_dir/Caddyfile.legacy"
tunnel_legacy=$(caddy_adapt "$adapt_dir/Caddyfile.tunnel" "$adapt_dir")
have "$tunnel_legacy" 'pantry.rhionin.com' "tunnel adapt redirects the previous name"
have "$tunnel_legacy" '308' "tunnel adapt uses a permanent redirect"
lack "$tunnel_legacy" '"module": "acme"' "tunnel redirect must not request a certificate"

rm -rf "$adapt_dir"

echo "check-publish-modes ok"
