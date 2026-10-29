---
id: settings/general
title: {{ui:settings:shell.page.general.name}}
summary: {{ui:settings:shell.page.general.description}}
link: /settings/general
kind: reference
---
# {{ui:settings:shell.page.general.name}}

Open this page: [{{ui:settings:shell.page.general.name}}](/settings/general)

| Name | Key | Values | Default | Change class | Where |
|---|---|---|---|---|---|
| {{ui:settings:appearance.label}} | `account.appearance` | system, light, dark |  | direct | `/settings/general#appearance` |
| {{ui:settings:language.label}} | `account.language` | a language code, or empty for automatic |  | direct | `/settings/general#region` |
| {{ui:settings:chats.chat_notifications}} | `account.notify_chats` | on or off |  | direct | `/settings/general#chats` |
| {{ui:settings:chats.task_notifications}} | `account.notify_tasks` | on or off |  | direct | `/settings/general#chats` |
| {{ui:settings:primary.label}} | `account.primary_bot` | text |  | confirm | `/settings/general#primary` |
| {{ui:settings:general.activity_title}} | `account.show_activity` | on or off |  | direct | `/settings/general#chats` |
| {{ui:settings:shell.page.general.name}} | `account.sign_out` | not available to the tool |  | forbidden: the person changes it on the page | `/settings/general#account` |
| {{ui:settings:timezone.label}} | `account.timezone` | an IANA timezone name |  | direct | `/settings/general#region` |
