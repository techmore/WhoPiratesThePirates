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
- Start operator-authorized magnet or `.torrent` downloads through `aria2c`
- Track external download status in the admin surface
- Deploy on macOS, Ubuntu, or an Apple `container machine`

## Screenshots

_Placeholder: add screenshots of the browse page (`/`), a torrent detail page,
and the admin console (`/admin`) here. The pages render without any external
assets, so a screenshot of a local instance loaded with the fixture from
`go run ./scripts/fixture` is representative._

## What it does not do

`whop2p` does not mirror, seed, or create torrents. Downloads are opt-in from
the admin import form and delegated to `aria2c`; only use references for
content you own or are explicitly authorized to retrieve.

External torrent clients such as `aria2c` or qBittorrent may be used for
content you own or are explicitly authorized to use. The admin import flow
invokes `aria2c` when it is installed or configured with `APP_ARIA2_PATH`.

The catalog database is not committed to this repository. Provide an authorized
backup through `whop2p load-catalog` or `APP_DB_PATH`.

## Install

### Default macOS deployment: Apple Container + Orchard

The preferred macOS deployment is a persistent Apple `container machine`
managed by Orchard. The machine provides an isolated Linux environment with
systemd, persistent storage, logs, and resource indicators.

```sh
brew install --cask orchard
# Install Apple's container CLI separately if needed.

make orchard-create
```

The machine is named `whop2p` and appears in Orchard's Machines view. Open it
with:

```sh
make orchard-shell
```

Transfer an authorized catalog backup into or out of the machine:

```sh
./scripts/orchard-transfer.sh import ~/Downloads/catalog.sqlite
./scripts/orchard-transfer.sh export ~/Downloads/catalog-export.sqlite
```

The transfer helper validates the SQLite file and transfers catalog data only.
It does not download content or contact trackers. Stop the native Homebrew
service before using the machine against the same port and catalog.

### Optional native macOS service

```sh
brew install techmore/tap/whop2p
whop2p --version
whop2p setup
whop2p load-catalog /path/to/catalog.sqlite
brew services start whop2p
whop2p open
```

Use either the native service or the Orchard machine, not both against the same
catalog and port.

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

## Command line

```text
whop2p [command]

  serve               Start the catalog server (default when no command is given)
  setup               Create the local configuration directory, an empty catalog,
                      an app-state database, and a service.env file
  load-catalog FILE   Validate FILE and install it as the local catalog backup
  open                Check the running server's health and open it in a browser
  version             Print the build version
  help                Show the full help, including every environment variable
```

Exit codes: `0` success, `1` runtime error (startup, validation, or server
failure), `2` usage error. An unknown command or bad arguments print the usage
to stderr and never start the server. `whop2p --help` lists every
configuration variable with its default.

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

The customer-facing browse page only exposes and copies approved magnets. The
admin **Download Magnet** action invokes `aria2c` and writes the download to the
configured download directory. After an authorized client has produced a local
SQLite backup, an admin can select its configured source—or enter the local
path directly—and choose **Validate and Load Catalog**. When a local path is
provided while adding a recovery source, the checked **Validate and load
immediately** option performs that validation and live load automatically. The
running search UI detects the catalog change, refreshes its
counts/categories/results, and remembers the selection across service restarts
while the recovered file remains valid; the source database is never modified.
Uploading the database does not create or seed the matching torrent; an
approved torrent client must seed the magnet customers use.

## Environment

| Variable | Purpose | Default |
| --- | --- | --- |
| `APP_DB_PATH` | Read-only catalog database | `tpb.sqlite` |
| `APP_STATE_PATH` | Writable application state | `app_state.sqlite` |
| `APP_BIND_ADDR` | Listen address | `127.0.0.1` |
| `PORT` | Listen port | `8080` |
| `ADMIN_PASSWORD` | Optional admin login password; unset enables local no-password mode | unset |
| `ADMIN_SESSION_SECRET` | HMAC session secret | random per process |
| `APP_ALLOWED_HOSTS` | Extra `Host` names (comma separated) allowed to reach the admin surface in no-password mode | unset |
| `APP_ARIA2_PATH` | Explicit `aria2c` executable path | auto-detected |
| `APP_ARCHIVE_EXTRACTOR` | Explicit `7z`/`7zz` executable used to unpack downloaded archives | auto-detected |
| `APP_DOWNLOAD_DIR` | Directory for started downloads | state-directory `downloads` |
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

### No-password mode and the Host header

When `ADMIN_PASSWORD` is unset, every request that reaches the admin surface
is trusted. To stop a malicious web page from using DNS rebinding to drive the
admin API through the operator's own browser, the admin page and `/api/admin/*`
only answer requests whose `Host` header is `localhost`, a `*.localhost` name,
or an IP literal (for example `127.0.0.1:8080` or `192.168.64.5:8080`). Other
DNS names get `403` unless listed in `APP_ALLOWED_HOSTS`:

```text
APP_ALLOWED_HOSTS=catalog.lan,whop2p.internal:8080
```

The check is skipped when a password is configured, because the signed session
cookie already binds admin access to the real origin.

### Timeouts

Requests have 30-second read/write deadlines. The catalog upload and catalog
load routes extend their own deadline (two hours and one hour respectively) so
a multi-gigabyte backup or a first-time search-index build is not cut off.
`aria2c --version` probes are bounded to 5 seconds and archive extraction to
one hour. `SIGINT`/`SIGTERM` drain in-flight requests for up to 10 seconds and
then close both databases.

Public browse routes never echo internal error text; the cause is written to
the server log and the client receives a generic `internal server error`.

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
- `/api/admin/import-references` — start an authorized magnet or `.torrent` download
- `/api/admin/import-references/list` — list references and download status
- `/api/admin/catalog-sources` — manage recovery catalog labels, magnets, and local paths
- `/api/admin/catalog-sources/upload` — upload, validate, register, and optionally load a SQLite recovery catalog
- `/api/admin/catalog-sources/load` — validate and live-load a local catalog backup

### Magnet download

The admin UI includes a **Download Magnet** action with a Heroicon.
It accepts:

- `magnet:?xt=urn:btih:...` references with a valid v1 info hash
- `https://.../*.torrent` URLs

It records the reference and parsed metadata in app state, then starts
`aria2c` with the reference. It does not write to the read-only catalog.

## Development

```sh
make test
make vet
make race
make lint      # golangci-lint (config in .golangci.yml)
make build
```

CI (`.github/workflows/ci.yml`) runs the unit tests with and without the race
detector, `go vet`, `gofmt`, `golangci-lint`, `govulncheck`, the end-to-end
script, and a release-style build on Ubuntu and macOS.

To run a local instance against a small synthetic catalog (metadata only,
nothing is fetched or seeded):

```sh
make build
go run ./scripts/fixture /tmp/catalog.sqlite
APP_DB_PATH=/tmp/catalog.sqlite APP_STATE_PATH=/tmp/state.sqlite ./bin/whop2p
```

The end-to-end test builds the binary, writes the same fixture, checks the CLI
exit-code contract, starts the server, and verifies health, search, detail
rendering, magnet generation, anonymous admin rejection, and graceful shutdown.
It only needs `curl` and `python3`:

```sh
./scripts/e2e.sh
```

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
internal/sqliteuri SQLite file-URI construction shared by every database open
deploy/macos     native launchd deployment
deploy/ubuntu    native systemd deployment
deploy/orchard   Apple container machine deployment
scripts          E2E test, synthetic fixture generator, and OCI publishing helpers
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
