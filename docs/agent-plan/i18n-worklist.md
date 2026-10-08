# Web UI i18n — conversion worklist

Phase 1 (infrastructure, branch `feat/i18n`) is done: i18next runtime, lazy catalogs, language
preference (Settings › General, stored server-side in `/api/preferences.language`), Intl formatting
helpers, the checker (`npm run test:i18n`, in CI) and reference conversions. Phase 2 is this list:
five parallel batches that move the remaining hardcoded text into catalogs.

Rules every batch follows are in [`ui/src/locales/README.md`](../../ui/src/locales/README.md). Read it first.

Counts are the checker's numbers from `ui/scripts/i18n-baseline.json` at the end of phase 1
(CJK text runs in strings/JSX, comments ignored; "English phrases" are obvious English UI text).
Total: 67 files, 2382 CJK runs, 10 English phrases.

## Already converted in phase 1 (reference examples)

| File | Namespace | Notes |
|---|---|---|
| `ui/src/OwnerSession.tsx` | `auth`, `common` | Sign-in/setup/password gate, account menu. Server error codes -> keys, `Trans` with `<code>`. |
| `ui/src/TaskIssueCard.tsx`, `ui/src/TaskRunBlock.tsx`, `ui/src/taskIssuePresentation.ts` | `tasks` | Replaced the old `taskText(locale, zh, en)` helper; code -> key maps; plurals; `locale?: Language` pin for tests. |
| `ui/src/MailDraftCard.tsx` | `mail` | Full component; times via `formatClock`. |
| `ui/src/UserTimezone.tsx`, `ui/src/LanguageSetting.tsx` | `settings` | Settings › General panels. |
| `ui/src/api.ts`, `ui/src/timezone.ts` | `common` | Request-failure fallback, unknown time. |

Partially touched in phase 1 (the batch that owns the file finishes it): `QuestionCard.tsx` and
`toolTimeline.ts` (the `taskText` call sites are converted), `App.tsx` (task status strings, Language
setting mount), and display-locale routing (`intlLocale()`) in `App.tsx`, `UsagePanel.tsx`,
`TeamBoard.tsx`, `scheduledRunMetadata.ts`, `ProviderSettings.tsx`, `postmark.ts`, `QuestionCard.tsx`.

## Ownership

Each batch owns its **files** and its **namespaces** exclusively: it edits only the files in its table,
plus `ui/src/locales/en/<ns>.json` and `ui/src/locales/zh-CN/<ns>.json` for its namespaces (and the test
scripts that load its files). No two batches edit the same file.

Nobody edits during phase 2: `ui/src/i18n/*`, `ui/src/locales/*/index.ts`, `common.json` in any
language, other batches' namespace files, `ui/scripts/check-i18n.mjs`, `ui/scripts/i18n-baseline.json`
(counts only go down, so a stale baseline still passes; the integrator ratchets it after merging).
Reading existing keys from another namespace (for example `common:action.retry`, `tasks:phase.reviewing`)
is allowed when the key already exists on the base branch; otherwise duplicate the text in your own namespace.
The integrator dedupes into `common` afterwards.

zh-TW, ja, ko, de, fr catalogs stay untouched in phase 2 (English fallback). Translation is phase 3.

## Batches

| Batch | Namespaces | Files | CJK runs | English phrases |
|---|---|---:|---:|---:|
| A | `chat` | 7 | 513 | 7 |
| B | `tasks`, `schedules`, `work`, `mail` | 19 | 495 | 0 |
| C | `settings` | 16 | 482 | 1 |
| D | `extensions` | 9 | 451 | 2 |
| E | `computer`, `bots` | 16 | 441 | 0 |

### Batch A — namespaces: `chat` — 513 CJK runs, 7 English phrases

| File | CJK runs | English phrases |
|---|---:|---:|
| `ui/src/App.tsx` | 438 | 7 |
| `ui/src/MessageAttachment.tsx` | 21 | 0 |
| `ui/src/ViewOnlyChat.tsx` | 20 | 0 |
| `ui/src/DeleteConversationDialog.tsx` | 15 | 0 |
| `ui/src/MessageReactions.tsx` | 14 | 0 |
| `ui/src/MessageMarkdown.tsx` | 4 | 0 |
| `ui/src/WebFileDropOverlay.tsx` | 1 | 0 |

### Batch B — namespaces: `tasks`, `schedules`, `work`, `mail` — 495 CJK runs, 0 English phrases

| File | CJK runs | English phrases |
|---|---:|---:|
| `ui/src/WorkPanel.tsx` | 92 | 0 |
| `ui/src/QuestionCard.tsx` | 80 | 0 |
| `ui/src/questionTimeline.ts` | 55 | 0 |
| `ui/src/TeamBoard.tsx` | 41 | 0 |
| `ui/src/toolTimeline.ts` | 38 | 0 |
| `ui/src/ScheduledTaskRow.tsx` | 30 | 0 |
| `ui/src/scheduledRunMetadata.ts` | 27 | 0 |
| `ui/src/DisplayCard.tsx` | 23 | 0 |
| `ui/src/userForm.ts` | 17 | 0 |
| `ui/src/WorkExecution.tsx` | 16 | 0 |
| `ui/src/ConversationTaskStatus.tsx` | 15 | 0 |
| `ui/src/ScheduleEditor.tsx` | 11 | 0 |
| `ui/src/SecretInputCard.tsx` | 10 | 0 |
| `ui/src/workExecutionState.ts` | 8 | 0 |
| `ui/src/ScheduledRun.tsx` | 8 | 0 |
| `ui/src/runFamily.ts` | 7 | 0 |
| `ui/src/ApprovalCard.tsx` | 7 | 0 |
| `ui/src/postmark.ts` | 6 | 0 |
| `ui/src/displayMetadata.ts` | 4 | 0 |

### Batch C — namespaces: `settings` — 482 CJK runs, 1 English phrases

| File | CJK runs | English phrases |
|---|---:|---:|
| `ui/src/PortabilitySettings.tsx` | 89 | 0 |
| `ui/src/UsagePanel.tsx` | 62 | 0 |
| `ui/src/ProviderSettings.tsx` | 56 | 1 |
| `ui/src/ModelSettings.tsx` | 50 | 0 |
| `ui/src/SettingsShell.tsx` | 48 | 0 |
| `ui/src/PermissionCoach.tsx` | 32 | 0 |
| `ui/src/AdminAccounts.tsx` | 31 | 0 |
| `ui/src/ConnectionInfo.tsx` | 21 | 0 |
| `ui/src/AutoReviewSettings.tsx` | 17 | 0 |
| `ui/src/DictationSettings.tsx` | 16 | 0 |
| `ui/src/WorkspacePurgeSettings.tsx` | 15 | 0 |
| `ui/src/portabilitySelection.ts` | 14 | 0 |
| `ui/src/useDictation.ts` | 11 | 0 |
| `ui/src/InteractionSystem.tsx` | 9 | 0 |
| `ui/src/portabilityCrypto.ts` | 8 | 0 |
| `ui/src/modelCatalog.ts` | 3 | 0 |

### Batch D — namespaces: `extensions` — 451 CJK runs, 2 English phrases

| File | CJK runs | English phrases |
|---|---:|---:|
| `ui/src/integrationCatalog.ts` | 138 | 0 |
| `ui/src/MCPSettings.tsx` | 125 | 2 |
| `ui/src/LocalMCPPanel.tsx` | 100 | 0 |
| `ui/src/ExtensionPanel.tsx` | 33 | 0 |
| `ui/src/IntegrationBrowser.tsx` | 18 | 0 |
| `ui/src/mcpOAuthRoute.ts` | 17 | 0 |
| `ui/src/VMOAuthDialog.tsx` | 12 | 0 |
| `ui/src/skillCatalog.ts` | 6 | 0 |
| `ui/src/mcpTokenHeaders.ts` | 2 | 0 |

### Batch E — namespaces: `computer`, `bots` — 441 CJK runs, 0 English phrases

| File | CJK runs | English phrases |
|---|---:|---:|
| `ui/src/ComputerCredentials.tsx` | 91 | 0 |
| `ui/src/BotDesktopPanel.tsx` | 82 | 0 |
| `ui/src/ComputerResources.tsx` | 43 | 0 |
| `ui/src/ComputerPanel.tsx` | 42 | 0 |
| `ui/src/MemoryPanel.tsx` | 35 | 0 |
| `ui/src/RemoteDesktopControl.tsx` | 33 | 0 |
| `ui/src/TerminalPanel.tsx` | 28 | 0 |
| `ui/src/ArchivePanel.tsx` | 20 | 0 |
| `ui/src/BotInspector.tsx` | 18 | 0 |
| `ui/src/BotAvatarPicker.tsx` | 15 | 0 |
| `ui/src/botPackage.ts` | 14 | 0 |
| `ui/src/FloatingDesktop.tsx` | 11 | 0 |
| `ui/src/BotIdentityCard.tsx` | 4 | 0 |
| `ui/src/TerminalScreen.tsx` | 3 | 0 |
| `ui/src/DesktopVideo.tsx` | 1 | 0 |
| `ui/src/DesktopPointerMarker.tsx` | 1 | 0 |

## Batch-specific notes

**A (`chat`)** — `App.tsx` is one file with many embedded components (conversation list, Composer,
MembersPanel, BotPanel, CodexPanel, NotificationSetting, DebugSettings, tool activity runs). All of
their text goes into `chat` under an area per component (`chat:composer.*`, `chat:members.*`,
`chat:botPanel.*`, `chat:codex.*`, `chat:notifications.*`, `chat:debug.*`), even where the panel is shown
in Settings. Moving components out of `App.tsx` is out of scope. `formatHoverTime`/`formatTime`/`formatDay`
already use `intlLocale()`; keep the user timezone argument. `scripts/test-reply-ack.mjs` and
`scripts/test-multiplex-events.mjs` read `App.tsx`/compiled App source by text; keep the callback blocks
they slice (`const handleWorkspaceEvent = useCallback(` etc.) intact. `test-multiplex-events` already
fails on `main` (`selectConversation is not defined`); do not mask it.

**B (`tasks`, `schedules`, `work`, `mail`)** — `QuestionCard.tsx` already uses `useTranslation("tasks")`
for its migrated strings; continue in the same file. `toolTimeline.ts`, `questionTimeline.ts`,
`userForm.ts`, `scheduledRunMetadata.ts`, `runFamily.ts` are pure modules: use `i18n.t("<ns>:…")` at call
time (or return keys/codes and translate in the component). Tests that load them
(`test-tool-timeline`, `test-question-timeline`, `test-auto-review`, `test-user-form`,
`test-scheduled-run-metadata`, `test-run-family`, `test-display-metadata`, `test-task-issue-ux`,
`test-task-issue-followup`) assert Chinese copy: keep them on `openUiModules({ language: "zh-CN" })`
and convert any `tsc --ignoreConfig` loader to it. The user-form question copy belongs to `tasks`;
postmark/display cards to `mail`.

**C (`settings`)** — `SettingsShell.tsx` holds the settings navigation (names + descriptions); keep page ids
unchanged. `UsagePanel.tsx` numbers/dates already go through `intlLocale()`; currency stays USD.
`PermissionCoach.tsx` is the macOS permission coach for Dictate/screen recording. `AdminAccounts.tsx`
role labels keep "Admin". `test-portability` covers the portability files; `test-model-catalog` covers
`modelCatalog.ts` (shared with batch E's `botPackage.ts`: edit only your file).

**D (`extensions`)** — `integrationCatalog.ts` (138 runs) is catalog data: descriptions of third-party
integrations. Give each entry keys like `extensions:catalog.<id>.description`; vendor/product names stay as
written. Server-provided names and descriptions shown verbatim stay untouched (see
`docs/agent-plan/i18n-server-strings.md`). `test-integration-catalog`, `test-oauth-completion`,
`test-mcp-oauth` load these files. `test-design-icons` already fails on `main` (ad-hoc SVG in
`MCPSettings.tsx`); do not fix it in this batch.

**E (`computer`, `bots`)** — `scripts/test-task-issue-ux.mjs` step 8 asserts `BotDesktopPanel.tsx` and
`MemoryPanel.tsx` are byte-identical to commit `fcb2157`; that guard was for an older review and blocks this
batch. Replace it with a check that those files contain no hardcoded CJK (the checker already does) and say
so in the PR. `scripts/test-remote-desktop-control.mjs` string-replaces the compiled imports of
`RemoteDesktopControl.tsx`; when you add `import { useTranslation } from "./i18n"`, stub it in that harness
(`const useTranslation = () => ({ t: key => key })`) and assert on keys, or move the test to
`openUiModules`. `test-desktop-video-recovery`, `test-desktop-video-visibility`, `test-floating-desktop`,
`test-model-catalog` (`botPackage.ts`) also load batch E files.

## Worker procedure (copy into each batch prompt)

1. `git fetch origin && git checkout -b feat/i18n-<batch> origin/main` (after `feat/i18n` is merged).
   `cd ui && npm ci`.
2. Read `ui/src/locales/README.md` and one reference conversion (`OwnerSession.tsx` for components,
   `taskIssuePresentation.ts` for code -> key maps).
3. For each file in your table, largest first:
   - Add `const { t } = useTranslation("<ns>")` (or `["<ns>", "common"]`) to each component; in non-component
     code use `i18n.t("<ns>:…")` at call time. Import from `./i18n`, never from `react-i18next`.
   - Move every user-visible string: JSX text, `aria-label`, `title`, `placeholder`, `data-hint`, `alt`,
     error/notice strings set into state, confirm dialogs, toasts, `document.title`.
   - zh-CN value = the current Chinese text verbatim. en value = natural English (tone rules in the README).
   - One key per sentence with `{{variables}}`; no string concatenation; plurals with `_one/_other` in en
     and `_other` only in zh-CN; inline markup via `Trans` with named tags.
   - Server error codes: `errorText(cause, "<ns>", t("…fallback"))` and `apiError.<code>` keys.
   - Display dates/numbers: `src/i18n/format.ts` (`formatClock`, `formatDateTime`, `formatNumber`,
     `formatRelativeTime`) or `intlLocale()`; keep the user timezone.
   - Do not translate user content, bot names, model ids, server-sent prose, diagnostics JSON, or
     model-facing prompt text.
4. After each file: `npx tsc --noEmit -p .` (unknown keys fail here) and
   `npm run test:i18n` (your file's count must drop; the target is 0).
5. Update tests that load your files: pin `zh-CN` with `openUiModules`, keep existing Chinese assertions,
   add one `en` assertion per converted surface where cheap.
6. Before handing back: `npx tsc --noEmit -p . && npm run test:i18n && npm run build`, the tests listed for
   your batch, and `node scripts/check-i18n.mjs` output showing your files at 0. Report exit codes, not
   PASS-line counts. Do not edit `i18n-baseline.json`.
7. Commit per file or small group; do not push or merge unless told to.

## Integration (after all batches)

1. Merge batches in any order (disjoint files and namespace files).
2. `npm run test:i18n -- --update-baseline`; target: no files left (then delete the English-phrase
   allowances that are real false positives by renaming, not by raising counts).
3. Dedupe repeated generic strings into `common` (Cancel, Save, Retry, Loading…), one PR.
4. Phase 3: translate zh-TW, ja, ko, de, fr from `npm run test:i18n -- --untranslated <lang>`; add each finished
   language to `COMPLETE_LANGUAGES`.
5. Phase 4: server strings per `docs/agent-plan/i18n-server-strings.md`.
