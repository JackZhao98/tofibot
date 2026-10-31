# Security policy

## Report a vulnerability

Report a vulnerability in private. Do not open a public issue or pull request.

Use GitHub private vulnerability reporting:

1. Open <https://github.com/JackZhao98/tofibot/security/advisories/new>.
2. Describe the problem, the affected version and the steps to reproduce it.
3. Add proof of concept code only if it is needed. Use synthetic data.

If this page is not available, open a public issue that says only "I need a
private way to report a security problem". Do not put details in it. A
maintainer will then start a private advisory with you.

Do not include real credentials, tokens or other people's data in a report.

## What to expect

A maintainer reads each report and replies in the advisory. We do not promise
a fixed response time. We fix confirmed problems in a new release and publish
an advisory. We credit you if you want that.

## Supported versions

Only the latest stable release gets security fixes. At the time of writing this
is `v0.2.1`. Older stable releases and release candidates (`-rc.N`) get no fixes.
Update with `sudo tofi update`.

## Scope

In scope:

- **The App container** (Go server and web app): authentication, invite and
  session handling, per-account data isolation, OAuth flows, credential and
  secret storage, file upload and download, and the network guards for web fetch
  and MCP.
- **The host tool** (`tofi`, `install.sh`, `deploy/self-host/`). It runs as
  root on the host. It installs, updates, backs up and restores TOFI. A bug that
  lets the App container, a Bot or a web user run commands or write files as
  root on the host is critical. So is a bug that lets an update, a backup or a
  restore touch files outside the TOFI directories.
- **The Worker and Bot computers.** The Worker starts one Firecracker
  Bot computer for each account and gives each one its own disk. A bug that lets
  one account's Bot computer reach another account's data, the host, or the
  Worker's control socket is in scope. So is an escape from the Bot computer.
- **Release artifacts:** images, checksums and the installer download path.

Known limits, not security reports:

- A Bot can run commands inside its own account's Bot computer. That is by design.
- Inside one Bot computer, the shell tool and the shared browser's debugging port
  share one user and the loopback address. They are not isolated from each
  other.
- TOFI is meant for a dedicated host. We do not support running other services
  with sensitive data on the same host.
- A default install uses a self-signed certificate. Check the fingerprint the
  installer prints, or use `--domain` for a trusted certificate.
- Issues in third-party services, models or MCP servers that you connect.
- Denial of service by an Admin or by a user who has an invite.

`sudo tofi security audit` prints where an install differs from safe defaults.
