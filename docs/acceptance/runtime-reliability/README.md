# Synthetic Web runtime acceptance

These screenshots and [results.json](results.json) come from the actual
production `App` and `OwnerSessionGate`, served only on loopback. Every HTTP/SSE
response is synthetic. Unknown mutations fail closed; the fixture cannot execute
tools, change real accounts or approve real actions.

All **20 cases** pass: the ten scenarios below run at both 390×844 and 1280×900.
No page errors, stale alerts, snapshot notifications, horizontal overflow or
execution endpoint requests occur. Browser login and approval answers are
synthetic; this fixture has no credentials or production connection.

| Scenario | Narrow | Desktop |
| --- | --- | --- |
| Pending approval remains visible with historical failed runs | Pass | Pass |
| Relogin restores pending approval without stale error flood | Pass | Pass |
| Expiry remains distinct and resumable | Pass | Pass |
| Duplicate renewal click produces a fresh pending card | Pass | Pass |
| Duplicate answer submits once and resumes | Pass | Pass |
| Local answer → later backend expiry → fresh renewal | Pass | Pass |
| SSE reconnect refreshes current question without stale replay | Pass | Pass |
| Information wait is visible, answerable and resumable | Pass | Pass |
| Stream failure shows system explanation and rejects late frames | Pass | Pass |
| Budget failure retains partial output and terminal explanation | Pass | Pass |

Representative evidence:

- [Full Go suite](go-test.txt) and [Web checks](web-checks.txt)
- [Astra findings, rereview blockers and budget issue: implementation/test mapping](review-fixes.md)
- [Descriptor/evidence gate, affected Go suite and Linux cross-build](descriptor-gate.txt)
- [Auto Review probe: expired local snapshot, zero requests](auto-review-live-probe.json)
- [Authorized production shadow probe: HTTP 200, one request, zero tools](auto-review-production-shadow-probe.json)
- [Fixed synthetic candidate request](auto-review-candidate-request.json)
- [Narrow expired approval and fresh-review control](narrow-expired.png)
- [Narrow answered card replaced by backend expiry](narrow-answered-then-expired.png)
- [Desktop budget failure with retained partial output](desktop-budget-failure.png)
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
or desktop-client login test. The separate production shadow-probe JSON records
the user-authorized model availability check; it is not part of the browser
fixture or an execution approval.
