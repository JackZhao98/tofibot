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
Springs are tokens too: `SPRING` in `motion.ts` (`push`, critically damped; `test:motion-tokens` fails on a literal
`stiffness`/`damping`/`mass` elsewhere). The phone swipe-back (D35) is `phoneSwipeGesture.ts` (gesture core, rules in
`SWIPE`) + `phoneSwipeEngine.ts` (the release, a spring with the finger's velocity baked into WAAPI keyframes), a lazy chunk
loaded by `usePhoneSwipeBack.ts` on a phone-sized window only; it moves the panes by transform and commits through the same
`setMobileList(true)` as the back button (flushSync, `data-swipe=done` keeps CSS transitions from replaying). `test:swipe-back`
(also `SWIPE_BACK_ENGINE=webkit`) holds it. Motion is not a dependency of the app (step 4 measured it and kept our code,
D35); `motion` is a devDependency only for `test:motion-experiment` and `TOFI_MOTION_EXPERIMENT=motion` in the dev server.

# Process line

What a Bot is doing right now is one quiet line, never a block: `LiveMark` (14px ink ring, a lagoon stroke turning on `transform`, static under reduced motion), the status, the seconds, a chevron. It is a real button (`aria-expanded`, focus ring) that opens the tool steps through the mechanism already there: the task block's `<details>` (`ActivityDisclosure`, `viewActivity` when Activity is hidden) or the web tool row's drawer (`ActivityViewContext.reveal` from `LiveToolLine`). Look in `ui/src/live-line.css`: `font: var(--type-meta)`, `color: var(--text-process)`, one line with an ellipsis (the opened view carries the whole text), no cat, no mono or semibold on the line. A new live line uses `.process-line` and `LiveMark` instead of its own style; the cat stays only on the composer perch and in the sender column. States: catalog section 1 (`ui/src/catalog/sections/process.tsx`).

# View context

A reply's "View activity" ends with a "Context" row (D53): what that run's first model call received, part by part. `execute` sets `r.ctxCapture` (`context_snapshot.go`); `buildContextPartsWith` reports the history window and note pieces to it, `assembleRunSystemSegments` (`prompt_budget.go`, concatenates to the exact system bytes) supplies the system parts, and `recordRunContext` adds the tool list and stores one gzip-JSON row per run in `run_context_snapshots` (first call only; newest 50 per conversation; <= 256 KB before compression, largest texts dropped first; `redactContextText` replaces credential-shaped values). Part kinds are open strings: a new block in `buildContextPartsWith` needs one row in `contextMarkerKinds` (else it shows as `other`) and a `chat:runContext.kind.<kind>` string in 7 locales. The capture never changes a prompt byte (`TestContextCaptureDoesNotChangePromptBytes`, `TestAssembleRunSystemSegmentsKeepPromptBytes`). `GET /api/runs/{id}/context` sits behind the owner session gate; no snapshot is 404 `context_snapshot_unavailable`. UI: `RunContext.tsx` is a lazy chunk (`RunContextEntry` in `App.tsx` is the only entry code); catalog states `context-*` in `catalog/sections/process.tsx`. Web only; `DesktopToolActivityRun` has no Context row.

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
`task_checklist`: only the task's creator adds or removes items or changes the request; the holder sets an item's status and reason (every ended item has a reason).
`say(outcome=done)` with an `open` item goes back once, in the run's one send-back shared with the `pending` check (`state.pendingCheck`).

`waits` is the one table for waiting: kinds `bot_post`, `task_result`, `human_answer` (a mirror of a question card; the card keeps its own resume), `command_exit` (decision 0061), `timer`; groups `any`/`all`; a group settles once (`settleGroupTx`) into one `bot_inbox` row. `recordWaitTx` runs the cycle check in the same
short transaction (walk <= 64 steps, code `wait_cycle`, a graph too large to walk is refused) and the limits (16 open per Bot, `wait_chain_limit` after 24 self-wakes in a row).
A timeout is a result: `timed_out` (a timer is `fulfilled`). Per-kind defaults are `waitTimeoutFor` (15 min / 2 h / 30 min). There is no in-memory timer: one sweeper
(`sweepWaits`, 15 s, also at start) ends due waits, reconciles question mirrors, and calls `flushInbox`. Tests call `sweepWaits` directly; do not add `time.AfterFunc`.
`flushInbox` wakes a Bot with a new `group_followup` run whose `run_wakes.source` is `inbox` (the rows are the request, block `[wait result]`); a single `bot_post` wait in a
group keeps the old mention wake (`via='mention'`). A Bot's own run reads unread rows as `[task updates]` (`consumeInboxForRun`, idempotent per run) when it starts, and at each boundary while it runs
(`drainInboxForRun`, decision 0062, section "Per-Bot lanes and queued steering"); a Bot that is not running is woken by a new run. `outcome=pending` is honest only while the run holds a pending wait (`runHasPendingWait`).

Tools: `delegate` (one member = handoff path, several = message path; `title`, `checklist`), `wait_for` (receipt at once), `cancel_wait`, `task_update`, `task_ask`, `say` with
`task_id`. `handoff` and `message` are deferred aliases for one version. `cancel_wait`, `task_update`, `task_ask` are on demand (`onDemandTools`): the `[waiting on]`,
`[task record]` and question texts say how to load them. Tool and block texts are English principles in Simplified Technical English with no examples.
A person's message to a member that holds a task bumps `spec_version` and stores a `task_update` row for both sides (`noteUserMessageForTasks`); results carry
`result_based_on_version`. Context blocks `[waiting on]` (<= 1600 runes), `[task record]`, `[task updates]`, `[wait result]` each have a row in `contextMarkerKinds` and a
`chat:runContext.kind.*` string in 7 locales. Stop (`CancelRunTree`), Bot delete (inside `deleteWorkspaceTargetWith`) and group member removal (`UpdateGroup`) end tasks,
waits and the runs below them by `parent_task_id`/`origin_run_id`/`assignee_run_id`; `GET /api/bots/{id}/task-impact?group_id=` feeds the confirmation count.
Retention (`task_retention_days`, default 30, 0 = keep) clears the request text, checklist and revisions of finished delegated tasks, ended waits and read inbox rows; it never
touches messages, attachments or runs, so the files a failed run kept (decision 0038) stay. Messages a run sends after it registered a wait count against their own
allowance of three (`chatSegmentCountTx`, decision 0050 unchanged before that). The new tables are not exported (`portable*`).

Claim and pass (decision 0060 part C, `internal/app/task_claim.go`). Group triage (`AddHandoff` with sender `system`) makes a task in the same transaction as the answering run: holder = the chosen member, giver = the person (`assigner_kind='user'`, no wait, no inbox), `assignee_run_id` = that run, so a one-turn answer closes it as `done` with no tool call. `claim_task` (holder only, idempotent; writes `task_claims`, moves `todo` to `in_progress`, posts nothing) and `pass_task` (holder only, reason required, target an active member of the same group, at most 4 passes per task via `task_passes`; one transaction: new holder, status `todo`, claim cleared, `assignee_run_id` = a new queued `group_chat` run for the new holder, a non-waking inbox row with the reason; giver, waits, tree and checklist unchanged) are group-only on-demand tools (`onDemandTools`). `WorkItem.claimed_at` is read from `task_claims` in `decorateTaskTx`. The group prompt carries `groupTriageGuidance` (English, STE, principles only); the triage prompt lists up to 12 unfinished group tasks with holders (`openTasksForTriage`). `workItemEventTx` also sends the `work_item` event to `work_conversation_id`, so a task delegated to a Bot's chat draws its card there. `GET /api/conversations/{id}/task-cards` feeds cards and the member list.

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
(`standingClaimValid`), so Revoke (Settings > Automatic behavior, `/api/standing-allowances`) bites an in-progress run at its next call (attended: the
card returns to a person; unattended: it is closed and the call refused `mcp_needs_human_unattended`). Each use bumps `use_count`/`last_used_at` and
writes `mcp_standing_allowance_uses` (null `call_id` when the runtime gave none) inside the claim transaction, and links the tool activity
(`ToolActivity.allowed_by`). Archiving/deleting the Bot or the server (`deleteMCPServer`: use it, not `extensions.DeleteMCP`) revokes the grants
(`revoked_by` `bot_archived` | `bot_deleted` | `server_removed`) and keeps the rows and uses.
Community connections (decision 0047): TOFI curates official connectors only; a community MCP is installed by the person under Settings > Connections > Add connection > Custom > Local package
(npm or PyPI, pinned version, optional npm `sha512-` digest verified after install). Its secrets are 0600 files in the Runner, never in a response, status or manifest.
`attachLocalMCP` writes `provenance` (origin `community`, kind, package, version) from the Runner's status only; it is part of the configuration fingerprint, so a
different package or version voids standing allowances. For a community server the provider's `readOnlyHint` is shown but not obeyed (`communityServer` in
`resolveMCPToolTier`): its tools are reviewed as writes until the owner marks them read-only. Installed Skills with scripts are copied to the Bot's computer on
`read_skill` like built-in ones, so prefer MCP for anything holding a credential. Pins and recipes live in `docs/integrations/community/`.
Session cookies are Secure when `TOFI_PUBLIC_ORIGIN` is https; Caddy sends HSTS. Known open: `computer_shell` shares a uid and loopback with the
shared Chrome DevTools port.

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

# Web UI layers

One layer scale, in `ui/src/design-tokens.css` (`--z-base` 0, `--z-raised` 1, `--z-edge-blur` 10, `--z-chrome` 20, `--z-pill` 30,
`--z-panel` 40, `--z-popover` 50, `--z-menu` 60, `--z-cover` 70, `--z-dialog` 80, `--z-toast` 90, `--z-tooltip` 100, `--z-drag` 110) and
mirrored as `Z` in `ui/src/layers.ts` (docs/design/tokens.md §9). A z-index is `var(--z-*)`, `calc(var(--z-*) +/- 1)` or, inside a component's own
stacking context, -1..3; TS writes `"var(--z-*)"`, never a number. Overlays portal into `layerRoot()` (`<div id="tofi-layers">` after `#root`,
deliberately not a stacking context), never into `document.body`. `npm --prefix ui run test:layers` fails on any of that (older local values
above 3 are a ceiling in `ui/scripts/layers-baseline.json` that only shrinks) and checks the paint order of overlays stacked on one point.
Note `container-type` does not create a stacking context: on wide screens the chat pane's chrome competes with the sidebar and panels.

# Google connections and the Connections page

Settings › Connections is one list, one row per connection (`ui/src/MCPSettings.tsx`, `ui/src/connections/`); every setup step opens in `ConnectDialog` (never inline), and a row's status comes from `connections/status.ts` (a plugin whose Google account was never authorised is `needs_auth`, never "Connected"). The directory curates official connectors only (`upstream: "vendor"`, asserted by `test:integration-catalog`); community servers go through the custom path. Gmail is the gogcli path (`local_gog`, shown as "Gmail (personal account)"); Calendar and Drive are services of the same connection: the dialog multi-selects them, `gog mcp --allow-tool` is derived at every start from the services recorded in `gog-account.json` (`gogMCPArgs`), and `GogCheck` reads each authorised service once; Google's Workspace MCP servers sit under "Workspace preview (enterprise)".
`internal/googleapi` is the one place that knows Google Cloud: the project of an uploaded OAuth client (`ParseClient`), `EnableURL(api, project)` (built from a known API id and a validated project; a provider error's URL or text is never reflected), and `ParseFailure`, which recognises "API not enabled" only from Google's structured error (`google.rpc.ErrorInfo` reason `SERVICE_DISABLED`, or legacy `accessNotConfigured`), never from wording. The result is recorded on the connection (`MCPStatus.error_code/api/enable_url`, from a check, from a failed tool call via `mcpCallObserver.failed`, and from the gogcli check as a 409 `api_disabled`) and the row shows "API not enabled" with the Enable button. The uploaded client JSON is kept sealed in the secret vault (`google_client.go`, id `workspace:google_client`, readable only by the server): a second Google service signs in with it (`/api/extensions/local-mcp/gog/oauth/start` without `credentials_json`, gogcli services are incremental via `include_granted_scopes`; an MCP entry with `google_client: true` gets its client id/secret filled server-side). GitHub star counts for the repo links come from `GET /api/extensions/repo-stars` (`repo_stars.go`: anonymous public read by the server, 24 h cache, failures remembered for an hour, no number when unknown; the UI never calls GitHub). The OAuth result page (`oauth_completion.go`, `oauthVerifiedPage`) reads the tools right after authorisation and shows setup help only for what is actually missing.
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
on. UI: Settings > Skills is the library (`ui/src/settings/SkillLibrary.tsx`), the Bot settings panel has the checkboxes (`ui/src/BotSkills.tsx`, beside Bot Secrets);
catalog states `skills-*` and `bot-skills-*`.
A model sees a rendered image only through `computer_files` `files.view` (`internal/app/computer_view_file.go`, decision 0054): shell output is text and the browser tool refuses `file://`; the result is bounded (8 MiB / 24 MP source, 1568 px long side, 640 KiB, 16 views per run) and the agent keeps the newest 4 tool images.

# publish_file paths and failures

`publish_file` reads `files.export_chunk` from the guest. A relative `path` starts in the Bot's directory, `/workspace/bots/<bot_id>`, the
same directory `shell.exec` runs in (each command is a fresh shell, so `cd` does not carry over); an absolute path must stay inside
`/workspace`. A missing source comes back from the guest as outcome code `file_not_found` (`tooloutcome.FileNotFound`, permanent,
`not_executed`, HTTP 404) and an escaping or non-regular path as `action_rejected`; the host classifies on the code, never on the wording.
In `internal/agent/tool_recovery.go` a lost `publish_file` result fences only its identical replay, and `file_not_found` never fences a later call.

# Browser navigation failures

`browser.navigate` classifies Chrome's own net error name (`Page.navigate` `errorText`, `tooloutcome.ClassifyNavigation`) in the guest and answers HTTP 502 with a structured outcome (`execution_certainty: no_side_effects`, `host`, `net_error`): `browser_tls_error` / `browser_dns_error` / `browser_connection_refused` (next action `try_another_source`: the host will fail the same way, use another source), `browser_unsafe_port` (`repair_arguments`), `browser_connection_failed` (transient, `retry`), `browser_navigation_failed` (`explain_blocker`). `internal/computer/client.go` trusts only these codes with that certainty (`tooloutcome.IsBrowserNavigation`); classify on the code, never on the message. A change to the guest side needs a rebuilt guest image; no new action name is involved.

# Group triage and reactions

A group message that names no one is queued by the ingress transaction (`addUserRuns`) as a `triage` run when the group has two or more active members (`triage.go`, `triageSystemPrompt`). The run owner is the Bot the older rule picks (Primary Assistant, else random), so `routeAfterFailedTriage` (called from `failRun` and the engine-error path) can hand the message to a default member when the background model fails or answers invalidly; the chosen member answers as an ordinary `group_chat` run (`AddHandoff` with sender `system`, which publishes no chat message and no message event; triage runs are excluded from `WorkingBotIDs`). The model call never runs inside ingress. Settings: `group_triage` (default on) and `group_triage_model` (default the background/triage model); off restores the older rule exactly.

A person's reaction on a Bot's group message wakes that Bot once per 10-second window (`reaction_wake.go`: `noteReactionWake` -> `AddReactionWake`, a `group_mention` run with `run_wakes.source='reaction'`, no parent, no reply required); a Bot's reaction never wakes anyone, and an archived or departed author is skipped. Reactions reach the model only as a `[reactions: ...]` line under each message in the history window (`Store.reactionSummaries`), never in the system prompt. A reaction never changes task state. See decision 0060.
