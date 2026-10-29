---
id: connections/connect
title: Connect a service
summary: How a person connects a service, and how a Bot starts the sign-in.
link: /settings/connections
kind: guide
see_also: connections/google, connections/pause, connections/disconnect, connections/tool-permissions, settings/connections
---
# Connect a service

Open this page: [{{ui:settings:shell.page.connections.name}}](/settings/connections)

A connection gives Bots the tools of one outside service. The Connections page has one row for each connection. A row shows its status in one of six words: Connected, Needs sign-in, Setup needed, Can't connect, Paused, or a short transition.

1. Open the page and choose "Add connection". Choose the service in the directory. A custom address and a local package are in the Custom part.
2. Follow the steps in the dialog. Most services sign in with a browser window. A service that only signs in on this computer asks you to paste the address you land on.
3. Wait for the status to read Connected. The tools of the service are listed on its page.

Every connection is open to every Bot until a person limits it. The Tools section of a Bot's settings limits it per Bot.

A Bot that needs a connection that is not signed in can open the sign-in card for the person in a chat. The person finishes the sign-in. A Bot cannot sign in for a person.
