#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/orchard-transfer.sh export <destination.sqlite>
  scripts/orchard-transfer.sh import <source.sqlite>

Transfers the local catalog SQLite file into or out of the whop2p Apple
container machine. This is a file transfer only; it never downloads content,
contacts trackers, or invokes aria2.
EOF
}

machine=${MACHINE:-whop2p}
data_dir=/var/lib/who-pirates-the-pirates
service=who-pirates-the-pirates
stage_dir="$HOME/.cache/whop2p-transfer"

[[ $# -eq 2 ]] || { usage; exit 2; }
command -v container >/dev/null || { echo "Apple container CLI is required" >&2; exit 1; }
command -v sqlite3 >/dev/null || { echo "sqlite3 is required for local validation" >&2; exit 1; }

validate_catalog() {
  local path=$1
  [[ -f "$path" ]] || { echo "catalog not found: $path" >&2; exit 1; }
  local integrity
  integrity=$(sqlite3 "$path" 'pragma integrity_check;')
  [[ "$integrity" == "ok" ]] || { echo "catalog integrity check failed: $integrity" >&2; exit 1; }
  for table in torrents files categories; do
    sqlite3 "$path" "select 1 from sqlite_master where type='table' and name='$table';" | grep -qx 1 || {
      echo "required table is missing: $table" >&2
      exit 1
    }
  done
  local count
  count=$(sqlite3 "$path" 'select count(*) from torrents;')
  [[ "$count" -gt 0 ]] || { echo "catalog contains no torrents" >&2; exit 1; }
}

case "$1" in
  export)
    destination=$2
    container machine run -n "$machine" --root -- cat "$data_dir/catalog.sqlite" >"$destination"
    validate_catalog "$destination"
    echo "exported catalog to $destination"
    ;;
  import)
    source=$2
    validate_catalog "$source"
    # Apple container machines map the host home directory. Stage the file
    # there so the transfer works even when the CLI does not forward stdin.
    mkdir -p "$stage_dir"
    staged="$stage_dir/catalog.sqlite"
    cp "$source" "$staged"
    container machine run -n "$machine" --root -- systemctl stop "$service" || true
    container machine run -n "$machine" --root -- cp "$staged" "$data_dir/catalog.sqlite"
    container machine run -n "$machine" --root -- chown who-pirates-the-pirates:who-pirates-the-pirates "$data_dir/catalog.sqlite"
    container machine run -n "$machine" --root -- chmod 0640 "$data_dir/catalog.sqlite"
    container machine run -n "$machine" --root -- systemctl start "$service"
    rm -f "$staged"
    echo "imported catalog into machine $machine"
    ;;
  *)
    usage
    exit 2
    ;;
esac
