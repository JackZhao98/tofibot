---
id: failures/schedule_run_at_invalid
title: schedule_run_at_invalid
summary: run_at is not a valid date and time.
link: /b/{bot}?side=tasks
kind: failure
---
# schedule_run_at_invalid

Open this page: [Where to look](/b/{bot}?side=tasks)

**Meaning.** run_at is not a valid date and time.

**Usual cause.** The text is not in RFC 3339 form.

**Steps.**
1. Write run_at as year-month-day, a T, the time and a timezone offset.
