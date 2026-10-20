#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/whop2p-e2e.XXXXXX")
pid=""
cleanup() {
  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

command -v curl >/dev/null || { echo "curl is required for the E2E test" >&2; exit 1; }

make -C "$root" build >/dev/null
bin="$root/bin/whop2p"

# The synthetic metadata fixture is written with the project's own SQLite
# driver, so the sqlite3 CLI is not required.
catalog="$tmp/catalog.sqlite"
(cd "$root" && go run ./scripts/fixture "$catalog")

# CLI contract: usage errors exit 2, help/version exit 0, and nothing starts
# a server by accident.
if "$bin" not-a-command >/dev/null 2>&1; then
  echo "unknown command must fail" >&2; exit 1
fi
"$bin" not-a-command >/dev/null 2>&1 || status=$?
[[ "${status:-0}" -eq 2 ]] || { echo "unknown command exited $status, want 2" >&2; exit 1; }
"$bin" --help | grep -q 'Usage:'
"$bin" version | grep -q .

APP_DB_PATH="$catalog" \
APP_STATE_PATH="$tmp/state.sqlite" \
ADMIN_PASSWORD='e2e-secret' \
ADMIN_SESSION_SECRET='01234567890123456789012345678901' \
APP_BIND_ADDR=127.0.0.1 \
PORT=18080 \
"$bin" >"$tmp/server.log" 2>&1 &
pid=$!

for _ in $(seq 1 50); do
  if curl --silent --fail http://127.0.0.1:18080/healthz >/dev/null; then
    break
  fi
  sleep 0.2
done

curl --silent --fail http://127.0.0.1:18080/healthz | grep -q '"status": "ok"'
search=$(curl --silent --fail 'http://127.0.0.1:18080/api/search?q=Ubuntu&limit=10&sort=newest')
python3 -c 'import json,sys; d=json.loads(sys.argv[1]); assert d["total"] == 1; assert d["items"][0]["name"].startswith("Ubuntu 24.04")' "$search"
detail=$(curl --silent --fail http://127.0.0.1:18080/torrent/1)
grep -q 'magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567' <<<"$detail"

# Public routes must not reveal internal paths; unknown admin hosts are only
# rejected in no-password mode, so this instance (with a password) answers 401.
unauth=$(curl --silent -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/api/admin/status)
[[ "$unauth" == "401" ]] || { echo "expected 401 for anonymous admin status, got $unauth" >&2; exit 1; }

# SIGTERM must stop the server cleanly.
kill -TERM "$pid"
wait "$pid"
pid=""
grep -q 'shutting down' "$tmp/server.log"

echo "E2E passed: CLI contract, health, search, detail, validated magnet link, graceful shutdown"
