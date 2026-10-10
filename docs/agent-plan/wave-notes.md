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
