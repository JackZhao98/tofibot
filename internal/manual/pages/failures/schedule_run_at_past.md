---
id: failures/schedule_run_at_past
title: schedule_run_at_past
summary: run_at is already in the past.
link: /b/{bot}?side=tasks
kind: failure
---
# schedule_run_at_past

Open this page: [Where to look](/b/{bot}?side=tasks)

**Meaning.** run_at is already in the past.

**Usual cause.** The time was calculated in another timezone, or an old date was reused.

**Steps.**
1. Pick a time in the future.
2. Check the timezone of the schedule.
