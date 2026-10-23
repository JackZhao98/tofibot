# deadend-scan

Finds plumbing breaks in TOFI run records: places where a run hit a wall the product could have prevented. Python 3, standard library only, read-only.

```
python3 scripts/deadend-scan/deadend_scan.py --eval ../tofibot-taskeval/eval/results/deliv-terra-r3
python3 scripts/deadend-scan/deadend_scan.py --eval '../tofibot-taskeval/eval/results/deliv-terra-r*' --json
python3 scripts/deadend-scan/deadend_scan.py --remote https://host:port --cookie-file jar.txt --insecure \
    --since 2026-10-20T00:00:00Z --exclude-title-prefix 评测 --baseline known.json
```

Inputs
- `--eval PATH` (repeatable, globs ok): a results folder (`results.json` + `deliverables/<task>-<model>-r<n>.context.json`) or a root holding such folders. One eval task is one scanned run; its `tool_texts` ("tool args\nresult") are the calls. Tasks without a context file are skipped.
- `--remote` with `--cookie-file` (Netscape jar, `#HttpOnly_` lines understood) and `--insecure`: GET only: `/api/conversations`, `/api/conversations/{id}/runs`, `.../messages`, `.../tools?run_id=` (full results, paged). `/api/runs/{id}/context` is not needed by any detector and is not fetched. `--since`, `--exclude-title-prefix`, `--max-conversations` bound the sample.

Output: markdown (summary table, then top groups per detector with run ids, bounded evidence with secrets masked), or `--json` (summary, groups, every finding). `--top N` groups per detector.

Exit code: `0` nothing new in a hard detector; `1` a hard detector (1, 4, 6) has a `detector|tool|code` group that is not in `--baseline` (without a baseline every hard finding is new); `2` usage or input error. `--write-baseline FILE` records the current hard groups.

## Detectors and signals

| # | Detector | Gate | Signals (structured unless noted) |
|---|---|---|---|
| 1 | Dead-end refusal | hard | A failed call (activity status `failed`, or outcome status validation/permanent/transient failure, or uncoded `Tool error:`) with no repair data: `next_action` not in repair/retry/refresh/replan/verify_target, no alternatives (result keys such as `available`, `servers`, `schema`, `options`; a bullet list; 3+ backticked names), or a code shaped `*_not_found` / `*_unavailable` / `*_not_configured` / `unknown_*` with no alternatives even when `next_action` is repair. Denied, approval and need-input outcomes are skipped (a person's decision). Uncoded failures and code `unclassified_tool_failure` are marked **legacy-uncoded** (the only place error prose is used: the group key is the error head with numbers stripped). |
| 2 | Identical retry | soft | Same tool and canonical-JSON argument hash called again after a failure in the same run. A `transient_failure` with `next_action: retry` is a sanctioned retry and is skipped. |
| 3 | Claim without a call | soft, heuristic | Final answer (last accepted `say`) has a visual-object term and a looked-at term within 40 chars (lists in `CLAIM_OBJECTS` / `CLAIM_VERBS`, English and Chinese substrings, negators such as "not"/"没"/"未" skip it) and the run has no successful view call. |
| 4 | Format leak | hard | Published assistant message that is (a) a JSON object (optionally fenced) with `purpose`+`text`, or whose keys are a subset of some tool's argument keys seen in the scanned data (2+ keys); (b) contains `[message_id=` anywhere but the very start (the start is the quote-reply syntax); (c) is empty with no attachment, or only Cf/Cc/space characters. |
| 5 | Unseen artefact | soft | Run published an image (`generate_image`, png/jpg/gif/webp/svg) or a visual document (pdf/docx/pptx/html), by `publish_file` or message attachment, and made no successful view call. Code `image_not_viewed` or `document_not_viewed`. Spreadsheets, csv and zip are ignored. |
| 6 | Quiet or empty completion | hard | Run kind `schedule`, status `done`, with `final_origin` `none`/`implicit`, or no visible assistant text (empty, or Cf/Cc/space only). |
| 7 | Review loop | soft | `say` refused with `answer_review` / `answer_revise`, then no accepted `say`, an invisible final answer, or a final answer under half the length of the longest refused draft. |
| 8 | Run error | soft | Run status `failed`; grouped by the bracketed code in the error (`[server_is_overloaded]`), else `HTTP <status>` plus the API error type, else the error head. |

View calls (detectors 3 and 5): `computer_files` `files.view`; `computer_browser` `browser.snapshot` / `screenshot`; `computer_desktop` `desktop.capture` / `snapshot` / `screenshot`. Failed calls do not count.

## Known false-positive risks

- 1: `next_action: explain_blocker` on a permanent failure is by design for some environment faults (connection refused, unsafe port); it is flagged because the model gets no way forward, but a person may decide it is acceptable. Baseline it.
- 1/2 in eval folders: results are stored as text and may be cut; an outcome that was truncated parses as uncoded.
- 2: a deliberate poll (same arguments after a failure that really was transient but coded `permanent_failure`) is flagged.
- 3: wording-based. It cannot tell a claim about the product from a claim about its own check ("the layout looks good" about a design brief), and it misses claims in other languages or phrasing.
- 4: a user-requested JSON answer whose keys happen to equal a tool's argument keys; tool shapes come from calls seen in the same scan, so a small sample knows few tools.
- 5: the model may have verified a document by another route (e.g. reading its text); the scan only knows about image views. `generate_image` output is not shown to the model, so it counts as unseen unless viewed.
- 6: eval folders keep one reply per task, not per run, so a schedule run's own answer is judged from the task's reply.
- Eval tasks bundle several runs (chat, schedule, group follow-up); evidence is per task, not per run.
- Masking is pattern-based (bearer/cookie/token/key forms, long hex and mixed tokens); read evidence before pasting it outside the team.

Tests: `python3 -m unittest discover -s scripts/deadend-scan/tests -p 'test_*.py'`.
