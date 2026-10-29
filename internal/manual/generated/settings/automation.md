---
id: settings/automation
title: Behavior
summary: See and adjust what Tofi does on its own
link: /settings/automation
kind: reference
---
# Behavior

Open this page: [Behavior](/settings/automation)

| Name | Key | Values | Default | Change class | Where |
|---|---|---|---|---|---|
| Review scheduled and background work before delivery | `automation.answer_gate` | whole number 0 to 5 | 2 | direct | `/settings/automation#checks` |
| Background model | `automation.background_model` | a configured model ID, or default |  | confirm | `/settings/automation#models` |
| Command check interval | `automation.command_check_minutes` | whole number 5 to 1440 | 60 minutes | direct | `/settings/automation#computer` |
| Completion review after tool work | `automation.completion_review` | on or off | true | direct | `/settings/automation#checks` |
| Conversation model | `automation.conversation_model` | a configured model ID, or default | claude-sonnet-5-5 | confirm | `/settings/automation#models` |
| Desktop idle hibernation | `automation.desktop_idle_seconds` | read only |  | forbidden: the person changes it on the page | `/settings/automation#computer` |
| Reply tool required for Haiku | `automation.force_say_haiku_dm` | on or off | true | direct | `/settings/automation#replies` |
| Finished group exchanges, summarized | `automation.group_exchange_gists` | on or off | true | direct | `/settings/automation#history` |
| Follow-up messages go to the member still working | `automation.group_followup_to_working` | on or off | true | direct | `/settings/automation#replies` |
| Route unaddressed group messages | `automation.group_triage` | on or off | true | direct | `/settings/automation#replies` |
| Group routing model | `automation.group_triage_model` | a configured model ID, or default |  | confirm | `/settings/automation#models` |
| Own replies kept in full | `automation.own_replies_verbatim` | whole number 1 to 20 | 3 | direct | `/settings/automation#history` |
| Automatic retries | `automation.provider_retries` | whole number 0 to 10 | 8 | direct | `/settings/automation#reliability` |
| Remembered approvals last | `automation.remembered_approval_hours` | whole number 1 to 720 | 24 hours | confirm | `/settings/automation#checks` |
| Check scheduled runs before they finish | `automation.schedule_receipt_review` | on or off | true | direct | `/settings/automation#schedules` |
| Settings changes per run | `automation.settings_change_limit` | read only | 8 | forbidden: the person changes it on the page | `/settings/automation#checks` |
| Structured replies | `automation.structured_output` | on or off | true | direct | `/settings/automation#replies` |
| Finished tasks kept for | `automation.task_retention_days` | whole number 0 to 3650 | 30 days | direct | `/settings/automation#history` |
| Stall report after | `automation.task_stall_rounds` | whole number 1 to 20 | 3 | direct | `/settings/automation#checks` |
| Idle messages per task tree | `automation.task_tree_idle_message_limit` | whole number 10 to 500 | 30 | direct | `/settings/automation#reliability` |
| Web search | `automation.web_search` | on or off | true | direct | `/settings/automation#replies` |
| Open Artifacts when a Bot makes a file | `automation.widget_artifacts_auto` | on or off | true | direct | `/settings/automation#widgets` |
| Open the shared computer when a Bot uses it | `automation.widget_computer_auto` | on or off | true | direct | `/settings/automation#widgets` |

**Review scheduled and background work before delivery.** A reviewer reads each draft result of a scheduled run against a rubric before it is delivered, and a weak draft goes back to the Bot for another try. This is how many times a draft can be sent back; 0 turns the review off. Chat replies are never reviewed.

**Background model.** Handles light work behind the scenes: history summaries, routing, reviews and follow-up checks. Automatic means Haiku with Claude, and the conversation model elsewhere.

**Command check interval.** When a Bot waits for a command to end, a timed check wakes it after this long in case the end signal is lost. A timer the Bot sets itself replaces it.

**Completion review after tool work.** After using tools, a Bot checks its answer against the actual results once before it replies.

**Conversation model.** Runs chats, group work, scheduled runs and anything that uses tools. A Bot's own model setting overrides it.

**Desktop idle hibernation.** The shared computer hibernates after this long unused and wakes on the next tool call. Set by the computer service.

**Reply tool required for Haiku.** In direct messages, Haiku must reply through the chat every turn, so its answer always lands in the conversation.

**Finished group exchanges, summarized.** In group chats, exchanges that wrapped up without a Bot are folded into a short gist for it.

**Follow-up messages go to the member still working.** When a group message names no one and a member is still working on the person's previous message, the new message goes to that member as a queued note, read at its next step, instead of being routed again. Once that member has answered, the next message is routed as usual. Off routes every such message.

**Route unaddressed group messages.** When a group message names no one, a background model picks the member whose role fits it best. Off sends it to the Primary Assistant, or to a random member when there is none. A group with one active member skips the step.

**Group routing model.** The model that picks the member for an unaddressed group message. Automatic means the background model.

**Own replies kept in full.** A Bot sees this many of its latest replies word for word. Older ones shrink to a one-line gist so it stops copying its own phrasing.

**Automatic retries.** Temporary model errors, such as overload or a dropped connection, are retried this many times before a run fails. 0 turns retries off.

**Remembered approvals last.** When you choose Remember on an approval, the same call is allowed in that chat for this long.

**Check scheduled runs before they finish.** A scheduled run that ends without a completion record gets one chance to finish the job before it is marked done.

**Settings changes per run.** The most settings changes a Bot may make by itself in one run. Past it, the Bot lists the rest for you to make. Only you change this number.

**Structured replies.** Only a Bot's finished replies appear in the chat; what it writes while working stays in the activity view. Turn off to post everything as before.

**Finished tasks kept for.** After this many days a finished task loses its request text, checklist and revisions. Its title and one-line result stay. 0 keeps everything.

**Stall report after.** When a Bot that holds a task finishes this many rounds with no change in the task's checklist or status, the Bot at the root of the work is told once. The count starts again when the task moves.

**Idle messages per task tree.** How many messages the Bots of one piece of work may pass between them without progress. Progress is a checklist item that ends, a task that closes done, or a result handed in, and it restarts the count. At the limit they stop handing work to each other until progress happens, and the root Bot decides what to tell you.

**Web search.** Claude models search the web on their own, and read the pages they rely on, before answering when a question may be newer than what they know.

**Open Artifacts when a Bot makes a file.** The first time a Bot hands over a file in a chat, the Artifacts widget opens beside the messages. Later files refresh its list. Off keeps it closed until you open it.

**Open the shared computer when a Bot uses it.** When a Bot takes the shared computer in the chat you are viewing, its widget opens beside the messages. It stays open and folds into a strip 30 seconds after the Bot lets go. Off keeps it closed until you open it.
