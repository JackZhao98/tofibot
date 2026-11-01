---
id: failures/schema_required
title: schema_required
summary: The tool was not read in this run, so the call was not sent.
link: /
kind: failure
---
# schema_required

Open this page: [Where to look](/)

**Meaning.** The tool was not read in this run, so the call was not sent.

**Usual cause.** The call used a tool name that search_mcp_tools did not return in this run.

**Steps.**
1. Call search_mcp_tools with the server name. Read the schema in the result.
2. Call call_mcp_tool again with arguments that match the schema.
3. This refusal does not count as a repair attempt.
