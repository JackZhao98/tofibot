---
id: failures/schema_change_blocked
title: schema_change_blocked
summary: This release changes the data schema, so the web page cannot install it.
link: /settings/advanced#version
kind: failure
---
# schema_change_blocked

Open this page: [Where to look](/settings/advanced#version)

**Meaning.** This release changes the data schema, so the web page cannot install it.

**Usual cause.** Schema changes need the host service.

**Steps.**
1. Run `sudo tofi update` on the server.
