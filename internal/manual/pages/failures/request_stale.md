---
id: failures/request_stale
title: request_stale
summary: The update request expired before the server read it.
link: /settings/advanced#version
kind: failure
---
# request_stale

Open this page: [Where to look](/settings/advanced#version)

**Meaning.** The update request expired before the server read it.

**Usual cause.** The host service did not pick it up within an hour.

**Steps.**
1. Try again.
2. If it repeats, check `sudo tofi status` on the server.
