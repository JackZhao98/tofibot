---
id: settings/computer
title: {{ui:settings:shell.page.computer.name}}
summary: {{ui:settings:shell.page.computer.description}}
link: /settings/computer
kind: reference
---
# {{ui:settings:shell.page.computer.name}}

Open this page: [{{ui:settings:shell.page.computer.name}}](/settings/computer)

| Name | Key | Values | Default | Change class | Where |
|---|---|---|---|---|---|
| {{ui:settings:automation.item.desktop_idle_seconds.name}} | `automation.desktop_idle_seconds` | read only |  | forbidden: the person changes it on the page | `/settings/computer#environment` |
| {{ui:settings:automation.item.environment_failover.name}} | `automation.environment_failover` | on or off | true | direct | `/settings/computer#environment` |
| {{ui:settings:automation.item.environment_order_app.name}} | `automation.environment_order_app` | this_mac, bot_computer | this_mac | direct | `/settings/computer#environment` |
| {{ui:settings:automation.item.environment_order_web.name}} | `automation.environment_order_web` | bot_computer, mac | bot_computer | direct | `/settings/computer#environment` |
| {{ui:settings:computer.keys_row}} | `keys.secrets` | not available to the tool |  | forbidden: the person changes it on the page | `/settings/computer#keys` |
| {{ui:settings:shell.page.computer.name}} | `system.computer_resources` | not available to the tool |  | forbidden: the person changes it on the page | `/settings/computer#resources` |

**{{ui:settings:automation.item.desktop_idle_seconds.name}}.** {{ui:settings:automation.item.desktop_idle_seconds.what}}

**{{ui:settings:automation.item.environment_failover.name}}.** {{ui:settings:automation.item.environment_failover.what}}

**{{ui:settings:automation.item.environment_order_app.name}}.** {{ui:settings:automation.item.environment_order_app.what}}

**{{ui:settings:automation.item.environment_order_web.name}}.** {{ui:settings:automation.item.environment_order_web.what}}
