#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <binary> <catalog.sqlite>" >&2
  exit 2
fi

binary=$1
catalog=$2
support="$HOME/Library/Application Support/WhoPiratesThePirates"
plist="$HOME/Library/LaunchAgents/com.who-pirates-the-pirates.plist"
service_env="$support/service.env"
run_script="$support/run.sh"

[[ -f "$binary" ]] || { echo "binary not found: $binary" >&2; exit 1; }
[[ -f "$catalog" ]] || { echo "catalog not found: $catalog" >&2; exit 1; }

mkdir -p "$support/bin" "$support/logs" "$HOME/Library/LaunchAgents"
install -m 0755 "$binary" "$support/bin/whop2p"
ln -sf whop2p "$support/bin/who-pirates-the-pirates"

if [[ ! -f "$support/catalog.sqlite" ]]; then
  install -m 0640 "$catalog" "$support/catalog.sqlite"
fi

if [[ ! -f "$service_env" ]]; then
  session_secret=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
  {
    printf 'APP_DB_PATH=%q\n' "$support/catalog.sqlite"
    printf 'APP_STATE_PATH=%q\n' "$support/app_state.sqlite"
    printf 'APP_BIND_ADDR=%q\n' "127.0.0.1"
    printf 'PORT=%q\n' "8080"
    printf 'ADMIN_PASSWORD=%q\n' ""
    printf 'ADMIN_SESSION_SECRET=%q\n' "$session_secret"
  } >"$service_env"
  chmod 0600 "$service_env"
  echo "created $service_env; set ADMIN_PASSWORD before using the admin surface"
fi

cat >"$run_script" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
set -a
. "$(cd "$(dirname "$0")" && pwd)/service.env"
set +a
exec "$(cd "$(dirname "$0")" && pwd)/bin/whop2p"
EOF
chmod 0700 "$run_script"

sed -e "s#__APP_SUPPORT__#$support#g" "$(dirname "$0")/com.who-pirates-the-pirates.plist" >"$plist"
chmod 0644 "$plist"

launchctl bootout "gui/$(id -u)/com.who-pirates-the-pirates" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$plist"

echo "installed com.who-pirates-the-pirates"
echo "edit $service_env, set ADMIN_PASSWORD, then run: launchctl kickstart -k gui/$(id -u)/com.who-pirates-the-pirates"
