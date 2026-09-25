# Deployment

The server is a single Go binary and does not require Docker.

## Recommended targets

- **macOS:** native `launchd` service, or an Apple `container machine` managed by Orchard.
- **macOS package:** Homebrew tap name `whop2p` after a pinned release is published; see `homebrew/README.md`.
- **Ubuntu:** native `systemd` service behind loopback and a TLS reverse proxy.
- **Local development:** `go run ./cmd/server`.

## Trust boundary

The catalog database is read-only input. Only the app-state database is writable. Keep
catalog backups and application state in separate directories, and never point the
service at a catalog file that an untrusted process can replace while it is running.

## Build

```sh
make build
```

The resulting executable is `bin/whop2p`; the build also creates a
`bin/who-pirates-the-pirates` symlink for discoverability. Cross-compiled static
binaries are available with `make linux` and `make darwin`.

## macOS

See `macos/README.md` for the lightweight `launchd` setup and `orchard/README.md` for
the isolated Apple `container machine` setup.

## Ubuntu

See `ubuntu/README.md` and `ubuntu/install.sh` for the `systemd` setup. The service
runs as an unprivileged user and binds to loopback by default.
