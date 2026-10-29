---
id: bots
title: Bots
summary: What a Bot is, what a person can set on it, and what a Bot may change about itself and others.
link: /b/{bot}/settings#identity
kind: guide
see_also: bot-settings/identity, bot-settings/role, bot-settings/model, bot-settings/tools, groups, memory
---
# Bots

Open this page: [{{ui:chat:header.bot_settings}}](/b/{bot}/settings#identity)

A Bot has a name, a role, a model, a set of tools and its own chat. The Bot settings page has six sections: {{ui:chat:botSettings.section.identity}}, {{ui:chat:botSettings.section.role}}, {{ui:chat:botSettings.section.model}}, {{ui:chat:botSettings.section.tools}}, {{ui:chat:botSettings.section.secrets}} and {{ui:chat:botSettings.section.advanced}}.

- **The Primary Assistant.** One Bot handles the general chores. There is always exactly one.
- **Model.** A Bot can follow the default model or be pinned to a model. A Bot that follows the default also follows its reasoning effort.
- **Archive.** An archived Bot leaves the list. Its chats and memory stay.
- **Delete.** Deleting a Bot removes its chat, memory, tasks and schedules. It cannot be undone.

A Bot changes its own name, role and skills with the settings tool, and the change is applied at once and can be undone. Changing another Bot needs the person's approval on a card. The model, the connections, the archive and the deletion of any Bot need a card too. Secrets and API keys are never read or written by a Bot: they have their own page.
