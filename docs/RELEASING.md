# Releasing

The project publishes a pure-Go server binary named `whop2p`, with a
`who-pirates-the-pirates` compatibility alias.

The release tag is injected into `main.version` by GoReleaser. The same value
is passed into the application, shown in the web UI header as `Release ...`,
returned by `/healthz` and `/api/admin/status`, and printed by `whop2p
--version`. Local Make builds derive a comparable value from Git; use
`make build VERSION=vX.Y.Z` when an explicit local label is needed.

## One-time GitHub setup

1. Create a public repository named `techmore/homebrew-tap` with a `Formula`
   directory.
2. Create a fine-grained GitHub token with **Contents: Read and write** access
   only to that tap repository.
3. Add it as the `HOMEBREW_TAP_TOKEN` Actions secret in this repository.
4. The default `GITHUB_TOKEN` used by the workflow only needs permission to
   create releases in this source repository.

The tap repository must exist before the first release. GoReleaser will commit
and push the generated formula there.

## Validate a release locally

```sh
make test
go vet ./...
goreleaser check
goreleaser release --snapshot --clean
```

The snapshot build writes archives and a formula into `dist/`. It does not
publish a GitHub release or update the tap.

## Publish

```sh
git tag v0.1.0
git push origin v0.1.0
```

The tag workflow will:

- build `whop2p` for macOS and Linux on amd64 and arm64
- create compressed archives and `checksums.txt`
- create a GitHub release
- generate `Formula/whop2p.rb` with pinned URLs and SHA-256 checksums
- commit the formula to `techmore/homebrew-tap`

Users can then install it with:

```sh
brew install techmore/tap/whop2p
whop2p --version
```

Do not tag a release until the catalog and state deployment paths are reviewed
and the `deploy/` instructions have been verified on a clean machine.
