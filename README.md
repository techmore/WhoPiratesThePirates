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
- Publish approved catalog-backup magnets in a customer dropdown
- Switch the running browser to a validated local backup from the admin panel
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

Builds derive their displayed release from the current Git tag and commit
(for example, `v0.1.6-dirty` for a tagged worktree with local changes). To
label a local build explicitly, use `make build VERSION=v0.1.7`. Tagged
GoReleaser releases inject the tag automatically. The release is shown in the
web UI header and is also returned by `/healthz` and `/api/admin/status`.

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
can be edited when password protection is needed:

```sh
chmod 600 ~/.config/whop2p/service.env
$EDITOR ~/.config/whop2p/service.env
```

For local loopback or Mac container use, leave `ADMIN_PASSWORD` empty and the
admin surface is available without a login. Set it for any deployment exposed
to a network:

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
3. Verifies the catalog tables used by the browser exist.
4. Rejects an empty catalog.
5. Backs up the current catalog.
6. Atomically installs the replacement.

The catalog remains read-only after installation. App-owned state is written
only to `app_state.sqlite`.

### Recovery catalog links

Admins can open **Catalog Recovery Sources** under `/admin` to save an
approved magnet link and upload an authorized SQLite backup. The upload form
validates the database, stores a private copy under the app state directory,
registers the magnet, and can load that copy immediately. Enabled magnets
appear in the **Authorized catalog backups** dropdown on the browse page so
customers can copy or open the link in an approved recovery client.

The server does not fetch magnet content or invoke a torrent client. After an
authorized client has produced a local SQLite backup, an admin can select its
configured source—or enter the local path directly—and choose **Validate and
Load Catalog**. When a local path is provided while adding a recovery source,
the checked **Validate and load immediately** option performs that validation
and live load automatically. The running search UI detects the catalog change,
refreshes its counts/categories/results, and remembers the selection across
service restarts while the recovered file remains valid; the source database
is never modified. Uploading the database does not create or seed the matching
torrent; an approved torrent client must already seed the magnet customers use.

## Environment

| Variable | Purpose | Default |
| --- | --- | --- |
| `APP_DB_PATH` | Read-only catalog database | `tpb.sqlite` |
| `APP_STATE_PATH` | Writable application state | `app_state.sqlite` |
| `APP_BIND_ADDR` | Listen address | `127.0.0.1` |
| `PORT` | Listen port | `8080` |
| `ADMIN_PASSWORD` | Optional admin login password; unset enables local no-password mode | unset |
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
- `/api/catalog-sources` — enabled customer-facing recovery magnets
- `/api/catalog-status` — active catalog revision for automatic browse refresh
- `/api/search` — search, sorting, and pagination
- `/api/torrents/:id` — JSON detail and files

Browse routes are intended for a private network or loopback deployment. Put
authentication at the network edge if exposing the browse surface beyond the
operator's machine.

## Admin surface

When `ADMIN_PASSWORD` is set, the admin panel is protected by a signed,
server-revocable session cookie. When it is unset, the app uses password-free
local mode; keep that mode bound to loopback or an equivalent private container
network.

- `/admin` — admin UI
- `/api/admin/status` — health, counts, session, and external-client status
- `/api/admin/audits` — paged audit inspection
- `/api/admin/import-sources/*` — approved source records
- `/api/admin/import-runs/*` — import run records
- `/api/admin/import-manifests/*` — validated manifest records
- `/api/admin/import-references` — record authorized magnet or `.torrent` metadata
- `/api/admin/import-references/list` — list recorded metadata references
- `/api/admin/catalog-sources` — manage recovery catalog labels, magnets, and local paths
- `/api/admin/catalog-sources/upload` — upload, validate, register, and optionally load a SQLite recovery catalog
- `/api/admin/catalog-sources/load` — validate and live-load a local catalog backup

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
