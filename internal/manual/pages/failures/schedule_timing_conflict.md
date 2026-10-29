---
id: failures/schedule_timing_conflict
title: schedule_timing_conflict
summary: A timing field does not fit the kind of the schedule.
link: /b/{bot}?side=tasks
kind: failure
---
# schedule_timing_conflict

Open this page: [Where to look](/b/{bot}?side=tasks)

**Meaning.** A timing field does not fit the kind of the schedule.

**Usual cause.** For example weekdays on a daily schedule, or daily_time on an interval schedule.

**Steps.**
1. Remove the field that does not belong, or change the kind first.
