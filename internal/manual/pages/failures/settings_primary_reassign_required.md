---
id: failures/settings_primary_reassign_required
title: settings_primary_reassign_required
summary: The Primary Assistant cannot be archived or deleted while other Bots exist.
link: /b/{bot}/settings#identity
kind: failure
---
# settings_primary_reassign_required

Open this page: [Where to look](/b/{bot}/settings#identity)

**Meaning.** The Primary Assistant cannot be archived or deleted while other Bots exist.

**Usual cause.** One Bot always holds the Primary Assistant role.

**Steps.**
1. Ask the person to choose another Primary Assistant first (account.primary_bot).
2. Then repeat the request.

**Fix with the settings tool.** The key is `account.primary_bot`.
