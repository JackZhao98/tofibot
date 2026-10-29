---
id: failures/schedule_interval_invalid
title: schedule_interval_invalid
summary: interval_seconds is outside the allowed range.
link: /b/{bot}?side=tasks
kind: failure
---
# schedule_interval_invalid

Open this page: [Where to look](/b/{bot}?side=tasks)

**Meaning.** interval_seconds is outside the allowed range.

**Usual cause.** The interval is shorter than the minimum or longer than the maximum.

**Steps.**
1. Use a longer or a shorter interval.
2. The error text names the limit.
