---
id: failures/mcp_needs_human_unattended
title: mcp_needs_human_unattended
summary: A person must decide this call, and nobody attends this scheduled run. The call was not sent.
link: /settings/connections
kind: failure
---
# mcp_needs_human_unattended

Open this page: [Where to look](/settings/connections)

**Meaning.** A person must decide this call, and nobody attends this scheduled run. The call was not sent.

**Usual cause.** The review asked for a person. A scheduled run cannot ask.

**Steps.**
1. Finish the other work of the run.
2. In the summary, name the connection, the tool and the step that waits.
3. The owner can choose Always allow for the tool on an approval card in chat. Then later scheduled runs send the call without asking.
4. Do not retry the call in this run.
