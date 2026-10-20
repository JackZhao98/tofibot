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

# Fast UI preview against a running server

Iterate on the UI without a release: `cd ui && TOFI_DEV_API_ORIGIN=https://<server>:8321 npm run dev:remote`, then open http://localhost:5173 in Chrome and sign in once. Vite hot-reloads UI edits instantly while `/api` is proxied to that server's real data; `dev:remote` accepts the default self-signed certificate. Safari will not keep the Secure session cookie on plain-http localhost, so use Chrome. Backend changes still need a build and deploy.
