# Settings Redesign: Implementation Contract

Status: approved by owner 2026-10-09. Phase 1 (fb74527) and Phase 2 (d43d8dc) shipped in v0.1.0-rc.12. Phase 3 waits on tools-redesign WS2 and WS6.

Design reference: the owner's Claude Design document "TOFI 设置页重设计". It is not in the repo; the dispatcher gives its local path. When this contract and the design disagree, this contract wins. Section 1.3 lists the known differences.

Companion plan: `docs/agent-plan/tools-redesign.md`. This contract builds the settings shell and every page except the new Connections and Approvals internals. Those internals belong to tools-redesign WS2 (Connections UI) and WS6 (Approvals). Phase 3 here is just the rule that WS2 and WS6 must render to this design.

---

## 1. Scope

### 1.1 Information architecture (binding)

The rail has 9 items (10 for multi-account admins) in 4 groups. Tab ids change, and every caller is listed in 2.2.

| Group (i18n key) | Tab id | EN name | Content | Replaces |
|---|---|---|---|---|
| `you` | `general` | General | Account card, Appearance, Language and region (language, timezone), Notifications, an info banner linking to Advanced | `account` minus portability and purge |
| `bots` | `connections` | Connections | Phase 1: the existing `MCPSettings` + `LocalMCPPanel`, restyled. Phase 3: tools-redesign WS2 | `mcp` |
| `bots` | `skills` | Skills | Skill cards, install, **per-Bot access** (Phase 2) | `skills` |
| `bots` | `approvals` | Approvals | Phase 1: the Reviewer section (existing AutoReview control). Phase 3: tools-redesign WS6 | AutoReview section of `models` |
| `bots` | `models` | Models | Providers (Codex, OpenAI, Claude), defaults for new Bots (model and reasoning), Dictation | `models` + `dictate` + provider part of `connection` |
| `workspace` | `computer` | Computer | Existing `ComputerResources` + `ComputerPanel` | `computers` |
| `workspace` | `keys` | Keys & secrets | Existing `ComputerCredentials` | `credentials` |
| `workspace` | `usage` | Usage | Existing `UsagePanel` | `usage` |
| `system` | `advanced` | Advanced | Server connection info, data export/import, debug mode, danger zone (Start over) | `connection` (server info), `debug`, portability and purge from `account` |
| `system` | `admin` | Admin console | Existing `AdminAccounts`; same gating as today | `admin` |

Icons (`TofiIconName`): general `sliders`; connections `plug`; skills `skill`; approvals `shield-check`; models `sparkles`; computer `monitor`; keys `key`; usage `progress`; advanced `settings`; admin `group`. No two tabs may share an icon.

### 1.2 Cut by owner (do not build)

- The "Ask me in chat" toggle on Approvals.
- The Codex weekly quota bar on Models.
- "Week starts on" on General: nothing in the product reads it yet.
- The Edit-profile button and the "Signed in on this Mac" line on the account card.

### 1.3 Corrections to the design (binding)

1. **Robinhood** is Official, with Sign in (OAuth). It is not Community with an API key. Sample failure states must use some other service.
2. **"Popular with Tofi users"** becomes "Recommended". Self-hosted installs have no usage data.
3. **"Run on my computer"** copy says the Bot's computer, never "your Mac". On-computer connections run inside the Tofi computer VM.
4. **Reasoning control.** "Thinking: Light / Normal / Deep" is not a fixed three-way switch. Render a segmented control from the selected model's `reasoning_efforts` list, using the existing `effortKeys` labels. When a model offers more than 4 values, fall back to a select.
5. **No real or personal data.** Mock data, fixtures, screenshots and i18n examples must not contain real repo names, hostnames or people. Use neutral samples such as `acme/web` or "Research Bot".
6. **Service marks** stay letter monograms (design rule). Do not ship third-party logos in this work.

### 1.4 Non-goals

- No change to the computer runtime, scheduling or chat surfaces.
- No connection or approval server changes. Those are tools-redesign WS1, WS3, WS5 and WS6.
- No new design tokens. Use `ui/src/design-tokens.css` as it is. The design's colors match it, and the separate "Tofi V2" design-system palette is **not** adopted.

---

## 2. Phase 1: shell, IA and restyled pages (UI only, plus one server string fix)

### 2.1 Shell (`ui/src/SettingsShell.tsx`, `ui/src/settings-system.css`)

Keep the existing mechanics unchanged:
- `SettingsDraftContext` and the save bar
- the leave dialog
- `visited` keep-mounted pages
- the Escape / backdrop / `tofi-settings-close` handling
- the `beforeunload` guard

Changes:

- **`SettingsTab` and sections**
  - Replace the union with the ids in 1.1.
  - The `Page` type currently exempts `debug` from `description`. Make `description` required for every tab.
- **Rail (desktop, at least 701px wide)**
  - Width 248px, background `--sunk`, right border 1px `--line-soft`.
  - **Account card** at the top: avatar initial, name, email · role. Data comes from `useOwnerSession()`. Clicking it opens `general`.
  - Single-owner mode has no session owner. Then show the workspace name, or "Tofi" as a fallback, with no email.
  - The account card replaces `.settings-brand` and the "Settings" title.
  - **Group labels:** mono, 10px, 0.14em tracking, uppercase.
- **Nav item**
  - Minimum height 40px, padding 8px 10px, radius 12px.
  - Default: no border, text `--ink-soft`. Hover: background `--surface-2`.
  - Selected: background `--surface`, 2px `--line` border, `--hard-sm`, icon colored `--clay-text`, font weight 600.
  - Labels may wrap to at most 2 lines, never with an ellipsis.
- **Nav count badge**
  - Show it only when the count is above 0. Mono 10px pill.
  - `connections`: count of connections whose status is not OK, colored `--honey` with `--on-block` text.
    - Phase 1 source: the same data `MCPSettings` already loads.
    - Lift it into a small hook, `useConnectionAttention()`, that reads `/api/extensions/mcp` once on shell mount and on the `config` workspace event. It must make no Runner or VM request.
  - `approvals`: pending approval count colored `--iris`. Phase 1 has no source, so hide it.
- **Header**
  - Title 24px/1.15 in `--display` (Fredoka), description 14px `--ink-soft` with at most 70 characters per locale line.
  - Optional page primary action slot: at most one primary button, plus the close button.
- **Body:** padding 22px 30px 30px; sections 22px apart. At content widths of 1000px or more, a page may use the two-column `.settings-two` layout (main 1.35fr, side 1fr). Narrower, it stacks.
- **Mobile (700px and narrower)**
  - Remove `<select id="settings-category">`. The settings home becomes a grouped list:
    - The account card as the first row.
    - Then each group as a `.settings-mlist` card of rows: icon tile, name, right-side value hint, chevron.
  - Value hints:
    - models: the provider name of the global model.
    - computer: the computer state label.
    - keys: the number of variables.
    - usage: nothing in Phase 1.
  - Tapping a row pushes the page. The top bar shows back, the centered title and close.
  - State: add `mobileView: "home" | "page"` in the shell. Browser back is not wired.
  - Desktop deep links (2.2) still open the target page directly with `mobileView="page"`.
- **Motion** (all disabled under `prefers-reduced-motion`)
  - The selected rail indicator is one element that slides between items (translateY and height), 220ms ease-out.
  - On page change the new content fades in from 8px below over 160ms. No horizontal slide on desktop.
  - On mobile the page pushes in from the right while the list shifts left 30% and dims, 280ms `cubic-bezier(.22,1,.36,1)`. Back reverses it.
  - Buttons that have a hard shadow: on hover, lift 1px and grow the shadow; on press, translate 2px 3px and remove the shadow; 120ms.

### 2.2 Tab-id migration (every caller)

| Old | New | Callers to update |
|---|---|---|
| `account` | `general` | App.tsx:433 initial state; App.tsx:1473 `importBotPackage` (→ `advanced`, because portability moved); App.tsx:1728 sidebar settings; App.tsx:1845 BotPanel `onExportData` (→ `advanced`, keep `portabilityBotID`) |
| `mcp` | `connections` | App.tsx:1127-1131 `tofi:open-tool-settings`; App.tsx:1625 `onOpenTools` in TaskRunBlock; QuestionCard.tsx:127 (event unchanged) |
| `connection` | `models` for provider and Codex callers; `advanced` for server info | App.tsx:1127-1131 `tofi:open-codex-settings` (→ `models`); App.tsx:1728 sidebar "connection" entry (→ `models`); ModelSettings.tsx:24 `openConnection`; TaskRunBlock.tsx:168 `open_codex` |
| `dictate` | `models` | none outside the shell |
| `computers` | `computer` | grep |
| `credentials` | `keys` | grep |
| `debug` | `advanced` | grep |

Acceptance: `grep -rnE '"(account|mcp|connection|dictate|computers|credentials|debug)"' ui/src --include=*.tsx` shows no settings-tab uses.

### 2.3 Page content (App.tsx:1846 `renderPage`)

Replace the inline ternary with a `switch` in a new `ui/src/settings/SettingsPages.tsx`. It receives the props App currently passes (`bots`, `timezone`, `usageBotId`, `portabilityBotID`, `portabilityFile`, and the rest).

Shared components go in a new `ui/src/settings/` folder, with styles in `ui/src/settings/settings-components.css`:

- `SettingsSection` (h4 16px display, optional count or hint, optional description)
- `SettingsCard` (1px soft border, radius 18px, rows separated by 1px lines)
- `SettingsRow` (label 14px/600, description 12.5px muted, control right-aligned; on mobile the control drops below, except switches and segmented controls)
- `StatusBadge` (props `state: ok|need|bad|asleep|testing`; icon plus text, never color alone; text uses the `--*-text` token on a 14-18% tint)
- `Banner` (props `tone: error|warn|info`; icon, bold "what happened", one line "what to do", at most one action button, optional "Technical details" disclosure in mono; `role="alert"` only for error)
- `DangerZone` (1.5px dashed danger border, mono uppercase title)
- `Segmented`

Existing pages move into these components. Keep their logic and draft registration exactly as they are.

- **general**
  - Account card: avatar, name, email · role; Sign out stays a ghost button.
  - Appearance: the existing `AppearancePicker`, restyled as three thumbnail cards with selected = 2px edge plus `--hard-sm`.
  - "Language and region" card with two rows: `LanguageSetting` and `TimezoneSetting`. Their behavior is unchanged: language applies immediately, timezone uses a draft.
  - Notifications row.
  - An info `Banner`: "Looking for export, server info or Start over? They moved to Advanced.", with an "Open Advanced" button.
  - Legacy-archive entry: keep it if it is present today.
- **models**
  - Two columns on wide screens.
  - Left: the Providers card. One row per provider from `/api/providers` plus Codex, each with a status badge and either a connect action or an Add key action. Reuse the logic in `ProviderSettings.tsx` and `CodexPanel`; the key entry stays inline as it is today.
  - Left: "Defaults for new Bots", which is `ModelDefaults` with the reasoning control from 1.3 #4.
  - Right: the Dictation card (`DictationSettings`).
  - The AutoReview section is removed from this page.
- **approvals** (Phase 1)
  - The `AutoReviewSettings` control under the heading "Reviewer".
  - Fix the hardcoded "AutoReview" heading and draft label (AutoReviewSettings.tsx:31-32) by moving them to i18n keys.
  - Mode values stay `off|shadow|auto` until tools-redesign D3 is decided.
  - Below it, one info `Banner` explaining that trust defaults and always-allowed rules arrive with the Connections update. The text goes through i18n.
- **connections** (Phase 1)
  - The existing `ExtensionPanel kind="mcp"`. Header title "Connections"; the "Add" primary button moves into the header action slot if it can do so without logic changes. Otherwise leave it.
  - `ExtensionPanel.tsx:43` uses the unstyled `.error-banner`. Swap it for `Banner tone="error"`.
- **skills:** the existing `SkillsSettings`, restyled to cards. Per-Bot access is Phase 2.
- **computer, keys, usage:** existing components inside the new body. CSS changes only where needed to fit the new cards (no logic changes).
- **advanced**, in this order:
  - a Server card (`ConnectionInfo`)
  - a Data card (`PortabilitySettings`, keeping the `portabilityBotID` / `portabilityFile` props)
  - a Debug row (`DebugSettings` toggle, still localStorage)
  - `DangerZone` holding `WorkspacePurgeSettings`; its logic (typing PURGE) is unchanged.

### 2.4 Server string fix (dictation)

`internal/app/dictation.go:32-33` carries Chinese `Description` and `Cost` strings that the UI renders verbatim.

- Server: remove `Description` and `Cost` from the payload. If anything else reads them, keep the fields but stop sending them; check with grep.
- UI: render the description from `settings:dictation.model.<id>.hint`, and the price from `settings:dictation.model.<id>.price`, in all 7 locales.
- For an unknown id, show nothing (no fallback text).
- Update `dictation_test.go` if it asserts on these fields.

### 2.5 i18n

- New and renamed keys go in `ui/src/locales/<lang>/settings.json` for all 7 languages: `shell.group.{you,bots,workspace,system}` and `shell.page.<newid>.{name,description}`. Delete keys no longer used.
- German must fit. Check `Verbindungen`, `Genehmigungen` and `Schlüssel & Geheimnisse` in the 248px rail with no ellipsis; at most 2 lines is allowed.
- `npm --prefix ui run test:i18n` must pass with **no new baseline entries**. The baseline file stays empty.

### 2.6 Phase 1 acceptance (all required)

1. `go build ./... && go test ./...` exits 0.
2. `npm --prefix ui ci && npm --prefix ui run typecheck && npm --prefix ui run test:i18n && npm --prefix ui run build` exits 0. Also run `test:auto-review`, `test:model-catalog`, `test:portability`, `test:design-icons`, `test:final-v2-tokens` and `test:v2-palette`; each exits 0.
3. New `ui/scripts/test-settings-shell.mjs`, wired as `npm run test:settings-shell`. It is a Playwright fixture in the style of `test-auto-review.mjs`, with `/api/*` stubbed. It asserts:
   - The rail shows exactly the 9 tabs in 1.1 in order, and `admin` appears only with a multi-account admin session.
   - Each tab renders its heading and description, with no duplicate icons.
   - Dispatching `tofi:open-tool-settings` opens `connections`, and `tofi:open-codex-settings` opens `models`.
   - At 390px: no `#settings-category` element; home list → tap Models → Models page → back → home.
   - The dirty-draft leave dialog still appears when switching away from a dirty Timezone draft.
   - `prefers-reduced-motion: reduce` shows no running CSS animations or transitions on page switch.
   - With locale `de`, no rail label has `text-overflow: ellipsis` clipping (check `scrollWidth <= clientWidth` per line box), and every rail item is at most 2 lines.
4. Screenshots, delivered with the PR: every tab at 1440×900 and 390×844, light and dark (`data-theme`), under `artifacts/settings-redesign/phase1/`, gitignored. The reviewer compares them with the design side by side.
5. No hardcoded user-facing strings added (enforced by check-i18n). No real or personal names in fixtures.

---

## 3. Phase 2: per-Bot skill access (server and UI)

Today skills are workspace-wide by design (`internal/extensions/mcp.go:345-347`). This phase adds an **opt-in** restriction while keeping the default "All Bots".

### 3.1 Storage

- New SQLite table, created in a `migrateSkillAccess` called next to the other migrations in `app.go`:
  `skill_access(skill_name TEXT NOT NULL, bot_id TEXT NOT NULL, PRIMARY KEY(skill_name, bot_id))`.
  - A skill with no rows is available to all Bots.
  - A skill with rows is available only to those Bots.
- Also delete the dead `.enabled.json` path:
  - `Manager.SetSkillEnabled` and `SkillView.Enabled` in `internal/extensions/management.go:49-76,456-475`.
  - Keep the test that asserts the legacy file is ignored (`management_test.go:93`).
- When a skill is deleted (`DeleteSkill`), delete its rows.
- When a Bot is deleted or archived, keep the rows; archived Bots simply don't run.
- When a skill is reinstalled under the same name, keep the rows.

### 3.2 API (`internal/app/extension_management.go`)

- `GET /api/extensions/skills` gains `access: {mode: "all"} | {mode: "selected", bot_ids: string[]}` per skill.
- New `PUT /api/extensions/skills/:name/access` with body `{mode, bot_ids}`.
  - Validate that the skill exists and that every bot id exists and is not archived. An unknown id returns 400 `unknown_bot`.
  - `mode: "selected"` with an empty `bot_ids` returns 400. Use DELETE semantics through `mode: "all"` instead.
  - Write in one transaction and emit `insertWorkspaceEventTx(tx, workspaceScopeConfig, now())`.
- Requests must come from the owner session or an admin, using the same auth as the other `/api/extensions` writes.

### 3.3 Run path

- Pass an access filter into the extensions manager, so that `prepareMode` (`internal/extensions/mcp.go:301`) drops skills the current `botID` cannot use **before** `discoverableSkillTools` and `skillIndex` build the tools and instructions.
- `read_skill` and `read_skill_file` must also refuse a skill outside the filter, so the model can't reach it by guessing the name.
- Update the `manage_extensions` tool description (`internal/app/extension_tools.go:50`) to say skills can be limited to selected Bots.
- Portability keeps excluding skills. Note in `portability.go:29` that `skill_access` is excluded with them.

### 3.4 UI

On the Skills page, each skill card gets a "Who can use it" row:
- Bot avatar chips, a summary such as "All Bots" or "3 of 4 Bots", and a Change button.
- Change opens a small sheet with an "All Bots" / "Only selected Bots" radio and a Bot checklist (non-archived Bots only).
- The sheet saves through the PUT.
- i18n in all 7 locales.

### 3.5 Phase 2 acceptance

1. Go tests:
   - `skill_access` CRUD, plus cascade on `DeleteSkill`.
   - PUT validation (unknown bot, empty selected list, unknown skill).
   - A run for a Bot outside the list gets no skill tools and no skill index entry, and `read_skill` refuses that skill.
   - A run for an allowed Bot is unchanged.
   - The default (no rows) leaves every Bot with the skill.
   - `TestRunPromptBudget*` still passes.
2. `npm run test:settings-shell` is extended: change access to one Bot; the card shows "1 of N Bots"; reload keeps it.
3. Real check on the test VM (VM 106) after an rc install:
   - Two Bots, one skill limited to Bot A.
   - Ask Bot B to use it: it reports that it has no such skill.
   - Bot A uses it.

---

## 4. Phase 3: Connections and Approvals internals (tools-redesign WS2 and WS6)

There is no separate work here. When WS2 and WS6 are implemented, their UI must:
- render inside this shell using the Phase 1 components (`StatusBadge`, `Banner`, `DangerZone`, `SettingsCard`, `Segmented`);
- follow the design's screens 02-11: the list with an attention banner, the empty state, the catalog with a "Needs your own developer app" section, the three-step connect sheet, the test, failure and done states, connection detail with the trust table, and Approvals;
- respect corrections 1.3 #1-3.

The nav badges from 2.1 get their real sources then.

---

## 5. Known traps

- **Visited pages stay mounted.** `useSettingsDraft` registrations from hidden pages persist, and drafts are keyed by `page`. When a component moves to a new tab id (for example DictationSettings into `models`), its registration must use the new page id. Otherwise the save bar shows on the wrong tab.
- **Final fallback in the old ternary.** The old `renderPage` ternary falls through to `ExtensionPanel kind={page}`. The new `switch` must have an explicit case for every tab and a `never` exhaustiveness check.
- **Old CSS can override the new styles.** `styles.css` still holds old `.settings-tabs` rules (241, 289-290, 350-352), and `interaction-system.css:106-109,140,145-146` holds more. Delete the ones the new shell no longer uses, and confirm by screenshot that nothing old leaks through.
- **No connection status probe.** `useConnectionAttention()` must not call `/api/extensions/local-mcp` or anything that wakes the computer. Opening settings must never wake a hibernated computer.
- **CI only runs part of the UI tests.** CI runs only typecheck, i18n and build. The other UI `test:*` scripts must be run locally, and their exit codes reported in the PR.

## 6. Dispatch

- **Phase 1:** one Sonnet worker in a worktree, on branch `feat/settings-shell`.
- **Phase 2:** server work (run-path filter and API) is written by Sonnet, then reviewed separately by Fable, because it is an access control change. UI by Sonnet.
- **Release:** after each phase merges, cut an rc, install it on test VM 106, and do the real checks before deploying to production.
