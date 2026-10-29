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
have .github/workflows/ci.yml "inputs.publish_image != 'skip'" "publish job is gated on publish_image"

if grep -q "github.event_name == 'workflow_call'" .github/workflows/ci.yml; then
  fail "ci.yml must not gate docker-publish on workflow_call (reusable runs inherit the caller event_name)"
fi
docker_publish_if=$(
  awk '
    /^  docker-publish:/ { in_job = 1; next }
    in_job && /^  [a-zA-Z0-9_-]+:/ && $0 !~ /^  docker-publish:/ { exit }
    in_job && /^    if:/ { sub(/^    if: */, ""); print; exit }
  ' .github/workflows/ci.yml
)
if [ -z "$docker_publish_if" ]; then
  fail "docker-publish job is missing a job-level if:"
fi
if ! printf '%s\n' "$docker_publish_if" | grep -qE "github\.event_name != 'pull_request'|github\.event_name == 'push'"; then
  fail "docker-publish if must exclude pull_request or require push (got: ${docker_publish_if})"
fi

deploy_hook_if=$(
  awk '
    /- name: Ask the Stockbot host to deploy/ {
      if (getline > 0 && $0 ~ /^        if:/) {
        sub(/^        if: */, "")
        print
      }
      exit
    }
  ' .github/workflows/ci.yml
)
if [ -z "$deploy_hook_if" ]; then
  fail "Ask the Stockbot host to deploy step is missing an if:"
fi
if ! printf '%s\n' "$deploy_hook_if" | grep -q "github.ref == 'refs/heads/master'"; then
  fail "deploy hook step must gate on github.ref == refs/heads/master (got: ${deploy_hook_if})"
fi
have .github/workflows/ci.yml 'deploy-master' "master image publish shares one queue"
have .github/workflows/ci.yml 'cancel-in-progress: false' "a running master publish is not cancelled"
have .github/workflows/ci.yml 'queue: max' "waiting master publishes stay queued"
have .github/workflows/ci.yml 'bash scripts/publish-moving-tags.sh' "workflow runs the tip guard script"
have .github/workflows/ci.yml "steps.moving.outputs.publish == 'true'" "deploy hook runs only after moving tags are published"
have scripts/publish-moving-tags.sh 'git ls-remote origin refs/heads/master' "tip guard compares this commit to the tip of master"
have scripts/publish-moving-tags.sh 'leaving :latest and :master unchanged' "a stale master publish does not move :latest"
have scripts/publish-moving-tags.sh '::notice::' "tip guard emits a notice"
have scripts/publish-moving-tags.sh 'GITHUB_STEP_SUMMARY' "tip guard writes the job summary"
have scripts/publish-moving-tags.sh 'set -euo pipefail' "tip guard keeps pipefail"
have scripts/publish-moving-tags.sh 'pantry-grocy-sync:latest' "tip guard moves the sidecar tag"
if grep -q 'publish_image: skip' .github/workflows/release.yml; then
  fail "Release still skips the image publish job"
fi
have deploy/README.md 'deploy-master' "README documents the master publish queue"
have deploy/README.md 'queue: max' "README documents the waiting queue"
have deploy/README.md 'Re-run the master workflow' "README documents re-running the tip"
have deploy/README.md 'cancelled' "README says a cancelled waiting job is not a failure"
if grep -q 'git fetch' scripts/publish-moving-tags.sh .github/workflows/ci.yml; then
  fail "tip guard still fetches master"
fi
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
have deploy/systemd/pantry-update.sh 'last-deploy-status' "update script records deploy status for operators"
have deploy/systemd/pantry-update.sh 'refresh_host_scripts_from_image' "update script refreshes host scripts after a successful deploy"
have Dockerfile '/usr/share/stockbot/host-bundle' "pantry image ships the host deploy bundle"
have deploy/build-host-bundle.sh 'pantry-update.sh' "host bundle includes the updater script"

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
