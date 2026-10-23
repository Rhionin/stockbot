#!/usr/bin/env bash
# Static contract for the deploy hook: the signature bytes, the public
# Caddy exception, the host path unit, and the workflow skip.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

have() {
  local file="$1" needle="$2" label="$3"
  if ! grep -q -F -- "$needle" "$file"; then
    fail "$label (missing $needle in $file)"
  fi
}

# Same vector as internal/deployhook/hook_test.go. The workflow signs with
# this openssl invocation, so a drift here fails before a push.
secret=test-secret
ts=1700000000
body='{"sha":"abc1234","ref":"master"}'
sig=$(printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$secret" -hex | awk '{print $NF}')
have internal/deployhook/hook_test.go "$sig" "Go test vector matches openssl"
have .github/workflows/ci.yml "printf '%s.%s'" "workflow signs timestamp, a dot, then the body"
have .github/workflows/ci.yml 'X-Pantry-Deploy-Timestamp' "workflow sends the timestamp header"
have .github/workflows/ci.yml 'X-Pantry-Deploy-Signature' "workflow sends the signature header"
have .github/workflows/ci.yml 'https://stockbot.rhionin.com/api/deploy-hook' "workflow posts to the public hook"
have .github/workflows/ci.yml 'https://pantry.rhionin.com/api/deploy-hook' "workflow falls back to the previous host"
have .github/workflows/ci.yml 'DEPLOY_HOOK_SECRET is not set; skipping the deploy hook' "workflow skips when the repo secret is unset"
have .github/workflows/ci.yml "github.ref == 'refs/heads/master'" "workflow notifies only after a master push"
have .github/workflows/ci.yml "inputs.publish_image != 'skip'" "reusable Release run does not publish the image"
have .github/workflows/ci.yml 'deploy-master' "master image publish shares one queue"
have .github/workflows/ci.yml 'cancel-in-progress: false' "a running master publish is not cancelled"
have .github/workflows/ci.yml 'git fetch --no-tags --depth=1 origin +refs/heads/master:refs/remotes/origin/master' "workflow fetches master before moving tags"
have .github/workflows/ci.yml 'git ls-remote origin refs/heads/master' "workflow compares this commit to the tip of master"
have .github/workflows/ci.yml 'leaving :latest and :master unchanged' "a stale master publish does not move :latest"
have .github/workflows/ci.yml "steps.moving.outputs.publish == 'true'" "deploy hook runs only after moving tags are published"
have .github/workflows/release.yml 'publish_image: skip' "Release does not publish :latest and :master"
have deploy/README.md 'deploy-master' "README documents the master publish queue"
if grep -q "TAGS=\"\$TAGS,ghcr.io/rhionin/pantry:latest" .github/workflows/ci.yml; then
  fail "build tags still include :latest before the tip check"
fi

have deploy/Caddyfile '@deploy path /api/deploy-hook' "certificate Caddyfile exempts the hook"
have deploy/Caddyfile.tunnel '@deploy path /api/deploy-hook' "tunnel Caddyfile exempts the hook"
# The exception has to be above the catch-all household handle. route_body
# equality is checked by check-publish-modes.sh; here, confirm the hook is
# not inside that handle. `handle {` is the catch-all; `handle @name {` is not.
caddy_before=$(awk '/handle \{/{exit} {print}' deploy/Caddyfile)
printf '%s\n' "$caddy_before" | grep -q -F '@deploy path /api/deploy-hook' || fail "deploy hook is behind the household login"

have deploy/systemd/pantry-update.timer 'OnUnitActiveSec=1min' "fallback poll is one minute"
if grep -q 'OnUnitActiveSec=5min' deploy/systemd/pantry-update.timer; then
  fail "timer still polls every 5 minutes"
fi
have deploy/systemd/pantry-update.path 'PathChanged=/opt/pantry/deploy-trigger/request' "path unit watches the trigger file"
have deploy/systemd/pantry-update.path 'Unit=pantry-update.service' "path unit starts the existing update service"
have deploy/systemd/pantry-update.sh 'flock' "update script serializes overlapping triggers"
have deploy/systemd/pantry-update.sh 'trigger changed during the pull' "update script pulls again when a trigger lands mid-run"

# The compose value is a literal ${...} interpolation, not a shell expansion.
# shellcheck disable=SC2016
have deploy/docker-compose.yml 'DEPLOY_HOOK_SECRET: ${DEPLOY_HOOK_SECRET:-}' "compose passes the hook secret"
have deploy/docker-compose.yml './deploy-trigger:/deploy-trigger' "compose mounts the trigger directory"
have deploy/setup.sh 'openssl rand -hex 32' "setup generates the hook secret"
have deploy/setup.sh 'deploy-secret' "setup can print the hook secret"
have deploy/setup.sh 'pantry-update.path' "setup installs the path unit"
have deploy/.env.example 'DEPLOY_HOOK_SECRET=' ".env.example leaves the hook secret empty"
have deploy/README.md 'sudo ./setup.sh deploy-secret' "README prints the Pi command"
have deploy/README.md 'DEPLOY_HOOK_SECRET' "README names the repo secret"

echo "check-deploy-hook ok"
