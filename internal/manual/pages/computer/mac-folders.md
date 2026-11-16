---
id: computer/mac-folders
title: Folders on a Mac for Bots
summary: Give a Bot folders on your Mac; it can read and change files and run commands only there.
link: /settings/computer#environment
kind: guide
see_also: computer/environments, settings/computer, permissions
---
# Folders on a Mac for Bots

Open this page: [{{ui:settings:shell.page.computer.name}}](/settings/computer#environment)

In the Tofi Mac app, Settings > Environments has **Folders on this Mac**. Choose **Add folder**, pick a Bot, choose one or more folders in the macOS panel and confirm. Each grant is one folder for one Bot; give the same folder to more Bots by adding it again for each. **Remove** in the row's menu takes access away at once, for one Bot or for all of them. Grants are kept on the Mac, for the server they were made on.

With a grant, the Bot can list, read, write and edit files in that folder and run commands there: build, test, format, install a project's dependencies, git status, diff, add and commit. Without a grant it can touch nothing on that Mac. Paths outside the folders, links that lead out of them, other people's files, your SSH keys, Keychain and saved credentials are refused by the macOS sandbox, for files and commands alike. Git hooks and git config, editor settings and shell startup files cannot be written. A folder in Library, a dot-folder at the top of home, home itself and network volumes cannot be granted.

Commands run in a sandbox. A command that only reads (effect none) runs read-only with no network and is not reviewed. Any other command is reviewed first, can reach only package registries and code hosts, and may need your approval. Pushing to a private remote needs your credentials, so the Bot asks you to push. Servers and tests that listen on localhost cannot start. A command stops after its timeout (at most 4 minutes); nothing keeps running after it returns. File writes and edits inside a granted folder are not reviewed by default; **Review changes in Mac folders** in Settings > Permissions > Advanced can review them too.

The Mac must be on and the Tofi app open. When it is offline, the Bot says so and does nothing there. Control-Option-Command-Escape stops a Bot's commands on this Mac.

The grants keep each Bot to its own folders. They are not a boundary against your own server: the server tells the Mac which Bot asks.
