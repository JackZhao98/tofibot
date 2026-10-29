---
id: failures/setting_change_unknown
title: setting_change_unknown
summary: The approval target does not name a settings change the server has seen in this run.
link: /settings/general
kind: failure
---
# setting_change_unknown

Open this page: [Where to look](/settings/general)

**Meaning.** The approval target does not name a settings change the server has seen in this run.

**Usual cause.** The token was made up, was issued in another run, or is for a different value.

**Steps.**
1. Call settings with set first.
2. Use the exact token from its result.
