#!/bin/sh
set -eu

if [ ! -f "$APP_DB_PATH" ]; then
  whop2p setup
fi

exec whop2p "$@"
