#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "run this installer as root: sudo $0 <binary> <catalog.sqlite>" >&2
  exit 1
fi

if [[ $# -ne 2 ]]; then
  echo "usage: sudo $0 <binary> <catalog.sqlite>" >&2
  exit 2
fi

binary=$1
catalog=$2
service_user=who-pirates-the-pirates
state_dir=/var/lib/who-pirates-the-pirates
env_file=/etc/who-pirates-the-pirates.env
unit=/etc/systemd/system/who-pirates-the-pirates.service

[[ -f "$binary" ]] || { echo "binary not found: $binary" >&2; exit 1; }
[[ -f "$catalog" ]] || { echo "catalog not found: $catalog" >&2; exit 1; }

if ! getent passwd "$service_user" >/dev/null; then
  useradd --system --user-group --home-dir "$state_dir" --shell /usr/sbin/nologin "$service_user"
fi

install -d -m 0750 -o root -g "$service_user" "$state_dir"
install -d -m 0755 /usr/local/bin
install -m 0755 "$binary" /usr/local/bin/whop2p
ln -sf whop2p /usr/local/bin/who-pirates-the-pirates

if [[ ! -f "$state_dir/catalog.sqlite" ]]; then
  install -m 0640 -o root -g "$service_user" "$catalog" "$state_dir/catalog.sqlite"
fi

install -d -m 0750 -o "$service_user" -g "$service_user" "$state_dir"

if [[ ! -f "$env_file" ]]; then
  session_secret=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
  cat >"$env_file" <<EOF
APP_DB_PATH=$state_dir/catalog.sqlite
APP_STATE_PATH=$state_dir/app_state.sqlite
APP_BIND_ADDR=127.0.0.1
PORT=8080
ADMIN_PASSWORD=
ADMIN_SESSION_SECRET=$session_secret
EOF
  chmod 0600 "$env_file"
  chown root:root "$env_file"
  echo "created $env_file; set ADMIN_PASSWORD before using the admin surface"
fi

install -m 0644 "$(dirname "$0")/who-pirates-the-pirates.service" "$unit"
systemctl daemon-reload
systemctl enable who-pirates-the-pirates.service
systemctl restart who-pirates-the-pirates.service
systemctl --no-pager --full status who-pirates-the-pirates.service || true

echo
echo "Next: edit $env_file, set ADMIN_PASSWORD, and restart the service."
