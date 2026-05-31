# Torrent Catalog

This repository is a lawful, SQLite-backed torrent metadata browser with a Go backend and a dense, search-first UI.

It intentionally does **not** implement scraping, mirroring, or update automation for Pirate Bay or other infringing sources.

## What Exists Now

- Read-only catalog browsing from the bundled SQLite database
- Search, category filtering, detail pages, and a health endpoint
- A protected admin surface for app-owned Tor-related settings
- A separate writable SQLite file for application state
- Audit logs, approved import sources, and import runs in app state
- Authorized manifest validation with checksum preview and record flow
- Live admin status, paged viewers, and inline refresh controls
- Server-side admin session revocation with login/logout audit timestamps

## Environment

- `APP_DB_PATH`: path to the read-only catalog database, defaults to `tpb.sqlite`
- `APP_STATE_PATH`: path to the writable app-state SQLite file, defaults to `app_state.sqlite`
- `ADMIN_PASSWORD`: password required to sign into the admin panel
- `ADMIN_SESSION_SECRET`: optional secret for signing admin sessions
- `PORT`: listen port, defaults to `8080`

## Roadmap

### Phase 1

- Serve the existing catalog data in a modern shell
- Keep all catalog access read-only
- Keep state and audit data in the app-owned SQLite file

### Phase 2

- Add authorized import adapters only
- Add optional validation for approved datasets
- Add derived indexes or search acceleration if needed
- Package for macOS, Linux, and Windows

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
