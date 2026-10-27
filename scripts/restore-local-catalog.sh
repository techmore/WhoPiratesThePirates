#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
config_home="${WHOP2P_HOME:-${HOME}/.config/whop2p}"
archive="${1:-${config_home}/downloads/The Pirate Bay & YTS - Full Database Backup -  2024-06-01.sqlite.7z}"

if ! command -v 7z >/dev/null 2>&1; then
  echo "7z is required to extract the catalog archive." >&2
  exit 1
fi
if [[ ! -f "$archive" ]]; then
  echo "Catalog archive not found: $archive" >&2
  echo "Pass the archive path as the first argument." >&2
  exit 1
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/whop2p-restore.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

echo "Checking archive integrity..."
7z t "$archive"
echo "Extracting catalog to temporary storage..."
7z x -y "-o${tmp_dir}" "$archive" >/dev/null
catalog="${tmp_dir}/tpb.sqlite"
if [[ ! -s "$catalog" ]]; then
  echo "Archive did not contain tpb.sqlite." >&2
  exit 1
fi

echo "Building Whop2P from this project..."
(cd "$repo_root" && CGO_ENABLED=0 go build -trimpath -o "${tmp_dir}/whop2p" ./cmd/server)

mkdir -p "$config_home"
WHOP2P_HOME="$config_home" "${tmp_dir}/whop2p" setup
WHOP2P_HOME="$config_home" "${tmp_dir}/whop2p" load-catalog "$catalog"
echo "Catalog restored to ${config_home}/catalog.sqlite"
echo "Start it on demand with: brew services start whop2p"
