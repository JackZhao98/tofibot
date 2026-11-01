---
id: failures/mcp_server_unknown
title: mcp_server_unknown
summary: No connection has the name in the call.
link: /settings/connections
kind: failure
---
# mcp_server_unknown

Open this page: [Where to look](/settings/connections)

**Meaning.** No connection has the name in the call.

**Usual cause.** The name has a typo, or the connection was removed or paused.

**Steps.**
1. Call list_mcp_servers to read the names.
2. Send the call again with an exact name.
