#!/usr/bin/env bash
# How this Pi publishes the public hostname.
#
# Prints acme, tunnel, or none. Returns 2 when tunnel mode was chosen but
# CLOUDFLARE_TUNNEL_TOKEN is empty, and 1 when PUBLISH_MODE is not acme or
# tunnel. A non-empty token selects tunnel, so a later setup cannot open
# ports 80 and 443 while the token is still in .env. With the token empty
# and PUBLISH_MODE unset, the result is the certificate path.

# Service URL saved on the tunnel's public hostname in Zero Trust.
# Read by setup.sh after this file is sourced.
# shellcheck disable=SC2034
PANTRY_TUNNEL_ORIGIN=http://caddy:80

publish_env_value() {
  { grep "^${1}=" "${PANTRY_DIR}/.env" 2>/dev/null || true; } | head -1 | cut -d= -f2- | tr -d '[:space:]'
}

# publish_mode_from_env REQUESTED
# REQUESTED is "tunnel" when setup was invoked with --tunnel.
publish_mode_from_env() {
  local requested="${1:-}" token mode host
  token=$(publish_env_value CLOUDFLARE_TUNNEL_TOKEN)
  mode=$(publish_env_value PUBLISH_MODE)
  mode=$(printf '%s' "$mode" | tr '[:upper:]' '[:lower:]')
  if [[ -n "$mode" && "$mode" != acme && "$mode" != tunnel ]]; then
    printf 'PUBLISH_MODE must be acme or tunnel (got: %s)\n' "$mode" >&2
    return 1
  fi
  if [[ "$requested" == tunnel || "$mode" == tunnel || -n "$token" ]]; then
    if [[ -z "$token" ]]; then
      return 2
    fi
    printf 'tunnel'
    return 0
  fi
  host=$(publish_env_value PUBLIC_HOST)
  if [[ -n "$host" ]]; then
    printf 'acme'
    return 0
  fi
  printf 'none'
}

# compose_project_name is the Compose project. The directory name is the
# project (/opt/pantry -> pantry) unless COMPOSE_PROJECT_NAME is set.
# PANTRY_TEST_PROJECT_PREFIX is a test-only override.
compose_project_name() {
  local name
  if [[ -n "${PANTRY_TEST_PROJECT_PREFIX:-}" ]]; then
    printf '%s' "$PANTRY_TEST_PROJECT_PREFIX"
    return 0
  fi
  if [[ -n "${COMPOSE_PROJECT_NAME:-}" ]]; then
    printf '%s' "$COMPOSE_PROJECT_NAME"
    return 0
  fi
  name=$(basename "${PANTRY_DIR:-/opt/pantry}")
  printf '%s' "$name"
}

# publish_container SERVICE is the container_name from the compose files.
# Those names follow the compose project: pantry, pantry-caddy, pantry-cloudflared
# when the project is pantry.
publish_container() {
  local project
  project=$(compose_project_name)
  case "$1" in
    pantry) printf '%s' "$project" ;;
    caddy|caddy-tunnel) printf '%s' "${project}-caddy" ;;
    cloudflared) printf '%s' "${project}-cloudflared" ;;
    *) printf '%s' "${project}-$1" ;;
  esac
}

# publish_container_label prints NAME's compose project label.
# Returns 1 when the container is absent. An empty label means the
# container exists and is not part of a compose project.
publish_container_label() {
  local label rc=0
  label=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$1" 2>/dev/null) || rc=$?
  if [[ "$rc" -ne 0 ]]; then
    return 1
  fi
  label=${label//$'\r'/}
  label=${label//$'\n'/}
  printf '%s' "$label"
}

# remove_project_container runs docker rm only when the compose project matches.
# An absent container is a no-op. A container that only shares the name, or
# that has the proxy's name and no project label, is left alone and named.
remove_project_container() {
  local name="$1" project label rc=0
  project=$(compose_project_name)
  label=$(publish_container_label "$name") || rc=$?
  if [[ "$rc" -ne 0 ]]; then
    return 0
  fi
  if [[ -z "$label" ]]; then
    echo "[info] Not removing ${name}; it has no compose project label" >&2
    return 0
  fi
  if [[ "$label" != "$project" ]]; then
    echo "[info] Not removing ${name}; its compose project is not ${project}" >&2
    return 0
  fi
  docker rm -f "$name"
}

# publish_removal_names prints "<project>-caddy" and "<project>-cloudflared".
# A PANTRY_DIR other than /opt/pantry returns 1 and prints nothing, so a
# fixture run cannot delete the household's containers. Tests that need the
# removal set PANTRY_TEST_PROJECT_PREFIX.
publish_removal_names() {
  local project
  if [[ "${PANTRY_DIR:-/opt/pantry}" != /opt/pantry && -z "${PANTRY_TEST_PROJECT_PREFIX:-}" ]]; then
    echo "publish containers were not removed because PANTRY_DIR is not /opt/pantry" >&2
    return 1
  fi
  project=$(compose_project_name)
  printf '%s\n' "${project}-caddy" "${project}-cloudflared"
}

# drop_other_publish_containers MODE removes the proxy that belongs to the
# other mode. Both modes use the container name <project>-caddy, and Compose
# will not replace a container labeled for the other service.
drop_other_publish_containers() {
  local mode="$1" names caddy_name cloud_name svc
  if ! names=$(publish_removal_names); then
    return 0
  fi
  caddy_name=$(printf '%s\n' "$names" | head -n 1)
  cloud_name=$(printf '%s\n' "$names" | tail -n 1)
  svc=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.service"}}' "$caddy_name" 2>/dev/null || true)
  svc=${svc//$'\r'/}
  svc=${svc//$'\n'/}
  if [[ "$mode" == tunnel && "$svc" == caddy ]]; then
    echo "[info] Removing the port-forward proxy so nothing listens on ports 80 or 443"
    remove_project_container "$caddy_name" >/dev/null
  elif [[ "$mode" != tunnel && "$svc" == caddy-tunnel ]]; then
    echo "[info] Removing the tunnel proxy so Caddy can listen on ports 80 and 443"
    remove_project_container "$caddy_name" >/dev/null
  fi
  if [[ "$mode" != tunnel ]]; then
    remove_project_container "$cloud_name" >/dev/null || true
  fi
}

# drop_stale_cloudflared removes <project>-cloudflared when a later
# `compose up` would not place it at 10.77.77.2.
#
# Compose recreates cf-tunnel when the IPAM config changes, so a network
# left by a failed start does not have to be deleted here. It does not
# recreate a cloudflared container whose service spec is unchanged. A
# container left Created ("Address already in use") is reconnected with
# a dynamic address instead of the pin. A running connector already on
# 10.77.77.2 is left alone. If inspect cannot show an address, a running
# container is left alone rather than interrupting a live tunnel.
drop_stale_cloudflared() {
  local names cloud_name running ips
  if ! names=$(publish_removal_names); then
    return 0
  fi
  cloud_name=$(printf '%s\n' "$names" | tail -n 1)
  running=$(docker inspect -f '{{.State.Running}}' "$cloud_name" 2>/dev/null || true)
  running=${running//$'\r'/}
  running=${running//$'\n'/}
  if [[ -z "$running" ]]; then
    return 0
  fi
  if [[ "$running" == true ]]; then
    ips=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{"\n"}}{{end}}' "$cloud_name" 2>/dev/null || true)
    if printf '%s\n' "$ips" | grep -qx '10.77.77.2'; then
      return 0
    fi
    if ! printf '%s\n' "$ips" | grep -q '^[0-9]'; then
      return 0
    fi
    echo "[info] Removing ${cloud_name} so it can take 10.77.77.2"
    remove_project_container "$cloud_name" >/dev/null
    return 0
  fi
  echo "[info] Removing ${cloud_name} left from a failed start so it can take 10.77.77.2"
  remove_project_container "$cloud_name" >/dev/null
}

# grocy_compose_suffix prints no compose arguments. grocy-sync is profile
# grocy-sync in docker-compose.grocy.yml. Setup and the update timer must
# not select that profile, and must not select profile grocy expecting the
# sidecar to come along. The compose file is still copied into PANTRY_DIR.
# The sidecar volume is left in place.
grocy_compose_suffix() {
  return 0
}
