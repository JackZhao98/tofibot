---
id: failures/schedule_run_at_required
title: schedule_run_at_required
summary: A one-time schedule needs the time to run.
link: /b/{bot}?side=tasks
kind: failure
---
# schedule_run_at_required

Open this page: [Where to look](/b/{bot}?side=tasks)

**Meaning.** A one-time schedule needs the time to run.

**Usual cause.** run_at was left out.

**Steps.**
1. Give run_at as a full date and time with a timezone offset.
