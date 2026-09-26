# Regular Apple container

This is the default macOS deployment. It runs `whop2p` as a single process in
an Apple `container` and appears directly in Orchard's **Containers** view.

```sh
make container-build
make container-run
```

The default container is named `whop2p`, uses the `whop2p-data` volume, and
publishes host port `18081` to container port `8080`.

The image includes `aria2c` for authorized magnet and `.torrent` downloads and
`7z` for extracting supported catalog archives. Rebuild the image after
changing the container definition.

```sh
make container-stop
container start whop2p
```

The container creates an empty catalog on first boot. To replace it with an
authorized local backup, use the regular loader or copy the validated file into
the volume while the container is stopped. The persistent container machine
workflow remains available under `deploy/orchard` when systemd or a full Linux
VM is useful.

The container listens on its private network interface. The published port is
for local/private access; do not expose it directly to the public internet.
