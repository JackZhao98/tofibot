# Synthetic Web runtime acceptance

These screenshots and [results.json](results.json) come from the actual
production `App` and `OwnerSessionGate`, served only on loopback. Every HTTP/SSE
response is synthetic. Unknown mutations fail closed; the fixture cannot execute
tools, change real accounts or approve real actions.

The matrix covers pending approval, relogin with historical failures, expired
approval, duplicate renewal, duplicate answer/resume, reconnect refresh,
information wait/answer, and stream failure with late-frame rejection, at both
390×844 and 1280×900. No stale alerts or notifications replay from snapshots.

Representative evidence:

- [Full Go suite](go-test.txt) and [Web checks](web-checks.txt)
- [Auto Review probe: expired local snapshot, zero requests](auto-review-live-probe.json)
- [Fixed synthetic candidate request](auto-review-candidate-request.json)
- [Narrow expired approval and fresh-review control](narrow-expired.png)
- [Desktop stream-failure system notice](desktop-stream-failure.png)
- [Desktop resumed approval](desktop-resumed.png)
- [Narrow current question after reconnect](narrow-reconnected.png)

Reproduce with an available Playwright installation and Chromium:

```sh
npm --prefix ui run dev -- --host 127.0.0.1 --port 5196
PLAYWRIGHT_MODULE=/absolute/path/to/playwright-core/index.mjs \
CHROME_EXECUTABLE=/absolute/path/to/Chromium \
node ui/scripts/test-runtime-reliability-browser.mjs
```

The runner also accepts `RUNTIME_UI_ORIGIN` and `RUNTIME_UI_OUTPUT`. Its default
output directory is this folder. This is source/UI acceptance, not a production
or desktop-client login test.
