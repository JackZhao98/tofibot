---
id: update
title: Update TOFI
summary: How a new version is found, installed and rolled back, and where the release notes are.
link: /settings/advanced#version
kind: guide
see_also: settings/advanced, failures/runs_still_active
---
# Update TOFI

Open this page: [{{ui:settings:advanced.version}}](/settings/advanced#version)

The Version card shows the installed version, the latest release and the time of the last check.

- **From this page.** An admin presses "Update to" the new version. The server backs up first, waits for running Bots to finish, installs the version, restarts and reconnects. If the new version fails its health check, the server rolls back.
- **From the server.** Run `sudo tofi update` on the host. This is also the way when a release changes the data schema.
- **Automatic fixes.** The host can install fix releases overnight. It is a host setting: `sudo tofi config auto-update on` or `off`.
- **Release candidates.** A release candidate is a test build ahead of the stable release. An admin can switch the channel on the Version card.
- **What changed.** Ask for the release notes of the installed version. They are short and list what people see.

If an update does not finish, the banner shows a code. Each code has a page that says the cause and the next step.
