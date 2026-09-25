# whop2p

A private, read-only torrent metadata browser for operator-provided SQLite
catalogs.

`whop2p` is a single pure-Go server binary with an embedded web UI. It is
designed for private catalog exploration, backup rotation, and metadata review.
The catalog database is opened read-only; application state is stored in a
separate writable database.

The short command is `whop2p`. A `who-pirates-the-pirates` compatibility alias
is also installed.

## What it does

- Search titles, descriptions, categories, and info hashes
- Browse torrent detail pages and file listings
- Render validated BitTorrent v1 magnet links
- Load a validated local SQLite catalog backup
- Run a private admin surface with audit logging
- Record operator-authorized magnet or `.torrent` references as metadata
- Detect whether an external `aria2c` binary is available
- Deploy on macOS, Ubuntu, or an Apple `container machine`

## What it does not do

`whop2p` does not fetch, mirror, seed, or automatically download content. It
does not contact trackers or acquire data from sources the operator is not
authorized to use.

External torrent clients such as `aria2c` or qBittorrent may be used for
content you own or are explicitly authorized to use. The server only reports
whether `aria2c` is installed; it never invokes it.

The catalog database is not committed to this repository. Provide an authorized
backup through `whop2p load-catalog` or `APP_DB_PATH`.

## Install

### macOS with Homebrew

```sh
brew install techmore/tap/whop2p
whop2p --version
```

### Build from source

```sh
make build
./bin/whop2p --version
```

### macOS native service

```sh
./deploy/macos/install.sh ./bin/who-pirates-the-pirates /path/to/catalog.sqlite
```

The installer creates a per-user `launchd` service under:

```text
~/Library/Application Support/WhoPiratesThePirates/
~/Library/LaunchAgents/com.who-pirates-the-pirates.plist
```

### Ubuntu

```sh
sudo deploy/ubuntu/install.sh ./bin/whop2p /path/to/catalog.sqlite
sudo systemctl status who-pirates-the-pirates
```

The Ubuntu service runs as an unprivileged user, binds to loopback by default,
and keeps the catalog separate from writable app state.

### Apple Container and Orchard

The isolated macOS path uses Apple's native `container` runtime rather than
Docker:

```sh
make build
container build -f deploy/orchard/Containerfile -t whop2p:local .
container machine create whop2p:local --name catalog
container machine run -n catalog
```

Orchard can manage the machine, logs, resources, mounts, and network from its
native macOS UI.

## First run

```sh
whop2p setup
```

This creates:

```text
~/.config/whop2p/catalog.sqlite
~/.config/whop2p/app_state.sqlite
~/.config/whop2p/service.env
```

`setup` never overwrites an existing catalog. `service.env` is mode `0600` and
should be edited before enabling the admin surface:

```sh
chmod 600 ~/.config/whop2p/service.env
$EDITOR ~/.config/whop2p/service.env
```

Set at least:

```text
ADMIN_PASSWORD=use-a-long-random-password
```

Start and open the local service:

```sh
brew services start whop2p       # macOS
whop2p open
```

`whop2p open` checks `/healthz` before opening the default browser.

On Ubuntu, use `systemctl` instead of `brew services`.

## Loading a catalog backup

The application never modifies a source catalog in place. Validate and install
a local backup with:

```sh
whop2p load-catalog /path/to/catalog.sqlite
brew services restart whop2p     # macOS
sudo systemctl restart who-pirates-the-pirates # Ubuntu
```

The loader:

1. Verifies the file is a regular SQLite database.
2. Runs `PRAGMA integrity_check`.
3. Verifies `torrents`, `files`, and `categories` exist.
4. Rejects an empty catalog.
5. Backs up the current catalog.
6. Atomically installs the replacement.

The catalog remains read-only after installation. App-owned state is written
only to `app_state.sqlite`.

## Environment

| Variable | Purpose | Default |
| --- | --- | --- |
| `APP_DB_PATH` | Read-only catalog database | `tpb.sqlite` |
| `APP_STATE_PATH` | Writable application state | `app_state.sqlite` |
| `APP_BIND_ADDR` | Listen address | `127.0.0.1` |
| `PORT` | Listen port | `8080` |
| `ADMIN_PASSWORD` | Admin login password | unset |
| `ADMIN_SESSION_SECRET` | HMAC session secret | random per process |
| `APP_TLS_CERT_FILE` | Direct TLS certificate | unset |
| `APP_TLS_KEY_FILE` | Direct TLS key | unset |
| `APP_COOKIE_SECURE` | Secure cookies behind trusted TLS proxy | unset |
| `APP_ALLOW_INSECURE_HTTP` | Explicit non-loopback HTTP override | unset |
| `WHOP2P_HOME` | Local configuration directory | `~/.config/whop2p` |
| `WHOP2P_URL` | Override URL used by `whop2p open` | derived from bind/port |

`ADMIN_SESSION_SECRET` should be set to a stable random value. If it is
omitted, a new secret is generated at startup and existing admin sessions are
invalidated after restart.

The server refuses plaintext HTTP on non-loopback addresses unless TLS is
configured or `APP_ALLOW_INSECURE_HTTP=true` is explicitly set. Prefer a
private overlay, SSH tunnel, or TLS reverse proxy.

## Public routes

- `/` — search UI
- `/torrent/:id` — torrent detail and magnet action
- `/healthz` — health check
- `/api/stats` — catalog counts
- `/api/categories` — category list
- `/api/search` — search, sorting, and pagination
- `/api/torrents/:id` — JSON detail and files

Browse routes are intended for a private network or loopback deployment. Put
authentication at the network edge if exposing the browse surface beyond the
operator's machine.

## Admin surface

The admin panel is protected by a signed, server-revocable session cookie.

- `/admin` — admin UI
- `/api/admin/status` — health, counts, session, and external-client status
- `/api/admin/audits` — paged audit inspection
- `/api/admin/import-sources/*` — approved source records
- `/api/admin/import-runs/*` — import run records
- `/api/admin/import-manifests/*` — validated manifest records
- `/api/admin/import-references` — record authorized magnet or `.torrent` metadata
- `/api/admin/import-references/list` — list recorded metadata references

### External reference import

The admin UI includes an **Import External Reference** action with a Heroicon.
It accepts:

- `magnet:?xt=urn:btih:...` references with a valid v1 info hash
- `https://.../*.torrent` URLs

It records the reference and parsed metadata in app state. It does not fetch
the URL, contact a tracker, invoke `aria2c`, or write to the read-only catalog.

## Development

```sh
make test
make vet
make race
make build
```

The local end-to-end test uses a synthetic Ubuntu metadata fixture:

```sh
./scripts/e2e.sh
```

It validates health, search, detail rendering, and magnet generation without
contacting a tracker or downloading content.

Container publishing to Harbor or another OCI registry uses Apple's `container`
CLI:

```sh
container login harbor.example.com
REGISTRY=harbor.example.com IMAGE=whop2p TAG=0.1.5 ./scripts/oci-publish.sh
```

## Repository layout

```text
cmd/server       CLI, setup, backup loading, HTTP entrypoint
internal/app     routes, auth, templates, and handlers
internal/catalog read-only catalog queries
internal/state   app-owned SQLite state and audit data
internal/importer manifest and external-reference validation
deploy/macos     native launchd deployment
deploy/ubuntu    native systemd deployment
deploy/orchard   Apple container machine deployment
scripts          E2E and OCI publishing helpers
docs             release and testing notes
```

## Documentation

- [`docs/TESTING.md`](docs/TESTING.md)
- [`docs/RELEASING.md`](docs/RELEASING.md)
- [`deploy/README.md`](deploy/README.md)
- [`DESIGN.md`](DESIGN.md)

## License

A license has not yet been declared. Add one before distributing a release
outside the project maintainers' private environment.
