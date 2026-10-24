#!/usr/bin/env bash
# Copy the existing Caddy bcrypt hash into the file Stockbot reads, and keep
# one session secret. Sourced by setup.sh and pantry-update.sh.
# auth.caddy is never rewritten. A secret that is already set is never rotated,
# so a redeploy does not sign phones out or require a new password.

# pantry_runtime_uid is the distroless nonroot account. The runtime image is
# gcr.io/distroless/static-debian12:nonroot and Dockerfile sets
# USER nonroot:nonroot. That account is uid 65532. The pantry process drops
# every capability, so a root-owned 0700 directory is unreadable and the
# public site fails closed.
pantry_runtime_uid=65532

# give_auth_to_pantry matches prepare_deploy_trigger_dir: a failed chown stops
# setup, except the no-sudo test seam, which warns and continues.
give_auth_to_pantry() {
  local path="$1"
  if chown "${pantry_runtime_uid}:${pantry_runtime_uid}" "$path"; then
    return 0
  fi
  if [[ "${PANTRY_SETUP_SKIP_ROOT:-}" == 1 ]]; then
    echo "[warn] Could not give ${path} to uid ${pantry_runtime_uid}" >&2
    return 0
  fi
  echo "Could not give ${path} to uid ${pantry_runtime_uid}" >&2
  return 1
}

sync_household_credential() {
  local dir dest tmp
  if [[ -z "${PANTRY_DIR:-}" ]]; then
    echo "PANTRY_DIR is not set" >&2
    return 1
  fi
  dir="${PANTRY_DIR}/auth"
  dest="${dir}/household"
  mkdir -p "$dir" || return 1
  chmod 700 "$dir" || return 1
  # Every run, including one that finds a root-owned file from a partial setup.
  give_auth_to_pantry "$dir" || return 1
  if [[ -f "${PANTRY_DIR}/auth.caddy" ]]; then
    tmp=$(mktemp "${dir}/household.XXXXXX") || return 1
    if ! cp "${PANTRY_DIR}/auth.caddy" "$tmp"; then
      rm -f "$tmp"
      return 1
    fi
    chmod 600 "$tmp" || { rm -f "$tmp"; return 1; }
    mv "$tmp" "$dest" || { rm -f "$tmp"; return 1; }
  fi
  if [[ -f "$dest" ]]; then
    chmod 600 "$dest" || return 1
    give_auth_to_pantry "$dest" || return 1
  fi
}

ensure_session_secret() {
  local env current secret tmp
  if [[ -z "${PANTRY_DIR:-}" ]]; then
    echo "PANTRY_DIR is not set" >&2
    return 1
  fi
  env="${PANTRY_DIR}/.env"
  if [[ ! -f "$env" ]]; then
    echo "No .env to store the session secret" >&2
    return 1
  fi
  current=$({ grep '^PANTRY_SESSION_SECRET=' "$env" || true; } | head -1 | cut -d= -f2- | tr -d '[:space:]')
  if [[ -n "$current" ]]; then
    return 0
  fi
  secret=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
  if [[ ${#secret} -ne 64 ]]; then
    echo "Could not generate a session secret" >&2
    return 1
  fi
  if grep -q '^PANTRY_SESSION_SECRET=' "$env"; then
    tmp=$(mktemp "${env}.XXXXXX") || return 1
    if ! sed "s|^PANTRY_SESSION_SECRET=.*|PANTRY_SESSION_SECRET=${secret}|" "$env" > "$tmp"; then
      rm -f "$tmp"
      return 1
    fi
    chmod 600 "$tmp" || { rm -f "$tmp"; return 1; }
    mv "$tmp" "$env" || { rm -f "$tmp"; return 1; }
  else
    printf '\nPANTRY_SESSION_SECRET=%s\n' "$secret" >> "$env" || return 1
  fi
  chmod 600 "$env"
}

# public_host_is_name matches setup.sh valid_hostname: a DNS name, no scheme.
public_host_is_name() {
  local host="$1"
  [[ "$host" =~ ^[A-Za-z0-9.-]+$ && "$host" == *.* && "$host" != .* && "$host" != *. ]]
}

# setup_dir_group is the Pi user's group. The container writes the display
# file as uid 65532, and setgid on the directory keeps that group so the
# operator can read the code without sudo.
setup_dir_group() {
  local group gid path
  if [[ -n "${PANTRY_SETUP_GROUP:-}" ]]; then
    printf '%s\n' "$PANTRY_SETUP_GROUP"
    return 0
  fi
  if [[ -n "${SUDO_USER:-}" && "${SUDO_USER}" != root ]]; then
    group=$(id -gn "$SUDO_USER" 2>/dev/null || true)
    if [[ -n "$group" && "$group" != root ]]; then
      printf '%s\n' "$group"
      return 0
    fi
  fi
  for path in "${PANTRY_DIR}/.env" "${PANTRY_DIR}"; do
    if [[ -e "$path" ]]; then
      gid=$(stat -c '%g' "$path" 2>/dev/null || true)
      group=$(stat -c '%G' "$path" 2>/dev/null || true)
      if [[ -n "$group" && "$group" != root && "$gid" != 0 ]]; then
        printf '%s\n' "$group"
        return 0
      fi
    fi
  done
  if getent group rhionin >/dev/null 2>&1; then
    printf '%s\n' rhionin
    return 0
  fi
  printf '%s\n' 65532
}

# prepare_setup_dir creates the host directory bind-mounted at /run/setup.
# chown can clear the setgid bit, so the mode is applied after the owner.
prepare_setup_dir() {
  local dir group
  if [[ -z "${PANTRY_DIR:-}" ]]; then
    echo "PANTRY_DIR is not set" >&2
    return 1
  fi
  dir="${PANTRY_DIR}/setup"
  mkdir -p "$dir" || return 1
  group=$(setup_dir_group)
  if ! chown "65532:${group}" "$dir"; then
    if [[ "${PANTRY_SETUP_SKIP_ROOT:-}" == 1 ]]; then
      echo "[warn] Could not give ${dir} to uid 65532:${group}" >&2
    else
      echo "Could not give ${dir} to uid 65532:${group}" >&2
      return 1
    fi
  fi
  chmod 2750 "$dir" || return 1
}

# ensure_app_base_url sets APP_BASE_URL from PUBLIC_HOST when the public
# proxy has a hostname and the operator has not chosen an origin. An empty
# PUBLIC_HOST is a LAN-only install and is left alone. A value already set
# is never replaced. The file mode is kept, including 0600.
ensure_app_base_url() {
  local env host current mode tmp
  if [[ -z "${PANTRY_DIR:-}" ]]; then
    echo "PANTRY_DIR is not set" >&2
    return 1
  fi
  env="${PANTRY_DIR}/.env"
  if [[ ! -f "$env" ]]; then
    return 0
  fi
  host=$({ grep '^PUBLIC_HOST=' "$env" || true; } | head -1 | cut -d= -f2- | tr -d '[:space:]')
  if [[ -z "$host" ]] || ! public_host_is_name "$host"; then
    return 0
  fi
  if grep -q '^APP_BASE_URL=' "$env"; then
    current=$({ grep '^APP_BASE_URL=' "$env" || true; } | head -1 | cut -d= -f2- | tr -d '[:space:]')
    if [[ -n "$current" ]]; then
      return 0
    fi
  fi
  mode=$(stat -c '%a' "$env") || return 1
  tmp=$(mktemp "${env}.XXXXXX") || return 1
  if grep -q '^APP_BASE_URL=' "$env"; then
    if ! awk -v url="https://${host}" '
      BEGIN { done = 0 }
      /^APP_BASE_URL=/ && done == 0 {
        print "APP_BASE_URL=" url
        done = 1
        next
      }
      { print }
    ' "$env" > "$tmp"; then
      rm -f "$tmp"
      return 1
    fi
  else
    if ! cat "$env" > "$tmp"; then
      rm -f "$tmp"
      return 1
    fi
    if [[ -s "$tmp" ]] && [[ $(tail -c 1 "$tmp" | wc -l) -eq 0 ]]; then
      printf '\n' >> "$tmp" || { rm -f "$tmp"; return 1; }
    fi
    printf 'APP_BASE_URL=https://%s\n' "$host" >> "$tmp" || { rm -f "$tmp"; return 1; }
  fi
  chmod "$mode" "$tmp" || { rm -f "$tmp"; return 1; }
  mv "$tmp" "$env" || { rm -f "$tmp"; return 1; }
}
