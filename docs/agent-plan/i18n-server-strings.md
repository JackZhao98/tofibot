# Server-side user-visible strings (i18n phase 4 plan)

Status: survey from phase 1 (`feat/i18n`). Nothing here is converted yet. Model-facing prompts,
tool descriptions and built-in Skill instructions stay English and are out of scope.
Re-run the greps below before starting; counts are from 2026-10-08.

## 1. HTTP error responses (`writeErr(w, status, code, message)`)

491 calls in 40 non-test files. Codes are already stable `snake_case`; messages are English prose that the
UI shows via `ApiError.message` when it has no catalog entry. Largest files:

| File | Calls |
|---|---:|
| internal/app/app.go | 74 |
| internal/app/computers.go | 43 |
| internal/app/schedule.go | 26 |
| internal/app/accounts.go | 24 |
| internal/app/accounts_management.go | 21 |
| internal/app/local_mcp.go | 19 |
| internal/app/mail_drafts.go | 17 |
| internal/app/work_items.go, questions.go, owner_auth.go | 16 each |
| internal/app/terminal.go | 15 |
| internal/app/providers.go, extension_management.go | 14 each |
| internal/app/portability_http.go, dictation.go, computer_control.go | 13 each |
| internal/app/computer_resources.go | 12 |
| remaining 23 files | 1–9 each |

Known problems for code-based rendering:
- Generic codes reused with different meanings: `storage`, `invalid_request`, `not_found`
  (e.g. owner_auth.go:383 vs :399 both `storage`). The UI can only map them to a generic sentence.
- Messages built from `err.Error()` (e.g. preferences.go `invalid_timezone`) leak English internals;
  they need a specific code plus params.

`grep -rn 'writeErr(' internal --include='*.go' | grep -v _test.go`

## 2. CJK prose in non-test Go files

| File | Lines with CJK | Likely visibility |
|---|---:|---|
| internal/models/skill.go, models.go, resolver.go | 51 / 46 / 45 | Model catalog names/descriptions shown in model pickers; check each (some model-facing) |
| internal/executor/http.go, shell.go, linter.go | 15 / 12 / 11 | Tool results shown in step details (and to the model) |
| internal/app/oauth_completion.go | 14 | Server-rendered OAuth completion page (mixed English title + Chinese heading) |
| internal/app/work_execution.go | 10 | Work execution notices |
| internal/app/mail_drafts.go, computer_control.go | 8 each | Draft/remote-control errors and notices |
| internal/app/vm_oauth.go, terminal.go | 6 each | OAuth dialog and terminal banners |
| internal/extensions/inspection_diagnostic.go, app/extension_management.go, display_cards.go, computer_resources.go | 5 each | Extension diagnostics, display card text |
| internal/app/dictation.go, computers.go, approval_expiry.go | 4 each | Errors, expiry notices in chat |
| internal/agent/agent.go | 3 | Check: model-facing vs notice |
| internal/app/local_mcp.go, app.go | 2 each | |
| work_items.go, usage.go, ssh_keys.go, prompt_budget.go, oauth_service_page.go, follow_global.go, final_answer_guard.go, conversation_task_state.go, collaboration.go | 1 each | |

`grep -rlP '[\x{4e00}-\x{9fff}]' internal --include='*.go' | grep -v _test.go`

## 3. English prose stored and shown verbatim

Chat notices (`messages.kind = "notice"`, `notice_data`), `run.error` / `run.failure.message`,
tool `outcome.message`, schedule failure details, `/api/usage` `note`, model/dictation catalog descriptions,
integration/Skill descriptions served by the API. The UI already prefers codes for run failures and tool
outcomes (`tasks:status.*`, `tasks:outcome.*`); messages are shown only in technical details.

## Recommended approach

1. **Errors**: keep `error.code`, make codes specific (split `storage`/`invalid_request` per action), add
   `error.params` (object of strings/numbers) for dynamic values. Keep `message` English as a fallback for
   old clients and logs. UI renders `errorText(cause, ns, fallback)` with `<ns>:apiError.<code>` keys and
   `{{param}}` interpolation (extend `errorText` to pass `cause.params`).
2. **Chat notices**: store `{code, params}` in `notice_data`; render from `chat:notice.<code>`; keep the
   stored English content for old clients and exports.
3. **Catalog data** (models, dictation, integrations): server sends ids; UI owns display text keyed by id,
   falling back to the server string for unknown ids.
4. **Server-rendered HTML** (OAuth completion/service pages): pick language from the account preference
   (`user_preferences.language`), else `Accept-Language`, with small Go string tables for the 7 languages;
   these pages cannot load the UI bundle.
5. **Tool/executor output** that the model also reads stays English (single string for both audiences);
   the UI explains it through codes, not by translating the raw output.
6. Add a Go test that fails on new CJK in non-test `internal/` files outside an allowlist, mirroring the UI ratchet.
