---
id: schedules
title: Scheduled tasks
summary: How a Bot runs work on a timer, and how timing is changed.
link: /b/{bot}?side=tasks
kind: guide
see_also: memory, failures/schedule_timing_locked
---
# Scheduled tasks

Open this page: [{{ui:chat:header.tasks}}](/b/{bot}?side=tasks)

A scheduled task is a standing request for one Bot. At the set time the Bot runs it and posts the result in the chat.

- **Kinds.** One time, every N seconds, daily at a time, or weekly on chosen days.
- **Timezone.** A task uses the timezone you set when you made it. The account timezone is on [{{ui:settings:shell.page.general.name}}](/settings/general#region).
- **Pause and resume.** A paused task keeps its timing and does not run.
- **Change the timing.** Use the editor in the Tasks view, or ask the Bot to use `update_schedule`. A change calculates the next run from now. A run that has started is not changed.
- **Nobody is there.** A scheduled run has no person to ask. It cannot change settings, and a tool call that needs a person does not run. The Bot says in its result what a person needs to do.

A task result is a message of the Bot. Notifications for task results are a switch on the General page.
