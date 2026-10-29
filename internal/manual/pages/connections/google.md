---
id: connections/google
title: Connect Google
summary: Gmail, Calendar and Drive as one connection, and what the setup needs.
link: /settings/connections
kind: guide
see_also: connections/connect, failures/api_disabled
---
# Connect Google

Open this page: [{{ui:settings:shell.page.connections.name}}](/settings/connections)

Google is one tile in the directory. Gmail, Calendar and Drive are services of the same connection. The dialog lets a person choose the services. Gmail access has two levels, and Read only is the default.

- The account email is required, because the sign-in takes it as its account.
- A person uploads one Google OAuth client. The server keeps it sealed. A second Google service signs in with the same client.
- If Google says an API is not enabled, the connection shows Setup needed with an Enable button. The button opens the Google page for that API in the right project.
- Workspace servers from Google are inside the Google step under an Advanced part. They are a developer preview.

A Bot never sees the client or the tokens.
