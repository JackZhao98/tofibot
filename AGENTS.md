# Core source boundaries

This repository owns Server, Web UI, contracts and Docker/Worker source. Keep the
Web source canonical; desktop clients consume a versioned UI artifact. Do not
introduce desktop source imports into core builds or tests. Keep user examples in
isolated acceptance tests, not production prompts or provider routing. All
assistant-authored model system prompts and built-in Skill instructions must be
in English. Preserve user content and third-party metadata.

Use synthetic data for tests. Source build success is not deployment proof.
The dedicated-host installer is `install.sh` -> `deploy/self-host/tofi_host.py`
(released by `.github/workflows/release.yml`); treat it as release-candidate
until clean Linux/KVM acceptance in docs/agent-plan/one-command-install.md passes,
and change it, its compose.yaml and the release workflow together. Its interactive UI is
`deploy/self-host/tofi_tui.py`, embedded verbatim in install.sh (`make self-host-embed` after edits). Never read or export credentials for a
source-tree review. Retain third-party notices and verify release contents.

Web UI text is localized: user-visible strings live in `ui/src/locales/<lang>/<namespace>.json`
(rules in ui/src/locales/README.md); `npm --prefix ui run test:i18n` blocks new hardcoded text.

# Asking a human (one rendezvous)

Every Bot-to-human question goes through the durable questions rendezvous in
`internal/app/questions.go`: it suspends the run (`runtime.SuspendForUserInput`
plus `run_input_waits`), survives restart, expires (30 min by default) and is
shown by `ui/src/QuestionCard.tsx`. Model tools: `ask_user_question`,
`request_approval`, `ask_user_form` (webpage fields; password fields are sealed
into the vault and return an origin-bound `secret_ref`) and `request_secret_input`
(one secret with no website: a thin front that stores a one-password-field form
question; its `secret_ref` is bound to the run, Bot and conversation, lives 15
minutes, is consumed by `use_secret_input` for env/ssh/shell_exec only and is never typed into a page). Do not add a second mechanism:
no polling loop in a tool, no separate HTTP route, no callback hook. Passwords
are written only to the vault (`AnswerUserForm`), never to SQL, events or results.
All of these, and the MCP approval gate, refuse a conversation that is not user-visible (`requireHumanPresent`,
outcome `conversation_not_user_visible`); a hidden Bot-to-Bot run reaches a human only
through its reply, which returns to the assigning Bot or conversation. Mail drafts
(`prepare_email`) are still a separate card path.

Two Bot-initiated handover cards ride the same rendezvous (`internal/app/handover.go`, UI `ui/src/HandoverCards.tsx`,
`ui/src/TakeoverDialog.tsx`); they add question types `takeover` and `connection` and one stored column (`questions.handover_target`, added by
`ensureColumn`). Their answer is one enumerated word (takeover: `done`/`cannot`; connection: `connected`/`failed`/`declined`) and the model result
(`handoverResult`) is a status word plus a fixed sentence: nothing the person types, copies or sees on the remote screen is part of an answer,
event, result or table (the keystroke/clipboard path stays browser -> `/api/computers/firecracker/actions` -> guest). `request_takeover` (needs the
shared computer; site is stored as an origin only; window <= 14 min because the guest collects an idle desktop after 15) opens a modal around
`RemoteDesktopControl`, which acquires with `handover_question`: that hold is bound to the open card, is never yielded by the 15 s idle release
(`desktop.control.renew`) and ends on the next heartbeat or on answer/cancel/expiry/run start; the run's own desktop tabs are kept while it is parked
on the card. `request_connection` runs the Settings connect flow for this client (native app, https popup, or the shared computer on plain http,
via `mcpOAuthRoute`); `connected` is accepted only if the server sees the token (`connection_not_ready` otherwise); a blocked route reports
`failed`, a retryable error keeps the card open. Both cards are refused in hidden conversations (`requireHumanPresent`) and, like
`ask_user_question`, park and expire in scheduled runs. `manage_extensions oauth_required` points at `request_connection` only in
user-visible conversations.

# Security gates

`manage_extensions` changes (MCP create/update/delete/test, Skill install/delete) execute only after a person's
approval of the exact payload (`internal/app/extension_gate.go`: request_approval target = digest token, one use) and are refused in unattended
runs. The approval card carries server-built `approval.facts` (kind, name, address with masked query, transport, tool lists, header names, skill
manifest summary) rendered above the Bot's own wording; the Bot cannot set them and a token the server has not seen in the run gets no card.
Bot-created MCP servers are refused for loopback/private/link-local/CGNAT/metadata hosts at create time (fail closed on DNS errors, odd numeric
forms, IPv6/mapped/NAT64/6to4) and again at every connection: `bot_managed` servers dial through `extensions.GuardedTransport` (dial-time `Control`
hook, no env proxy, redirects never followed). The owner's allow path is Settings: a server saved there is not `bot_managed`. A spent approval is
returned only when the guarded call fails with certainty before any effect (`not_executed`); otherwise it stays spent. Click approvals
(`claimApproval`) match the label in one normal form (`approvalLabelText`, also what the refusal shows) and are consumed once
(`click_approval_claims`). Login limits are per peer address (IPv6 /64) and per account identifier; a peer that earlier proved the password skips
the per-identifier bucket (`owner_auth_known_sources`) so strangers cannot lock the real user out; password changes use their own `pwchange`
bucket namespace; there is no global bucket.
Session cookies are Secure when `TOFI_PUBLIC_ORIGIN` is https; Caddy sends HSTS. Known open: `computer_shell` shares a uid and loopback with the
shared Chrome DevTools port.

# Web timeline performance

The message list is built for cost that does not grow with the conversation. Each row is `MessageRow` (a `memo` component compared by
`rowModelEqual` in `ui/src/timelineRow.ts`); the Workspace derives a plain-data `RowModel` per row and rows call the Workspace only through
the identity-stable `RowHandlers`. Streamed text never goes through Workspace state: `ui/src/draftStream.ts` holds the growing text, `drafts`
changes only in structure (a draft appears, changes owner or sequence, becomes visible, gets a reply marker, goes away), and the one row that
shows a draft follows it with `useLiveDraft`. Do not read `drafts[...].content` in the Workspace for anything but emptiness. A conversation
that opens at its newest message draws `FIRST_PAINT_BLOCKS` blocks first and the rest in the next task (`landing` in `useConversationScroll`
decides; an unread divider, a restored position or a pending task focus draw everything). Settled Markdown is kept as elements (`reuse`).
Budgets, each enforced by a script: `test:stream-cost` (<= 4 ms per streamed chunk, <= 16 ms at 4x CPU, no frame over 34 ms),
`test:switch-budget` (cached switch p95 < 60 ms, < 100 ms at 4x, also in WebKit), `test:chat-switch` (also `CHAT_SWITCH_ENGINE=webkit`),
`test:timeline-rows`, `test:perf-structure`. WebKit has no native scroll anchoring: whatever changes layout above the reader after the position
was set (sender marks, a prepended block) must be followed by `viewport.reposition()` or an anchor.

# Fast UI preview against a running server

Iterate on the UI without a release: `cd ui && TOFI_DEV_API_ORIGIN=https://<server>:8321 npm run dev:remote`, then open http://localhost:5173 in Chrome and sign in once. Vite hot-reloads UI edits instantly while `/api` is proxied to that server's real data; `dev:remote` accepts the default self-signed certificate. Safari will not keep the Secure session cookie on plain-http localhost, so use Chrome. Backend changes still need a build and deploy.
