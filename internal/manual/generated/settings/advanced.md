---
id: settings/advanced
title: {{ui:settings:shell.page.advanced.name}}
summary: {{ui:settings:shell.page.advanced.description}}
link: /settings/advanced
kind: reference
---
# {{ui:settings:shell.page.advanced.name}}

Open this page: [{{ui:settings:shell.page.advanced.name}}](/settings/advanced)

| Name | Key | Values | Default | Change class | Where |
|---|---|---|---|---|---|
| {{ui:settings:automation.item.answer_gate.name}} | `automation.answer_gate` | whole number 0 to 5 | 2 | direct | `/settings/advanced#reviews` |
| {{ui:settings:automation.item.background_model.name}} | `automation.background_model` | a configured model ID, or default |  | confirm | `/settings/models#more` |
| {{ui:settings:automation.item.bots_can_keep_files.name}} | `automation.bots_can_keep_files` | on or off | true | direct | `/settings/advanced#replies` |
| {{ui:settings:automation.item.browser_engine.name}} | `automation.browser_engine` | classic, playwright | playwright | direct | `/settings/advanced#computer` |
| {{ui:settings:automation.item.command_check_minutes.name}} | `automation.command_check_minutes` | whole number 5 to 1440 | 60 minutes | direct | `/settings/advanced#computer` |
| {{ui:settings:automation.item.completion_review.name}} | `automation.completion_review` | on or off | true | direct | `/settings/advanced#reviews` |
| {{ui:settings:automation.item.computer_idle_off_minutes.name}} | `automation.computer_idle_off_minutes` | whole number 5 to 60 | 15 minutes | direct | `/settings/advanced#behavior` |
| {{ui:settings:automation.item.conversation_model.name}} | `automation.conversation_model` | a configured model ID, or default | claude-sonnet-5-5 | confirm | `/settings/models#defaults` |
| {{ui:settings:automation.item.desktop_engine.name}} | `automation.desktop_engine` | classic, a11y | classic | direct | `/settings/advanced#computer` |
| {{ui:settings:automation.item.desktop_idle_seconds.name}} | `automation.desktop_idle_seconds` | read only |  | forbidden: the person changes it on the page | `/settings/computer#environment` |
| {{ui:settings:automation.item.group_exchange_gists.name}} | `automation.group_exchange_gists` | on or off | true | direct | `/settings/advanced#groups` |
| {{ui:settings:automation.item.group_followup_to_working.name}} | `automation.group_followup_to_working` | on or off | true | direct | `/settings/advanced#groups` |
| {{ui:settings:automation.item.group_triage.name}} | `automation.group_triage` | on or off | true | direct | `/settings/advanced#groups` |
| {{ui:settings:automation.item.group_triage_model.name}} | `automation.group_triage_model` | a configured model ID, or default |  | confirm | `/settings/models#more` |
| {{ui:settings:automation.item.monthly_spend_limit_usd.name}} | `automation.monthly_spend_limit_usd` | read only | 0 usd | forbidden: the person changes it on the page | `/settings/usage#spend` |
| {{ui:settings:automation.item.own_replies_verbatim.name}} | `automation.own_replies_verbatim` | whole number 1 to 20 | 3 | direct | `/settings/advanced#replies` |
| {{ui:settings:automation.item.personal_input_review.name}} | `automation.personal_input_review` | declared, commands, all | commands | direct | `/settings/permissions#advanced` |
| {{ui:settings:automation.item.provider_retries.name}} | `automation.provider_retries` | whole number 0 to 10 | 8 | direct | `/settings/advanced#behavior` |
| {{ui:settings:automation.item.remembered_approval_hours.name}} | `automation.remembered_approval_hours` | whole number 1 to 720 | 24 hours | confirm | `/settings/permissions#remembered` |
| {{ui:settings:automation.item.schedule_receipt_review.name}} | `automation.schedule_receipt_review` | on or off | true | direct | `/settings/advanced#reviews` |
| {{ui:settings:automation.item.settings_change_limit.name}} | `automation.settings_change_limit` | read only | 8 | forbidden: the person changes it on the page | `/settings/advanced#data` |
| {{ui:settings:automation.item.structured_output.name}} | `automation.structured_output` | on or off | true | direct | `/settings/advanced#behavior` |
| {{ui:settings:automation.item.task_fanout_depth.name}} | `automation.task_fanout_depth` | whole number 1 to 5 | 2 | direct | `/settings/advanced#tasks` |
| {{ui:settings:automation.item.task_fanout_width.name}} | `automation.task_fanout_width` | whole number 1 to 12 | 4 | direct | `/settings/advanced#tasks` |
| {{ui:settings:automation.item.task_part_deadline_minutes.name}} | `automation.task_part_deadline_minutes` | whole number 1 to 1440 | 30 minutes | direct | `/settings/advanced#tasks` |
| {{ui:settings:automation.item.task_part_deadline_scheduled_minutes.name}} | `automation.task_part_deadline_scheduled_minutes` | whole number 1 to 1440 | 60 minutes | direct | `/settings/advanced#tasks` |
| {{ui:settings:automation.item.task_retention_days.name}} | `automation.task_retention_days` | whole number 0 to 3650 | 30 days | direct | `/settings/advanced#replies` |
| {{ui:settings:automation.item.task_stall_rounds.name}} | `automation.task_stall_rounds` | whole number 1 to 20 | 3 | direct | `/settings/advanced#reviews` |
| {{ui:settings:automation.item.task_tree_idle_message_limit.name}} | `automation.task_tree_idle_message_limit` | whole number 10 to 500 | 30 | direct | `/settings/advanced#tasks` |
| {{ui:settings:automation.item.trace_private_origins.name}} | `automation.trace_private_origins` | text |  | confirm | `/settings/advanced#computer` |
| {{ui:settings:automation.item.trace_retention_days.name}} | `automation.trace_retention_days` | whole number 0 to 90 | 7 days | direct | `/settings/advanced#computer` |
| {{ui:settings:automation.item.upload_retention_days.name}} | `automation.upload_retention_days` | whole number 0 to 3650 | 0 days | confirm | `/settings/advanced#replies` |
| {{ui:settings:automation.item.web_fetch_limit.name}} | `automation.web_fetch_limit` | whole number 1 to 100 | 20 | direct | `/settings/advanced#replies` |
| {{ui:settings:automation.item.web_search.name}} | `automation.web_search` | on or off | true | direct | `/settings/advanced#behavior` |
| {{ui:settings:automation.item.widget_artifacts_auto.name}} | `automation.widget_artifacts_auto` | on or off | true | direct | `/settings/advanced#behavior` |
| {{ui:settings:automation.item.widget_computer_auto.name}} | `automation.widget_computer_auto` | on or off | true | direct | `/settings/advanced#behavior` |
| {{ui:settings:automation.item.work_trace.name}} | `automation.work_trace` | on or off | true | direct | `/settings/advanced#behavior` |
| {{ui:settings:advanced.data}} | `system.data` | not available to the tool |  | forbidden: the person changes it on the page | `/settings/advanced#data` |
| {{ui:settings:advanced.danger}} | `system.purge` | not available to the tool |  | forbidden: the person changes it on the page | `/settings/advanced#danger` |
| {{ui:settings:advanced.version}} | `system.update` | not available to the tool |  | forbidden: the person changes it on the page | `/settings/advanced#version` |
| {{ui:settings:advanced.version}} | `system.version` | not available to the tool |  | forbidden: the person changes it on the page | `/settings/advanced#version` |

**{{ui:settings:automation.item.answer_gate.name}}.** {{ui:settings:automation.item.answer_gate.what}}

**{{ui:settings:automation.item.background_model.name}}.** {{ui:settings:automation.item.background_model.what}}

**{{ui:settings:automation.item.bots_can_keep_files.name}}.** {{ui:settings:automation.item.bots_can_keep_files.what}}

**{{ui:settings:automation.item.browser_engine.name}}.** {{ui:settings:automation.item.browser_engine.what}}

**{{ui:settings:automation.item.command_check_minutes.name}}.** {{ui:settings:automation.item.command_check_minutes.what}}

**{{ui:settings:automation.item.completion_review.name}}.** {{ui:settings:automation.item.completion_review.what}}

**{{ui:settings:automation.item.computer_idle_off_minutes.name}}.** {{ui:settings:automation.item.computer_idle_off_minutes.what}}

**{{ui:settings:automation.item.conversation_model.name}}.** {{ui:settings:automation.item.conversation_model.what}}

**{{ui:settings:automation.item.desktop_engine.name}}.** {{ui:settings:automation.item.desktop_engine.what}}

**{{ui:settings:automation.item.desktop_idle_seconds.name}}.** {{ui:settings:automation.item.desktop_idle_seconds.what}}

**{{ui:settings:automation.item.group_exchange_gists.name}}.** {{ui:settings:automation.item.group_exchange_gists.what}}

**{{ui:settings:automation.item.group_followup_to_working.name}}.** {{ui:settings:automation.item.group_followup_to_working.what}}

**{{ui:settings:automation.item.group_triage.name}}.** {{ui:settings:automation.item.group_triage.what}}

**{{ui:settings:automation.item.group_triage_model.name}}.** {{ui:settings:automation.item.group_triage_model.what}}

**{{ui:settings:automation.item.monthly_spend_limit_usd.name}}.** {{ui:settings:automation.item.monthly_spend_limit_usd.what}}

**{{ui:settings:automation.item.own_replies_verbatim.name}}.** {{ui:settings:automation.item.own_replies_verbatim.what}}

**{{ui:settings:automation.item.personal_input_review.name}}.** {{ui:settings:automation.item.personal_input_review.what}}

**{{ui:settings:automation.item.provider_retries.name}}.** {{ui:settings:automation.item.provider_retries.what}}

**{{ui:settings:automation.item.remembered_approval_hours.name}}.** {{ui:settings:automation.item.remembered_approval_hours.what}}

**{{ui:settings:automation.item.schedule_receipt_review.name}}.** {{ui:settings:automation.item.schedule_receipt_review.what}}

**{{ui:settings:automation.item.settings_change_limit.name}}.** {{ui:settings:automation.item.settings_change_limit.what}}

**{{ui:settings:automation.item.structured_output.name}}.** {{ui:settings:automation.item.structured_output.what}}

**{{ui:settings:automation.item.task_fanout_depth.name}}.** {{ui:settings:automation.item.task_fanout_depth.what}}

**{{ui:settings:automation.item.task_fanout_width.name}}.** {{ui:settings:automation.item.task_fanout_width.what}}

**{{ui:settings:automation.item.task_part_deadline_minutes.name}}.** {{ui:settings:automation.item.task_part_deadline_minutes.what}}

**{{ui:settings:automation.item.task_part_deadline_scheduled_minutes.name}}.** {{ui:settings:automation.item.task_part_deadline_scheduled_minutes.what}}

**{{ui:settings:automation.item.task_retention_days.name}}.** {{ui:settings:automation.item.task_retention_days.what}}

**{{ui:settings:automation.item.task_stall_rounds.name}}.** {{ui:settings:automation.item.task_stall_rounds.what}}

**{{ui:settings:automation.item.task_tree_idle_message_limit.name}}.** {{ui:settings:automation.item.task_tree_idle_message_limit.what}}

**{{ui:settings:automation.item.trace_private_origins.name}}.** {{ui:settings:automation.item.trace_private_origins.what}}

**{{ui:settings:automation.item.trace_retention_days.name}}.** {{ui:settings:automation.item.trace_retention_days.what}}

**{{ui:settings:automation.item.upload_retention_days.name}}.** {{ui:settings:automation.item.upload_retention_days.what}}

**{{ui:settings:automation.item.web_fetch_limit.name}}.** {{ui:settings:automation.item.web_fetch_limit.what}}

**{{ui:settings:automation.item.web_search.name}}.** {{ui:settings:automation.item.web_search.what}}

**{{ui:settings:automation.item.widget_artifacts_auto.name}}.** {{ui:settings:automation.item.widget_artifacts_auto.what}}

**{{ui:settings:automation.item.widget_computer_auto.name}}.** {{ui:settings:automation.item.widget_computer_auto.what}}

**{{ui:settings:automation.item.work_trace.name}}.** {{ui:settings:automation.item.work_trace.what}}
