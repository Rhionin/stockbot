#!/usr/bin/env bash
# Restore drill against a fixture this test creates. No live database and no R2.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
fail() {
  echo "FAIL: $*" >&2
  exit 1
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/bucket/nightly" "$work/bucket/secrets"
cd "$ROOT"

go build -o "$work/bin/stockbot-backup" ./cmd/backup
cat >"$work/agewrap.go" <<'EOF'
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
)

func main() {
	switch os.Args[1] {
	case "keygen":
		id, err := age.GenerateX25519Identity()
		if err != nil {
			panic(err)
		}
		body := fmt.Sprintf("# public key: %s\n%s\n", id.Recipient(), id.String())
		if err := os.WriteFile(os.Args[2], []byte(body), 0o600); err != nil {
			panic(err)
		}
	case "decrypt":
		id := identity(os.Args[2])
		in, err := os.Open(os.Args[3])
		if err != nil {
			panic(err)
		}
		defer in.Close()
		r, err := age.Decrypt(in, id)
		if err != nil {
			panic(err)
		}
		out, err := os.OpenFile(os.Args[4], os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			panic(err)
		}
		defer out.Close()
		if _, err := io.Copy(out, r); err != nil {
			panic(err)
		}
	case "encrypt":
		id := identity(os.Args[2])
		in, err := os.Open(os.Args[3])
		if err != nil {
			panic(err)
		}
		defer in.Close()
		out, err := os.OpenFile(os.Args[4], os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			panic(err)
		}
		defer out.Close()
		w, err := age.Encrypt(out, id.Recipient())
		if err != nil {
			panic(err)
		}
		if _, err := io.Copy(w, in); err != nil {
			panic(err)
		}
		if err := w.Close(); err != nil {
			panic(err)
		}
	default:
		panic(os.Args[1])
	}
}

func identity(path string) *age.X25519Identity {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			id, err := age.ParseX25519Identity(line)
			if err != nil {
				panic(err)
			}
			return id
		}
	}
	panic("identity file has no age identity")
}
EOF
go run "$work/agewrap.go" keygen "$work/cj-age.txt"
pub=$(awk '/public key:/ {print $NF}' "$work/cj-age.txt")
[[ "$pub" == age1* ]] || fail "age identity was not generated"

python3 - "$work/pantry.db" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
db.execute("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)")
db.execute("INSERT INTO items (name) VALUES ('oats')")
db.execute("CREATE TABLE schema_migrations (filename TEXT PRIMARY KEY, applied_at TEXT)")
db.execute("INSERT INTO schema_migrations (filename, applied_at) VALUES ('001_initial_schema.sql', '2026-01-01')")
db.commit()
PY
sha=$(sha256sum "$work/pantry.db" | awk '{print $1}')
gzip -c "$work/pantry.db" >"$work/pantry.db.gz"
go run "$work/agewrap.go" encrypt "$work/cj-age.txt" "$work/pantry.db.gz" "$work/bucket/nightly/2026-10-23T030000Z.pantry.db.gz.age"
python3 - "$work/bucket/nightly/2026-10-23T030000Z.manifest.json" "$sha" <<'PY'
import json, sys
manifest = {
    "object_key": "nightly/2026-10-23T030000Z.pantry.db.gz.age",
    "plaintext_sha256": sys.argv[2],
    "integrity": "ok",
    "foreign_key_check": "ok",
    "tables": {"items": 1, "schema_migrations": 1},
    "last_migration": "001_initial_schema.sql",
    "row_total": 2,
    "staging": "tmpfs",
}
with open(sys.argv[1], "w") as handle:
    json.dump(manifest, handle)
PY

printf 'token-not-a-path\n' >"$work/secret.txt"
tar -C "$work" -cf "$work/secrets.tar" secret.txt
go run "$work/agewrap.go" encrypt "$work/cj-age.txt" "$work/secrets.tar" "$work/bucket/secrets/2026-10-23T030000Z.tar.age"
python3 - "$work/bucket/secrets/2026-10-23T030000Z.manifest.json" <<'PY'
import json, sys
with open(sys.argv[1], "w") as handle:
    json.dump({"object_key": "secrets/2026-10-23T030000Z.tar.age", "file_count": 1}, handle)
PY
rm -f "$work/pantry.db" "$work/pantry.db.gz" "$work/secrets.tar" "$work/secret.txt"

cat >"$work/bin/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${DOCKER_LOG:?}"
if [[ "$1" == network ]]; then
  exit 0
fi
if [[ "$1" == rm ]]; then
  if [[ -f "${SMOKE_PID:-}" ]]; then
    kill "$(cat "$SMOKE_PID")" >/dev/null 2>&1 || true
    rm -f "$SMOKE_PID"
  fi
  exit 0
fi
if [[ "$*" == *" -d "* ]]; then
  port=$(printf '%s\n' "$*" | sed -n 's/.*127\.0\.0\.1:\([0-9][0-9]*\):8080.*/\1/p')
  [[ -n "$port" ]] || exit 1
  python3 - "$port" <<'PY' &
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
port = int(sys.argv[1])
class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path in ("/api/build", "/api/products"):
            body = b'{"ok":true}'
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()
    def log_message(self, fmt, *args):
        return
HTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY
  printf '%s\n' "$!" >"${SMOKE_PID:?}"
  printf '%s\n' "drill-container"
  exit 0
fi
exit 0
EOF
chmod +x "$work/bin/docker"
cat >"$work/bin/age" <<EOF
#!/usr/bin/env bash
set -euo pipefail
identity=""
out=""
input=""
decrypt=0
while [[ \$# -gt 0 ]]; do
  case "\$1" in
    -d) decrypt=1; shift ;;
    -i) identity="\$2"; shift 2 ;;
    -o) out="\$2"; shift 2 ;;
    *) input="\$1"; shift ;;
  esac
done
[[ "\$decrypt" -eq 1 && -n "\$identity" && -n "\$out" && -n "\$input" ]]
cd "$ROOT"
exec go run "$work/agewrap.go" decrypt "\$identity" "\$input" "\$out"
EOF
chmod +x "$work/bin/age"

drill() {
  local rc
  set +e
  DOCKER_LOG="$work/docker.log" \
    SMOKE_PID="$work/smoke.pid" \
    PATH="$work/bin:$PATH" \
    BACKUP_DRILL_DOCKER="$work/bin/docker" \
    bash "$ROOT/deploy/backup/restore-drill.sh" "$@" >"$work/out" 2>"$work/err"
  rc=$?
  set -e
  printf '%s' "$rc"
}

rc=$(AGE_IDENTITY="not-a-key-value" drill --identity "$work/cj-age.txt" --bucket-dir "$work/bucket")
[[ "$rc" == 2 ]] || fail "identity env rc=$rc"
grep -q 'environment variable AGE_IDENTITY' "$work/err" || fail "AGE_IDENTITY was not named"
[[ "$(cat "$work/out" "$work/err")" != *not-a-key-value* ]] || fail "AGE_IDENTITY value was printed"

rc=$(BACKUP_TEST_LEAK="AGE-SECRET-KEY-example" drill --identity "$work/cj-age.txt" --bucket-dir "$work/bucket")
[[ "$rc" == 2 ]] || fail "secret marker env rc=$rc"
grep -q 'environment variable BACKUP_TEST_LEAK' "$work/err" || fail "BACK_TEST_LEAK was not named"
[[ "$(cat "$work/out" "$work/err")" != *AGE-SECRET-KEY-example* ]] || fail "environment private key was printed"

rc=$(drill --identity /opt/pantry/cj-age.txt --bucket-dir "$work/bucket")
[[ "$rc" == 2 ]] || fail "pi identity path rc=$rc"
grep -q "CJ's own computer" "$work/out" || fail "drill did not say it runs only on CJ's computer"

rc=$(drill --bucket-dir "$work/bucket")
[[ "$rc" == 2 ]] || fail "missing --identity rc=$rc"

: >"$work/docker.log"
rc=$(drill --identity "$work/cj-age.txt" --bucket-dir "$work/bucket" --secrets)
[[ "$rc" == 0 ]] || fail "drill rc=$rc err=$(cat "$work/err") out=$(cat "$work/out")"
grep -q 'verify ok integrity=ok' "$work/out" || fail "verify line missing"
grep -q 'secrets ok files=1' "$work/out" || fail "secrets archive was not reported"
grep -q 'scratch removed' "$work/out" || fail "scratch was not removed"
grep -q 'drill finished in' "$work/out" || fail "elapsed time was not reported"
[[ -f "$work/cj-age.txt" ]] || fail "identity file outside scratch was deleted"
log=$(cat "$work/docker.log")
[[ "$log" == *"network create --internal"* ]] || fail "smoke network was not internal"
[[ "$log" == *DISABLE_EXTERNAL_PRODUCT_LOOKUP=true* ]] || fail "external lookup was not disabled"
[[ "$log" == *PANTRY_AUTH_FILE* ]] && fail "smoke set PANTRY_AUTH_FILE"
[[ "$(cat "$work/out" "$work/err")" != *token-not-a-path* ]] || fail "secrets plaintext was printed"
[[ "$(cat "$work/out" "$work/err")" != *AGE-SECRET-KEY-* ]] || fail "identity contents were printed"
