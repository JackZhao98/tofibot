---
id: failures/schedule_timezone_invalid
title: schedule_timezone_invalid
summary: The timezone of the schedule is empty or is not an IANA timezone name.
link: /settings/general#region
kind: failure
---
# schedule_timezone_invalid

Open this page: [Where to look](/settings/general#region)

**Meaning.** The timezone of the schedule is empty or is not an IANA timezone name.

**Usual cause.** A city or an abbreviation was given in place of an IANA name.

**Steps.**
1. Use an IANA timezone name.
2. If the person has not set one, read it with the settings tool (account.timezone) or ask for their city.

**Fix with the settings tool.** The key is `account.timezone`.
