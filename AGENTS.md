# 从这里开始 / Start here

TOFI 是自托管的 AI Bot 工作台（Go 后端 `internal/`，React 19 网页端 `ui/`）。接手前先读：

1. [docs/README.md](docs/README.md) — 全部文档的索引，标明哪些现行、哪些是历史或已被取代。
2. [docs/product/status.md](docs/product/status.md) — 产品现状、线上版本、owner 的打分标准和评测结果；路线图在 [docs/product/roadmap.md](docs/product/roadmap.md)，每个版本改了什么在 [docs/releases.md](docs/releases.md)。
3. [docs/decisions/](docs/decisions/README.md) — owner 拍板的每一条决定（D01 起），写明时间、理由和影响的代码。
4. [docs/process.md](docs/process.md) — 测试、评测、预览、发版、安全审查和模型分工的流程。
5. [docs/design/](docs/design/README.md) — 设计令牌、组件清单、设计原则和迁移方案；动界面之前读。

规则：**每次合并、发版或 owner 拍板，都在同一个提交里更新对应文档**（决定 D27；哪种事件改哪份文件见 `docs/process.md`
第 10 节）。Every merge, release or owner decision updates the matching docs in the same commit. 本文件仍是总入口，下面各节是代码约定。

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

# Web UI motion

One vocabulary, in `ui/src/design-tokens.css` and mirrored for Web Animations in `ui/src/motion.ts`: durations
`--m-instant` 80 / `--m-press` 120 / `--m-quick` 140 / `--m-base` 220 / `--m-panel-in` 280 / `--m-panel-out` 180 /
`--m-flight` 640 ms (leaving is shorter than entering) and easings `--ease-out`, `--ease-settle`, `--ease-spring`
(the only bounce, never for layout), `--ease-inout`, `--ease-in`. New transitions and animations use them;
`npm --prefix ui run test:motion-tokens` fails on literal durations under 800 ms, `cubic-bezier()` or bare `ease`
outside the tokens (existing leftovers are a ceiling in `ui/scripts/motion-tokens-baseline.json` that only shrinks) and
on a `backdrop-filter` without its `-webkit-` twin. The follow-bottom scroll is a tween owned by
`useConversationScroll` (`GLIDE_MS` 220, `GLIDE_STREAM_MS` 120, at most 24 px per frame; reduced motion lands at once);
`test:arrival-glide` holds its budgets. Do not scroll the message pane with a bare `scrollTop =` for organic growth.

# Sidebar order and pins

Two per-account integers on `conversations` (added by `ensureColumn`): `pin_order` (1-based pinned slot, 0 = not pinned) and `sort_order`
(1-based manual slot, 0 = never placed by hand). `PUT /api/conversations/order {pinned, order}` (`internal/app/sidebar_order.go`) replaces both
atomically and rejects duplicates, unknown ids, ids in both lists and archived pins; archiving a Bot or group clears its pin. The list is recency
until the person reorders; after that their order wins, and a conversation the server has not numbered (new since the last reorder) enters at the
top, newest first. Exports (`portable*`) deliberately carry neither field. In the UI, `sidebarOrder.ts` is the pure ordering and `sidebarDndEngine.ts`
the drag/keyboard/long-press engine (its own chunk, loaded in idle time by `useSidebarDnd.ts` to hold the shell budget): it moves elements with transforms from rects measured once at pickup, and React renders once, on drop. Do not
put a style that depends on an attribute or custom property of an ancestor of the list on pickup (it restyles every cat; measured +60 ms at 4x CPU).
`npm --prefix ui run test:sidebar-pins` holds it (frame budget over 60 conversations, touch at 390 px, keyboard, reduced motion).

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
MCP write-tier calls can be allowed for good with "Always allow" (`internal/app/mcp_standing_allowance.go`, table `mcp_standing_allowances`, created by
`migrateStandingAllowances`: `CREATE ... IF NOT EXISTS` + `ensureColumn`). A grant is (account, Bot, MCP server, tool), independent of the payload, and
binds to the server's config fingerprint (name, address, transport, headers), a digest of its credential target, and the tool definition digest
(`MCPToolTierView.Digest`: name, description, schema, annotations): a changed definition or server voids it (Settings shows `tool_changed` /
`server_changed` / `tier_changed`; the next "Always allow" supersedes it). A server change is sticky: `server_changed_at` is set the first time a
different identity is seen (configuration writer hook `Config.ServerIdentityChanged`, any call, the Settings list), so setting an address back does not
revive it; an out-of-process file edit that is changed and reverted between two looks is the one gap. It is created only by the owner question route from a
card the gate marked `standing_eligible` (write tier, conversation a person can see), never for `high_risk`/read-only tools, never from model input
(`approval.standing` is reserved like `remember`); the archived-Bot check is part of the INSERT. Setting a tool to "Always ask me" revokes its grants
(`revoked_by=tier_raised`). It decides when no person attends too (schedules, Bot turns, inbound content) and says so in the card hint and Settings, but
never in a conversation no person can see (`matchStandingAllowance` needs `UserVisible`, and a live grant in a hidden conversation refuses with
`conversation_not_user_visible` even in an unattended run). In AutoReview "auto" mode the reviewer still runs: the grant replaces only the "a person
decides" verdict (`finishMCPReview`); a deny still blocks. The decision is a card answered by actor `standing-allowance`, re-validated at claim time
(`standingClaimValid`), so Revoke (Settings > Automatic behavior, `/api/standing-allowances`) bites an in-progress run at its next call (attended: the
card returns to a person; unattended: it is closed and the call refused `mcp_needs_human_unattended`). Each use bumps `use_count`/`last_used_at` and
writes `mcp_standing_allowance_uses` (null `call_id` when the runtime gave none) inside the claim transaction, and links the tool activity
(`ToolActivity.allowed_by`). Archiving/deleting the Bot or the server (`deleteMCPServer`: use it, not `extensions.DeleteMCP`) revokes the grants
(`revoked_by` `bot_archived` | `bot_deleted` | `server_removed`) and keeps the rows and uses.
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
Every component state is also on the dev-only component catalog, http://localhost:5173/catalog.html (`ui/catalog.html`, `ui/src/catalog/`, D34): real
components on synthetic data in iframes at 1280/390 px, light/dark, 7 languages. It never calls `/api` and never ships: `npm --prefix ui run test:catalog`
fails if it reaches `dist/`, if product code imports it, or if a frame leaks a request. A component or state added to `docs/design/components.md` gets a
state in `ui/src/catalog/sections/` (or a listed gap).

# Web UI layers

One layer scale, in `ui/src/design-tokens.css` (`--z-base` 0, `--z-raised` 1, `--z-edge-blur` 10, `--z-chrome` 20, `--z-pill` 30,
`--z-panel` 40, `--z-popover` 50, `--z-menu` 60, `--z-cover` 70, `--z-dialog` 80, `--z-toast` 90, `--z-tooltip` 100, `--z-drag` 110) and
mirrored as `Z` in `ui/src/layers.ts` (docs/design/tokens.md §9). A z-index is `var(--z-*)`, `calc(var(--z-*) +/- 1)` or, inside a component's own
stacking context, -1..3; TS writes `"var(--z-*)"`, never a number. Overlays portal into `layerRoot()` (`<div id="tofi-layers">` after `#root`,
deliberately not a stacking context), never into `document.body`. `npm --prefix ui run test:layers` fails on any of that (older local values
above 3 are a ceiling in `ui/scripts/layers-baseline.json` that only shrinks) and checks the paint order of overlays stacked on one point.
Note `container-type` does not create a stacking context: on wide screens the chat pane's chrome competes with the sidebar and panels.
