---
name: email
description: Email search, reading and sending through the connected mail tools.
kind: procedure
---

# Email

## Route

- Connected tools come first. When the run has search_mcp_tools, search with no server name for a source that reads or sends mail: a Google or Gmail connection, or any MCP source that offers mail tools. Use it to search, read and, when asked, send. Its results are the answer, and mail needs no Bot computer while it works.
- When the connection reports that it is not connected, not authorized or expired, stop and tell the person that mail needs to be reconnected in Settings > Connections, name the connection, and offer to continue once they have done it. When no mail connection exists, say that one can be added in Settings > Connections.
- The signed-in Gmail in the Bot computer's browser is the fallback. It is used only when there is no mail connection, or when the connection is not authorized and the request needs the mail now. A run with a Bot computer reads `references/gmail-browser.md` with read_skill_file for how.

## Reading and acting

- Opening a mail to read it fits a read-only request. Archiving, labeling, marking, deleting and replying happen only when asked.
- The reply says what the relevant mails say and whether the person needs to act.
