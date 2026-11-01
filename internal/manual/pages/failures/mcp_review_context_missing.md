---
id: failures/mcp_review_context_missing
title: mcp_review_context_missing
summary: The review could not check the call, because a fact about the request is missing. The call was not sent.
link: /settings/connections
kind: failure
---
# mcp_review_context_missing

Open this page: [Where to look](/settings/connections)

**Meaning.** The review could not check the call, because a fact about the request is missing. The call was not sent.

**Usual cause.** The run has no verified user request behind it, or the record of the task changed during the review.

**Steps.**
1. With a person in the chat, ask the person to send the request as a message. Then call again.
2. In a scheduled run, report the step as blocked.
3. Do not send the same call again without a change. An identical call stays blocked.
