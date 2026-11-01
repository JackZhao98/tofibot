---
id: failures/mcp_not_configured
title: mcp_not_configured
summary: The connection is missing or its configuration changed. The call was not sent.
link: /settings/connections
kind: failure
---
# mcp_not_configured

Open this page: [Where to look](/settings/connections)

**Meaning.** The connection is missing or its configuration changed. The call was not sent.

**Usual cause.** The connection was removed, paused or edited.

**Steps.**
1. Call list_mcp_servers to read the current state.
2. Search for the tool again with search_mcp_tools.
3. If the connection is gone, tell the person.
