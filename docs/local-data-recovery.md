# Local data recovery

The project source is the recoverable application. Runtime state, credentials,
and the compressed catalog archive stay outside Git in `~/.config/whop2p`.
Never commit `service.env`, catalog databases, download contents, or recovery
indexes.

The macOS setup keeps the service stopped unless it is needed. To reconstruct
the local catalog from the preserved source archive, run:

```sh
./scripts/restore-local-catalog.sh
```

The script checks the 7z archive, extracts `tpb.sqlite` into a temporary
directory, builds the app from this checkout, initializes missing local
configuration without overwriting existing settings, and installs the catalog
through the validated `load-catalog` command. Pass another archive path as the
first argument to use a different backup.

The large `search-index` directory under the config home is derived from the
catalog and is rebuilt by the app when needed. It is safe to remove while the
service is stopped; do not remove `service.env` or the original compressed
archive if you want to restore the catalog later.

Build and run it only while working on it:

```sh
make build
WHOP2P_HOME="$HOME/.config/whop2p" ./bin/whop2p
```

The foreground server stops with Ctrl-C. If you prefer a managed Homebrew
service later, reinstall it with `brew install techmore/tap/whop2p` and start it
only while needed. The service is currently stopped and the Homebrew binary is
removed from this Mac. Colima is a separate Docker-compatible Linux VM manager;
it is unrelated to Codex.
