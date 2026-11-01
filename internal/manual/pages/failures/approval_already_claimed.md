---
id: failures/approval_already_claimed
title: approval_already_claimed
summary: This exact call already used its approval in this run. Nothing was sent now.
link: /
kind: failure
---
# approval_already_claimed

Open this page: [Where to look](/)

**Meaning.** This exact call already used its approval in this run. Nothing was sent now.

**Usual cause.** The same call with the same arguments ran before, or is running.

**Steps.**
1. Read the result of the earlier call.
2. Do not send the same call again.
3. If another call is needed, change its arguments and ask for a new approval.
