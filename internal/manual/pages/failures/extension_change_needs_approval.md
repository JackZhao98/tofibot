---
id: failures/extension_change_needs_approval
title: extension_change_needs_approval
summary: This extension change needs the person to approve a card for exactly this change.
link: /settings/connections
kind: failure
---
# extension_change_needs_approval

Open this page: [Where to look](/settings/connections)

**Meaning.** This extension change needs the person to approve a card for exactly this change.

**Usual cause.** A connection or a published Skill version is workspace-wide.

**Steps.**
1. Ask with request_approval and put the token in target.
2. Repeat the same call after the person approves.
