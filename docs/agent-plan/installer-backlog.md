# Installer and self-host backlog

Items found while running v0.1.0 for real. Each one names the fix and how to check it. None are done yet.

## 1. Ask about an existing reverse proxy before offering `--domain` (agreed with owner 2026-10-10)

**Problem**
- Many self-hosters already run a reverse proxy (Caddy, Nginx, Traefik, a Cloudflare tunnel) on another machine, and it holds ports 80/443.
- If such a user picks `--domain`, the bundled Caddy never gets a certificate, because the ACME challenge reaches their proxy instead. The install only warns when DNS does not point at this host.
- The App then listens on 127.0.0.1 only, so the existing proxy gets 502. The install "succeeds", but the site is down.
- On the default direct install, the user must also know to add `TOFI_TRUSTED_PROXIES` by hand. Without it, every visitor shares one login rate-limit bucket.

**Fix**
- Make the first exposure question: "Is there already a reverse proxy in front of this server (Caddy, Nginx, Traefik, Cloudflare tunnel…)?"
  - **Yes:** use the direct install (self-signed HTTPS on the port).
    - Ask for the proxy's address and write it to `TOFI_TRUSTED_PROXIES`. Offer the peer seen on the first proxied request as a default, if that is practical.
    - Print a ready-to-paste proxy snippet for Caddy and Nginx: `reverse_proxy https://<ip>:<port>`, skip verification for the self-signed certificate, and preserve `Host`.
  - **No:** offer `--domain` or direct, as today.
- When `--domain` is chosen and DNS does not resolve to this host, explain the consequence above and ask for confirmation. Do not just warn.
- Add a flag for non-interactive installs: `--behind-proxy <ip[,ip…]>`.

**Check**
- `tofi_tui.py` and `tofi_host.py` tests cover the new question, the flag and the env written.
- On a test VM, install behind a separate Caddy. The site works through the proxy, and the login limiter keys on the real client IP.

## 2. `tofi uninstall --purge` leaves the TOFI Docker images

**Problem:** after a purge, `ghcr.io/jackzhao98/tofi` and `tofi-worker` images stay on disk.

**Fix:** purge removes the images referenced by the current and previous env (app, worker, caddy if domain), then prunes dangling layers. A plain uninstall keeps them.

**Check:** a unit test asserts the `docker rmi` calls. On a VM, `docker images` is empty after a purge.

## 3. Flaky Go test on GitHub runners

**Problem:** the v0.1.0-rc.17 release failed in `test / go`. The same commit passed as rc.18, and four local runs passed.

**Fix:** find the test (`go test -count=20 -race ./...` on a slow or loaded machine, or read the Actions log) and remove its timing dependence.

**Check:** `-count=50` passes locally under `GOMAXPROCS=2`.

## 4. Real Bot run on each release

**Problem:** v0.1.0 shipped without a real model-backed task on the release build: chat, computer use, a restricted skill and an approval.

**Fix:** add an owner-run checklist to the release steps, or a test VM with a model key provided by the owner.

**Check:** the checklist passes before a stable tag.
