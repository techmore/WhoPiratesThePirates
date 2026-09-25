# Ubuntu deployment

This target uses a native binary and `systemd`; Docker is not required.

## Layout

```text
/usr/local/bin/whop2p
/usr/local/bin/who-pirates-the-pirates -> whop2p
/etc/who-pirates-the-pirates.env
/var/lib/who-pirates-the-pirates/catalog.sqlite   # read-only
/var/lib/who-pirates-the-pirates/app_state.sqlite # writable
```

## Install

Build or copy the Linux binary, then run as root:

```sh
sudo deploy/ubuntu/install.sh ./bin/who-pirates-the-pirates /path/to/catalog.sqlite
```

The installer creates an unprivileged service account, installs the unit, creates
an environment file if one does not already exist, and enables the service.

Edit `/etc/who-pirates-the-pirates.env` and set `ADMIN_PASSWORD` and
`ADMIN_SESSION_SECRET` before using the admin surface:

```sh
sudo chmod 600 /etc/who-pirates-the-pirates.env
sudo systemctl restart who-pirates-the-pirates
```

The service binds to `127.0.0.1` by default. Put Caddy, Nginx, or another TLS
reverse proxy in front of it for network access. Keep the catalog file owned by
root and not writable by the service account.

## Operations

```sh
sudo systemctl status who-pirates-the-pirates
sudo journalctl -u who-pirates-the-pirates -f
curl --fail http://127.0.0.1:8080/healthz
```

To replace a validated catalog backup, stop the service, atomically install the
new file, and start it again. Do not modify the catalog in place.
