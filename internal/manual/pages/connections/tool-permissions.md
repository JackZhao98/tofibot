---
id: connections/tool-permissions
title: Tool permissions of a connection
summary: The three marks a tool can have, who sets them, and what loosening needs.
link: /settings/connections
kind: guide
see_also: permissions, settings/allowances
---
# Tool permissions of a connection

Open this page: [{{ui:settings:shell.page.connections.name}}](/settings/connections)

Each tool of a connection has one of three marks. The mark is chosen on the connection's page, in the Tools section.

- **Runs without asking** (`read_only`). The service marks the tool as read only, or a person did.
- **Asks when unsure** (`write`). The default. Each call is reviewed.
- **Always asks me** (`high_risk`). A person decides every call. Standing allowances for the tool end.

A mark that a person set holds only while the tool definition is the same. If the server changes the tool, the mark asks again.

A Bot can make a mark stricter with the settings tool (`connection.tool_tier`) at once. Making a mark looser needs the person's approval on a card.
