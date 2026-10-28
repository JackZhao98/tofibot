<p align="center"><img src="assets/banner.png" alt="TOFI — A home for your bots" width="100%"></p>

<p align="center">Persistent bots, conversations and connected tools — on your own server.</p>

TOFI is a self-hosted AI bot workspace. It has a Go server and a React web app.
Bots keep their identity, conversations, scheduled work and tool connections in
a persistent workspace. Current release: **v0.2.1** (see
[GitHub Releases](https://github.com/JackZhao98/tofibot/releases)).

- Direct and group conversations, streaming replies, attachments and generated files.
- Persistent queues, scheduled work, cancellation and retry handling.
- Bot configuration, memory and collaboration across conversations.
- MCP and Skills management, credentials and supported OAuth connections.
- Server-side web search and web fetch, with an ordered chain of providers.
- Invite-only accounts, Admin management and per-account data isolation.
- Optional: one Firecracker virtual computer for each account.

## Install

Use a dedicated Linux server or VM. TOFI supports x86_64 and aarch64.
TOFI supports Ubuntu 22.04/24.04 and Debian 12/13.

```sh
curl -fsSL https://raw.githubusercontent.com/JackZhao98/tofibot/main/install.sh | sudo bash
```

The installer shows what it found on the machine. It asks how you want to open
TOFI. It changes nothing until you confirm. Then it prints the web addresses and
a one-time setup key. Open the address and enter the key. This creates the
Admin account. Then open Settings > Model provider.

By default TOFI serves HTTPS with a self-signed certificate on port 8321.
Compare the SHA-256 fingerprint that the installer prints with the one in your
browser. On a cloud server, allow TCP 8321 in the firewall.

### Requirements

| | Without a computer (server and web app) | With a computer (needs KVM) |
|---|---|---|
| Host | Any supported host | Host with `/dev/kvm`, TUN, cgroup v2 and AppArmor |
| CPU | 2 vCPUs recommended | At least 2 vCPUs |
| RAM | 2 GiB minimum, 4 GiB recommended | 4 GiB minimum, 8 GiB recommended |
| Free disk | 10 GiB minimum, 20 GiB recommended | 30 GiB minimum, 40 GiB recommended |

On a cloud VM, turn on nested virtualization to use a computer. A host without
KVM is not refused. The installer installs the server and web app only. You can
add a computer later without a reinstall:

```sh
sudo tofi computer add kvm      # needs /dev/kvm
sudo tofi computer remove       # computer disks are kept (--purge deletes them)
```

### Install options

```sh
# Public HTTPS with a trusted certificate (ports 80/443, DNS points here)
... | sudo bash -s -- --domain tofi.example.com --email you@example.com
# This server only (127.0.0.1); reach it with ssh -L 8321:127.0.0.1:8321
... | sudo bash -s -- --local-only
# No questions (scripts, cloud-init)
... | sudo bash -s -- --yes --port 8321
# Server and web app only, even on a host with KVM
... | sudo bash -s -- --computer none
```

Replace `...` with the `curl` command above. The full list of flags and
`TOFI_*` variables is in [deploy/self-host/README.md](deploy/self-host/README.md).

## Update, back up, restore

Prefer the web page? An admin can update from **Settings > Advanced > Version** (or the banner at the top): press **Update to vX**, confirm, and the
server updates itself the same way `sudo tofi update` does: it waits for running Bots, backs up first, checks the release and rolls back if the new version
fails. The page shows the steps and reconnects when the App restarts. Turn on **Receive release candidates** in the same card to be offered test builds too
(nothing installs until you press Update). On a server installed before this feature, the button appears after the first `sudo tofi update` to this version
plus one `sudo tofi status` (or the next reboot); until then the page keeps telling you to run the command.

```sh
sudo tofi status               # installed and latest version, services, computers
sudo tofi update --check       # what an update would change (exit 10 = update available)
sudo tofi update               # back up, update, and roll back if the new version fails
sudo tofi backup               # data, settings and computer disks, then verify
sudo tofi backups              # list backups
sudo tofi restore <id>         # restore a verified backup
sudo tofi doctor               # check the install
```

`tofi update` makes a backup first. It restores the previous version if the new
version does not start. `tofi uninstall` keeps all data. `tofi uninstall --purge`
deletes it. Details: [deploy/self-host/README.md](deploy/self-host/README.md).

## Build and run from source

Install Docker with Compose. Then run:

```sh
docker compose up -d --build
```

Open `http://127.0.0.1:8321`. This development setup runs the App and the shared
MCP Runner. It does not set up account computers. Do not expose it beyond
loopback without authentication and a trusted ingress.

To work on the code, see [CONTRIBUTING.md](CONTRIBUTING.md). The short version:

```sh
npm --prefix ui ci
npm --prefix ui run build
go build ./cmd/tofi ./cmd/tofi-guest
```

## Documentation

- [GitHub Releases](https://github.com/JackZhao98/tofibot/releases): what each version changed.
- [deploy/self-host/README.md](deploy/self-host/README.md): installer and `tofi` host tool.
- [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).

## Project boundary

This repository holds the Server, Web UI, API contracts and Docker/Worker
source. The Electron/macOS client is a separate project and is not included.
The web build can produce a versioned UI artifact for that client with
`scripts/export-desktop-ui.cjs`. Private deployment data, runtime volumes and
credentials are not in this repository.

## Licensing

- **Server, installer and everything outside `ui/`**: [Apache-2.0](LICENSE).
- **Web app (`ui/`)**: [PolyForm Shield 1.0.0](ui/LICENSE). You can read, use and
  change the source to run TOFI. You cannot use it to build a competing product.

Third-party notices are in `ui/THIRD_PARTY_NOTICES.md` and `ui/public/licenses`.
The header uses the project's own four-cat artwork and Fredoka lettering (font
notice: `assets/OFL-Fredoka.txt`). Fredoka and JetBrains Mono stay under the SIL
Open Font License 1.1.
