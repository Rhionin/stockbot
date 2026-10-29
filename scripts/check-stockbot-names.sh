#!/usr/bin/env bash
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
    fail "$label"
  fi
}

forbidden() {
  local needle="$1" label="$2" hits
  hits=$(git grep -n -F -- "$needle" -- ':!scripts/rename-to-stockbot.py' ':!scripts/check-stockbot-names.sh' || true)
  if [[ -n "$hits" ]]; then
    echo "$hits" >&2
    fail "$label"
  fi
}

have go.mod 'module github.com/Rhionin/stockbot' "go.mod module path"
have .github/workflows/ci.yml "github.repository == 'Rhionin/stockbot'" "image publish guard"
have .github/workflows/ci.yml 'ghcr.io/rhionin/pantry:' "published image name"
have .github/workflows/ci.yml 'https://stockbot.rhionin.com/api/deploy-hook' "deploy hook host"
have .github/workflows/ci.yml 'https://pantry.rhionin.com/api/deploy-hook' "deploy hook fallback host"
have .github/workflows/ci.yml 'X-Pantry-Deploy-Signature' "deploy hook header"
have deploy/docker-compose.yml 'image: ghcr.io/rhionin/pantry:' "compose image"
have deploy/docker-compose.yml 'container_name: pantry' "compose container"
have deploy/docker-compose.yml 'DB_PATH: /data/pantry.db' "database file"
have deploy/docker-compose.grocy.yml 'ghcr.io/rhionin/pantry-grocy-sync:' "sidecar image"
have deploy/docker-compose.grocy.yml 'name: pantry_cf-tunnel' "tunnel network"
have deploy/README.md '/opt/pantry' "Pi directory"
have deploy/README.md 'named Pantry' "Grocy location name"
have deploy/README.md 'the Pantry location' "Grocy location in the backfill steps"
have deploy/setup.sh 'never deleted' "legacy auth.caddy on disk is ignored"
have deploy/systemd/pantry-update.sh 'public_proxy_in_update' "updater public proxy without auth.caddy"
have internal/inventory/inventory.go 'type Pantry struct' "inventory type"
have internal/inventory/inventory.go 'func NewPantry(' "inventory constructor"
have internal/group/brands.go '"Market Pantry"' "store brand"
have internal/grocysync/reconcile.go '"name":        "Pantry"' "Grocy location record"
have frontend/index.html '<title>Stockbot</title>' "page title"
have frontend/src/components/shopping/ShoppingListPage.tsx 'label="Pantry item"' "shopping field label"

forbidden 'github.com/Rhionin/pantry' "old module path"
forbidden 'Rhionin/pantry' "old repository slug"
forbidden 'pantry-server' "old server binary name"
forbidden 'com.rhionin.pantry' "old image label"

echo "stockbot names ok"
