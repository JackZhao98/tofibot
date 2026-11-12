---
id: failures/browser_login_required
title: browser_login_required
summary: The page needs the person to enter login details.
link: /settings/computer
kind: failure
---
# browser_login_required

Open this page: [Environments](/settings/computer)

**Meaning.** The page needs the person to enter login details. Ask them to open the browser, log in, then explicitly return control to the Bot. Do not type a password.

**Usual cause.** The browser permission, page identity, control connection or preview availability does not meet this step's requirements.

**Steps.**
Never automatically retry a step whose effect is unknown. Continue only from a fresh observation after the person resolves the cause.
