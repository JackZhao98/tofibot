---
id: failures/settings_change_needs_approval
title: settings_change_needs_approval
summary: This change needs the person to approve a card for exactly this change.
link: /settings/permissions#mode
kind: failure
---
# settings_change_needs_approval

Open this page: [Where to look](/settings/permissions#mode)

**Meaning.** This change needs the person to approve a card for exactly this change.

**Usual cause.** The key is confirm class: approval mode, models, another Bot's identity or connections, archive, delete, group members, disconnect, a looser tool mark.

**Steps.**
1. Ask with request_approval and put the token from the result in target.
2. Repeat the same settings call unchanged after the person approves.
