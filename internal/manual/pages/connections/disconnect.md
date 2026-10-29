---
id: connections/disconnect
title: Disconnect a service
summary: Remove the sign-in of a connection and what that ends.
link: /settings/connections
kind: guide
see_also: connections/pause, permissions
---
# Disconnect a service

Open this page: [{{ui:settings:shell.page.connections.name}}](/settings/connections)

Disconnect removes the saved sign-in of a connection. The connection stays in the list and shows Needs sign-in. The tools a person always allowed on it stop being allowed. Local credentials are removed even when the service cannot be told to revoke them.

A Bot can disconnect a connection with the settings tool (`connection.disconnect`) only after the person approves a card for that connection. Removing a connection completely is a separate step on its page.
