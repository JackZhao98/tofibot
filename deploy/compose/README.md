# TOFI on Docker Compose (no Bot computer)

Use this when your server has no KVM, or when you want to manage Docker yourself.
It runs the same pinned release images as the one-command installer.
It runs the server and the web app. Bots, conversations and local connections
work. The Bot computer (a virtual desktop for each account) is an optional upgrade.

This folder does not install the host tool `tofi`. It does not make backups or
updates for you. If you want those, use the installer (see the main README).

## Start

You need Docker with Compose v2 on Linux (x86_64 or aarch64).

```sh
cd deploy/compose
cp .env.example .env
# Edit .env: put in the image digests of one release (steps are in the file).
docker compose up -d                 # behind your own reverse proxy
docker compose --profile tls up -d   # with Caddy and a domain (set TOFI_DOMAIN)
```

Read the one-time setup key, then open the address and enter it. This creates the
Admin account. Then open Settings > Model provider.

```sh
docker compose exec app cat /app/data/owner-bootstrap.secret
```

## HTTPS

- **Your own reverse proxy.** Proxy to `http://127.0.0.1:8321`. Set
  `TOFI_PUBLIC_ORIGIN` to your public `https://` address. Set `TOFI_TRUSTED_PROXIES`
  to the proxy address. Pass `X-Forwarded-For`.
- **Bundled Caddy.** Set `TOFI_DOMAIN` (and `TOFI_EMAIL`). Point DNS at this
  server. Open ports 80 and 443. Start with `--profile tls`. Caddy gets the
  certificates itself.

Do not publish port 8321 on a public address without HTTPS. Keep `TOFI_BIND=127.0.0.1`.

## Data

All data is in the Docker volume `tofi-data` (database, settings, files). Caddy
certificates are in `caddy-data`.

## Back up and restore

```sh
docker compose stop app
docker run --rm -v tofi_tofi-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/tofi-data-$(date +%F).tgz -C /data .
docker compose start app
```

To restore, stop the App, empty the volume, unpack the archive into it
(`tar xzf ... -C /data`), then start the App. The files must stay owned by user
10001. Keep backups off this server.

## Upgrade

1. Back up (above).
2. Get the new release manifest and copy its `images.app` (and `images.caddy`)
   into `.env`.
3. Run:

```sh
docker compose pull
docker compose up -d
```

To roll back, put the old digests back in `.env` and run `up -d` again. If the
new version changed the data format, restore the backup too.

## Add the Bot computer later

A Bot computer needs a host with `/dev/kvm`. The host tool `tofi` adds it
(`sudo tofi computer add kvm`). The one-command installer sets up that tool, the
Worker and the AppArmor profile. It manages the whole stack (`/etc/tofi`,
`/var/lib/tofi`) and does not adopt a Compose volume.

To move: make a backup, stop this stack, and run the installer on a host with
KVM. Then copy the contents of the backup archive into `/var/lib/tofi/data`
while TOFI is stopped (`sudo tofi stop`), and run `sudo tofi start`. Test this
with a copy first.

## Check

`docker compose -f compose.yaml --env-file testdata/fixture.env --profile tls config --quiet`
renders the file without pulling an image. CI runs the same check.
