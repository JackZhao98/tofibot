---
id: settings/automation
title: {{ui:settings:shell.page.automation.name}}
summary: {{ui:settings:shell.page.automation.description}}
link: /settings/automation
kind: reference
---
# {{ui:settings:shell.page.automation.name}}

Open this page: [{{ui:settings:shell.page.automation.name}}](/settings/automation)

| Name | Key | Values | Default | Change class | Where |
|---|---|---|---|---|---|
| {{ui:settings:automation.item.answer_gate.name}} | `automation.answer_gate` | whole number 0 to 5 | 2 | direct | `/settings/automation#checks` |
| {{ui:settings:automation.item.background_model.name}} | `automation.background_model` | a configured model ID, or default |  | confirm | `/settings/automation#models` |
| {{ui:settings:automation.item.browser_engine.name}} | `automation.browser_engine` | classic, playwright | playwright | direct | `/settings/automation#computer` |
| {{ui:settings:automation.item.command_check_minutes.name}} | `automation.command_check_minutes` | whole number 5 to 1440 | 60 minutes | direct | `/settings/automation#computer` |
| {{ui:settings:automation.item.completion_review.name}} | `automation.completion_review` | on or off | true | direct | `/settings/automation#checks` |
| {{ui:settings:automation.item.conversation_model.name}} | `automation.conversation_model` | a configured model ID, or default | claude-sonnet-5-5 | confirm | `/settings/automation#models` |
| {{ui:settings:automation.item.desktop_engine.name}} | `automation.desktop_engine` | classic, a11y | classic | direct | `/settings/automation#computer` |
| {{ui:settings:automation.item.desktop_idle_seconds.name}} | `automation.desktop_idle_seconds` | read only |  | forbidden: the person changes it on the page | `/settings/automation#computer` |
| {{ui:settings:automation.item.force_say_haiku_dm.name}} | `automation.force_say_haiku_dm` | on or off | true | direct | `/settings/automation#replies` |
| {{ui:settings:automation.item.group_exchange_gists.name}} | `automation.group_exchange_gists` | on or off | true | direct | `/settings/automation#history` |
| {{ui:settings:automation.item.group_followup_to_working.name}} | `automation.group_followup_to_working` | on or off | true | direct | `/settings/automation#replies` |
| {{ui:settings:automation.item.group_triage.name}} | `automation.group_triage` | on or off | true | direct | `/settings/automation#replies` |
| {{ui:settings:automation.item.group_triage_model.name}} | `automation.group_triage_model` | a configured model ID, or default |  | confirm | `/settings/automation#models` |
| {{ui:settings:automation.item.own_replies_verbatim.name}} | `automation.own_replies_verbatim` | whole number 1 to 20 | 3 | direct | `/settings/automation#history` |
| {{ui:settings:automation.item.provider_retries.name}} | `automation.provider_retries` | whole number 0 to 10 | 8 | direct | `/settings/automation#reliability` |
| {{ui:settings:automation.item.remembered_approval_hours.name}} | `automation.remembered_approval_hours` | whole number 1 to 720 | 24 hours | confirm | `/settings/automation#checks` |
| {{ui:settings:automation.item.schedule_receipt_review.name}} | `automation.schedule_receipt_review` | on or off | true | direct | `/settings/automation#schedules` |
| {{ui:settings:automation.item.settings_change_limit.name}} | `automation.settings_change_limit` | read only | 8 | forbidden: the person changes it on the page | `/settings/automation#checks` |
| {{ui:settings:automation.item.structured_output.name}} | `automation.structured_output` | on or off | true | direct | `/settings/automation#replies` |
| {{ui:settings:automation.item.task_retention_days.name}} | `automation.task_retention_days` | whole number 0 to 3650 | 30 days | direct | `/settings/automation#history` |
| {{ui:settings:automation.item.task_stall_rounds.name}} | `automation.task_stall_rounds` | whole number 1 to 20 | 3 | direct | `/settings/automation#checks` |
| {{ui:settings:automation.item.task_tree_idle_message_limit.name}} | `automation.task_tree_idle_message_limit` | whole number 10 to 500 | 30 | direct | `/settings/automation#reliability` |
| {{ui:settings:automation.item.web_fetch_limit.name}} | `automation.web_fetch_limit` | whole number 1 to 100 | 20 | direct | `/settings/automation#replies` |
| {{ui:settings:automation.item.web_search.name}} | `automation.web_search` | on or off | true | direct | `/settings/automation#replies` |
| {{ui:settings:automation.item.widget_artifacts_auto.name}} | `automation.widget_artifacts_auto` | on or off | true | direct | `/settings/automation#widgets` |
| {{ui:settings:automation.item.widget_computer_auto.name}} | `automation.widget_computer_auto` | on or off | true | direct | `/settings/automation#widgets` |

**{{ui:settings:automation.item.answer_gate.name}}.** {{ui:settings:automation.item.answer_gate.what}}

**{{ui:settings:automation.item.background_model.name}}.** {{ui:settings:automation.item.background_model.what}}

**{{ui:settings:automation.item.browser_engine.name}}.** {{ui:settings:automation.item.browser_engine.what}}

**{{ui:settings:automation.item.command_check_minutes.name}}.** {{ui:settings:automation.item.command_check_minutes.what}}

**{{ui:settings:automation.item.completion_review.name}}.** {{ui:settings:automation.item.completion_review.what}}

**{{ui:settings:automation.item.conversation_model.name}}.** {{ui:settings:automation.item.conversation_model.what}}

**{{ui:settings:automation.item.desktop_engine.name}}.** {{ui:settings:automation.item.desktop_engine.what}}

**{{ui:settings:automation.item.desktop_idle_seconds.name}}.** {{ui:settings:automation.item.desktop_idle_seconds.what}}

**{{ui:settings:automation.item.force_say_haiku_dm.name}}.** {{ui:settings:automation.item.force_say_haiku_dm.what}}

**{{ui:settings:automation.item.group_exchange_gists.name}}.** {{ui:settings:automation.item.group_exchange_gists.what}}

**{{ui:settings:automation.item.group_followup_to_working.name}}.** {{ui:settings:automation.item.group_followup_to_working.what}}

**{{ui:settings:automation.item.group_triage.name}}.** {{ui:settings:automation.item.group_triage.what}}

**{{ui:settings:automation.item.group_triage_model.name}}.** {{ui:settings:automation.item.group_triage_model.what}}

**{{ui:settings:automation.item.own_replies_verbatim.name}}.** {{ui:settings:automation.item.own_replies_verbatim.what}}

**{{ui:settings:automation.item.provider_retries.name}}.** {{ui:settings:automation.item.provider_retries.what}}

**{{ui:settings:automation.item.remembered_approval_hours.name}}.** {{ui:settings:automation.item.remembered_approval_hours.what}}

**{{ui:settings:automation.item.schedule_receipt_review.name}}.** {{ui:settings:automation.item.schedule_receipt_review.what}}

**{{ui:settings:automation.item.settings_change_limit.name}}.** {{ui:settings:automation.item.settings_change_limit.what}}

**{{ui:settings:automation.item.structured_output.name}}.** {{ui:settings:automation.item.structured_output.what}}

**{{ui:settings:automation.item.task_retention_days.name}}.** {{ui:settings:automation.item.task_retention_days.what}}

**{{ui:settings:automation.item.task_stall_rounds.name}}.** {{ui:settings:automation.item.task_stall_rounds.what}}

**{{ui:settings:automation.item.task_tree_idle_message_limit.name}}.** {{ui:settings:automation.item.task_tree_idle_message_limit.what}}

**{{ui:settings:automation.item.web_fetch_limit.name}}.** {{ui:settings:automation.item.web_fetch_limit.what}}

**{{ui:settings:automation.item.web_search.name}}.** {{ui:settings:automation.item.web_search.what}}

**{{ui:settings:automation.item.widget_artifacts_auto.name}}.** {{ui:settings:automation.item.widget_artifacts_auto.what}}

**{{ui:settings:automation.item.widget_computer_auto.name}}.** {{ui:settings:automation.item.widget_computer_auto.what}}
