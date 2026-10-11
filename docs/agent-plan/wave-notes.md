# Release wave v0.1.1 (shipped 2026-10-10)

Shipped as v0.1.1 (tag on 1ec7e00) after the owner approved; production upgraded the same day. Earlier text kept for the record: The owner reviews this list before a release is cut (no release until the owner says so).

| # | Change | Status | Verification | Commits |
|---|---|---|---|---|
| 1 | Bundle Fredoka and JetBrains Mono fonts locally (were loaded from Google Fonts and failed silently); OFL notices added | merged | typecheck, i18n, build, test:empty-states, go test: exit 0 | f428d52 |
| 2 | First-run hero (4 live cats, dot-grid card); 19 empty states get a contextual live cat; at most 6 live cats at once | merged | test:empty-states (64 screenshots, reduced motion, 390px), settings-shell: exit 0 | f428d52 |
| 3 | Onboarding sheet: Welcome → Connect a model (ChatGPT device sign-in or API key, required) → pick connections (optional) → first Bot DM; resumable, server-side state | merged | go test, test:onboarding (13 checks incl. 390px, de, reduced motion), settings-shell, empty-states, integration-catalog, mcp-oauth: exit 0 | 7e78754 |
| 4 | "Connect a model to start chatting" composer banner and a "Finish setup" sidebar chip | merged with #3 | as #3 | 7e78754 |
| 5 | Admin console: capacity card, account list → detail (role, computer disk, reset password, danger zone); mobile pushed page | merged | go test, settings-shell (admin flow), i18n: exit 0 | 08048a0 |
| 6 | Delete account (after deactivation): encrypted export first (importable in Data transfer with the passphrase; link valid 30 days), then computer + disk removed, quota and slot freed, data removed; resumable; legacy owner refused | merged after Fable review (no blockers; 1 major + 7 minor fixed) | go test, microvm suite, test:portability, settings-shell: exit 0 | 08048a0 |
| 7 | Robinhood added to the connection catalog (official MCP, sign in with Robinhood); first Bot asks its name and job in chat | merged with #3 | as #3 | 7e78754 |
| 8 | Settings polish: every native control and fallback font removed (1,119 audit violations → 0); Data transfer and Server rebuilt as cards; one Details per card; consistent input widths and footer buttons; Enter decrypts on import; new `test:settings-polish` audit (156 runs, also catches clipped scroll boxes and floating Details) | merged | settings-polish 0 violations, settings-shell, portability, onboarding, empty-states, auto-review, go test: exit 0 | 91cd754 |

Known flaky Go tests seen this wave (pass on rerun): TestCancelDoesNotBecomeDone, TestUsageRecordsRunContextAndAgentTool; see installer-backlog item 3.

## Acceptance on a fresh install (v0.1.1-rc.1, test VM, 2026-10-10)

- One-line install of v0.1.1-rc.1: exit 0. `tofi status` shows the new compact view.
- **Setup:** key first, then register. The admin was created, the onboarding sheet opened, and the welcome step showed 3 live cats.
- **Fonts:** Fredoka 500–700 and JetBrains Mono 400/600 have status `loaded`.
- **Model step, invalid OpenAI key:** shows the inline "That key didn't work…" error.
- **"Not now":** shows the first-run hero (4 cats), the sidebar sleeping cat, and a "Finish setup 2 of 3" chip.
- **Delete account:** deactivate → delete, then the account is gone. The promised quota went 16 GiB → 8 GiB. The export downloaded as `tofi-<user>-<date>.tofi`, and a tampered link returned 404.
- **Export → import round trip** (member account with one Bot):
  - The export counts showed 1 `bot_config` and 1 conversation.
  - Data transfer: Decrypt and view (passphrase in lower case, no dashes, with spaces) → Preview import → Create copies. The Bot was restored in the admin account.
  - After acknowledgement, the passphrase endpoint returns 409.
- **Login limiter:** works. Repeated logins from one IP got 429; the test had to wait 15 minutes.
- **Not verified (needs a real model):**
  - the first Bot asking its name and job in chat;
  - ChatGPT device sign-in end to end;
  - a real OAuth connection.

### Small issues seen during acceptance (not fixed yet)

- ~~Enter in the import passphrase field does not decrypt~~ (fixed in #8).

## Next wave (after v0.1.2) — merged to main, not released

1. **Onboarding connect step** (fix/onboarding-connect): service letter marks, "Connected ✓", no "Connect 0", an all-set screen with Continue, tablet viewports. test:onboarding (20 checks) and test:settings-polish (328 runs) exit 0, Chromium only.
2. **Tool refusals are definite** (fix/tool-outcome-ux): local argument refusals show "Not executed" and no longer block the corrected retry; compact uncertain card; "Save the Bot's profile" label; thinking line not cut mid-sentence.
3. **Reasoning kept within a task** (feat/run-resume-p1, Phase 0–1 of run-resume.md): microCompact no longer strips reasoning; Anthropic history edits skipped while thinking replays; compaction at 0.70 replays the transcript and adds a "Current line of thinking" section; compaction calls are accounted; context breakdown recorded. Live-model acceptance not run yet.
4. **Bot replies no longer vanish** (fix/mid-run-message-label + feat/progress-tag): text a Bot writes mid-task stays visible as an answer; only text the model wraps in `<progress>…</progress>` folds away when the task ends. Tags never reach the user (stream, final, budget, approval-expiry and schedule-failure paths; literal inside markdown code). Progress notes do not count as unread. Old progress rows still fold. Follow-up: compaction leaves room for max_tokens; tool_choice none on the compaction call.
5. **Scheduled run blocked by a failed page open** (fix/browser-no-page + fix/schedule-uncertain-fence): navigate opens a page when none exists; guest refusals before acting are recorded as not executed; navigate/new/switch are observations; in unattended runs only an uncertain *effect* fences later *effects*, and reads (trusted read-only or readOnlyHint) are never fenced.
6. **Opening the computer wakes it** (fix/wake-on-open, after rc.2): opening the computer window wakes a hibernated computer (POST /api/computers/firecracker/wake) and connects when ready; no top banner for asleep/going to sleep; the small window now clears composer notices.
7. **Connections show last known status** (fix/connections-cached-status): cards open on the persisted last check ("Connected · 49 tools · Checked …"), re-verify quietly with one retry before showing a problem; plug animation removed; key-label overlap fixed.
8. **Auto-review failures are named** (fix/auto-review-diagnostics): unavailable reviews store and show a category (timeout, provider_error, malformed_response, …); one retry only for malformed/empty replies; a whole-answer code fence is unwrapped, everything else stays strict; reviewer asked for a short English reason (Chinese reasons could pass the 600-byte cap); reviewer usage recorded; Chrome no longer shows "Restore pages?" after a cold boot.
9. **Repeat reads are no longer refused** (fix/mcp-read-repeat, incident on v0.1.3-rc.3): a Bot that fetched a page, updated it and fetched it again to verify was refused twice (`approval_already_claimed`), even with different arguments. Cause: the approval is one-shot per exact call, and the refusal was recorded as an *uncertain effect*, which the recovery guard then applied to every later call of that operation whatever its arguments. Now a refused call is `denied` / `not_executed` (it fences only the identical call and never fences later effects in scheduled runs), and a read-only proposal (owner-trusted read-only or remote readOnlyHint, the same predicate as the scheduled fence) is reviewed afresh on its own card and claim when repeated; an identical effect is still refused before dispatch.
