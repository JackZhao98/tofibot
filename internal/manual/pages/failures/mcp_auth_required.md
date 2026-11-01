---
id: failures/mcp_auth_required
title: mcp_auth_required
summary: The connection needs sign-in. The call was not sent.
link: /settings/connections
kind: failure
---
# mcp_auth_required

Open this page: [Where to look](/settings/connections)

**Meaning.** The connection needs sign-in. The call was not sent.

**Usual cause.** The sign-in did not happen, ended, or the token expired.

**Steps.**
1. With a person in the chat, call request_connection with the name of the connection.
2. Or send the person to the page of the connection in Settings.
3. In a scheduled run, report that the connection needs sign-in. A scheduled run cannot sign in.
