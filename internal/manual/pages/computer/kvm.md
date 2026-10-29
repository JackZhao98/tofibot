---
id: computer/kvm
title: The shared computer
summary: What the shared Linux computer gives Bots, and where a person manages it.
link: /settings/computer
kind: guide
see_also: computer/none, settings/computer, settings/keys
---
# The shared computer

Open this page: [{{ui:settings:shell.page.computer.name}}](/settings/computer)

This workspace has a shared Linux computer. All Bots use it. It gives a Bot a shell, files, a browser, a desktop and a terminal. It can sleep when idle and wakes when a Bot needs it.

- **Resources.** A person sets processors, memory and disk on the Computer page. A change needs a restart. The restart waits for running commands.
- **Keys.** Environment variables and SSH keys for the computer are on the {{ui:settings:shell.page.keys.name}} page. A Bot cannot read or write them.
- **Taking over.** A Bot can ask the person to take over the screen for a step that needs a human, for example a sign-in.
- **Files.** Files a person sends in chat are copied to the computer under the conversation's upload folder.
