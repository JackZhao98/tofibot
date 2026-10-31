---
name: code-delivery
description: Change code in a project, run it, and ship a verified archive.
---

# Code delivery

## What good looks like

- The existing project is read before it is changed: layout, language and versions, dependency files, build and test commands, conventions in neighbouring code. The change follows the project's style and touches only what the task needs.
- The change is exercised: the project's tests or the program itself are run, and a test covers the change when the project has tests.
- The archive holds the project and leaves out dependency folders, caches, version control data and secrets.

## Verifying

- Run the result the way the recipient will: unpack the archive in a fresh folder and run the documented command.
- Every claim in the reply is true of the delivered files. A claim that something works rests on an exit code you saw, and the reply quotes the exact command and its real output. A command that cannot run on the computer is named, with the reason.
- Publish only the final archive; scratch files and logs stay on the Bot computer.

## Tools

`scripts/run_and_record.py` runs a command and records its exact command line, exit code and output, returning the command's own exit status. `scripts/package_project.py` writes a zip, lists what it included and excluded, reopens it to test it, and prints size and SHA-256. `--help` describes each.
