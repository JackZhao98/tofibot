# 从这里开始 / Start here

TOFI 是自托管的 AI Bot 工作台（Go 后端 `internal/`，React 19 网页端 `ui/`）。接手前先读：

1. [docs/README.md](docs/README.md) — 全部文档的索引，标明哪些现行、哪些是历史或已被取代。
2. [docs/product/status.md](docs/product/status.md) — 产品现状、线上版本、owner 的打分标准和评测结果；路线图在 [docs/product/roadmap.md](docs/product/roadmap.md)，每个版本改了什么在 [docs/releases.md](docs/releases.md)。
3. [docs/decisions/](docs/decisions/README.md) — owner 拍板的每一条决定（D01 起），写明时间、理由和影响的代码。
4. [docs/process.md](docs/process.md) — 测试、评测、预览、发版、安全审查和模型分工的流程。
5. [docs/design/](docs/design/README.md) — 设计令牌、组件清单、设计原则和迁移方案；动界面之前读；加新页面、面板或组件按 [docs/design/new-page.md](docs/design/new-page.md) 一步步做。
6. [docs/conventions.md](docs/conventions.md) — Go 与 TypeScript/React 的现行代码约定（错误码而不是消息文字、context 与超时、迁移、日志、测试、前端取数、CSS）；写代码之前读。

规则：**每次合并、发版或 owner 拍板，都在同一个提交里更新对应文档**（决定 D27；哪种事件改哪份文件见 `docs/process.md`
第 10 节）。Every merge, release or owner decision updates the matching docs in the same commit. 本文件仍是总入口，下面各节是代码约定。

**Before pushing to main, merging a branch or tagging, run `scripts/ci-local.sh` (or `make ci-local`; `--all` runs every `ci.yml` job) and push only on exit code 0.**
Every failed workflow run emails the owner. The script runs what `ci.yml` runs, with the same environment (`TOFI_SKIP_TIMING=1`, `SHELL_BUDGET_ONLY=struct`;
`PLAYWRIGHT_MODULE` defaults to tofibot-web's Playwright). Rules and how to write tests that do not flake: `docs/process.md` 3.6.

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

# Computer is optional

A workspace may have no computer. `TOFI_COMPUTER_BACKEND=none|kvm` (unset: `kvm` when a computer socket is configured, else `none`; `cmd/tofi/accounts.go`)
decides it, in single and multi-account mode alike; `none` needs no Worker socket, provisioner or disk quota. `s.microVM == nil` is the server's one test for
it, exposed as `GET /api/computer/capabilities` and `computer` in `GET /api/config` (`{backend, available, capabilities}`, `computer_capabilities.go`). Model-facing text
must not claim a computer when there is none: use the capability-driven variants (`taskCompletionGuidanceFor`, `onboardingSystemPromptFor`, `onboardingWelcomeFor`,
`scheduledFallbackGuidance`, `noComputerStatus`) and never add a computer sentence to an always-on prompt. The Web app reads `config.computer` through
`ui/src/computerCapability.ts` (`useHasComputer`) and hides every computer surface; Settings > Computer then lists how to add one. The web app never installs on the host (the one thing it may ask for is an update, which only the root host service performs: next section).
Design and record: docs/design/computer-optional.md (P0, P1 done).

# Computers are providers and rows (computer-decoupling S5, S6)

`computer.Provider` (`internal/computer/provider.go`) is what the server holds; `*computer.Client` (Firecracker) is the only implementation. Optional interfaces (`Streamer`, `Resourcer`, `OAuthBridge`,
`RunnerHost`, `LegacyBlobs`, `Socketed`, `Healther`, `Heartbeater`) are type-asserted behind a capability. `s.computer()` is the account's VM or nil (`s.microVM` is its storage; write it only through
`AttachVM`/`DetachVM`, or directly in tests); `/api/computers/{id}/` resolves `{id}` through `Provider.ID()` (`isVMRoute`). Computers are rows in `computers` (`computer_rows.go`: id, name, kind `vm|personal`,
platform, region, labels, protocol_version, status `attached|lost|detached`, last_seen; one row is the VM, up to `Config.MaxPersonalComputers` (default 6, env `TOFI_MAX_PERSONAL_COMPUTERS`) are the person's own).
Each computer has a runtime slot (`computer_registry.go`) with its own operator lock (only one Bot at a time; held until the run ends, `releaseComputerHolds`), a call counter and a context. The VM keeps its
shared-desktop lease, keyed by the computer id. Bots reach other computers with `list_computers` and `computer_action{computer_id}` (registered only when a personal computer exists); the convenience tools act on the VM.
Hot attach/detach needs no restart: the administrator calls `POST /api/admin/accounts/{id}/computer/{attach|detach|state}` (gateway) or `/api/admin/computer/*` (single account; never served inside a gateway workspace).
`DetachVM` stops new calls (state draining), waits for running ones up to `wait_seconds`, copies the outbox to the server (`syncOutboxForDetach`), cancels what is left (those calls end with the tool error
`computer_detached`, effect unknown) and stores status `detached`, which a restart keeps until an attach. A computer with a `Heartbeater` that is silent for `computerLostAfter` is marked lost: its lock is freed and calls fail
with `computer_offline`. The administrator's answers contain the VM and the server only (`adminVMSummary`); never put a personal computer, its name or its count into an admin response or log. A run's computer use is recorded in `run_computers`
(run_id, computer_id, nullable region and compute_ms; a table of its own because three more columns on `runs` move the planner off `runs_active`; no UI yet). Tests: `computer_registry_test.go`, `computer_attach_test.go`, `computer_gate_test.go` (the release gate), `computer_upgrade_test.go` (data written by main).
Computer configuration (computer-decoupling S2): the Keys page, `ComputerContents` ("What is on this computer": browser sign-ins, installed tools, work files, environment and keys, in words that say they are lost with the computer) and the wipe list `ComputerLossList` (names and counts from `GET /api/computer/contents`, `internal/app/computer_contents.go`) exist only while `useHasComputer()`; with no computer the web app makes no `/api/computer/*` request (`test:no-computer` holds it). `[run context]` adds `freshComputerStatus` while nothing is saved in Settings for the computer. `tofi computer remove --purge` prints the same class C list (`computer_loss_lines` in `tofi_host.py`).

# Update from the web page

The Version card (Settings > Advanced) and the banner have an **Update to vX** button for admins (decision D69, `docs/decisions/0069-web-update-request.md`). The web app only
REQUESTS: `POST /api/system/update/request {version, channel}` (`internal/app/update_request.go`) checks that the version is the cached latest stable (or the newest rc when the
root-server setting `update_channel_rc` is on, `PUT /api/system/update/settings`), then writes `request.json` atomically (temp file + hard link, so only one can be pending) into
`TOFI_UPDATE_REQUEST_DIR` (`/update/request` in the container = `/var/lib/tofi/update-request`, the only App-writable host directory, uid 10001). `tofi-update-request.path`
(PathExists) starts `tofi-update-request.service` as root, which runs `tofi update --from-request` (`update_from_request` in `deploy/self-host/tofi_host.py`): it reads the file as hostile
input (O_NOFOLLOW, regular file, 4096 bytes, exact keys `version, channel, requested_at, nonce`, release-tag regex, age <= 1 h, nonce seen once), refuses downgrades, an rc tag on `stable`
and the reverse, a stable version other than `releases/latest`, an unpublished release (manifest not fetchable), a version rejected here before and any schema change, then calls the same
`upgrade()` as `tofi update` (wait up to 60 min for Bot runs, backup, digest/sha256 checks, rollback; trigger `web` in `update-history.jsonl`). It writes `status.json` atomically into
`/var/lib/tofi/update-status` (mounted read-only at `/update/status`): `state` queued|waiting_for_runs|downloading|verifying|switching|done|failed|rolled_back, `code`, `progress`, `rolled_back_to`.
The server forwards only whitelisted, validated fields (never `detail`, never the nonce); the UI (`ui/src/UpdateNotice.tsx`, `UpdateDialog.tsx`, lazy) polls every 2 s while active and rides out the
App restarting ("Restarting...", then "Updated to vX / Reload"). The rc switch is a UI-only preference: the host does not read it (a compromised App can ask for a newer published rc, never anything else).
`host.json` in the status directory is the capability marker: until a command of this host version has run once (`tofi start`, `update`, `config`, root `status`, or an install) installs the units
(`ensure_update_request_channel`), the page keeps the old "run sudo tofi update" hint (`host_update_unavailable`). Change the request/status schema, the two units, `tofi.conf` directories and
`compose.yaml` mounts together, with the UI, the stable codes (`update.error.*`, `apiError.update_*` in 7 locales) and `test_web_update.py`. Uninstall removes the units.

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
Springs are tokens too: `SPRING` in `motion.ts` (`push`, critically damped; `test:motion-tokens` fails on a literal
`stiffness`/`damping`/`mass` elsewhere). The phone swipe-back (D35) is `phoneSwipeGesture.ts` (gesture core, rules in
`SWIPE`) + `phoneSwipeEngine.ts` (the release, a spring with the finger's velocity baked into WAAPI keyframes), a lazy chunk
loaded by `usePhoneSwipeBack.ts` on a phone-sized window only; it moves the panes by transform and commits through the same
`setMobileList(true)` as the back button (flushSync, `data-swipe=done` keeps CSS transitions from replaying). `test:swipe-back`
(also `SWIPE_BACK_ENGINE=webkit`) holds it. Motion is not a dependency of the app (step 4 measured it and kept our code,
D35); `motion` is a devDependency only for `test:motion-experiment` and `TOFI_MOTION_EXPERIMENT=motion` in the dev server.

# Process line

What a Bot is doing right now is one quiet line, never a block: `LiveMark` (14px ink ring, a lagoon stroke turning on `transform`, static under reduced motion), the status, the seconds, a chevron. It is a real button (`aria-expanded`, focus ring) that opens the tool steps through the mechanism already there: the task block's `<details>` (`ActivityDisclosure`, `viewActivity` when Activity is hidden) or the web tool row's drawer (`ActivityViewContext.reveal` from `LiveToolLine`). Look in `ui/src/live-line.css`: `font: var(--type-meta)`, `color: var(--text-process)`, one line with an ellipsis (the opened view carries the whole text), no cat, no mono or semibold on the line. A new live line uses `.process-line` and `LiveMark` instead of its own style; the cat stays only on the composer perch and in the sender column. The opened Activity list is compact (components.md section 1): state icons (`ToolStatusIcon`) instead of words, time only from 1 s, runs of identical finished calls folded by `foldToolRuns`; the drawer closes with `--m-panel-out` and animates padding and border to zero with the height (`animateDisclosure`). States: catalog section 1 (`ui/src/catalog/sections/process.tsx`). A folded row shows its count as a small muted pill (`.task-step-count`), then the chevron in its own slot, and the time at the right edge like every row.

Activity is one linear timeline with the rest of the chat. `placeOwners` (`ui/src/activityPlacements.ts`) cuts a run's calls with `splitActivitySegments` (`toolTimeline.ts`) wherever something else is drawn between two of them (a person's message, another run's ticket or notice, the Bot's own say or progress note); each stretch is its own `TaskRunBlock` (`segment` prop) under the last drawn message before its first call, in time order. The first stretch keeps the owner's anchor and the earlier attempts; only the last one (`primary`) carries the live line and clock, the issue card, pending decisions and drafts (the card stays last while it waits). A run nothing interrupts is one block with no `segment`. A message a task block follows directly gets `data-owns-next`: its hover bar floats and the block sits at the normal item gap. Test: `test:timeline` (a message between two calls gives two segments in order; a 600-call run is cut in a few ms); states `activity-segmented*` in the catalog.

# View context

A reply's "View activity" ends with a "Context" row (D53): what that run's first model call received, part by part. `execute` sets `r.ctxCapture` (`context_snapshot.go`); `buildContextPartsWith` reports the history window and note pieces to it, `assembleRunSystemSegments` (`prompt_budget.go`, concatenates to the exact system bytes) supplies the system parts, and `recordRunContext` adds the tool list and stores one gzip-JSON row per run in `run_context_snapshots` (first call only; newest 50 per conversation; <= 256 KB before compression, largest texts dropped first; `redactContextText` replaces credential-shaped values). Part kinds are open strings: a new block in `buildContextPartsWith` needs one row in `contextMarkerKinds` (else it shows as `other`) and a `chat:runContext.kind.<kind>` string in 7 locales. The capture never changes a prompt byte (`TestContextCaptureDoesNotChangePromptBytes`, `TestAssembleRunSystemSegmentsKeepPromptBytes`). `GET /api/runs/{id}/context` sits behind the owner session gate; no snapshot is 404 `context_snapshot_unavailable`. UI: `RunContext.tsx` is a lazy chunk (`RunContextEntry` in `App.tsx` is the only entry code); catalog states `context-*` in `catalog/sections/process.tsx`. Web only; `DesktopToolActivityRun` has no Context row.

## Conversation usage

`GET /api/conversations/{id}/usage` (`internal/app/conversation_usage.go`, same owner session gate as `/tools`; unknown conversation is 404 `not_found`) reads existing rows only; nothing new is stored. `totals.input_tokens/output_tokens` are summed from `run_usage` (no expiry; includes reviewer calls). `requests`, `equivalent_usd` (`usageEquivalent`: the Codex reference table with cached input at 0.1x, otherwise the `provider.Registry` price by prefix so dated Claude and OpenAI API IDs are priced, cache reads/writes at their own rates; plus web-search fees, $0.01 each, web fetch free) and `unpriced_requests` come from `usage_calls`, which `pruneUsage` keeps seven days, so they cover only runs whose per-call rows add up to the run totals (`runs_with_call_detail` vs `runs`). `latest_context` is the newest `run_usage` row of the conversation: `used_tokens` is the provider-reported input of its last call (`used_measured:true`), else the pre-call estimate; `window_tokens` is the stored window (`provider.GetContextWindow`: the window the Codex `/models` catalog reported (`context_window`, else `max_context_window`; `publishCatalogWindows` -> `provider.SetCatalogContextWindows`, also warmed from the stored catalog at start) outranks the `Registry` row, which outranks the 128k heuristic; `window_known:false` means heuristic and the UI shows it as estimated; a stored heuristic window is re-read live in `latestContext`), `cache_read_tokens` / `cache_hit_rate` are the last call's prompt-cache reads over its input (`run_usage.last_cache_*`; `totals.*` use `total_cache_read` over `total_cache_input`, the input of calls whose provider sent cache fields at all, `provider.Usage.CacheReported`; the rate is `null` when none did, never 0; Responses/Chat Completions read `cached_tokens`, Anthropic its cache fields); `parts` regroup that run's snapshot (first call only, on-demand parts excluded) into `instructions, history, tool_definitions, tool_results, memory_notes, attachments, other`, all estimates (`agent.EstimateTextTokens`; images are the flat `contextImageTokens` taken out of their history message into `attachments`; text attachments stay inside `history`). `usageGroupOf` maps kinds, and an unknown kind lands in `other`. A missing snapshot gives empty `parts` with `parts_code: context_snapshot_unavailable`. `tool_split` classifies each chat model call by whether the run started a tool (`tool_activities.started_at`) between that call and the next one (usage is recorded before tools run); reviewer calls are `review_tokens`, in neither side. It is `available:false` with `code` `no_usage` or `call_detail_incomplete` rather than a guess. Tool-result tokens that arrive mid-run are not in the snapshot (first call only), and per-call input repeats history, so the split says where calls were spent, not what text cost.

# Search palette

The sidebar has no search field: a magnifier button (`.search-trigger`, in the entry) and Cmd/Ctrl+K open `ui/src/SearchPalette.tsx`, a lazy chunk (`searchPalette` in `lazyPanels.tsx`, warmed first in idle time and on pointer-over of the button) on `ModalDialog` in `#tofi-layers` at `--z-dialog`. All its state is local, so typing never renders the workspace. Chats are filtered from the loaded list; messages come from `GET /api/search?q=&kind=all|chats|messages&limit=` (`internal/app/search.go`, owner session gate like the other conversation routes), debounced 150 ms with the older request aborted. Enter on a message calls `openMessage` (the `taskFocus` jump; `keepJump` in `App.tsx` holds it through the first 900 ms of a conversation opening, and the flash is the `data-jump-target` attribute, not a class, because React rewrites the block's className). No `backdrop-filter` on its scrim: it made each key 72 ms to paint instead of 32 ms.

Index: `messages_fts`, a standalone FTS5 table with the `trigram` tokenizer (case-insensitive substring match; Chinese, Japanese and Korean match with no segmentation), keyed by message id through `search_message_ids` and kept by three triggers (`migrateSearch`, additive and idempotent; refilled when new, when an older external-content index is found, or when its row count drifts from `messages`). It is deliberately not an external-content table on `messages.rowid`: that rowid is unstable (TEXT primary key, VACUUM) and a purge that emptied the shadow tables corrupted the file. `purgeWorkspaceData` skips `messages_fts_*` shadow tables. A query under three characters has no trigram and runs as a bounded `LIKE` (newest rows first, stops at the limit). The index holds every message; visibility is decided at query time: unarchived, `user_visible` conversations, no `bot_result`, `scheduled_task` (including legacy ones), `internal`, `progress`, `notice` or `message_ref` rows. Hits are newest first; `match_start`/`match_len` are UTF-16 offsets into `snippet`. The index is about three times the message text on disk. Tests: `search_test.go`, `npm --prefix ui run test:search-palette`, catalog section "Sidebar" states `search-*`.

# Sidebar order and pins

Two per-account integers on `conversations` (added by `ensureColumn`): `pin_order` (1-based pinned slot, 0 = not pinned) and `sort_order`
(1-based manual slot, 0 = never placed by hand). `PUT /api/conversations/order {pinned, order}` (`internal/app/sidebar_order.go`) replaces both
atomically and rejects duplicates, unknown ids, ids in both lists and archived pins; archiving a Bot or group clears its pin. The list is recency
until the person reorders; after that their order wins, and a conversation the server has not numbered (new since the last reorder) enters at the
top, newest first. Exports (`portable*`) deliberately carry neither field. In the UI, `sidebarOrder.ts` is the pure ordering and `sidebarDndEngine.ts`
the drag/keyboard/long-press engine (its own chunk, loaded in idle time by `useSidebarDnd.ts` to hold the shell budget): it moves elements with transforms from rects measured once at pickup, and React renders once, on drop. The pinned area is the drop target: `.pin-frame` wraps the dock (empty when nothing is pinned) as the first block of the list, under the search bar, no sticky or positioned layer over it; a row drag shows its dashed outline and hint, moving the rows down (and takes the list to the top), and `glideFrom` slides every visible child from where it was seen; the settle must clear transitions (`transition:none`) before it reads the landing rect, or the rect carries the lifted pose. The dragged and landing element sit on `--z-drag`. Do not
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

A person's own message supersedes an open card: `addUserRunsSteering` (and `steerTriagedMessage`) call `supersedeRunQuestionsTx` (`steer.go`) in the same
transaction that queues the message for the parked or working run. Every pending card of that run (approval, MCP call approval, question, form; not takeover or
connection) goes to `cancelled` with `answered_by='superseded'` (card field `superseded`), through the same single `UPDATE ... WHERE status='pending'` as the other
cancel path, so an Approve that committed first stands and a superseded card can never be approved. The run resumes at once and reads `supersededResult`
(`executed:false`), then the message at its next boundary. Schedule runs and runs under a schedule are left alone; other Bots' messages never come through this path.

Bot secrets (`internal/app/bot_secrets.go`, design `docs/design/bot-secrets.md`): the person, never the Bot, may tick "Save for this Bot" on a
`request_secret_input` card (`save_as` in the answer, `AnswerUserFormSaving`); the record is vault kind `bot_env` scoped by `BotID`, never listed
under Settings credentials. A Bot uses one only by naming it in `computer_shell` `secrets`: the host adds it to that one command's environment at
dispatch (guest `shell.exec` `env`), masks it in the command's output and in later `computer_*` results of the run, and a Bot-scoped secret beats a
global one of the same name. No Bot tool reads, lists values of, or deletes a secret; management is owner-only `/api/bots/{id}/secrets`.

Two Bot-initiated handover cards ride the same rendezvous (`internal/app/handover.go`, UI `ui/src/HandoverCards.tsx`,
`ui/src/TakeoverDialog.tsx`); they add question types `takeover` and `connection` and one stored column (`questions.handover_target`, added by
`ensureColumn`). Their answer is one enumerated word (takeover: `done`/`cannot`; connection: `connected`/`failed`/`declined`) and the model result
(`handoverResult`) is a status word plus a fixed sentence: nothing the person types, copies or sees on the remote screen is part of an answer,
event, result or table (the keystroke/clipboard path stays browser -> `/api/computers/firecracker/actions` -> guest). `request_takeover` (needs the
shared computer; site is stored as an origin only; window <= 14 min because the guest collects an idle desktop after 15) opens a modal around
`RemoteDesktopControl`, which acquires with `handover_question`: that hold is bound to the open card, is never yielded by the 15 s idle release
(`desktop.control.renew`) and ends on the next heartbeat or on answer/cancel/expiry/run start; the run's own desktop tabs are kept while it is parked
on the card. `request_connection` runs the Settings connect flow for this client (native app, https popup, or the shared computer on plain http,
via `mcpOAuthRoute`; a loopback-only service, below, signs in by paste-back instead); `connected` is accepted only if the server sees the token (`connection_not_ready` otherwise); a blocked route reports
`failed`, a retryable error keeps the card open. Both cards are refused in hidden conversations (`requireHumanPresent`) and, like
`ask_user_question`, park and expire in scheduled runs. `manage_extensions oauth_required` points at `request_connection` only in
user-visible conversations.

# Tasks and waits

A delegation is a task and a wait (decision 0059, `internal/app/tasks*.go`, `waits.go`, `bot_inbox.go`, `wait_sweeper.go`, `task_*.go`). A task is a `work_items` row
with task columns (`assigner_kind/assigner_bot_id`, `root_task_id/parent_task_id`, `origin_run_id/assignee_run_id`, `spec_version`, result fields, `closed_at`);
statuses add `input_required` and `failed`. `recordTaskForChildTx` makes it inside the transaction that creates the child run (`AddHandoff`,
`AddForwardHandoff`, `AddBotMessage(s)`), with the `task_result` wait the assigner holds; the existing return run still delivers the result, so that wait
settles in silence (`delivery='legacy'`). Run hooks keep the task true: `SetRunStatus` (start, failed, cancelled), `finishRunOpts` (result, `done`/`review`/`blocked`),
restart recovery, and the sweeper's `reconcileTaskRuns` for paths that miss them. A retry run takes over the task; a run a newer message took over passes it on.
`task_checklist`: the holder sets an item's status and reason (every ended item has a reason). Editing (owner 2026-10-24, `task_edit.go`): the holder and the giver may both change the request (`spec_version` +1), add, remove, rewrite (`rewrite_items`) and reorder (`order`) items, change dependencies, and merge (`absorb_task_id`); only the giver answers, accepts or cancels, only the holder reports status and item results. Every such edit needs `reason` (`edit_reason_required`), is logged in `task_edits` (task, version, actor, role, kinds, reason, detail; `task_revisions` gains `reason`) and is told to the other side through a non-waking inbox row (a changed request from the giver still wakes the holder; the giver gets the holder's edits, none when the person gave the task). A blocked item ended as skipped also tells the giver (`blocked_to_skipped`). Idle-budget anti-gaming: progress marks are keyed by `task_checklist.mark`; an item the holder adds has a `h:` mark and never counts as progress, a rewrite keeps the item's mark, a giver-added item counts once. Merge (`mergeTasksTx`): same tree only (`merge_other_tree`), refuses self, the tree root and an ancestor; checklists join (24 max), the progress marks of absorbed items carry over, tasks that depended on the absorbed task depend on the survivor, children and pending `task_result` waits move, the absorbed task closes as `cancelled` with `work_items.merged_into` and a pointer in `result_summary`, everyone it touches is told.
`say(outcome=done)` with an `open` item goes back once, in the run's one send-back shared with the `pending` check (`state.pendingCheck`).

`waits` is the one table for waiting: kinds `bot_post`, `task_result`, `human_answer` (a mirror of a question card; the card keeps its own resume), `command_exit` (decision 0061), `timer`; groups `any`/`all`; a group settles once (`settleGroupTx`) into one `bot_inbox` row. `recordWaitTx` runs the cycle check in the same
short transaction (walk <= 64 steps, code `wait_cycle`, a graph too large to walk is refused) and the limits (16 open per Bot, `wait_chain_limit` after 24 self-wakes in a row).
A timeout is a result: `timed_out` (a timer is `fulfilled`). Per-kind defaults are `waitTimeoutFor` (15 min / 2 h / 30 min). There is no in-memory timer: one sweeper
(`sweepWaits`, 15 s, also at start) ends due waits, reconciles question mirrors, and calls `flushInbox`. Tests call `sweepWaits` directly; do not add `time.AfterFunc`.
`flushInbox` wakes a Bot with a new `group_followup` run whose `run_wakes.source` is `inbox` (the rows are the request, block `[wait result]`); a single `bot_post` wait in a
group keeps the old mention wake (`via='mention'`). A Bot's own run reads unread rows as `[task updates]` (`consumeInboxForRun`, idempotent per run) when it starts, and at each boundary while it runs
(`drainInboxForRun`, decision 0062, section "Per-Bot lanes and queued steering"); a Bot that is not running is woken by a new run. `outcome=pending` is honest only while the run holds a pending wait (`runHasPendingWait`).

Visible group delegation (decision 0065, owner 2026-10-25, `group_visible_delegation.go`): when the conversation is a user-visible group and every recipient is a current member, a `delegate` (or `handoff`/`message` alias) call posts ONE ordinary message (`kind=''`, sender = the giver, `run_id` = the giver's run, `client_message_id` = `delegation:<id>`) with the text `@<name> [@<name>...] <brief>`; the recipients' `group_task` runs live in the group and have that message as `trigger_message_id` (`addHandoff` for one, `AddGroupDelegations` for several; second and dependent tasks post the message through `publishDelegationMessage` and start through the inbox). The task keeps the brief without mentions. `notDelegationSQL` keeps the message out of `hasPostedMessage`, the silent check, run audit and the last-message fallback; `isVisibleDelegation` lets the recipient's `[current handoff task]` block read it (and history skips it); `validateMCPScheduleEdges` accepts it as an assignment anchor. DM, other-group and non-member recipients and hidden-trace groups keep the notice/forward/hidden-trace paths; the notice rendering stays for old data and those paths.

Tools: `delegate` (one member = handoff path, several = message path; `title`, `checklist`), `wait_for` (receipt at once), `cancel_wait`, `task_update`, `task_ask`, `say` with
`task_id`. `handoff` and `message` are deferred aliases for one version. `cancel_wait`, `task_update`, `task_ask` are on demand (`onDemandTools`): the `[waiting on]`,
`[task record]` and question texts say how to load them. Tool and block texts are English principles in Simplified Technical English with no examples.
A person's message to a member that holds a task bumps `spec_version` and stores a `task_update` row for both sides (`noteUserMessageForTasks`); a message queued as steering (ingress, triage steer, failed-triage fallback) does the same for the task the target run holds, whoever gave it, in the transaction of the delivery (`noteUserMessageForBotTasksTx`), and the run's `run_spec_version` follows when it reads the message (`runReadsCurrentVersionTx`), so `result_based_on_version` is true; results carry
`result_based_on_version`. Context blocks `[waiting on]` (<= 1600 runes), `[task record]`, `[task updates]`, `[wait result]` each have a row in `contextMarkerKinds` and a
`chat:runContext.kind.*` string in 7 locales. Stop (`CancelRunTree`), Bot delete (inside `deleteWorkspaceTargetWith`) and group member removal (`UpdateGroup`) end tasks,
waits and the runs below them by `parent_task_id`/`origin_run_id`/`assignee_run_id`; `GET /api/bots/{id}/task-impact?group_id=` feeds the confirmation count.
Retention (`task_retention_days`, default 30, 0 = keep) clears the request text, checklist and revisions of finished delegated tasks, ended waits and read inbox rows; it never
touches messages, attachments or runs, so the files a failed run kept (decision 0038) stay. Messages a run sends after it registered a wait count against their own
allowance of three (`chatSegmentCountTx`, decision 0050 unchanged before that). The new tables are not exported (`portable*`).

Claim and pass (decision 0060 part C, `internal/app/task_claim.go`). Group triage (`AddHandoff` with sender `system`) makes a task in the same transaction as the answering run: holder = the chosen member, giver = the person (`assigner_kind='user'`, no wait, no inbox), `assignee_run_id` = that run, so a one-turn answer closes it as `done` with no tool call. `claim_task` (holder only, idempotent; writes `task_claims`, moves `todo` to `in_progress`, posts nothing) and `pass_task` (holder only, reason required, target an active member of the same group, at most 4 passes per task via `task_passes`; one transaction: new holder, status `todo`, claim cleared, `assignee_run_id` = a new queued `group_chat` run for the new holder, a non-waking inbox row with the reason; giver, waits, tree and checklist unchanged) are group-only on-demand tools (`onDemandTools`). `WorkItem.claimed_at` is read from `task_claims` in `decorateTaskTx`. The group prompt carries `groupTriageGuidance` (English, STE, principles only); the triage prompt lists up to 12 unfinished group tasks with holders (`openTasksForTriage`). `workItemEventTx` also sends the `work_item` event to `work_conversation_id`, so a task delegated to a Bot's chat draws its card there. `GET /api/conversations/{id}/task-cards` feeds cards and the member list. Hidden small talk (owner 2026-10-24): triage's task is created with `work_items.hidden=1` (data kept). A `hidden=1` task sends no `work_item` event, and `task-cards` and `ListWorkItems` (TeamBoard, WorkPanel, `list_work_items`) leave it out. `promoteTaskVisibleTx` is the one place that shows a task (`hidden=0`, then the card event): a checklist item is added (`addChecklistItemsAsTx`), the holder's run delegates or registers any wait (`recordWaitTx` via `promoteRunTasksTx`), a claim or pass, the first round ends with the task open (`taskFinishedOneTx`), or a second run works on it (retry, return run, steered successor, `say(task_id)` from another run). A one-turn answer that closes the task stays hidden.

Dependencies, giver inbox, stall and tree budget (decision 0064; `task_deps.go`, `task_stall.go`, `task_tree_budget.go`). `delegate` takes `depends_on[]` (task ids of one tree, kept in `work_items.depends_on`): a task with an open dependency is `blocked` with no run (`AddDependentTasks`); `settleClosedTaskTx` is the one place that reacts when a task closes: `done` unlocks each dependent whose dependencies are all done (`unlockTaskTx`: status `todo` plus one waking `task_update` row, same transaction), `failed`/`cancelled` tells the giver and leaves the dependent blocked. Only a run woken from the unlock row (`wakeBotFromInbox` calls `bindUnlockedTasksTx`, one unlocked task per run via `onlyFirstUnlockRow`) becomes the task's `assignee_run_id`; boundary and context reads skip unlock rows (`unlockRowSQL`). `dependencyCycleTx` walks the stored lists (<= 64 steps, `dependency_cycle`, an unwalkable graph is refused); `task_update` takes `add_depends_on`/`remove_depends_on` (either side, with a reason) for a blocked task that has not started. A second single-recipient `delegate` in one run, and any `delegate` with `depends_on`, does not use the run's one direct hand-over: the task starts through the inbox, so independent parts run in parallel. Every visible message a holder posts for a task another member gave (`routeHolderMessageTx`, called where `publishSay` and `publishAssistantPartsOpts` store a `segment`) is a `progress` inbox row of the giver with `wakes=0`; asks, results and stall reports wake; the holder's result marks that run's progress rows read (`supersedeProgressTx`). Stall: each round that ends with the task open compares `progressSigTx` (checklist states plus blocked/input_required) with `work_items.stall_sig`; `stall_count` reaching `task_stall_rounds` (default 3) reports once to the root member (`reportStallTx`); a changed signature resets it. Tree budget (owner amendment 2026-10-24: it guards against spinning, not real work): `task_tree_budget` per root task (or `run:<root run>` outside any task) counts Bot-to-Bot messages WITHOUT progress; `chargeTreeTx` counts hand-overs, mention wakes, waits on posts/results and ask/update messages (holder progress is not charged; the giver's answer to a question always passes). Progress anywhere in the tree resets the count and `reported` to 0 (`resetTreeIdleTx`): a checklist item that ends in `task_update`, a task of the tree that closes done (`settleClosedTaskTx`), a result submitted (`taskFinishedOneTx`, status done or review). Repeatable progress counts once per mark in `task_tree_progress` (`progressOnceTx`): an item the first time it ends, a result the first time per `spec_version`; reopening/re-ending or resubmitting does not reset. `treeKeyForRunTx` is the one resolver of a run's tree (inherited `run_tree.tree_key` from the waking inbox rows, assignee, origin) and every inbox row stores `tree_key`; parts handed out through the inbox share one wait group (the giver hears once); `task_tree_idle_message_limit` (default 30, 10-500; the old `task_tree_message_limit` value is migrated by `migrateAutomationSettings`) refuses new member-to-member dispatch with `task_message_limit` until progress resets it, and the count reaching the limit wakes the root member once per idle stretch with a private inbox row carrying a system-data block (`reportTreeLimitTx`; `afterTreeLimit` files it for a refused, rolled-back transaction). Nothing is posted to the chat: the `system` notice `tree_limit` no longer exists; the person hears only what the root Bot chooses to say. It replaced `maxHandoffRounds` and `maxMentionWakesPerLineage`; the `wake at most 3` fan-out check stays. Both numbers are settings read through `autoIntTx` inside store transactions.

Command-exit waits (decision 0061, `internal/app/command_exit.go`). `wait_for{kind:command_exit,target:<terminal id>}` or `computer_terminal` `notify_on_exit` on `open`/`write` (registered after
the action succeeded; a refusal is reported inside the result, never as a failed call). `registerCommandExit` asks the guest first, then in one transaction records the wait, the paired timer (mode `any`; the
Bot's own timer in the group counts, else one of `command_check_minutes`, default 60) and the cache row in `command_waits`. One monitor loop (`startCommandMonitor`, 5 s, `pollCommandExits`) reads pending
`command_exit` waits and calls guest `terminal.list` (source `viewer`) only for Bots that hold one. Process exit gives the exit code; an interactive shell ends when `foreground_job` returns to false after it
was seen true (no exit code); a guest without the field reports process exit only; a terminal missing from a successful list is `interrupted` (restart) and delivers the cached 8 KB tail. A failed or
unreachable list is never "gone". `finishCommandWait` ends the wait through `resolveWaitTx` (pending-only, so once) and writes one `command_exit` notice line (sender `system`, not in model history).
`terminal_cleanup.go` skips a Bot that holds a pending command wait. `tofi_host.py` counts pending command waits as busy (`command_waits`, read-only SQLite like `active_runs`); Settings asks
`GET /api/command-waits` before a computer restart. A guest change needs a rebuilt guest image; `foreground_job` is a field of the existing `terminal.list`, so `microVMActions` and the watchdog list did not change.
# Per-Bot lanes and queued steering

One Bot runs one run at a time, across every conversation it is in; different Bots run in parallel (decision 0062, `internal/app/bot_lanes.go`, `steer.go`). The conversation worker
(`runConversationWorker`) still looks at one conversation's queue, but `pickRunnable` returns the first run whose Bot has a free lane and `launchRun` starts it in its own goroutine
under that lane (`Server.lanes`: Bot id to run id in memory, plus a database recheck of `status='running'` for the same Bot; a claimed approval settlement does not count).
`triage` runs take no lane. A freed lane wakes every worker; a worker with something waiting on a lane or an order also rechecks every second. Heads of different conversations compete
by `laneRank`: same task tree as the run that just left the lane > a person's message > a delegation > a schedule (approval settlement first); inside one conversation the older order
stays. `runOrderBlocked` keeps two orders the serial worker gave for free: a run waits while the run that created it is running or queued, and a Bot addressed with others by one
message waits for the ones addressed before it. Tests that call `execute` directly bypass lanes: do not add code that relies on `launchRun` for correctness of one run.

A person's message to a Bot that is working is not a new run and cancels nothing. `addUserRunsSteering` (the ingress transaction) asks `steerTargetTx` for the Bot's running run in this
conversation (or in work whose origin is this conversation), or its run parked on a question card; one that already recorded its `say(answer)`, one that yields to a steering successor, an
approval settlement and a scheduled run are not targets (a message during a scheduled run starts a run of its own and the scheduled run yields to it, as before). The message is stored as always; a `bot_inbox` row (`kind=user_message`, `parent_run`, `anchor`=message id) says where it went, and the response carries
`queued`. `Message.delivery` is `queued` for unread rows (history never lists `read`); reading emits the stream event `inbox {message_id, run_id, state}`. The run reads at the boundary before each
model request: `Request.DrainInbox` (`agent.AgentConfig.DrainInbox`) calls `drainInboxForRun` (one transaction: marks the rows read, `drained=1`, emits the event), the agent appends one user
turn (`renderInjection`: `[new message from the person]` with the principle, or `[events from the task system]`), checkpoints it and refills the run's time, call, step and cost budget once
(`Continuation.BudgetCallsBase/StepBase/CostBase`). Every read counts toward `maxInjectionsPerRun` (5), and none happens past `injectContextShare` (60%) of the context window or within `minTurnsForReading` (100) of the assistant-turn limit (a refill is also cut to fit under it, `refilledStepLimit`); only a person's message refills the budget (`DrainInbox` returns `refill`). Past those limits the next unread person message
closes the run through `closeRunForContinuation` (a continuation run plus the old steering edge, then `steeringBoundary` yields). Other inbox rows of the Bot in this conversation (settled waits,
a member's question, task updates; not `via=mention`, not rows waiting for a still-active run) are read the same way. Messages still unread when a run ends become one continuation run in the
same transaction (`continueUnreadInboxTx`, from `finishRunOpts`, `SetRunStatus`, recovery and `MarkRunSteered`); Stop all drops them. A read that a restart lost before its checkpoint is given
back (`returnUndeliveredDrainsTx`, in `claimRun`). While a question card is open the card stays and the message waits for the run that resumes (`supersedeInputWaitsTx` skips a parked run with an
unread message). The `run_steering` edge and `steeringBoundary` remain for delegation, mention wakes and the cap; a person's message makes no edge any more. Delegation returns are still queued
return runs (`group_followup`), not inbox reads.

Two stop levels: `POST /api/runs/{id}/interrupt-step` cancels only the tool call in flight (`runtime.Request.BeginStep`, per-call context, typed outcome `step_interrupted`; 409 `no_step_in_flight`
when only the model is running) and the run goes on; `POST /api/runs/{id}/cancel` is Stop all (`CancelRunTree`, tasks and waits too). The composer is always enabled; its Stop button is in the entry (Stop all by itself when the menu chunk is not loaded) and opens the lazy two-item
`ComposerStopMenu` (`ui/src/ComposerStopMenu.tsx`; the step item only for a running run with a tool call in flight); the queued/read note is `DeliveryNote`, applied from the `inbox` event by `applyInbox` (`ui/src/messageDelivery.ts`).

# Security gates

`manage_extensions` changes (MCP create/update/delete/test, Skill `skill_publish_version`) execute only after a person's
approval of the exact payload (`internal/app/extension_gate.go`: request_approval target = digest token, one use) and are refused in unattended
runs. A Bot installing a skill for itself (`skill_install`) or turning a library skill on or off for itself (`skill_enable`/`skill_disable`) touches no
other Bot and needs no approval (decision 0058); install is still refused when no person can see the run, enable and disable are allowed there. The
approval card carries server-built `approval.facts` (kind, name, address with masked query, transport, tool lists, header names, skill manifest
summary, and for a published version the new version number, files added/removed/changed against the current files and how many Bots follow it)
rendered above the Bot's own wording; the Bot cannot set them and a token the server has not seen in the run gets no card. `skillPublishGuard` refuses,
before any card, a publish by a Bot that did not install the skill, for a missing skill, an invalid file set or files equal to the current version.
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
(`standingClaimValid`), so Revoke (Settings > Permissions, `/api/standing-allowances`) bites an in-progress run at its next call (attended: the
card returns to a person; unattended: it is closed and the call refused `mcp_needs_human_unattended`). Each use bumps `use_count`/`last_used_at` and
writes `mcp_standing_allowance_uses` (null `call_id` when the runtime gave none) inside the claim transaction, and links the tool activity
(`ToolActivity.allowed_by`). Archiving/deleting the Bot or the server (`deleteMCPServer`: use it, not `extensions.DeleteMCP`) revokes the grants
(`revoked_by` `bot_archived` | `bot_deleted` | `server_removed`) and keeps the rows and uses.
Local MCP Runner (decision 0071): in multi-account self-host it is the `runner` container (profile `runner`), not a process in the guest. Each account has a namespace
`/a/<account id>`, a token derived from the master key (`mcprunner.AccountToken`), a uid and private directories; `localRunnerConfig` uses it when the account has no computer or its
guest Runner state has been copied and verified (`local_runner_container.go`, state file `local-runner.json`). Never route a request to another account's namespace, never give a
workspace the master key, and keep `internal/mcprunner/hub_test.go` cross-account refusals passing. Root-side file work inside an account (below `plugins/<id>` or `tmp/`, which the account's uid owns) must go through `os.Root` (`confined.go`), never `os.ReadFile`/`os.RemoveAll`/path `chown` on a path the account can swap; the directory owner, not `uids.json`, is the authority for an account's uid. The guest Runner code stays only as the source of that copy.
Community connections (decision 0047): TOFI curates official connectors only; a community MCP is installed by the person under Settings > Connections > Add connection > Custom > Local package
(npm or PyPI, pinned version, optional npm `sha512-` digest verified after install). Its secrets are 0600 files in the Runner, never in a response, status or manifest.
`attachLocalMCP` writes `provenance` (origin `community`, kind, package, version) from the Runner's status only; it is part of the configuration fingerprint, so a
different package or version voids standing allowances. For a community server the provider's `readOnlyHint` is shown but not obeyed (`communityServer` in
`resolveMCPToolTier`): its tools are reviewed as writes until the owner marks them read-only. Installed Skills with scripts are copied to the Bot's computer on
`read_skill` like built-in ones, so prefer MCP for anything holding a credential. Pins and recipes live in `integrations/`.
Session cookies are Secure when `TOFI_PUBLIC_ORIGIN` is https; Caddy sends HSTS. Known open: `computer_shell` shares a uid and loopback with the
shared Chrome DevTools port.

# Web search and fetch

`web_search` and `web_fetch` are server-run tools (decision 0068) for runs whose model has no native search: an Anthropic model with the "Web search" automation switch on gets the provider's own tools and neither of these registers (`webNativeWins`, `web_search.go`). `internal/webtools` holds the providers (Tavily, Brave, SearXNG, the built-in fetch, Firecrawl, Bright Data) and the two chains; `internal/app/web_tools.go` holds the order (one stored `automation_settings` value, key `web_tools`: orders, switches, SearXNG address; D15), the keys, tool registration and the Settings API (`/api/web-tools`). A chain moves on by stable `webtools.Code` only (`auth_failed`, `quota_exceeded`, `blocked`, `empty_content`, `needs_javascript`, `network`, `not_configured`, `unsupported_content`); `target_refused`, `bad_request` and `not_found` end it, so a private address is never handed to a third-party fetcher or the Bot computer. Never put a vendor's message text in an error, log or response. Keys are vault records of kind `web_provider` (id `webtool:<provider>`, excluded from `/api/secrets` like `modelProviderKind`; Bright Data stores `key\x00zone`); the vault record's `Error` holds only a code. `web_search` is registered only when a non-native source is usable (never pretend to search); `web_fetch` always has the built-in fetch. The built-in fetch uses `extensions.GuardedTransport` (dial-time address control, no env proxy; `BlockedAddr` also refuses the host's own interface addresses and the `TOFI_PUBLIC_ORIGIN` host, refreshed each minute), `ValidateTarget` before every hop (`CheckRedirect`, max 5), ports 80/443/8080/8443 only, 20 s, 2 MiB, text and HTML only; `ParseHTML` has node, output and context budgets (keep every loop in it linear). The Bot computer fetch is not behind the dial guard: `webtools.CheckResolved` re-resolves the host before navigating and refuses any blocked answer, which narrows but does not close the DNS-rebinding window and cannot see redirects the browser follows. Paid sources have a rolling 24 h cap each (default 300 searches / 300 fetches, table `web_tool_calls`, adjustable in Settings); at the cap the chain falls to the free sources. Only vendor credential errors become `auth_failed` (Bright Data: 407 only; its 401/403/429 are the target site, so `blocked`). A tool result starts with `{"source":"<id>",…}`; `toolSourceLabel` shows it in Activity. The Bot computer source needs the browser capability (`hasComputerCapability(CapBrowser)`) and `computerAdmission()` (`webComputerExists`/`webComputerOn`). Tool descriptions follow the tools text rules: English, STE, no examples. Settings UI: `ui/src/WebToolsSettings.tsx` (`test:web-tools`).

# Editing a schedule's timing

`PATCH /api/schedules/{id}` and the Bot tool `update_schedule` accept optional `kind`, `run_at`, `interval_seconds`, `daily_time`, `weekdays`, `timezone`
(`SchedulePatch`, `internal/app/schedule_edit.go`). They go through `planScheduleTiming`, which merges the patch onto the stored schedule and calls the
creation validation and next-time calculation (`validateScheduleSpec`); there is no second calculator. A change recomputes `next_at_utc` from now; a
restated timing changes nothing. Unset fields keep their stored value unless `kind` changes, which starts from the fields the new kind needs
(`daily` <-> `weekly` keep the clock). Failures are `*ScheduleError` with stable codes (`schedule_kind_invalid`, `schedule_timezone_invalid`,
`schedule_run_at_required|invalid|past`, `schedule_interval_invalid`, `schedule_time_invalid`, `schedule_weekdays_invalid`, `schedule_timing_conflict`,
`schedule_timing_locked` = 409); create returns the same codes. It never touches a claimed or running occurrence (runs, `schedule_occurrences`
and the occurrence authorization snapshot are separate rows), leaves a paused schedule paused, and refuses a one-time schedule that was claimed or
completed. `expected_timing` is the opt-in baseline (a mismatch is `edit_conflict`, like `expected`). A timing edit is journaled as a `content_edit`
authorization revision from the same source kinds (form or native chat), so whoever may edit content may edit timing; `verifyMCPScheduleSources` therefore
lets timing differ between revisions (identity and the original anchor must still match) and shows each revision's timing to the reviewer. The UI is
`ScheduleEditor.tsx` using `ScheduleTimingFields.tsx`, the same controls as the create form; `test:schedule-timing` holds its patch building.

# Web timeline performance

The message list is built for cost that does not grow with the conversation. Each row is `MessageRow` (a `memo` component compared by
`rowModelEqual` in `ui/src/timelineRow.ts`); the Workspace derives a plain-data `RowModel` per row and rows call the Workspace only through
the identity-stable `RowHandlers`. Streamed text never goes through Workspace state: `ui/src/draftStream.ts` holds the growing text, `drafts`
changes only in structure (a draft appears, changes owner or sequence, becomes visible, gets a reply marker, goes away), and the one row that
shows a draft follows it with `useLiveDraft`. Do not read `drafts[...].content` in the Workspace for anything but emptiness. A conversation
that opens at its newest message draws `FIRST_PAINT_BLOCKS` blocks first and the rest in the next task (`landing` in `useConversationScroll`
decides; an unread divider, a restored position or a pending task focus draw everything). Settled Markdown is kept as elements (`reuse`).
Task cards (decision 0060 part C) follow the same rules: `ui/src/taskCards.ts` is plain data (`TaskCardModel`, one object per task whose identity holds until its content changes, `anchorCards` for the message a card sits under), `work_item` events reach the Workspace through `coalesce` (<= 1 state update a second), and `RowModel.taskCards` holds the models, never rendered nodes. `TaskCard` is collapsed to one line; `MemberTask` is the member list's line. `npm --prefix ui run test:task-cards` holds the logic. Budgets, each enforced by a script: `test:stream-cost` (<= 4 ms per streamed chunk, <= 16 ms at 4x CPU, no frame over 34 ms),
`test:switch-budget` (cached switch p95 < 60 ms, < 100 ms at 4x, also in WebKit), `test:chat-switch` (also `CHAT_SWITCH_ENGINE=webkit`),
`test:timeline-rows`, `test:perf-structure`. WebKit has no native scroll anchoring: whatever changes layout above the reader after the position
was set (sender marks, a prepended block) must be followed by `viewport.reposition()` or an anchor.

# Fast UI preview against a running server

Iterate on the UI without a release: `cd ui && TOFI_DEV_API_ORIGIN=https://<server>:8321 npm run dev:remote`, then open http://localhost:5173 in Chrome and sign in once. Vite hot-reloads UI edits instantly while `/api` is proxied to that server's real data; `dev:remote` accepts the default self-signed certificate. Safari will not keep the Secure session cookie on plain-http localhost, so use Chrome. Backend changes still need a build and deploy.
Every component state is also on the dev-only component catalog, http://localhost:5173/catalog.html (`ui/catalog.html`, `ui/src/catalog/`, D34): real
components on synthetic data in iframes at 1280/390 px, light/dark, 7 languages. It never calls `/api` and never ships: `npm --prefix ui run test:catalog`
fails if it reaches `dist/`, if product code imports it, or if a frame leaks a request. A component or state added to `docs/design/components.md` gets a
state in `ui/src/catalog/sections/` (or a listed gap).

# Composer resilience (optimistic send, outbox, drafts)

A sent message is on screen at once and never vanishes. Benchmark: iMessage (red "Not Delivered", tap to retry), WhatsApp (resend when the
connection returns), Claude.ai and ChatGPT (the prompt stays, Retry on the failed turn); we follow all three: failed bubble with Retry, Edit and
Delete, one automatic try when the connection is back, the text never lost. `ui/src/outbox.ts` is the outbox: every message the server has not
confirmed is mirrored to localStorage (`tofi:outbox:<origin>:<instance>:<owner>`, same scoping as the drafts). `App.tsx` `send` puts the bubble
(local copy, `message.local`) and the outbox entry in one commit; `deliver` uploads files not yet up (XHR with progress, `api.uploadAttachment`
with a listener), then posts with the same `client_message_id` on every attempt (the server stores it once and replays its answer: no duplicates).
`message.local.state` is `sending | retrying | failed`; `reason` is `offline | network | server | rejected`. The connection coming back (browser `online`
event or the live stream reconnecting) retries each failed message once, in order, labelled "Sending again" (`autoRetry`); a refusal (4xx), a second
failure and a message restored after a reload are never sent by themselves. After a reload the outbox entry comes back as a failed bubble when its
conversation loads (`reconcileOutbox`; an entry the server already holds is dropped). A file not yet uploaded cannot survive a reload (a File is not
storable): Retry is withheld, Edit puts the text in the composer and the file is asked for again (the draft's existing reselect flow). Pictures show
from the device (`attachmentImages.ts` `registerPreview`/`adoptPreview`: one object URL from the local tile to the server's copy) with a progress bar
(`Attachment.pending`); where the server stores files is not touched. Composer drafts moved from sessionStorage to localStorage (read once from
sessionStorage for older drafts), per conversation, per owner. Tests: `test:composer-resilience` (offline -> failed -> reconnect -> sent once,
reload, Delete/Edit, drafts, upload progress), `test:send-receipts`, catalog states `receipts/pending|offline|retrying|failed`.

# Web UI layers

One layer scale, in `ui/src/design-tokens.css` (`--z-base` 0, `--z-raised` 1, `--z-edge-blur` 10 (unused since the bottom blur was removed), `--z-chrome` 20, `--z-pill` 30,
`--z-panel` 40, `--z-popover` 50, `--z-menu` 60, `--z-cover` 70, `--z-dialog` 80, `--z-toast` 90, `--z-tooltip` 100, `--z-drag` 110) and
mirrored as `Z` in `ui/src/layers.ts` (docs/design/tokens.md §9). A z-index is `var(--z-*)`, `calc(var(--z-*) +/- 1)` or, inside a component's own
stacking context, -1..3; TS writes `"var(--z-*)"`, never a number. Overlays portal into `layerRoot()` (`<div id="tofi-layers">` after `#root`,
deliberately not a stacking context), never into `document.body`. `npm --prefix ui run test:layers` fails on any of that (older local values
above 3 are a ceiling in `ui/scripts/layers-baseline.json` that only shrinks) and checks the paint order of overlays stacked on one point.
Note `container-type` does not create a stacking context: on wide screens the chat pane's chrome competes with the sidebar and panels.

# Google connections and the Connections page

Settings › Connections is one list, one row per connection (`ui/src/MCPSettings.tsx`, `ui/src/connections/`); a row is the entry to that connection's page, not a container (`ConnectionDetailView`, a lazy import inside the Settings chunk: Status, Tools, Advanced, Danger; Back returns focus to the row), and every setup step opens in `ConnectDialog` (never inline). Words (docs/design/tools-and-connections.md section 2, owner decisions 2026-10-27): a connection's source is Official / Built in / Custom / Community (gogcli is Built in, not Community, D47); its status is one of six (`connections/status.ts`: Connected, Needs sign-in, Setup needed, Can't connect, Paused, or a transition; a plugin whose Google account was never authorised is `needs_auth`, never "Connected"; there is no "Not verified yet"); a tool's permission is Runs without asking / Asks when unsure / Always asks me (`read_only` / `write` / `high_risk`), chosen from an `OverlayMenu` on the page. A connection can be paused (`MCPServerConfig.Enabled`, only ever `false`; `PUT /api/extensions/mcp/{name}/enabled`, owner only): no run lists, searches or calls it, its configuration, sign-in, tool marks and standing allowances stay, and the fingerprint does not change, so resuming needs no new approval. The tool rows show the connection's "Always allowed" chips, and an approval card's `Manage` link opens the connection on that tool (`openConnectionDeepLink` in `settings/deepLinks.ts`). Every connection is open to every Bot until the owner limits it: table `bot_connection_access` (no row = open; written only by the owner routes `/api/bots/{id}/connections*` and `/api/extensions/mcp/{name}/bots`, never by a Bot), filtered in `prepareMode` through `extensions.Config.ConnectionAccess` before anything is listed, searched or dialled (a failed lookup withholds every server), and in the `mcp_list` action of `manage_extensions`; deleting a Bot or a connection clears its rows, archiving does not. UI: Bot settings > Tools (`ui/src/BotTools.tsx`) and the Bots section of the connection's page. The rail badge counts connections that need sign-in, setup or a retry (`useConnectionAttention`). Settings › Permissions (was Approvals; tab id `permissions`, the old `approvals` id redirects through `legacySettingsTabs`) holds the Reviewer control (`mcp_auto_review`, its only control; Automatic behavior shows the value and links here, D15), "How tools ask", and the Always allowed and Remembered in chats tables. The directory curates official connectors only (`upstream: "vendor"`, asserted by `test:integration-catalog`); community servers go through the custom path. Google is one directory tile (the `google` view of `ConnectDialog`): the gogcli path (`local_gog`, shown as "Gmail (personal account)"), whose Calendar and Drive are services of the same connection (the dialog multi-selects them, Gmail access is two cards, Read only by default; the account email is still required because `gog auth add` takes it as its account argument), with the Workspace MCP servers folded inside it under "Advanced: Google Workspace MCP (Developer Preview)" and a dated Banner for the agentic-client rule (D46). A service that registers its own OAuth client (`dcr`) is one click (`connections/oneClick.ts`): the tile saves the preset's defaults and the row starts its sign-in, the sign-in tab opened inside the click. `gog mcp --allow-tool` is derived at every start from the services recorded in `gog-account.json` (`gogMCPArgs`), and `GogCheck` reads each authorised service once; Google's Workspace MCP servers sit inside the Google step.
`internal/googleapi` is the one place that knows Google Cloud: the project of an uploaded OAuth client (`ParseClient`), `EnableURL(api, project)` (built from a known API id and a validated project; a provider error's URL or text is never reflected), and `ParseFailure`, which recognises "API not enabled" only from Google's structured error (`google.rpc.ErrorInfo` reason `SERVICE_DISABLED`, or legacy `accessNotConfigured`), never from wording. The result is recorded on the connection (`MCPStatus.error_code/api/enable_url`, from a check, from a failed tool call via `mcpCallObserver.failed`, and from the gogcli check as a 409 `api_disabled`) and the row shows "Setup needed" with the Enable button. The uploaded client JSON is kept sealed in the secret vault (`google_client.go`, id `workspace:google_client`, readable only by the server): a second Google service signs in with it (`/api/extensions/local-mcp/gog/oauth/start` without `credentials_json`, gogcli services are incremental via `include_granted_scopes`; an MCP entry with `google_client: true` gets its client id/secret filled server-side). GitHub star counts for the repo links come from `GET /api/extensions/repo-stars` (`repo_stars.go`: anonymous public read by the server, 24 h cache, failures remembered for an hour, no number when unknown; the UI never calls GitHub). The OAuth result page (`oauth_completion.go`, `oauthVerifiedPage`) reads the tools right after authorisation and shows setup help only for what is actually missing.
Loopback-only providers (decision 0055, Robinhood): a connection carries `oauth.redirect_policy: "loopback"` (`OAuthConfig.RedirectPolicy`; set from the catalog's `oauthRedirect`, or remembered when dynamic client registration with a public redirect is refused by the error code `invalid_redirect_uri`, never by wording). `Manager.OAuthStart` and `StartOAuth`/`RegisterClient` then refuse any redirect that is not `http://127.0.0.1…` (`ValidateLoopbackRedirect`), so no other redirect is ever registered or requested for it; the https `…/oauth/start` answers `400 oauth_loopback_only` (or `409 oauth_loopback_required` the one time the refusal is learned). The web client takes `mcpOAuthRoute(..., loopbackOnly)` mode `paste` whatever the origin and whether or not a shared computer exists: `POST …/oauth/paste/start` registers `desktopOAuthRedirect` (port 43821, shared with the native app) and `POST …/oauth/paste/complete {session_id,url}` takes the pasted redirected address (`internal/app/oauth_paste_back.go`: exact host/port/path, length caps, repeated parameters refused, `iss` checked against the metadata issuer when `authorization_response_iss_parameter_supported`, state compared in constant time, nothing echoed or logged). The pending row has no illustration: only the primary button breathes (`data-pending`).

# Web UI menus and popovers

Menus and popovers are Radix (`@radix-ui/react-dropdown-menu`, `@radix-ui/react-popover`, pinned), and Radix lives only in the lazy chunk
`ui/src/overlays/RadixOverlays.tsx`, fetched in idle time. The entry carries `ui/src/overlays/OverlayHost.tsx`: `OverlayMenu` and
`OverlayPopover`, which the owner renders only while the surface is open (or leaving), with its existing class as `className` and its
existing markup as children (every `<button>` in a menu becomes a Radix item; `data-keep-open` keeps the menu open on select). The trigger
stays TOFI's own `<button>` with `aria-haspopup`, `aria-expanded` and `onKeyDown={arrowOpens(setOpen)}`; pass it as `trigger`, and the box
the old CSS offsets were measured from as `anchor` (or a viewport `point` for context menus). Rules every menu follows: arrows, Home/End,
typeahead; Escape returns focus to the trigger; Tab closes and continues from the trigger (inside an `aria-modal` container it stays there);
a press outside closes without eating the press; a focus move alone does not close. Do not wrap the lazy module in `React.lazy` + `Suspense`
(React 19 holds a revealed boundary for ~300 ms). Do not use Radix Tooltip (hints are `data-hint` read by `ActionHints`;
`data-hint-tone="quiet"` gives the white look) or Radix ContextMenu (the sidebar drag engine owns the 380 ms long press). A surface opened
inside an `aria-modal` container stacks at `calc(var(--z-dialog) + 1)` (`data-layer="dialog"`). State that a large component sets but does
not render from (the sidebar row menu) goes in an `OverlaySlot` + `SlotView`, so opening it does not re-render the Workspace.
`npm --prefix ui run test:overlays` (also `OVERLAYS_ENGINE=webkit`) holds keyboard, dismissal, placement, layers, the bundle split and the
open cost; `test:shell-budget` keeps Radix out of the entry.

# Web UI dialogs

Modal dialogs use `ModalDialog` / `ConfirmDialog` from `ui/src/overlays/ModalDialog.tsx` (Radix `Dialog` / `AlertDialog`;
docs/design/migration.md step 3). Import it only from lazy chunks: Radix must stay out of the entry bundle (D31;
`test:dialogs` crawls the build). Pass the dialog element as the child (classes and role stay yours) and the scrim class
as `overlayClassName`; choose `closeOnEscape`, `closeOnOutside`, `locked`, `initialFocus` and `returnFocus` explicitly. It
sets `aria-modal="true"` and `data-tofi-modal` (App.tsx's desktop-shortcut gate and detail-pane trap read them), keeps Tab
inside, lets Escape close only the top layer and claims it with `preventDefault` (app Escape handlers must check
`defaultPrevented`), returns focus, hides the rest from screen readers and turns pointer events off on `<body>` (the
workspace toast opts back in). Not migrated on purpose: `TakeoverDialog` (Tab belongs to the remote screen), the terminal's
inline confirm, `PermissionCoach`, and the image lightbox and file preview (native `<dialog>`, top layer; hints and the
toast render inside the open one via `topLayerDialog()` / `InTopLayer` in `layers.ts`).
A dialog opened from a menu item reads where focus returns before it opens (`rememberDialogOpener` in App.tsx): the item
unmounts with the menu and the workspace turns inert in the same commit; from the sidebar row menu that is the row it was opened
on (recorded by the `OverlaySlot` listener; `test:overlays` holds it). Menus and dialogs share one copy of the Radix primitives
(one layer stack), so keep the four `@radix-ui/*` packages on versions npm dedupes.

# Web UI side panels

Web only, 1100 px and wider; the design is `docs/design/widgets-and-sidebar.md` and decision D67 (`side-panels.md` is the superseded first version). What a conversation can open has three tiers; the state is `surfaces` in `ui/src/surfaces.ts` (pure functions, `npm --prefix ui run test:surfaces`), and call sites use those functions directly (there is no `Panel` type or `setPanel` adapter any more: `openSurface`, `openSidebarId`, `openCoverPanel`, `toggleWidgetId` in `App.tsx`).

- **Widgets** (`computer`, `members`, `artifacts`, `usage`): small live blocks in a column inside the chat, right of the messages, at most four, each folds and closes (resting header is just "Title ⌄": the title is the fold button, the grip and X show on hover or keyboard focus). The order is the person's: default `WIDGET_ORDER`, changed by dragging a header or its grip, by pressing and moving on the computer's screen (no grip: 6 px, or held 350 ms / touch 400 ms then move; a plain press still enlarges; `WidgetGripApi.onPress` / `wasDragged`) or by Up / Down on the grip and Alt+Up / Alt+Down in a header or on the screen button (announced); `Surfaces.order` is a per-browser, per-account preference (`tofi:widget-order:<account>`, written only when it differs from the default) shared by every conversation, and an auto-opened widget lands in its saved slot (`moveWidget`, `normalizeWidgetOrder`). Nodes stay in a fixed DOM order and CSS `order` places them (moving a node would stop the live video), so a reorder never touches the tree; the slide is FLIP on `translate`. The space rule (`spaceFoldTarget`) folds from the bottom of that order up and never the computer. `usage` (`UsageWidget.tsx`, chunk `usageWidget`) reads `GET /api/conversations/{id}/usage` on open and once more after a run in that conversation finishes (throttled 4 s, no idle polling); More > Usage opens it. the message scrollbar stays right of the column. The column is a scroll container that bleeds `--widget-bleed-*` past its cards (their shadows are never clipped) and runs up under the header one layer below it, so the header's progressive blur is one band over chat and widgets and stops at `--scroll-bar-w`. A 404 / 405 from `/usage` (older server) shows one calm line, no retry. Computer is the bare monitor (no card, the chin's chevron closes it, it folds into a 56 px strip). Artifacts and Computer open by themselves (`useWidgetAutomation.ts`, Settings › Automatic behavior `widget_artifacts_auto` / `widget_computer_auto`, default on). Frames: `ContextCard` / `WidgetColumn` (lazy chunk `contextCard`). The big computer (`FloatingDesktop`, `--z-dialog`) must never be a descendant of the column: a fixed window under the column's scroll container, rise animation and `overflow: clip` card painted nothing in WebKit on iPad while its scrim still took every tap (owner, v0.2.1-rc.5). So `FloatingDesktop` keeps its shell in a host element it moves with plain DOM calls (React portal into the host): in the widget's slot while compact, a direct child of `.workspace` while big (the slot keeps its height as the placeholder; the same nodes, so the live video is not remounted; `data-moved` stops the boot animation replaying). `test:ipad-computer` (also `SURFACES_ENGINE=webkit`, `TOFI_TEST_WEBKIT=<executable>` when Playwright's WebKit build differs from the installed one) holds it with touch taps.
- **One sidebar** (`tasks`, `memory`, `terminal`, `archive`, `conversation`): a real grid track at the window edge that pushes the chat; an expand control; a second view replaces the first with a cross-fade. Frame `SidebarShell`. Its view is the only surface in the address: `?side=tasks|memory|terminal|archive` (`readSideParam` / `withSideParam` in `conversationRoute.ts`, replaced in place, no history entry, applied again on back/forward) and `tofi:open-sidebar` (`settings/deepLinks.ts`) opens one.
- **One cover** (`settings`, `bot-settings`, `group-settings`, `new-group`, `approvals`, `artifacts`): exclusive; widgets and sidebar are hidden but stay mounted while it is open. Frame `CoverShell` (a `ModalDialog` portaled into `.workspace`); `CoverForm.tsx` + `coverGuard.ts` make every way out of a form cover ask first; a popover or question opened from inside a cover takes `container={coverContainer()}`. The Bot settings cover (`botSettings` chunk) waits for its chunk before it opens, so it never shows an empty body.
- **Across conversations**: each conversation remembers its own widgets (open, folded, the runs the person closed the computer in) in memory only (`widgetMemory`, `stashWidgets` / `enterConversation`); a reload keeps just the sidebar. The sidebar stays and re-scopes to the new conversation (Archive is the account's), a cover closes. The computer widget is keyed by Bot id (one machine), never by conversation.
- **Escape** is `useSurfaceEscape.ts`, one press closes one thing: native top-layer `<dialog>` > Radix layers (they `preventDefault`) > big computer back to compact > cover (Settings through `tofi-settings-close`, the others are Radix dialogs that ask first when dirty) > sidebar > widgets bottom-up in the person's order (default Usage, Artifacts, Members, Computer; the computer last). Do not add another Escape handler for a surface.
- Every surface animates in and out with the motion tokens (leaving shorter than entering, reduced motion instant, no empty frame: `useLeaving` and `useSurfacePresence` in `surfacePresence.ts`). Below 1100 px the drawer/sheet stays (a 400 px drawer with a scrim; the computer is the floating window; Members and Artifacts open as covers); Electron is unchanged.
- Keep all of it out of the entry bundle except `surfaces.ts`, `surfacePresence.ts`, `useSurfaceEscape.ts` and the header (`test:shell-budget`). A new surface needs a title in 7 locales and a catalog state. Tests: `test:surfaces`, `test:surfaces-browser` (memory, `?side=`, Escape order, Usage widget, reordering), `test:widgets-auto`, `test:switch-budget` (includes two widgets open), `test:conversation-route`, `test:settings-shell`, `test:bot-skills`, `test:computer-window`. `scripts/shot-surfaces.mjs` (`SECTIONS=r2`) and `scripts/shot-bot-settings.mjs` take screenshots and enter/exit frame strips by pausing every animation and setting `currentTime`, so a loaded machine cannot drop frames.

# Server file store (computer-decoupling S1)

Every file a client shows or downloads (uploads, `publish_file`, generated images, outbox files) is written by `Store.addAttachmentOpts`
(`internal/app/attachments.go`) to `<account data dir>/attachments/<uuid>.upload`, in every mode, with or without a computer. The row records
`sha256`, `origin` (`upload|published|generated|outbox|mirror`) and `bot_id` (additive columns). The guest copy under `/workspace/home/uploads` is
a cache. The old guest-only form `disk_name = "vm:<uuid>"` is read-only legacy: `guest_attachments.go` copies such rows to the server (lazily on
read, and `file_store_copier.go` every 10 minutes, 50 rows per pass, at most one wake of a sleeping computer per hour). Order of one copy: read the
guest object, check its length, write a temp file and fsync, hash the file and compare, rename, then repoint the row and journal the guest object
in one transaction. The guest object is deleted later by `cleanupDeletedAttachments`, which re-checks the server file against the row's sha256.
A `vm:` file that cannot be reached answers HTTP 409 `file_on_detached_computer` (404 `file_missing_on_computer` when the computer answers and lacks it); the web app shows one sentence for either in the file card and the viewer (`AttachmentNotice`, `attachmentDownload.ts`). The portability import writes its attachment bytes straight to this store (`portability_staging.go`, origin `import`), so it needs no computer. `tofi computer remove --purge` refuses while any `vm:` row
exists unless `--discard-unsaved-files`. Outbox: `/workspace/home/outbox` is copied by `outbox_sync.go` at the end of a run that used the computer
(20 MiB per file, 200 MiB and 8 files per reply, symlinks refused by the guest, nothing deleted on the computer). Design:
`docs/design/computer-decoupling.md`.

# Chat uploads on the Bot's computer

Files a person sends in chat are copied to the guest at `/workspace/home/uploads/<conversation id>/<name>` (`internal/app/guest_inputs.go`,
decision 0036). Only attachments of user messages in the run's own conversation (the `read_attachment` visibility) are placed; the trigger
message's go first, then earlier ones within 40 files / 80 MB. The copy is lazy: a computer tool set syncs once before its first `computer_*`
call, never at context build, and the prompt only names the paths. Transport is the guest action `files.import_chunk` (reverse of
`files.export_chunk`, 512 KB chunks into `<path>.part`, renamed on the last): an existing file answers `exists`, so a repeat sends nothing and a
Bot's edits survive. A colliding name keeps its plain form for the earliest attachment and gets `-<id[:8]>` otherwise. Archives are never extracted.
A new guest action must also be added to `microVMActions` and the watchdog timeout list, and needs a rebuilt guest in the image.

# Bot computer image tooling

`deploy/microvm/Dockerfile` ships English-only office, PDF, OCR and charting tools (decision 0037); other languages are fetched by the Bot into user
`build-images.sh` builds natively on x86_64 or aarch64 (no cross-building; `TOFI_GUEST_ARCH` must equal the host). aarch64 uses the Firecracker aarch64 release, the CI `aarch64/vmlinux-6.1.186` Image (kept as `vmlinux`), a Debian trixie base and Debian `chromium` (x86_64 keeps Ubuntu + Google Chrome). The guest picks `google-chrome-stable` or `chromium` at runtime (`internal/computer/guest/browser_binary.go`); `account-release.json` carries an `arch` field that the checker validates against the ELF headers. Verified on a NanoPi R5C (RK3568): boot, guest actions, Chromium page, hibernate/restore.
directories, so the guest sets `TESSDATA_PREFIX=~/.local/share/tessdata` (links to the shipped data, writable; `internal/computer/guest/tessdata.go`) and
`/etc/fonts/local.conf` lists `~/.local/share/fonts`. Do not preinstall language packs or force a matplotlib font. `python-pptx` has no noble package and is
installed from a pinned wheel. `deploy/microvm/smoke-office.sh` is the in-image smoke test (run as uid 1000); run it after any change to that layer.

# Provider errors and failed runs

Transient provider failures are retried by code, not by message text: `provider.IsRetryable` (HTTP 429/5xx/529, and the in-stream `response.failed` / `error` codes in `retryableResponseFailure`, which arrive with HTTP 200). `RetryProvider` backs off with jitter, honors `Retry-After` (up to 60 s) and stops at `TotalBudget` (2 minutes); the agent loop retries once output has been delivered. A new provider error code that is transient goes into `retryableResponseFailure`. A provider 400 that no retry can fix and that has a known cause gets a stable code through `provider.RequestRejectionError` (wraps the `APIError`, never retried): `provider_invalid_request_prefill` marks a request that ended with an assistant message. The Anthropic converter closes any assistant-final history with a user turn (`Continue.`), so that code means a defect, not a condition to wait out; `internal/agent/prefill_guard_test.go` builds a request from every loop path and asserts the last turn is a user turn. Do not add assistant prefill to a request. A run that fails after `publish_file` keeps its files: `SetRunStatus(failed)` binds them to an attachments-only assistant message (`keepFailedRunFilesTx`); cancelled and interrupted runs still discard theirs (decision 0038).

Links written as `sandbox:/…` are normalized only on the way out (`Message.MarshalJSON`, `internal/app/sandbox_links.go`); the stored text stays as the model wrote it (decision 0039). The reply marker `[message_id=<uuid>]` is parsed by `ui/src/replyMarker.ts`.

# Built-in Skills

Deliverable Skills ship inside the binary (decision 0041): `internal/extensions/builtin_skills/<name>/SKILL.md` plus `scripts/*.py`, embedded by
`internal/extensions/builtin.go` (only those two patterns; `builtin_skills/tests/` holds the Python tests and is not embedded). They ride the
existing Skills tools (`list_skills`, `read_skill`, `read_skill_file`): the prompt directory carries one name and description line each, the body loads on
`read_skill`, and the same call copies the skill's files to the Bot's computer at `/workspace/home/uploads/.skills/<name>/<digest>/`
(`internal/app/builtin_skills.go`; inside uploads because the guest's `files.import_chunk` accepts nothing else). They are on for every Bot until
that Bot turns one off (an off row in `skill_enablement`, set in the Bot's settings > Skills or by the Bot's own `skill_disable`; the old global
`builtin_skill_off` is read once by the migration and never again); an unreadable lookup withholds them. They cannot be installed over or deleted, and
an installed skill with a built-in name is ignored with a diagnostic. SKILL.md text is English principles only (no example
sentences, Not/But contrasts, keyword lists, or a mandate to run a specific script; `tests/test_skill_text.py` enforces it): what good looks like, verify
as the recipient will experience it, every claim true of the delivered file. Scripts are general capabilities only (decision 0041 addendum, owner rule
2026-10-21: no task-tailored tools, builders or graders; the model writes its own code): `render_preview.py` (document/slides to page images),
`xlsx_summary.py` (recalculate and dump structure), `pptx_outline.py` (shape dump), `preview_page.py` (page at several widths, written as PNG sheets by headless Chrome),
`run_and_record.py`, `package_project.py`. A new or longer
description raises the prompt budget: measure it and move the caps in `internal/app/prompt_budget_test.go`. Run the script tests with
`python3 -m unittest discover -s internal/extensions/builtin_skills/tests -p 'test_*.py'`; `slides/scripts/render_preview.py` and
`document-report/scripts/render_preview.py` must stay byte-identical.
Skill library (decision 0058, `internal/app/skill_library.go`, `skill_library_http.go`, `skill_tools.go`; file operations in `internal/extensions/skill_library.go`): one
workspace library, one enabled set per Bot. Tables `skill_library` (installer, current version, bundle), `skill_versions` (files archived under
`<skills>/.versions/<name>/<n>/`, newest 20 kept), `skill_bundles`, `skill_enablement(bot_id, skill_name, enabled, set_by, set_at)` and `skill_library_markers`;
the files stay the source of truth and `reconcileSkillLibrary` makes the rows agree with them at startup and on each Settings list (diagnostic in the log). An
installed skill with no enablement row is off and an official one is on, so a Bot created later has the official skills only; the filter is
`Store.skillEnabledFilter` (`extensions.Config.SkillAccess`), so the prompt directory (`Enabled Skills (name and purpose):`) and `read_skill` see only the
Bot's enabled set and fail closed on an error. The one-time migration (`migrateSkillLibraryData`, marker `skill_library_per_bot_v1`) turned the old model into
rows without changing any existing Bot's set; `skill_access` and `builtin_skill_off` are kept and no longer read. Bot tool actions are `skill_list`
(scope `enabled`|`library`), `skill_enable`/`skill_disable` (the run's own Bot id, never an argument; a bundle name changes all members), `skill_install` and
`skill_publish_version`; there is no Bot delete. Delete, rollback and bundle install are owner routes (`/api/extensions/skills*`, `/api/extensions/skill-bundles*`);
a Bot's column is owner-edited through `/api/bots/{id}/skills*`. A bundle is a Claude Code plugin folder (`.claude-plugin/plugin.json`, `skills/<name>/SKILL.md`),
two levels only, installed all or nothing. A Bot export names the skills the Bot has on (`portableBot.EnabledSkills`) and nothing else; an import never turns one
on. UI: Settings > Skills is the library (`ui/src/settings/SkillLibrary.tsx`), Bot settings > Tools has the checkboxes (`ui/src/BotSkills.tsx` inside `ui/src/BotTools.tsx`, beside Bot Secrets);
catalog states `skills-*` and `bot-skills-*`.
A model sees a rendered image only through `computer_files` `files.view` (`internal/app/computer_view_file.go`, decision 0054): shell output is text and the browser tool refuses `file://`; the result is bounded (8 MiB / 24 MP source, 1568 px long side, 640 KiB, 16 views per run) and the agent keeps the newest 4 tool images.
`generate_image` also returns the generated PNG as image content (same bounds and a per-run count of 16, cache hits included, `imageToolResult` in `internal/app/image_generation.go`), so the Bot sees what it made. `reference_attachment_id` may name an image made earlier in the same run; the edit replaces that source in the final reply's staged files, other generated images stay. Unnamed outputs get a per-request filename so they never supersede each other.
Every generated image (new, edited, same-run cache hit) is also placed on the Bot's computer under the conversation's upload folder in `generated/` (`/workspace/home/uploads/<conversation id>/generated/<name>`, decision 0036, B18; `internal/app/generated_images_computer.go`) and the result carries `computer_path`: a Bot's image never shares a namespace with people's files, so a later upload of the same name is not shadowed by the guest's "exists" rule; collisions inside `generated/` use `guestInputPaths` over earlier Bot-published files then this run's images, so nothing already placed moves. Placement is lazy: `noteGeneratedImage` records it, `flushGeneratedImages` (called before every `computer_*` call, after the upload sync) places pending files and marks the computer in use, after which new images are placed at once; generate_image never boots a computer, and a failed copy is logged and retried once, never an error. `publish_file` takes optional `replaces_attachment_id` (must be a generate_image output still staged in the run) and drops it from the final reply once the derivative is staged; the base stays stored. Both pieces of guidance are tool text only; the core prompt is untouched.

# publish_file paths and failures

`publish_file` reads `files.export_chunk` from the guest. A relative `path` starts in the Bot's directory, `/workspace/bots/<bot_id>`, the
same directory `shell.exec` runs in (each command is a fresh shell, so `cd` does not carry over); an absolute path must stay inside
`/workspace`. A missing source comes back from the guest as outcome code `file_not_found` (`tooloutcome.FileNotFound`, permanent,
`not_executed`, HTTP 404) and an escaping or non-regular path as `action_rejected`; the host classifies on the code, never on the wording.
In `internal/agent/tool_recovery.go` a lost `publish_file` result fences only its identical replay, and `file_not_found` never fences a later call.

# Browser navigation failures

`browser.navigate` classifies Chrome's own net error name (`Page.navigate` `errorText`, `tooloutcome.ClassifyNavigation`) in the guest and answers HTTP 502 with a structured outcome (`execution_certainty: no_side_effects`, `host`, `net_error`): `browser_tls_error` / `browser_dns_error` / `browser_connection_refused` (next action `try_another_source`: the host will fail the same way, use another source), `browser_unsafe_port` (`repair_arguments`), `browser_connection_failed` (transient, `retry`), `browser_navigation_failed` (`explain_blocker`). `internal/computer/client.go` trusts only these codes with that certainty (`tooloutcome.IsBrowserNavigation`); classify on the code, never on the message. A change to the guest side needs a rebuilt guest image; no new action name is involved.

# Group triage and reactions

A group message that names no one is queued by the ingress transaction (`addUserRuns`) as a `triage` run when the group has two or more active members (`triage.go`, `triageSystemPrompt`). The run owner is the Bot the older rule picks (Primary Assistant, else random), so `routeAfterFailedTriage` (called from `failRun` and the engine-error path) can hand the message to a default member when the background model fails or answers invalidly; the chosen member answers as an ordinary `group_chat` run (`AddHandoff` with sender `system`, which publishes no chat message and no message event; triage runs are excluded from `WorkingBotIDs`). The model call never runs inside ingress. Settings: `group_triage` (default on) and `group_triage_model` (default the background/triage model); off restores the older rule exactly. Follow-ups (B12, reworked by B15): a message that names no one is always triaged, also while a member works on the person's previous message. When setting `group_followup_to_working` (default on) is on, the triage prompt gains a list of the members working now (`workingMembers` in `steer.go`: member, the person message its run handles, task title, start time; run running, queued or parked on a question and not yet answered) and a steer rule; the answer `{"bot_id":X,"steer":true}` (`parseTriageDecision`) queues the message as steering for X's running run (`steerTriagedMessage`, `bot_inbox` `user_message`, decision 0062), any other answer is a new request (a busy member takes it from its lane). A steer answer whose run has already answered finds no run and starts a normal run for X (continuation); a failed or invalid triage falls back to the member working on the most recent person message (`routeAfterFailedTriage`), else the default member. Ingress makes no steering decision for an unaddressed message and does not cancel the stand-in owner's queued work while a member works. Setting off: plain triage, no list, no steer option (a chosen member that works takes the message as queued input, as before). The triage answer may be JSON with a `brief` (`parseTriageAnswer`): a one-line restatement with references resolved, stored as the task title (`storeTriageBrief`) and shown in `[task record]`; the member's history already runs up to and including the person's message.

A person's reaction on a Bot's group message wakes that Bot once per 10-second window (`reaction_wake.go`: `noteReactionWake` -> `AddReactionWake`, a `group_mention` run with `run_wakes.source='reaction'`, no parent, no reply required); a Bot's reaction never wakes anyone, and an archived or departed author is skipped. Reactions reach the model only as a `[reactions: ...]` line under each message in the history window (`Store.reactionSummaries`), never in the system prompt. A reaction never changes task state. See decision 0060.

# Multi-click and browser upload

`computer_desktop` has `double_click` and `triple_click` (both are `desktop.click` with `clicks` 2 or 3, so effect review, approval and recovery follow click): the guest runs one `xdotool click --repeat N --delay 70`, well inside the toolkit double-click interval. A guest built before `clicks` rejects the unknown field before clicking; `microvm.go` turns that into a clear "needs a newer computer image" error. `computer_browser` `browser.upload` (`internal/app/browser_upload.go`) puts files into a page's file input through DevTools, never the system file dialog: `selector` names the input (`DOM.setFileInputFiles` on its object), or `click` names the element that opens the chooser (`Page.setInterceptFileChooserDialog` on, click, `Page.fileChooserOpened` -> `DOM.setFileInputFiles` with its `backendNodeId`, interception off in `finally`). `files` are paths inside `/workspace` (relative = the Bot's directory); `attachment_ids` are chat attachments staged by `pushGuestInput`. The helper runs on the computer (same python3 mechanism as `browser.read`, no guest change) and re-checks every path with `realpath` (a symlink out of `/workspace`, `/workspace/browser`, `.tofi-env`, `.ssh`, `.gnupg`, `.aws` are refused; 10 files, 50 MB each, 100 MB total). An upload is outgoing data: `guardAction` reviews it as effect `send` unless the model declared a stronger one.
