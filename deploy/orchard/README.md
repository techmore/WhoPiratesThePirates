# Orchard / Apple container machine deployment

This is the richer macOS path. It uses Apple's native `container` runtime and can
be managed from the Orchard GUI. It does not use Docker.

Requirements:

- Apple Silicon
- macOS 26 Tahoe
- Apple `container` 1.0 or newer
- Orchard: `brew install orchard`

## Build the machine image

From the repository root:

```sh
make build
container build -f deploy/orchard/Containerfile -t who-pirates-the-pirates:local .
```

The base image includes an init system because Apple container machines boot
`/sbin/init`. The application is installed as a `systemd` service inside the
machine. The image installs `whop2p` and a `who-pirates-the-pirates` symlink.

## Create a machine

You can create it from Orchard's Machines view or the CLI:

```sh
container machine create who-pirates-the-pirates:local --name catalog
container machine run -n catalog
```

Before starting the service, install a catalog backup and environment file in
the machine. The machine's storage is persistent. The example environment file
is at:

```text
/etc/who-pirates-the-pirates.env.example
```

Use a private overlay for access. Apple container machines do not provide normal
host port forwarding; the service is reached through the machine IP shown by
Orchard, or through a private network agent/reverse proxy.

## Private access

The example environment listens on the machine's private network. If the machine
is reachable beyond the Mac, use Tailscale, WireGuard, SSH, or a TLS reverse
proxy. Do not expose the admin surface on an untrusted network.

## Backups and lifecycle

The catalog database is read-only input. Copy a validated backup into the
machine, update the environment, and restart the service. Orchard provides the
persistent machine lifecycle, logs, resource indicators, mounts, and network
views; `systemd` provides service restart inside the machine.

For a simpler Mac deployment without a VM, use `deploy/macos` instead.
