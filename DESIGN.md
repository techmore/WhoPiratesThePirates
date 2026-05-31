# Design

## Purpose

Build a dense, search-first torrent metadata browser with a modern UI and a Go + SQLite backend.

The product should feel like a high-density legacy index, but it must stay lawful:

- read-only browsing of catalog metadata already present in SQLite
- separate writable app-state storage
- authorized import paths only
- cross-platform deployment as a single Go binary

## Non-Goals

- scraping The Pirate Bay
- bootstrapping from Pirate Bay database dumps
- updating links from TPB
- crawling or polling infringing sources
- publishing or facilitating infringing content

## Current State

The repository already has the first working slice:

- `cmd/server` starts the HTTP server
- `internal/app` hosts handlers, auth, and SQLite access
- `tpb.sqlite` is opened read-only for catalog browsing
- `app_state.sqlite` stores settings, audit rows, and session revocation state
- templates render a search page, detail page, and admin surface
- the admin page includes live status, paged viewers, and manifest validation tools

This is already a valid phase-1 baseline.

## Data Model

The bundled catalog database currently exposes these tables:

- `torrents`
- `files`
- `categories`
- `yts_movies`
- `yts_torrent_data`

The app-owned state database currently uses:

- `settings`
- `admin_audit`

The `settings` table also persists `admin_session_epoch`, which is used to revoke admin cookies server-side on logout.

Planned app-owned tables for the next phase:

- `import_sources`
- `import_runs`
- optional derived/search tables if we decide to denormalize

Implemented app-owned tables now also include:

- `import_sources`
- `import_runs`

## Runtime Boundaries

- `tpb.sqlite` is read-only input.
- `app_state.sqlite` is the writable application-owned database.
- catalog routes must keep working even if app state is empty.
- settings changes must never write into the catalog DB.
- any future ingest pipeline must write only to app-owned schema or to a separate validated staging database.

## Backend Shape

Keep the service small and explicit:

- `cmd/server`: entrypoint and env wiring
- `internal/app`: routes, templates, session handling
- `internal/catalog`: read-only query layer for the catalog DB
- `internal/state`: schema and persistence for app-owned settings
- `internal/importer`: authorized ingest adapters, added later
- `internal/ui`: view models and template helpers, if the UI grows

This keeps query code separate from transport and avoids letting admin concerns leak into browse paths.

## Phase 1

Phase 1 is the working product slice:

1. Read the bundled SQLite catalog.
2. Serve `/healthz`, `/api/stats`, `/api/categories`, `/api/search`, and `/api/torrents/:id`.
3. Render a responsive search shell, detail page, and admin page.
4. Keep the catalog database read-only.
5. Persist admin/Tor settings in the app-state database.

The current implementation already satisfies most of this shape.

Current admin routes also provide:

1. Live status and DB health via `/api/admin/status`.
2. Paged audit, import-run, and import-source inspection.
3. Authorized manifest preview and record flows with checksum validation.

The live status payload also includes:

1. `adminSessionEpoch` for the current revocation epoch.
2. `lastLoginAt` and `lastLogoutAt` for the newest matching audit timestamps.

## Phase 2

Phase 2 expands the system without changing the trust boundary:

1. Add migrations for app-owned tables.
2. Add authorized import adapters for datasets the operator controls or is allowed to ingest.
3. Add optional validation and checksum/manifest support.
4. Add derived indexes or FTS if search performance requires it.
5. Package the binary for macOS, Linux, and Windows.

## UI Direction

The UI should stay dense and functional:

- search-first landing page
- table-based results
- strong category and stats visibility
- compact detail view with file list and metadata
- one accent color, dark surfaces, clear hierarchy
- mobile layout that preserves search and browse first

The existing template direction is already close, but the next pass should consolidate styles into reusable patterns instead of growing one-off page CSS.

## Search And Pagination

The current query model is a good starting point:

- substring search over title and description
- exact category filter
- stable sort modes
- offset pagination with a bounded page size
- file list limited on the detail route

If the dataset grows more demanding, move search to an indexed path, but keep the read model separate from ingest staging.

## Admin And Tor

The admin panel should remain operational, but it must not imply live Tor control if the operator has not configured one.

- Tor enablement is opt-in
- default state is off
- settings live in app-state
- status should reflect configuration, not pretend to be a daemon manager

If Tor control is added later, it should be a separate adapter with explicit success/failure states and no background discovery.

## Import Policy

Only authorized ingestion paths are allowed:

- local files the operator has rights to use
- endpoints the operator owns or is authorized to access
- optional checksum or manifest validation before write

No hidden crawling, no automatic TPB polling, and no update feature tied to infringing sources.
