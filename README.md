# Torrent Catalog

This repository is a lawful, SQLite-backed torrent metadata browser with a Go backend and a dense, search-first UI.

It intentionally does **not** implement scraping, mirroring, or update automation for Pirate Bay or other infringing sources.

## What Exists Now

- Read-only catalog browsing from an operator-provided SQLite database
- Search, category filtering, detail pages, and a health endpoint
- A protected admin surface for app-owned Tor-related settings
- A separate writable SQLite file for application state
- Audit logs, approved import sources, and import runs in app state
- Authorized manifest validation with checksum preview and record flow
- Live admin status, paged viewers, and inline refresh controls
- Server-side admin session revocation with login/logout audit timestamps
- HTML templates embedded in the server binary

## Environment

- `APP_DB_PATH`: path to the read-only catalog database, defaults to `tpb.sqlite`
- `APP_STATE_PATH`: path to the writable app-state SQLite file, defaults to `app_state.sqlite`
- `ADMIN_PASSWORD`: password required to sign into the admin panel
- `ADMIN_SESSION_SECRET`: optional secret for signing admin sessions
- `APP_BIND_ADDR`: listen address, defaults to `127.0.0.1`
- `APP_TLS_CERT_FILE` and `APP_TLS_KEY_FILE`: optional certificate and key for direct HTTPS
- `APP_COOKIE_SECURE`: set to `true` when HTTPS terminates at a trusted reverse proxy
- `APP_ALLOW_INSECURE_HTTP`: explicit override for serving HTTP on a non-loopback address
- `PORT`: listen port, defaults to `8080`

The catalog database is not committed to this repository. Provide it through
`APP_DB_PATH` before starting the server. HTTP is loopback-only by default;
use direct TLS or a trusted TLS-terminating reverse proxy for network access.
The server refuses non-loopback plaintext HTTP unless
`APP_ALLOW_INSECURE_HTTP=true` is explicitly set.
Manifest validation is bounded to 256 files and 512 MiB of aggregate file
content by default.

## Roadmap

### Phase 1

- Serve the existing catalog data in a modern shell
- Keep all catalog access read-only
- Keep state and audit data in the app-owned SQLite file

### Phase 2

- Add authorized import adapters only
- Add optional validation for approved datasets
- Add derived indexes or search acceleration if needed
- Package for macOS and Linux with native service definitions
- Keep the catalog read-only and the app state separate in every deployment

## Deployment

The server is a single pure-Go binary and does not require Docker.

- macOS native: `deploy/macos` installs a per-user `launchd` service.
- macOS isolated: `deploy/orchard` builds an Apple `container machine` image that can be managed from Orchard.
- Ubuntu: `deploy/ubuntu` installs an unprivileged `systemd` service.
- The short executable name is `whop2p`; a `who-pirates-the-pirates` alias is installed alongside it.
- Homebrew releases will be available as `brew install techmore/tap/whop2p`.
- Cross-compile release binaries with `make linux` and `make darwin`.

In every deployment, keep the catalog database read-only and keep app state in a
separate writable database. The default bind address is loopback; use a private
network or TLS reverse proxy for remote access.

Release and Homebrew tap instructions live in [`docs/RELEASING.md`](docs/RELEASING.md).

## Admin And Tor

The admin panel stores Tor configuration in app state, but it does not manage a Tor daemon directly yet.

Supported modes:

- `off`
- `clearnet`
- `onion`
- `dual`

The admin surface also exposes:

- `/api/admin/status` for live counts, DB health, session epoch, and last login/logout audit timestamps
- `/api/admin/audits` for paged audit inspection
- `/api/admin/import-runs/list` and `/api/admin/import-sources/list` for paged operator review
- `/api/admin/import-manifest/preview` and `/api/admin/import-manifest/record` for authorized manifest validation and recording

## Product Notes

- Search is intentionally simple at the moment so the app stays easy to run cross-platform.
- The catalog database remains read-only.
- Any future ingestion work must be limited to datasets the operator is authorized to use.
