---
id: failures/browser_transport_unavailable
title: browser_transport_unavailable
summary: The browser connection is unavailable.
link: /settings/computer
kind: failure
---
# browser_transport_unavailable

Open this page: [Environments](/settings/computer)

**Meaning.** The browser connection is unavailable. A new step was not queued. An in-flight step may have run; do not replay it after reconnection.

**Usual cause.** The browser permission, page identity, control connection or preview availability does not meet this step's requirements.

**Steps.**
Never automatically retry a step whose effect is unknown. Continue only from a fresh observation after the person resolves the cause.
