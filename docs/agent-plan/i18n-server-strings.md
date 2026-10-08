# Server-side user-visible strings (i18n phase 4 plan)

Survey from i18n phase 1 (`feat/i18n`, 2026-10-08). Nothing here is converted yet. Model-facing prompts,
tool descriptions and built-in Skill instructions stay English and are out of scope.
UI side already in place: `ApiError` carries `code`; `ui/src/i18n/errors.ts` `errorText(cause, ns, fallback)`
renders `<ns>:apiError.<code>` → `common:apiError.<code>` → server `message`. There is no `params`
channel yet, and `ui/src/AdminAccounts.tsx:6` has its own `request()` that shows `error.message` directly.

## 1. HTTP errors — `writeErr(w, status, code, msg)` (internal/app/app.go:2597)

~567 call sites in 40 files; ~157 have a dynamic message (`err.Error()`, `fmt.Sprintf`, raw runner body);
31 have Chinese messages. All literal codes are snake_case (~140 distinct), but many are buckets.

| File | Calls (dynamic) | Notes / examples (line: code) |
|---|---|---|
| app/app.go | 74 (33) | 2604 `storage` err.Error(); 2890/2898 `ambiguous_recipient`/`unknown_recipient` (names); 2847 `archive_blocked`; `method` and `method_not_allowed` both used |
| app/computers.go | 43 (15) | `computer_busy` reused with several Chinese messages (987, 1009, 1019, 1027); 782 `pairing_limit` err |
| app/schedule.go | 26 (13) | 1103 root_run_id limits; 1191 `invalid_schedule` err |
| app/accounts.go | 24 (0) | specific codes (`invalid_bootstrap`, `invalid_credentials`, `password_change_required`) |
| app/accounts_management.go | 21 (0) | `auth_unavailable` used for both 400 and 500 |
| app/local_mcp.go | 19 (15) | `runner` ×16, raw bodies from internal/mcprunner/http.go `http.Error` |
| app/mail_drafts.go | 17 (8) | Chinese: 282 `draft_conflict`, 300 `gmail_permission`, 321 `send_unknown`; validation 58/62/68 (max 20 recipients) |
| app/work_items.go | 16 (4) | 490 `work_execution_unavailable` with 7 Chinese reasons from work_execution.go:215–291 |
| app/questions.go | 16 (4) | `invalid_answer` ×5 messages |
| app/owner_auth.go | 16 (0) | 413 `password_transport_required`, 418 `rate_limited`, 435 username/password limits |
| app/terminal.go | 15 (2) | `computer_busy` ×3 Chinese variants, `control_expired`, 162 `input_sequence` |
| app/providers.go | 14 (4) | `invalid_key` ×4 variants (provider, HTTP status); 775 `provider_unreachable` |
| app/extension_management.go | 14 (9) | `extensions` ×6 raw err; `oauth` via `OAuthPublicError` (4 English variants, extensions/management.go:706–720); 52 `oauth_https_required` Chinese |
| app/portability_http.go | 13 (7) | 59 `bundle_too_large` (16 MiB); `invalid_bundle` raw err |
| app/dictation.go | 13 (1) | Chinese 173 `dictation_unconfigured`, 203 `dictation_auth_unavailable` |
| app/computer_control.go | 13 (2) | `writeControlError` (282–295) derives the code by substring-matching error text; Chinese 103/116/160/173/291, 309/317 |
| app/computer_resources.go | 12 (3) | Chinese mixed with raw err (44 `restart_unavailable`) |
| app/secrets.go | 2 + `fail` helper (179) | 18 messages all under `secret_input` |
| app/ssh_keys.go | 7 (1) | all `ssh_keys`, mixed English/Chinese |
| app/vm_oauth.go | 6 (1) | `oauth` reused; Chinese 307/331 |
| app/work_execution.go | 3 | 368 `model_unavailable` Chinese |
| app/desktop_stream.go | 7 (1) | already code-only |
| 18 other files | 1–9 each | mostly specific codes; `storage`, `group_only`, `archive_blocked` carry raw err |

Codes that block rendering by code (one code, several meanings): `invalid_request` (82), `storage` (57, raw
SQL/internal text), `not_found` (55), `computer_busy` (12 calls, ~9 messages), `secret_input` (18),
`runner` (16), `archive_blocked` (8), `oauth` (7), `ssh_keys` (7), `extensions` (6),
`work_execution_unavailable` (7 reasons), `invalid_answer` (5), `control_expired` (3), `invalid_key` (4),
`unavailable`, `method` vs `method_not_allowed`.
Other writers: plain `http.Error` in oauth_completion.go:73 and internal/mcprunner/http.go (20 sites);
internal/computer/guest/oauth.go:323 writes `{"error": msg}`.

## 2. Chinese text in non-test Go files

Comments only (skip): models/skill.go, models/resolver.go, executor/*.go comments, agent/agent.go, follow_global.go.
Legacy/dead `fmt.Errorf` (models/models.go:302–397, executor/http.go, shell.go, linter.go): switch to English when touched.

| Where | What | Visibility | Suggested shape |
|---|---|---|---|
| extensions/inspection_diagnostic.go:14–40 | diagnostic messages per code; default (line 12) has an empty code | user + model (`extension_status`) | `extensions:diagnostic.<code>`; give the default a code |
| app/oauth_completion.go:44–118, oauth_service_page.go:13, extension_management.go:45–77, local_mcp.go:227/235 | OAuth completion HTML (see §4) | user | server catalog `oauth.page.*` |
| app/vm_oauth.go:103, 203, 212, 219 | VM OAuth status `error` | user | `vm_oauth_start_failed`, `_interrupted`, `_exchange_failed`, `_denied` |
| app/work_execution.go:103, 117, 119 | execution error/result | user (+model) | `delegate_incomplete`, `no_result`, `attachments_only` |
| app/dictation.go:32–33 | model description, `"$0.003 / 分钟"` | user | UI keys on model id; expose `usd_per_minute` |
| app/display_cards.go:119–128 | ui_card text fallback in `content` | transcript, model, notification preview | English, model-neutral; UI renders from `card` |
| app/collaboration.go:324 | "%s 已将任务转交给 %s。" notice | user | `chat:notice.forwarded` {from, to} |
| app/app.go:1469 | triage notice "已交给 X 处理。" | user + model | `chat:notice.triage_routed` {bot} |
| app/app.go:2868 | attachment-only user content "请查看附件。" | user bubble + model | store empty/English; UI placeholder |
| app/approval_expiry.go:213–219 | approval-expired conclusion notice | user + history | `chat:notice.approval_expired` {completed, not_executed, uncertain} |
| app/final_answer_guard.go:15 | no-final-answer note as assistant content | user + history | code `no_final_answer` in metadata |
| app/conversation_task_state.go:149 | `FailureReason` "协作成员未完成此步骤。" | user | `failure_code: delegate_incomplete` |
| app/usage.go:205 | SQL fallbacks '已删除 Bot', '已删除对话' | user | null + `deleted: true` |
| app/prompt_budget.go:71 | examples inside the voice-guidance prompt | model | out of scope |

## 3. English prose stored or served and shown verbatim

| Where | What | Notes |
|---|---|---|
| app/run_failure.go:18–111 | `run.failure.message` | codes exist; provider variants need `failure.params {provider}` (UI already renders `tasks:fact.model_*` by code) |
| app/schedule_failure_detail.go:4–25 → `run.error` → schedule.go:716 `status_error` | missing receipt + unconfirmed model text | no code; add `schedule_missing_receipt`, keep model output separate |
| tooloutcome/outcome.go:23 `Outcome.Message` (~65 producers) | tool outcome text | model-facing; UI renders by `code` (`tasks:outcome.*`). Dedupe codes first: `approval_window_expired` ×4, `mcp_proposal_closed` ×2, `run_inactive` ×2 |
| app/mcp_context_diagnostic.go:91–116, mcp_approval_claim.go, mcp_auto_review.go:384 | approval `Review.Reason` | render by `ContextFailure.Code` |
| app/usage.go:317 | `/api/usage` `note` | `settings:usage.note` |
| app/secrets.go:260, 343 | 200 `note` | `note_code` |
| app/model_settings.go:211–301, providers.go:303, 320 | `/api/models` `warning` (joined with "; "), "Codex · " prefix | `warnings: [{code, params}]`; UI composes provider label |
| app/providers.go:648 | persisted provider key error | `{code: "key_rejected", http_status}` |
| app/portability.go:593–645 | preview `warnings[]` | `[{code, params}]` |
| app/collaboration.go:439–601 | `message_ref` "Messaged X" / "Message from Y" | UI already parses (`localizeMessageRef`); render from notice type + bot ids |
| app/computers.go:714, 722 | job errors | `job_expired`, `parent_run_inactive` |
| extensions/management.go:706–720 | `OAuthPublicError` | split `oauth_timeout`, `oauth_redirect_localhost`, `oauth_discovery_failed`, `oauth_start_failed` |

Keep as-is (content or third-party metadata): Bot-authored notice text, model names, Skill and MCP server descriptions.
Model-facing, out of scope: prompt_budget.go, workflow_guides.go, built-in skills, all tool `Description:` literals,
work_execution.go:305, memory.go:170, tool_search_ai.go:57, context.go:559, usage.go:334, agent/*,
extensions/discovery.go, extensions/skills.go.

## 4. Server-rendered HTML

Only `app/oauth_completion.go` `writeOAuthCompletion` (MCP/Google/Notion/gog pending, success, denied, failed):
English `<title>`, Chinese body, hard-coded `lang="zh-CN"`; callers pass Chinese `page.Message`.

## Recommended approach

1. **Error envelope** `{"error": {"code", "message", "params"?}}`: add `writeErrP(w, status, code, params, fallback)`,
   keep `writeErr` as a wrapper, `message` English fallback for old clients/logs. Translate the 31 Chinese
   messages to English fallbacks. Stop returning raw `err.Error()` for `storage`/`runner`/`extensions`: log it,
   send a generic code, put a sanitized `params.detail` only where useful.
2. **Unique codes first**: split `computer_busy`, `control_expired`, `secret_input`, `ssh_keys`, `oauth`,
   `runner`, `extensions`, `invalid_key` (`key_rejected` {provider, http_status}), `invalid_answer`;
   `work_execution_unavailable` + `params.reason`; recipients + `params.names`; merge `method`; typed errors
   instead of substring matching in `writeControlError`. Generic `invalid_request` may keep `params.field/min/max`.
3. **UI**: add `params` to `ApiError`, pass to `i18n.t(key, params)` in `errorText`; route AdminAccounts through it.
4. **Notices/system messages**: `notice_data` gets `{code, params}` (bot ids, resolved client-side); English
   `content` stays as fallback and model transcript.
5. **Structured failures/payloads**: `failure.params`, `schedule_missing_receipt`, work-execution codes,
   `warnings: [{code, params}]`, `note_code`, deleted-name flags, provider key error codes, dictation `usd_per_minute`.
6. **OAuth HTML**: small server catalog (en, zh-CN first; same 7 languages later) keyed by state/message code;
   language from `lang` query/cookie (the UI can pass the active language), then the account preference
   (`user_preferences.language`), then `Accept-Language`, default en; `<html lang>` matches.
7. Add a Go test that fails on new CJK in non-test `internal/` files outside an allowlist, mirroring the UI ratchet.
