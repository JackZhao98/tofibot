# Next release wave (after v0.1.0)

Changes merged to `main` that have not shipped yet. The owner reviews this list before a release is cut (no release until the owner says so).

| # | Change | Status | Verification | Commits |
|---|---|---|---|---|
| 1 | Bundle Fredoka and JetBrains Mono fonts locally (were loaded from Google Fonts and failed silently); OFL notices added | merged | typecheck, i18n, build, test:empty-states, go test: exit 0 | f428d52 |
| 2 | First-run hero (4 live cats, dot-grid card); 19 empty states get a contextual live cat; at most 6 live cats at once | merged | test:empty-states (64 screenshots, reduced motion, 390px), settings-shell: exit 0 | f428d52 |
| 3 | Onboarding sheet: Welcome → Connect a model (ChatGPT device sign-in or API key, required) → pick connections (optional) → first Bot DM; resumable, server-side state | in progress (owner's design received 2026-10-10) | — | — |
| 4 | "Connect a model to start chatting" composer banner and a "Finish setup" sidebar chip | part of #3 | — | — |
| 5 | Admin console redesigned: account list → detail, clear role / computer / sign-in / danger sections | in progress | — | — |
| 6 | Delete account (after deactivation): removes data and the computer disk, frees its quota and slot; resumable on failure | in progress; needs Fable review before merge | — | — |
| 7 | Robinhood added to the connection catalog (official MCP, sign in with Robinhood) | part of #3 | — | — |
