---
id: failures/mcp_result_unknown
title: mcp_result_unknown
summary: The call was sent and then failed. The action may have happened.
link: /settings/connections
kind: failure
---
# mcp_result_unknown

Open this page: [Where to look](/settings/connections)

**Meaning.** The call was sent and then failed. The action may have happened.

**Usual cause.** The connection ended or the server returned an error after dispatch.

**Steps.**
1. Read the target state before you try again.
2. Do not repeat a write without that check.
3. The approval of this call cannot be reused.
