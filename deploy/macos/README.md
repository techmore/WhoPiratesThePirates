# macOS launchd deployment

This is the lightweight native macOS path. It does not require a container runtime
or a VM.

## Install

Build the binary first:

```sh
make build
```

Then install it as the current user:

```sh
./deploy/macos/install.sh ./bin/who-pirates-the-pirates /path/to/catalog.sqlite
```

The installer creates:

```text
~/Library/Application Support/WhoPiratesThePirates/
├── bin/whop2p
├── bin/who-pirates-the-pirates -> whop2p
├── catalog.sqlite
├── app_state.sqlite
├── service.env
├── run.sh
├── service.out.log
└── service.err.log

~/Library/LaunchAgents/com.who-pirates-the-pirates.plist
```

Edit `service.env` and set `ADMIN_PASSWORD`. The file is mode `0600` because it
contains the admin credential and session secret.

The service binds to `127.0.0.1` by default. Use an SSH tunnel, Tailscale, or a
local TLS reverse proxy for private network access.

## Operations

```sh
launchctl print gui/$(id -u)/com.who-pirates-the-pirates
launchctl kickstart -k gui/$(id -u)/com.who-pirates-the-pirates
launchctl bootout gui/$(id -u)/com.who-pirates-the-pirates
curl --fail http://127.0.0.1:8080/healthz
```

To replace a validated catalog backup, stop the service, replace the file, and
start it again. The server treats the catalog as read-only input.
