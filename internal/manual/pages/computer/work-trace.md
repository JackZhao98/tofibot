---
id: computer/work-trace
title: Work trace
summary: The pictures of a Bot's screen work, what is never kept, how long they stay, and how to turn them off or delete them.
link: /settings/automation#computer
kind: guide
see_also: computer/kvm, settings/automation
---
# Work trace

Open this page: [{{ui:settings:shell.page.automation.name}}](/settings/automation#computer)

When a Bot works on the Bot computer, the server keeps a small picture of each screenshot the Bot looks at. When the run ends, the pictures join into one strip. The strip is a folded line at the end of the run's Activity list. Open it to see the whole strip, then select one step to see its picture.

A run with fewer than two pictures has no strip. A run that a person watched or that ran without a person (a schedule) is treated the same way.

## What is never kept

The server decides this before it stores a picture. A step that is not kept shows as a grey cell with the word "redacted".

- **Private input.** From the moment a form password or a private input exists in the run until the Bot opens the next page, no picture is kept.
- **Private origins.** A site that a saved or form password is bound to is never kept. The person can add more sites in the setting {{ui:settings:automation.item.trace_private_origins.name}}. A host also covers its subdomains.
- **Takeover.** While the person controls the screen, the Bot gets no capture and nothing is kept.
- **Typed text.** The line under a step names the action, a host or a short label. It never holds typed text, a path or a query string.

The server cannot read the pixels of a picture. A secret that shows on screen outside these rules is not recognised. Turn the trace off, or list the site as a private origin, when a Bot works on pages that show secrets.

## Settings

- **{{ui:settings:automation.item.work_trace.name}}.** On by default. When it is off, the server stores no picture and no row, and the desktop.trace action says that the trace is off.
- **{{ui:settings:automation.item.trace_retention_days.name}}.** Seven days by default. After this period the pictures and strips are deleted. "Keep everything" never deletes them.
- **{{ui:settings:automation.item.trace_private_origins.name}}.** One host or origin per line.

## What the Bot can do

The Bot can set `detail` to `overview` on a capture to get a quarter-size picture for a check of state. The Bot can use the `desktop.trace` action to join its recent captures into one labelled grid, at most nine. The grid is made from stored pictures and takes no new screenshot. It counts toward the images a Bot may view in one run.

## Delete and export

A person can delete the trace of one run. The strip and the pictures of the run are removed. The trace is not part of an export of Bots, chats or the account.
