---
name: code-delivery
description: Change code in a project, run it, quote real output, ship a verified zip.
---

# Code delivery

Scripts live in the skill folder named in the header of this text, under `scripts/`. Run each with `--help` first.

## Principles

- Every claim in the reply is true of the delivered files, and you have verified that by running them. The reply quotes the exact command and its real output; a claim that something works rests on an exit code you saw.
- Read the existing project before changing it: layout, language and versions, dependency files, build and test commands, conventions in neighbouring code. A change follows the project's own style and touches only what the task needs.
- Publish only the final archive; scratch files and logs stay on the computer.

## Process

1. Locate the project from the provided files (uploads are listed with their paths on the computer; unpack archives yourself). Read the README, manifests and the code the change touches.
2. Make the change in a working copy on the computer.
3. Run the project's tests or the program itself through `run_and_record.py`, which executes the command, saves a transcript with the exact command line, exit code and output, and returns the command's own exit status. When the project has tests, add or update one that covers the change and run the whole suite.
4. Fix failures and rerun until the transcript shows the result you will report. When a command cannot run on the computer, the reply says which one and why.
5. Package the result with `package_project.py`. It leaves out dependency folders, caches, version control data and likely secrets, lists every inclusion and exclusion, reopens the archive to test it, and prints size and SHA-256. Unpack the archive in a fresh folder and run the documented command once more from there.
6. Publish the zip with `publish_file`. The reply gives what changed, the exact commands to run it, and the real output from the final run.
