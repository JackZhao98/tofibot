---
id: failures/invalid_arguments
title: invalid_arguments
summary: The arguments do not match the schema of the tool. The call was not sent.
link: /
kind: failure
---
# invalid_arguments

Open this page: [Where to look](/)

**Meaning.** The arguments do not match the schema of the tool. The call was not sent.

**Usual cause.** A required field is missing, a field is not in the schema, or a value has the wrong type.

**Steps.**
1. Read the field names in the message.
2. Compare the arguments with the schema of the tool.
3. Send the call again with corrected arguments. Each tool allows a limited number of repairs in one run.
