---
id: setup
title: Set up TOFI
summary: The first-run steps, in order, from a model to the first Bot.
link: /settings/models
kind: guide
see_also: settings/models, settings/connections, bots, permissions, computer/none
---
# Set up TOFI

Open this page: [{{ui:settings:shell.page.models.name}}](/settings/models)

Setup has four steps. Start at the first step that is not done.

1. **Connect a model.** A Bot needs a model to think. Open [{{ui:settings:shell.page.models.name}}](/settings/models) and connect one provider with an API key or a sign-in. Without a model, a Bot cannot answer.
2. **Connect services.** A service gives Bots more tools, for example mail or a calendar. Open [{{ui:settings:shell.page.connections.name}}](/settings/connections) and choose a service from the directory. This step is optional. Skip it and come back later.
3. **Create or choose a Bot.** The first Bot is made for you. Its first chat sets its name and its role. A role is what the Bot is responsible for. The Bot reads the role before every reply.
4. **Choose how tools ask.** Open [{{ui:settings:shell.page.permissions.name}}](/settings/permissions). Ask means a person decides tool calls that need a decision. Auto means the reviewer decides most of them. Start with Ask.

A computer is optional. Without one, Bots work with chat, memory, schedules and connected services. The page [{{ui:settings:shell.page.computer.name}}](/settings/computer) says how to add one.

Each step shows its own state on its page. A Bot can read the current state with the settings tool (`status`) and say which step comes next.
