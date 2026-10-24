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
if [[ "$1" == image ]]; then
  exit 0
fi
if [[ "$*" == *" -d "* ]]; then
  printf '%s\n' "drill-container"
  exit 0
fi
if [[ "$*" == *"/api/build"* || "$*" == *"/api/products"* ]]; then
  printf '200'
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
[[ "$log" == *"/api/build"* && "$log" == *"/api/products"* ]] || fail "probe did not ask for the API"
[[ "$log" == *"-p "* || "$log" == *127.0.0.1:* ]] && fail "smoke published a host port"
app_line=$(printf '%s\n' "$log" | grep ' -d ' | head -1)
probe_line=$(printf '%s\n' "$log" | grep '/api/build' | head -1)
app_net=$(printf '%s\n' "$app_line" | sed -n 's/.*--network \([^ ]*\).*/\1/p')
probe_net=$(printf '%s\n' "$probe_line" | sed -n 's/.*--network \([^ ]*\).*/\1/p')
[[ -n "$app_net" && "$app_net" == pantry-drill-* ]] || fail "app is not on the drill network: $app_line"
[[ "$app_net" != "bridge" && "$app_net" != "default" ]] || fail "app is on the default bridge"
[[ "$probe_net" == "$app_net" ]] || fail "probe network $probe_net is not the app network $app_net"
[[ "$probe_line" != *localhost* && "$probe_line" != *127.0.0.1* ]] || fail "probe hit localhost: $probe_line"
[[ "$probe_line" == *http://pantry-drill-* ]] || fail "probe did not use the drill container name"
[[ "$log" == *"curlimages/curl@sha256:c1fe1679c34d9784c1b0d1e5f62ac0a79fca01fb6377cdd33e90473c6f9f9a69"* ]] || fail "probe image is not pinned by digest"
[[ "$log" == *DISABLE_EXTERNAL_PRODUCT_LOOKUP=true* ]] || fail "external lookup was not disabled"
tmpfs_owner="uid=$(id -u),gid=$(id -g)"
[[ "$log" == *"--tmpfs /tmp:rw,noexec,nosuid,size=16m,mode=0700,${tmpfs_owner}"* ]] || fail "verification tmpfs is not owned by the drill user: $log"
[[ "$log" == *PANTRY_AUTH_FILE* ]] && fail "smoke set PANTRY_AUTH_FILE"
[[ "$(cat "$work/out" "$work/err")" != *token-not-a-path* ]] || fail "secrets plaintext was printed"
[[ "$(cat "$work/out" "$work/err")" != *AGE-SECRET-KEY-* ]] || fail "identity contents were printed"

loose="$work/loose-age.txt"
cp "$work/cj-age.txt" "$loose"
chmod 0644 "$loose"
rc=$(drill --identity "$loose" --bucket-dir "$work/bucket")
[[ "$rc" == 2 ]] || fail "loose identity rc=$rc"
grep -q 'readable only by its owner' "$work/err" || fail "loose identity was accepted"

bad_manifest="$work/bad-manifest.json"
cp "$work/bucket/nightly/2026-10-23T030000Z.manifest.json" "$bad_manifest"
python3 - "$bad_manifest" <<'PY'
import json, sys
path = sys.argv[1]
with open(path) as handle:
    manifest = json.load(handle)
manifest["plaintext_sha256"] = "0" * 64
with open(path, "w") as handle:
    json.dump(manifest, handle)
PY
rc=$(drill --identity "$work/cj-age.txt" \
  --file "$work/bucket/nightly/2026-10-23T030000Z.pantry.db.gz.age" \
  --manifest-file "$bad_manifest")
[[ "$rc" == 1 ]] || fail "sha mismatch rc=$rc err=$(cat "$work/err")"
grep -q 'sha256 does not match' "$work/err" || fail "sha mismatch was not reported"
[[ "$(cat "$work/out" "$work/err")" != *oats* ]] || fail "restored rows were printed"

rc=$(SOPS_AGE_KEY='AGE-SECRET-KEY-example' drill --identity "$work/cj-age.txt" --bucket-dir "$work/bucket")
[[ "$rc" == 2 ]] || fail "SOPS_AGE_KEY rc=$rc"
grep -q 'environment variable SOPS_AGE_KEY' "$work/err" || fail "SOPS_AGE_KEY was not named"
[[ "$(cat "$work/out" "$work/err")" != *AGE-SECRET-KEY-example* ]] || fail "SOPS_AGE_KEY value was printed"

multiline=$'# created: synthetic\n# public key: age1fake\nAGE-SECRET-KEY-example'
rc=$(MYKEY="$multiline" drill --identity "$work/cj-age.txt" --bucket-dir "$work/bucket")
[[ "$rc" == 2 ]] || fail "multiline identity env rc=$rc err=$(cat "$work/err")"
grep -q 'environment variable MYKEY' "$work/err" || fail "multiline variable was not named"
combined=$(cat "$work/out" "$work/err")
[[ "$combined" != *AGE-SECRET-KEY-example* ]] || fail "multiline private key was printed"
[[ "$combined" != *age1fake* ]] || fail "multiline public key was printed"
[[ "$combined" != *synthetic* ]] || fail "multiline comment was printed"

set +e
env 'AGE-SECRET-KEY-example=x' \
  DOCKER_LOG="$work/docker.log" \
  SMOKE_PID="$work/smoke.pid" \
  PATH="$work/bin:$PATH" \
  BACKUP_DRILL_DOCKER="$work/bin/docker" \
  bash "$ROOT/deploy/backup/restore-drill.sh" --identity "$work/cj-age.txt" --bucket-dir "$work/bucket" >"$work/out" 2>"$work/err"
rc=$?
set -e
[[ "$rc" == 2 ]] || fail "invalid identity name rc=$rc err=$(cat "$work/err")"
[[ "$(cat "$work/out" "$work/err")" != *AGE-SECRET-KEY-example* ]] || fail "invalid environment name was printed"
grep -q 'an environment variable' "$work/err" || fail "invalid environment name was not refused"

touch "$work/pi-marker"
rc=$(PANTRY_DRILL_PI_MARKER="$work/pi-marker" drill --identity "$work/cj-age.txt" --bucket-dir "$work/bucket")
[[ "$rc" == 2 ]] || fail "pi marker rc=$rc"
grep -q 'refusing to run on the pantry Pi' "$work/err" || fail "pi refusal missing"

rc=$(drill --identity "$work/cj-age.txt" --paste-identity --bucket-dir "$work/bucket")
[[ "$rc" == 2 ]] || fail "both identity modes rc=$rc"

rc=$(printf '%s\n' "$(cat "$work/cj-age.txt")" | drill --paste-identity --bucket-dir "$work/bucket")
[[ "$rc" == 0 ]] || fail "paste identity rc=$rc err=$(cat "$work/err") out=$(cat "$work/out")"
grep -q 'scratch removed' "$work/out" || fail "pasted drill did not remove scratch"
[[ -f "$work/cj-age.txt" ]] || fail "source identity was deleted after paste"
[[ "$(cat "$work/out" "$work/err")" != *AGE-SECRET-KEY-* ]] || fail "pasted identity was printed"
if find /dev/shm /tmp -maxdepth 2 -name 'age-identity.txt' -print 2>/dev/null | grep -q .; then
  fail "pasted identity file remains"
fi

python3 - "$ROOT/deploy/backup/restore-drill.sh" <<'PY'
import os, pathlib, subprocess, sys
text = pathlib.Path(sys.argv[1]).read_text()
start = text.index("import os, sys\n")
end = text.index('b"\\0")', start)
snippet = text[start:end] + 'b"\\0")\n'
if "os.environb" not in snippet:
    sys.exit("python fallback does not use os.environb")
runner = "import os, sys\nos.environb[b'BAD'] = bytes([0xff])\n" + snippet
bad = subprocess.run([sys.executable, "-c", runner], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
if bad.returncode == 0:
    sys.exit("a non-utf8 environment value was accepted")
good = subprocess.check_output([sys.executable, "-c", snippet])
if b"\0" not in good or b"PATH=" not in good:
    sys.exit("a valid environment was not emitted as NUL records")
PY

cat >"$work/bin/env" <<'EOF'
#!/bin/sh
if [ "$1" = "-0" ]; then
  exit 1
fi
exec /usr/bin/env "$@"
EOF
cat >"$work/bin/python3" <<'EOF'
#!/bin/sh
exit 1
EOF
chmod +x "$work/bin/env" "$work/bin/python3"
rc=$(drill --identity "$work/cj-age.txt" --bucket-dir "$work/bucket")
[[ "$rc" == 2 ]] || fail "python fallback failure rc=$rc err=$(cat "$work/err")"
grep -q 'could not read the environment' "$work/err" || fail "python fallback did not fail closed"
rm -f "$work/bin/env" "$work/bin/python3"
