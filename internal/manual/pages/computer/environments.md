---
id: computer/environments
title: Environments and browser order
summary: Where browser and desktop actions run, and how to change the order.
link: /settings/computer#environment
kind: guide
see_also: computer/kvm, computer/none, settings/computer
---
# Environments and browser order

Open this page: [{{ui:settings:shell.page.computer.name}}](/settings/computer#environment)

Settings > Environments shows the Bot computer and paired Macs. Each row shows the status of the built-in browser, desktop control, files and commands. Connected features can be used now. Disconnected features need the connection restored. Off features have not been enabled on that Mac.

Messages from web and mobile start with the Bot computer, then connected Macs. Messages from the Mac app start with that Mac, then the Bot computer, then other Macs. Scheduled work uses the web order. A person's instruction to use a named environment overrides this order.

On a Mac, the Bot first uses the Tofi app's built-in browser, which has its own sign-ins. If the job needs another browser, the Bot can use Safari or Chrome through desktop control, then continue in the next environment. The reply names the environment and browser that served the action.

Use **Preferred environment (web and mobile)** and **Preferred environment (Mac app)** to change the first environment. **Automatic environment failover** lets an opening request continue in the next available browser after a definite refusal. Effect review still applies. An action with an unknown effect is never replayed. Taking control or stopping the Bot makes it wait.
