---
name: install-software
description: Install or remove programs, packages and tools on the Bot computer, in user space.
requires: [computer]
kind: procedure
---

# Installing and removing software

## Where installs go

- System directories are read-only: there is no sudo and no system apt. Installs go to user space.
- Language package managers install into the user's directories, and Python packages go in a venv. Official release archives are unpacked under /workspace/home/.local/opt and linked into /workspace/home/.local/bin, which comes first on PATH and is shared by all Bots.
- A package that must come from apt can be extracted into user space with apt-get download and dpkg-deb -x, together with its dependencies and a wrapper.

## Care

- The existing command is inspected first and existing tools are kept.
- Uninstalling removes exactly what was installed (the unpacked directory and its links), then command -v and a version check verify it.
- Credentials stay out of commands, logs and files.
