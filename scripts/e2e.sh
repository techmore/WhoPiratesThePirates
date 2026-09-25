#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
pid=""
cleanup() {
  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

command -v sqlite3 >/dev/null || { echo "sqlite3 is required for the E2E fixture" >&2; exit 1; }
command -v curl >/dev/null || { echo "curl is required for the E2E test" >&2; exit 1; }

make -C "$root" build >/dev/null
catalog="$tmp/catalog.sqlite"
sqlite3 "$catalog" <<'SQL'
create table categories (id integer primary key, name text);
create table torrents (id integer primary key, category integer, status text, name text, numFiles integer, size real, seeders integer, leechers integer, username text, added integer, description text, imdb text, language text, textLanguage text, infoHash text);
create table files (id integer primary key, parentTorrentId integer, name text, size real);
create table yts_movies (id integer primary key);
create table yts_torrent_data (id integer primary key);
insert into categories(id, name) values (1, 'Linux');
insert into torrents values (1, 1, 'seeded', 'Ubuntu 24.04 LTS Desktop amd64', 1, 5970000000, 100, 10, 'ubuntu-torrent', 1710000000, 'Official Ubuntu desktop image', 'tt1234567', 'English', 'English', '0123456789abcdef0123456789abcdef01234567');
insert into files values (1, 1, 'ubuntu-24.04-desktop-amd64.iso', 5970000000);
SQL

APP_DB_PATH="$catalog" \
APP_STATE_PATH="$tmp/state.sqlite" \
ADMIN_PASSWORD='e2e-secret' \
ADMIN_SESSION_SECRET='01234567890123456789012345678901' \
APP_BIND_ADDR=127.0.0.1 \
PORT=18080 \
"$root/bin/whop2p" >"$tmp/server.log" 2>&1 &
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

echo "E2E passed: health, search, detail, and validated magnet link"
