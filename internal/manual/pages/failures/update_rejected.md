---
id: failures/update_rejected
title: update_rejected
summary: The new version failed its health check, and the server rolled back.
link: /settings/advanced#version
kind: failure
---
# update_rejected

Open this page: [Where to look](/settings/advanced#version)

**Meaning.** The new version failed its health check, and the server rolled back.

**Usual cause.** The new version did not start correctly here.

**Steps.**
1. Nothing was changed. Read `sudo tofi status` on the server for details.
2. Do not retry the same version until the cause is known.
