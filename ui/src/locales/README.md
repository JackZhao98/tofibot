# Web UI localization

Languages: `en` (source of truth and fallback), `zh-CN`, `zh-TW`, `ja`, `ko`, `de`, `fr`.
Runtime: i18next + react-i18next (`src/i18n/`). Gate: `npm run test:i18n` (`scripts/check-i18n.mjs`), also run in CI.

## Layout

```
src/i18n/languages.ts     language list, namespaces, endonyms, browser matching (no deps)
src/i18n/index.ts         i18next setup, lazy loading, preference, useLanguage()
src/i18n/format.ts        Intl helpers: formatClock, formatDateTime, formatNumber, formatRelativeTime, formatList, intlLocale
src/i18n/errors.ts        errorText(cause, ns, fallback): server error code -> catalog text
src/i18n/i18next.d.ts     en catalogs are the key schema (tsc rejects unknown keys)
src/locales/<lang>/<namespace>.json
src/locales/<lang>/index.ts   one lazy chunk per language (do not edit)
```

Every language is a lazy chunk, English included; the main bundle carries no catalog
text. `main.tsx` renders after `i18nReady`. Languages not listed in
`COMPLETE_LANGUAGES` also load English as the fallback for keys they lack.

| Namespace | Area |
|---|---|
| `common` | Shared chrome: navigation, theme, generic actions, time fallbacks, `apiError.request_failed`. Frozen during parallel batches; only the integrator adds keys. |
| `auth` | Sign-in, first-run setup, password change, account menu |
| `chat` | `App.tsx`: conversation list, messages, composer, members, bot panel, and every panel still embedded in `App.tsx` |
| `tasks` | Task runs, tool steps, issues, questions, approvals, user forms |
| `schedules` | Scheduled tasks, editor, scheduled-run metadata |
| `work` | Team board, work items, execution |
| `settings` | Settings shell, general (language, timezone, appearance), models, providers, voice, usage, admin, portability, connection |
| `extensions` | MCP, local MCP, integration catalog, Skills, tools, OAuth for tools |
| `computer` | Bot computer: desktop, remote control, terminal, credentials, resources |
| `bots` | Bot config, avatar picker, identity, memory, inspector, packages, archive |
| `mail` | Mail drafts, display cards, postmarks |

The namespace list is fixed in `languages.ts`, `i18next.d.ts` and every locale directory.
Adding one needs all three; ask first.

## Using it

Components:

```tsx
import { useTranslation } from "./i18n";             // always via ./i18n, never "react-i18next" directly
const { t } = useTranslation("settings");             // or ["settings", "common"] to also use common:...
<h3>{t("timezone.label")}</h3>
<p>{t("label.draft", { time: formatClock(draft.created_at) })}</p>
```

Outside components (helpers, callbacks, error handling): `i18n.t("ns:key", vars)` **at call time**.
Never call `t` at module scope: catalogs load asynchronously and a module-level constant freezes one language.
Map codes to keys, not to text:

```ts
const statusKeys = { pending: "status.pending", answered: "status.answered" } as const;
t(statusKeys[code])
```

Dynamic keys are fine when built from a finite union (`t(\`issue.title.${kind}\`)` with `kind: TaskIssueKind`); tsc checks every member.

A function or component that tests render in a fixed language takes `locale?: Language` and uses
`useTranslation(ns, { lng: locale })` or `i18n.getFixedT(locale ?? null, ns)`. Production callers omit it.

Language changes re-render the tree from `main.tsx`. A `memo`ized component that shows text must call `useTranslation` itself.

## Keys

- Reference form is `namespace:area.thing`, e.g. `tasks:issue.title.provider_busy`, `settings:language.auto`.
- Inside a component bound to its namespace, write `area.thing`.
- `snake_case` segments, nested JSON objects, no `.` or `:` inside a segment.
- Name by meaning, not by wording or position: `draft.send`, not `draft.send_email_button_text`.
- One key per distinct meaning. Same English with different meaning in another language = two keys.
- Keys for server codes use the code: `apiError.invalid_bootstrap`, `status.approval_expired`.

## Text rules

- **zh-CN** is the existing Chinese copy, verbatim. Do not rewrite it while converting.
- **en** is natural English written for this product, not a literal gloss of the Chinese.
- **Interpolation**: `{{name}}`. Format numbers, dates and times in code with `src/i18n/format.ts`
  (pass the user's timezone from `useUserTimezone()` for wall-clock times) and pass the string in.
- **Never concatenate sentences** or build them from fragments across keys or JSX. One sentence = one key with variables.
  Joining independent labels with ` · ` in code is fine.
- **Plurals**: `key_one` / `key_other` in en with `{{count}}`; call `t("key", { count })`.
  Each language lists exactly its CLDR categories: zh-CN, zh-TW, ja, ko only `_other`; de `_one`/`_other`; fr `_one`/`_many`/`_other`. The checker enforces this.
- **Inline markup**: `<Trans t={t} i18nKey="field.setup_key_hint" components={{ code: <code /> }} />` with named tags in the string
  (`Run <code>sudo tofi setup-secret</code> …`). Never numbered tags, never HTML strings with `dangerouslySetInnerHTML`.
- Punctuation follows the language: full-width in Chinese/Japanese, `…` (one character) for progress, no trailing period on buttons and labels in en.

### Tone

Concise, human, direct. Say what happened and what to do next.
No hedging or assistant voice ("It seems that…", "Unfortunately…", "I'm sorry", "Please note").
English buttons and labels are sentence case ("Save draft", "Open tool settings").
Errors: "Couldn't load your timezone. Try again." rather than "An error occurred while loading the timezone setting, please try again later."

### Glossary: kept as written in every language

| Term | Note |
|---|---|
| Tofi, TOFI | Product name. Tofi in prose; TOFI only where the design uses the wordmark (e.g. TOFI POST). |
| Bot, Bots | Product noun, capitalized, as zh-CN already does. |
| Skill, Skills | Product noun. |
| MCP | Protocol name. |
| Admin | Account role, matches the server role. |
| Codex, ChatGPT, OpenAI, Claude, Gmail, GitHub and other vendor/product names | Never translated or inflected. |
| API key | Kept in zh-CN/zh-TW/ja/ko; de/fr may use `API-Schlüssel` / `clé API`. |
| OAuth, URL, HTTP, HTTPS, SSH, IANA, token names | Technical identifiers. |
| Commands, paths, file names, config keys | Always in `<code>` via `Trans`, never translated. |

## Server text

User-visible server text must arrive as a stable `error.code` (plus params when needed), not prose.
`request()` keeps the server `message` only as a fallback. Render with
`errorText(cause, "<ns>", t("...fallback"))`, which looks up `<ns>:apiError.<code>`, then `common:apiError.<code>`.
Plan for the remaining server prose: `docs/agent-plan/i18n-server-strings.md`.
Model-facing prompts stay English and are out of scope.

## Languages other than en and zh-CN

They contain only translated keys; missing keys fall back to English at runtime.
There are no copied-English placeholders, so nothing untranslated can be mistaken for a translation.

- `npm run test:i18n -- --untranslated ja` lists the keys `ja` still lacks, with the English text.
- The checker always fails on keys absent from en, wrong plural categories, and `{{variables}}`/`<tags>` that differ from en.
- When a language is complete, add it to `COMPLETE_LANGUAGES` in `src/i18n/languages.ts`; from then on missing keys fail CI and it loads without the English chunk.
- `--strict` fails on any untranslated key in any language.

## Hardcoded text ratchet

`scripts/i18n-baseline.json` holds, per file, the count of CJK text runs and obvious English UI phrases still in source
(`src/locales`, `src/motion-lab` and `src/i18n/languages.ts` are excluded). A file may not exceed its count; files not listed must have none.
After converting files, the integrator runs `npm run test:i18n -- --update-baseline` (it refuses increases). Batch workers do not edit the baseline.
Comments are ignored; text in strings, templates and JSX counts.

## Tests

Load `src` modules through Vite, not `tsc --ignoreConfig` into a temp directory (that cannot resolve the catalogs or packages):

```js
import { openUiModules } from "./ui-modules.mjs";
const ui = await openUiModules({ language: "zh-CN" });   // shipped copy; English is loaded too
try { const { presentTaskIssue } = await ui.load("/src/taskIssuePresentation.ts"); /* … */ }
finally { await ui.close(); }
```

Browser fixtures render after `i18nReady` and choose a language with Playwright `newContext({ locale })`
(see `scripts/test-owner-bootstrap.mjs`).

## Converting a file (checklist)

1. `const { t } = useTranslation("<your namespace>")` in each component; `i18n.t("<ns>:...")` elsewhere.
2. Move every user-visible string (text, `aria-label`, `title`, `placeholder`, `data-hint`, `alt`, errors set into state, toasts) to en + zh-CN.
3. Replace hardcoded `"zh-CN"`/`"en-US"` display locales with `src/i18n/format.ts` helpers or `intlLocale()`.
4. Keep data that is not UI copy untouched: user content, bot names, model ids, server ids, log/diagnostic JSON.
5. `npx tsc --noEmit -p .`, `npm run test:i18n`, `npm run build`, and the tests that load your files; update assertions that pinned old copy (pin zh-CN with `openUiModules`).
