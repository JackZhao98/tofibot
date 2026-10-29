---
id: failures/extension_change_unknown
title: extension_change_unknown
summary: The approval target does not name an extension change the server has seen in this run.
link: /settings/connections
kind: failure
---
# extension_change_unknown

Open this page: [Where to look](/settings/connections)

**Meaning.** The approval target does not name an extension change the server has seen in this run.

**Usual cause.** The token was not issued in this run.

**Steps.**
1. Call manage_extensions first.
2. Use the exact token from its result.
