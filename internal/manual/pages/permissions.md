---
id: permissions
title: Permissions and approvals
summary: Who decides when a Bot uses a tool, and how a person changes it.
link: /settings/permissions
kind: guide
see_also: settings/permissions, settings/allowances, connections/tool-permissions
---
# Permissions and approvals

Open this page: [{{ui:settings:shell.page.permissions.name}}](/settings/permissions)

A tool call has a tier. The tier says how much asking it needs.

- **Read only.** The service marks the tool as read only, or a person marked it. It runs without asking.
- **Write.** This is the default. The call is reviewed. What the person asked for runs. The rest asks first.
- **High risk.** A person decides every call. When no person is there to ask, the call does not run.

The approval mode is one control on the Permissions page.

- **Ask.** A person decides every call that needs a decision.
- **Auto.** The reviewer decides low-risk and medium-risk calls. Calls that need a person still ask.

A person can allow one tool for good. This is a standing allowance. It is given on an approval card with "Always allow" and listed on [{{ui:settings:shell.page.allowances.name}}](/settings/allowances), where a person can revoke it.

A Bot can change the approval mode, also to a looser value, but only after the person approves a card that shows the exact change. A Bot cannot decide this card for itself. Other changes of this kind also need a card: the tier of a tool when it gets looser, the models, the connections of another Bot, and the deletion of a Bot or a conversation.
