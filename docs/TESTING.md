# Testing and validation

## Local E2E

The E2E test uses a synthetic, authorized Ubuntu metadata fixture. It does not
contact a tracker and does not download or seed content.

```sh
./scripts/e2e.sh
```

It validates:

- `whop2p` builds
- the app starts against a real SQLite catalog
- `/healthz` responds
- `/api/search` finds the Ubuntu record
- `/torrent/1` renders
- the detail page contains a validated `magnet:?xt=urn:btih:` link

For a real authorized Ubuntu torrent smoke test, use a torrent you own or are
explicitly licensed to use. Keep tracker/network access out of automated CI;
use a local fixture for deterministic tests and perform the real transfer
manually.

## OCI/Harbor publishing

Harbor is an OCI registry, so it can store the image built by Apple's native
`container` CLI. No Docker daemon is required.

Authenticate first using the registry's normal OCI credentials:

```sh
container login harbor.example.com
```

Then publish:

```sh
REGISTRY=harbor.example.com IMAGE=whop2p TAG=0.1.0 ./scripts/oci-publish.sh
```

The script builds the init-capable image from `deploy/orchard/Containerfile` and
pushes it to Harbor or any compatible OCI registry. Use a private Harbor project
for the catalog service image; do not bake catalog databases or credentials into
the image.
