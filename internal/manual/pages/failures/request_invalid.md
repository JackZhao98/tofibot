---
id: failures/request_invalid
title: request_invalid
summary: The server rejected the update request.
link: /settings/advanced#version
kind: failure
---
# request_invalid

Open this page: [Where to look](/settings/advanced#version)

**Meaning.** The server rejected the update request.

**Usual cause.** The request had a field the host does not accept.

**Steps.**
1. Reload the page and try again.
2. If it repeats, run `sudo tofi update` on the server.
