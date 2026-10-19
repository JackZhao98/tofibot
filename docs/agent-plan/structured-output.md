# Structured output: "what the user sees" as a typed act

Status: Phases 0-3 built (see sections 11 and 12). Line numbers in sections 1-10 were read from `main` at 385050e.

## 1. Why

Today a Bot's visible output is inferred after the fact from free text. One
week of live probes produced a stack of threshold heuristics, each with its own
bugs (wave-notes.md "Next wave" 1-5, "v0.1.3" 29, 34, 35):

| # | Heuristic | Where | Rule |
|---|-----------|-------|------|
| H1 | `<progress>` tags | `internal/app/progress_tag.go:19-191`, prompt `internal/agent/agent.go:30-35`, live filter `internal/app/stream.go:386-417` | text inside tags folds; `finalSplit` (progress_tag.go:170) decides the answer of the final turn |
| H2 | Step-narration fold | `internal/app/reply_repeat.go:117-159`, called `internal/app/app.go:1992` | segments <=80 runes and <1/3 of a final >=120 runes become `progress` after the run |
| H3 | Repeated reply | `reply_repeat.go:17-110`; final path `app.go:1931-1938`; mid-run path `stream.go:274-279` | Dice >= 0.5, length ratio >= 0.6, digit runs equal: final not published / segment folds |
| H4 | Delegation-note fold | `app.go:1927-1929`, `app.go:1966` | after a handoff, a final <=80 runes becomes `progress` |
| H5 | Scheduled result prepend | `internal/app/schedule_final_result.go:16-55`, called `app.go:3601-3606` | result from `complete_scheduled_task` is prepended unless the answer contains it or is >=50% its length |
| H6 | Leading-mention dispatch | `app.go:3654-3668`, `internal/app/collaboration.go:947-975` | a final starting with `@Member task` is turned into a handoff |
| H7 | No-final fallback | `internal/app/final_answer_guard.go:15`, `app.go:3672-3694` | publishes a hardcoded Chinese sentence pointing at the last progress note |
| H8 | Exact-dup guard | `app.go:3698-3700`, `internal/app/group_respond.go:68-85` | final equal to any earlier visible text is dropped |

Structural signals already exist but are partial: `respond` dispositions in
groups (`group_respond.go:121-199`), `send_chat_message` mid-run beats
(`internal/app/chat_segments.go:125-143`), `complete_scheduled_task`
(`app.go:4042-4062`), `handoff` (`app.go:3909-3975`) whose brief the UI now
collapses into one line (`ui/src/App.tsx:2269-2272`). The model (Haiku 5.5 by
default) ignores prose rules in `voiceGuidance` (`internal/app/prompt_budget.go:71`)
and the tag instruction, but it does call tools. The fix is to make every
visible act a tool call, make plain text internal by default, and replace
inference with one deterministic rule.

## 2. The protocol

One tool, `say`, is the only way text reaches a person.

```
say(purpose, text?, wake?, wait_for?, sources?)
purpose: answer | note | message | nothing | wait
```

- `answer`: the visible result. Ends the turn: the host publishes, then
  cancels the loop exactly as `respond(silent)` does today
  (`group_respond.go:185-196`). No further model call, no trailing text. A
  second `say(answer)` in one turn is refused (same as `group_respond.go:155-157`).
- `note`: transient, stored as kind `progress`, folds in the UI
  (`ui/src/App.tsx:1069`, `ui/src/TaskRunBlock.tsx:148`). The run continues.
  Replaces the tag and the `ProgressReportPrompt` reminder cadence
  (`internal/runtime/runtime.go:20`, 15 calls) stays, now satisfied only by a `note`.
- `message`: a visible beat while the run continues, stored as kind `segment`.
  Replaces `send_chat_message`, keeps its cap of 3 and pacing
  (`chat_segments.go:16-18`).
- `nothing`: ends the turn with no post. Replaces `silent` and `concluded`.
- `wait`: ends the turn, registers `wait_for` (`group_respond.go:205-217`). Replaces `follow_up`.
- `wake` (answer only, <=3): starts members with a request, as today (`group_respond.go:147-153`).
- `sources` (answer only, scheduled runs): the receipt now recorded by
  `complete_scheduled_task`; the `say(answer)` row in `tool_activities` is the
  completion record (`internal/app/tool_activity.go:218-235`).

Plain assistant text between tool calls is never published. It stays in the
provider transcript and in the run's activity, visible only under "View activity"
(wave-notes.md 24). `OnAssistantTurn` (`agent.go:843-851`) stops calling
`PublishAssistantTurn` (`app.go:3353-3380`). The reply marker
`[message_id=UUID]` keeps its place at the start of `text`
(`internal/app/context.go:205`, `ui/src/App.tsx:2193`): the composer already
writes it there (`ui/src/composerDraft.ts:7`), nothing changes.

### Providers

- Anthropic: thinking-on models (Sonnet 5.5, Opus 5.5, Fable 5.1) cannot force
  `tool_choice`; the request stays `auto` (today only `none` is ever sent,
  `internal/provider/anthropic.go:350-352`). The tool description carries the
  contract; the fallback in section 4 covers misses. Interleaved thinking
  (`anthropic.go:40`) means thinking and text blocks before the `say` call are
  free; they are internal anyway. Structured outputs (a JSON-schema final) are
  the alternative; rejected because `say` reuses the tool machinery the
  checkpoints already persist (section 5) and lets `note` and `answer` coexist
  in one turn without a second schema.
- OpenAI Responses: a function tool; set `strict: true` for `say` only
  (today every tool is `strict: false`, `internal/provider/openai.go:296`).
- Codex: wraps the same Responses client (`internal/provider/openai_codex.go:11-52`), same path.

### Streaming

The owner wants text to appear as it is written. Both providers already stream
tool arguments: `input_json_delta` (`anthropic.go:807-811`) and
`response.function_call_arguments.delta` (`openai.go:726`) reach the agent loop
as `ToolCallDelta` (`internal/provider/provider.go:101`, `agent.go:1270-1283`),
which today only counts their length. A small incremental JSON-string decoder
(state: inside the `"text"` value, escape pending, `\uXXXX` pending; held-back
bytes released on the next delta exactly like `progressScanner.pending`,
`progress_tag.go:31`) turns the `text` of a streaming `say` into the existing
draft deltas (`stream.go:380-425` `StreamControls` → `AppendStreamDelta`). The
UI's draft rendering (`App.tsx:2277`, `SmoothStreamMarkdown`) is untouched.
Argument order matters for latency: the schema lists `purpose` first so the
host knows whether to open a draft before `text` arrives; a `note` streams into
the same draft and folds on publish, as tagged text does today.

## 3. Groups, DMs and scheduled runs

One tool, one set of purposes; the host decides which purposes a run may end
with, as `respond` does now (`group_respond.go:160-184`):

| Run | Allowed terminal purposes | Notes |
|-----|---------------------------|-------|
| DM, direct question | answer | `nothing` refused (today's `finalAnswerRequired`, `final_answer_guard.go:20-31`) |
| Group, unaddressed / mention / follow-up | answer, nothing, wait | same rules as `respond` |
| Scheduled, group task, team, message | answer with `sources` | `nothing`/`wait` refused (`group_respond.go:172-174`) |
| After a `handoff` this turn | answer (optional), nothing | the run no longer owes an answer |

`respond`, `complete_scheduled_task` and `send_chat_message` are removed, not
aliased. The scheduled final review (`app.go:3439-3451`,
`internal/app/schedule_final_review.go`) checks for a completed `say(answer)`
instead of `complete_scheduled_task`; `scheduledResultGuidance`
(`context.go:210`) loses its two-step "call then repeat" instruction, which is
the root of H5.

Delegation: `handoff` is unchanged. The brief is the tool's `task`; the group
sees only "X asked Y to take this" with the brief collapsed (`App.tsx:2269-2272`).
Because plain text is internal, "passed it to X, I'll continue when they
reply" written as free text is simply not seen; H4 goes. If the model puts it
in `say(answer)`, that is a measured protocol failure (section 6), not something
the host rewrites. `wake` stays on `say(answer)` because a wake is a visible
act (the wake request is posted, `group_respond.go:308`). `wait` replaces
`follow_up`. The disposition map `s.dispositions` (`group_respond.go:49`) is
replaced by the persisted `tool_activities` row, so a restart between
`say(answer)` and publication no longer loses the choice.

## 4. When the model ignores the protocol

A model may end with a text-only turn and never call `say`. The rule, fixed
and threshold-free:

1. The run's answer is the text of its last assistant turn that has no tool
   calls (what `agent.go:1549-1593` already returns as `cleanContent`). It is
   published as the final message, kind `''`.
2. Every other plain text in the run is internal.
3. If the run ends with no `say(answer)` and no final text, but with at least
   one `say(note)` or `say(message)`, the last of them is promoted to the
   answer (kind rewritten to `''`, a message event emitted, as H2 does today
   in reverse, `reply_repeat.go:148-157`). Otherwise the run fails with
   `errNoFinalAnswer` (`final_answer_guard.go:11`). H7's hardcoded sentence goes.

Observability: a new column `runs.final_origin` with values `say`, `implicit`,
`promoted`, `none`, written in `finishRunState` (`app.go:1892`). It is shown in
the run's activity block and aggregated per model on Settings → Usage. The
offline eval reads it directly. No reminder round-trip by default: the
`BeforeFinalResponse` repair (`agent.go:1554-1593`) can be enabled per
workspace to send one "deliver through say" reminder before applying rule 1,
at the cost of one model call; this is owner decision D3.

## 5. Migration and retirement

| Heuristic | Replaced by | Delete when |
|-----------|-------------|-------------|
| H1 tags + `finalSplit` + live filter (`progress_tag.go`, `stream.go:386-417`, callers `app.go:3363,3562,3652,3685`, `approval_expiry.go:332`, `schedule_failure_detail.go:15`, `chat_segments.go:52`) | `say(note)` + argument streaming | Phase 2 flip; the scanner stays one release as a no-op strip for legacy Bots whose generated instructions still mention the tag |
| H2 `foldStepNarrationTx` | plain text internal | Phase 2 |
| H3 `repeatedSegmentTx` (both call sites) | `say(answer)` ends the turn, so nothing can follow it | Phase 2 |
| H4 delegation-note fold | plain text internal | Phase 2 |
| H5 `scheduledFinalWithResult` | `say(answer, sources)` is the record | Phase 3 |
| H6 `leadingMentionTarget` dispatch | delegation only via `handoff`/`wake` | Phase 3 |
| H7 `noFinalAnswerProgressNote` | rule 3 above | Phase 2 |
| H8 `postedVisibleText` | exact-equality check kept as a cheap invariant on `say(message)` vs `say(answer)`; H3's fuzzy path goes | keep |
| `respond`, `send_chat_message`, `complete_scheduled_task`, `groupDispositionGuidance` (`group_respond.go:31-34`) | `say` | Phase 3 |
| Tests `reply_repeat_test.go`, `progress_tag_test.go`, `schedule_final_result_test.go` | eval + new unit tests for the decoder and purpose gates | with their code |

Stored messages: kinds `''`, `segment`, `progress` keep their meaning; `say`
writes the same kinds, so old conversations, `portability_import.go:97`,
`mcp_schedule_context.go:707` and the UI filters need no change. `runs.final_origin`
is additive (NULL for old runs).

Run resume: `say` is a tool, so its call and result are protocol state inside
`Continuation.Messages` (`internal/agent/continuation.go:33-45`) and
`tool_activities`; checkpoints at tool boundaries (run-resume.md Phase 2)
cover it with no new fields. `say(message)` and `say(answer)` use the provider
call ID for idempotent publication, as `PublishChatSegment` does
(`chat_segments.go:48-56`). A run resumed after a completed `say(answer)` is
finished by the host without a model call. The `turnIndex` replay table
`stream_assistant_turns` (`stream.go:247`, `stream.go:325`) becomes unused once plain text is
not published; keep the table, stop writing it.

UI: no rendering change in Phases 1-3. The activity view gains internal text
turns (collapsed) in Phase 4 so the owner can audit what the model wrote but
did not say.

## 6. Prompt and cost

The DM system prompt is capped at 5300 runes (`internal/app/prompt_budget_test.go:246`;
`maxSystemRunes` 16000 is the hard ceiling, `context.go:26`). Changes:

- Removed from the prompt: the tag sentence in `ProgressReportPrompt` (~90
  runes, `agent.go:34`), `groupDispositionGuidance` (~430 runes, groups),
  the "call then repeat" sentence in `scheduledResultGuidance` (~120 runes).
- Removed tool definitions: `respond` (~230 tokens), `send_chat_message` (~110),
  `complete_scheduled_task` (~150).
- Added: `say` (~170 tokens, principle-only description, no examples). Tools are
  cached behind one breakpoint (`anthropic.go:347`), so this is a one-time
  prefix cost per cache window.
- Calls per run: a group turn today needs `respond` plus a text-only turn to
  terminate the loop (`group_respond.go:167`); `say(answer)` terminates in one.
  Net one fewer model call per group turn. The optional D3 reminder adds one
  call only on a miss.

## 7. Verification

Offline eval, run from a fresh database with fresh Bots created through the
persona generator (fix the generator, never a single Bot):

- Probes (fixed, ~36): DM short question; DM task with 5+ tool calls; DM reply
  to an earlier message; group unaddressed small talk; group mention with a
  concrete task; lead delegating to two members and reporting; scheduled
  report with sources; scheduled run blocked; a question that must be asked
  back; a run that hits the budget mid-task. Each run three times.
- Models: Haiku 5.5, Sonnet 5.5, Opus 5.5 (the owner's A/B set, wave-notes 13).
- Rubric per run, from the database: `final_origin` (adherence); count of
  visible messages vs expected; duplicate visible text (exact); coordination,
  step narration and delegation requirements in visible text (scored by a
  grader model with a principle rubric, not keywords); handoff brief in
  visible text; model calls and tokens.
- Done means, on all three models: `final_origin=say` on >= 95% of runs that
  owe an answer; zero exact duplicates; narration and coordination leaks each
  <= 2% of runs; cost per probe not above the Phase 0 baseline; `go test
  ./internal/...` and the UI suites exit 0; one live day on the owner's group
  with no new heuristic requested.

## 8. Risks

- Haiku puts the real answer in `note` and ends silent: rule 3 promotes it;
  the eval counts `promoted` and D3 can add the reminder.
- The incremental JSON-string decoder is new streaming code; a bug shows up
  as broken drafts, never as stored text (publication uses the complete
  arguments). Fuzz it against both providers' delta shapes.
- A provider that does not stream arguments (verify `gemini.go`) shows the
  answer at once; acceptable, flagged in the eval.
- The model writes coordination into `say(answer)` anyway. Then the problem is
  visible in `final_origin`/rubric instead of silently patched; the answer is a
  prompt principle or D5, not a new fold.
- Bots created before the flip carry generated instructions that mention the
  tag; the no-op strip covers one release.
- `wake` and `wait` move tools; the durable wait table (`group_respond.go:205-253`)
  is reused unchanged, but the tests around `respond` must be ported, not dropped.

## 9. Phases (each shippable)

0. Instrumentation: `runs.final_origin` written from the current paths
   (`say`=`respond`/final text), the eval harness, baseline numbers on main.
1. `say` tool with `note`/`message`/`answer`, argument streaming into drafts,
   purpose gates. Plain text still published through H1-H3 (dual mode).
   Measure adherence. No deletions.
2. The flip: plain text internal, rules 1-3 live, delete H1 (strip only), H2,
   H3, H4, H7. Workspace flag, default on in the rc.
3. Merge: `nothing`/`wait`/`wake`/`sources` into `say`; remove `respond`,
   `send_chat_message`, `complete_scheduled_task`; delete H5, H6; persist the
   choice via `tool_activities`.
4. Retire the scanner and `stream_assistant_turns` writes; internal text in the
   activity view; remove the flag.

## 10. Owner decisions

**Decided 2026-10-17 (owner):** D1 (a) one `say` tool; D2 (a) internal, visible under "View activity"; D3 (a) publish the last text turn tagged `implicit`, no extra call; D6 (a) workspace flag, default on in the rc. D4 (a) and D5 (a) taken as recommended (owner delegated the minor choices).

- D1 Delivery mechanism: (a) one `say` tool, recommended; (b) a JSON-schema
  structured final answer; (c) keep tags and prose rules.
- D2 Plain text between tool calls: (a) internal, visible under "View activity",
  recommended; (b) discarded entirely; (c) visible as today.
- D3 On a protocol miss: (a) publish the last text turn, tag `implicit`, no
  extra call, recommended; (b) one reminder call, then (a); (c) fail the run.
- D4 Merge scope: (a) `respond`, `send_chat_message` and
  `complete_scheduled_task` all fold into `say`, recommended; (b) keep
  `complete_scheduled_task` as a separate receipt; (c) keep `respond` in groups.
- D5 `say(answer)` after a `handoff` in the same turn: (a) allowed and measured,
  recommended; (b) refused (the handoff line is the turn's visible act).
- D6 Rollout: (a) workspace flag, default on in the rc, recommended;
  (b) hard switch; (c) per-Bot opt-in.

## 11. Implementation notes: Phases 1 and 2 (branch feat/structured-output-say)

What was built, and where it differs from the sections above.

- `say(purpose, text)` exists only with the workspace flag on (`user_preferences.structured_output`, default on, Settings → General → Chat). With the flag off no request, prompt or publication path changes. A run reads the flag once when it starts.
- `answer` is recorded in `run_say_answers` (one row per run, keyed by the provider call ID) and the loop ends through `runtime.EndTurn`, which cancels the loop after the tool result is recorded. The host then publishes the recorded text through the normal finish path, so the streamed draft keeps its identity, attachments, follow-ups and result forwarding work as before. A resumed run that already holds a recorded answer is finished from it with no model call. This differs from "publish inside the tool, then cancel as respond(silent) does" only in where publication happens; the cancellation is the same idea, but a crash between recording and publishing is recoverable.
- `note` is stored as kind `progress` (client id `say-note:`), `message` as kind `segment` (client id `chat-segment:`, so the cap of three and the 420 ms pacing are shared with `send_chat_message`). Both adopt the draft their text streamed into.
- Plain assistant text between tool calls is stored as one folded `progress` message per turn, which "View activity" already renders. The `stream_assistant_turns` replay record is still written; Phase 4 stops that.
- Rule 3 promotes the last say note or message (client id `say-note:` or `chat-segment:`) by rewriting its kind to `''` and emitting a `message` event. Plain internal text is never promoted: a run with only internal text and no final text fails with `no_final_answer`.
- Streaming: the decoder lives in `internal/saystream` and is fed from `ToolCallDelta` fragments for Anthropic (`input_json_delta`), OpenAI Responses (`function_call_arguments.delta`), Codex (same client) and Chat Completions. Gemini returns a function call whole (`internal/provider/gemini.go`), so its `say` text appears at once, complete; this is recorded by a test, not worked around. A backend that sends Responses arguments only in the done event behaves the same way.
- Two things the doc did not state: the completion review (`taskCompletionFinalReview`) runs on a text-only final, so a run that ends with `say(answer)` skips it; a scheduled run must have `complete_scheduled_task` completed before `say(answer)` is accepted.
- Two further closings the flip needed. `respond(reply)` ends the turn under the protocol (a group model that kept working after its reply looped), and a run that handed work off may end with an empty response (`EndOnEmptyFinal`) instead of being nudged to continue; Phase 3's `nothing` purpose replaces both. The steering line ("deliver your reply by calling say") sits in the run context note beside the request, not at the end of the system text: on Haiku 5.5 the same words measured 45% say adherence at the end of the system text and 77-88% beside the request.
- Adherence on the offline eval (24 probes, 1 run, flag on, tool_choice auto, D3 fallback): Haiku 5.5 76%, Sonnet 5.5 93%; the Phase 0 Haiku baseline was 7%. The 95% bar is not met on either model. Misses are `implicit`: short chat answers on Haiku, and the two lookup probes on Sonnet (a text-only final after tool work). Variants measured on Haiku DM probes (40 runs each, no grading): line at the end of the system text 45%, beside the request 77-88% under several wordings, as a trailing message after the request 72-88%, `say` listed first among the tools 82-85%. Results are in `eval/results/structured-say-haiku-sonnet/`.
- Budget exhaustion is the one case where plain text is still published under the flag, by design: a run that ran out of budget publishes its partial final text (tags stripped) with the budget notice, because it has no other way to say what it found.
- Review fixes: plain text no longer streams into the chat draft under the flag (it feeds the live status line; only a say call's text is a draft producer, and a text-only final appears at once when the run finishes); text written after a handoff without a say is kept as a folded note (`final_origin=none`); `say(answer)` gets the same one-time completion review as a text final when tool work was done; a message arriving right after `say(answer)` or `respond(reply)` no longer cancels the finished turn; `say(note)` is capped at 10 per run with the message pacing.
- After the review fixes (24 probes, 2 runs per model): Haiku 5.5 81% say adherence, Sonnet 5.5 97%. Haiku misses remain short chat answers; Sonnet meets the bar.

## 12. Implementation notes: Phase 3 (branch feat/structured-output-phase3)

Everything below is under the workspace flag; with the flag off no prompt, tool, request or publication path changed, and the flag-off tests that existed before still pass.

### Decisions

- **Group replies: `say` takes the reply path completely; `respond` keeps only what posts nothing.** Reading `group_respond.go`: `respond(reply)` did four things, all of which `say(answer)` already does or can carry: record the text, end the turn (`runtime.EndTurn`, added for the structured protocol), publish through the normal finish path, and start the `wake` list after publication. The other three dispositions (`silent`, `concluded`, `follow_up`) post nothing and end the run through `finishRun(silent)` plus a context cancel, which has no counterpart in the answer path, so they stay in `respond`, now with a schema of `disposition` (three values) and `wait_for` only. `respond(reply)` is refused with a pointer to `say`. Keeping a second reply path would have kept two sets of end-of-turn guarantees; the steering test (`TestSteeringMessageAfterRespondReplyKeepsTheReply`) now exercises `say(answer)` in a group and still requires the reply to be kept, the run to be `done` with origin `say`, and no second model call.
- **`wake` lives on `say(answer)`, in groups only, and is stored with the recorded answer.** `run_say_answers` gained `wake` and `sources` columns (additive, `ensureColumn`). `applyDisposition` reads the wake list from that record instead of the in-memory `dispositions` map, so a restart between recording and publishing keeps it (the plan's section 3 promise, delivered for `say`; the map is still used for the three `respond` dispositions and for flag-off). Validation is respond's: another member, a non-blank request, at most three. A run whose schema carries optional fields (`wake` in groups, `sources` in scheduled runs) is not `strict` for OpenAI; the plain DM schema stays strict.
- **`send_chat_message` is not offered under the flag.** `say(message)` already shared its cap of three, its 420 ms pacing and its target (the run's own conversation; the tool never had a target field, so none was added), and the same gates (user-visible conversation, not a scheduled root).
- **Scheduled runs: `say(answer)` with `sources` is the completion record; `complete_scheduled_task` is not offered.** The receipt was the only thing the old tool did that the answer does not, so the answer now writes it: the recorded `run_say_answers` row is what the final review, the failure check, the repair request and the occurrence's "delivered" flag read (`Store.scheduleReceipt`; the legacy `tool_activities` row is still honoured for runs that started before a change). Schedule state (`schedule_state_note.go`) and the failure-detail code are untouched. A blocked run ends with plain final text, which fails the run with that text as before; because an answer now records success, the scheduled guidance, the principle line and the single review reminder say that explicitly.
- **`nothing` and `wait` stay on `respond`.** The plan's `nothing`/`wait` purposes were not added: `respond` still carries them, and the owner scope for this phase was the reply, the mid-run message and the scheduled outcome.

### Heuristics

| # | State on the flag-on path | Evidence |
|---|---------------------------|----------|
| H1 tags, `finalSplit`, live filter | already unreachable for structured runs (every call site is gated by `structured`); the strip of literal tags stays for legacy Bots | Phase 2 |
| H2 narration fold, H3 repeat fold, H4 delegation fold | unreachable: they ran inside `finishRunOpts` unless `structured`, and the one structured path that still reached them was budget exhaustion (`finishRunBudget` passed zero options). It now passes `structured`. | `TestBudgetPartialTextIsNotFoldedWhenStructured` fails without the change (origin `heuristic:repeat`), and the flag-off twin keeps the heuristics |
| H5 schedule receipt prepend | deleted from the flag-on path | `TestScheduledTextFinalIsNeverRewrittenUnderTheFlag`: a run holding a legacy receipt row and a text final of "As above." is rewritten without the gate, published as written with it; the tool is also no longer offered, so the receipt never exists for a new run |
| H6 leading-mention dispatch | deleted from the flag-on path, and its sentence in the group prompt now says a mention only refers to a member | `TestLeadingMentionIsOnlyAMentionWhenStructured` and the text-final twin dispatch a child run without the gate |
| H7 no-final fallback | already unreachable: the structured branch is taken before `hasProgressReport` | Phase 2 (`TestRunWithNoTextAndNoSayFailsWithTheTypedCode`) |
| H8 exact-dup guard | stays | |

The flag-off code for H1-H7 stays until Phase 4 removes the flag.

### Haiku adherence

- **Where the misses are.** In the Phase 2 eval the 9 Haiku `implicit` runs of 64 are one-call turns with no tool use and a 300-1000 character reply (opinion, explanation, recommendation probes), not short chat answers: `dm_smalltalk` was 100%, `dm_opinion` 25%. So the model had decided the turn was a plain answer before the description or the principle line were read.
- **Prompt variants (tool_choice auto, Haiku 5.5, the four weakest DM categories, 40 or 64 runs each, no grading):** current prompt 58% and 72%; description rewritten so it says a reply of any length goes through the tool 70% then 58%; principle line merged into the request message 52%; a reworded principle line 50%; "never compose it as plain text" in the description and the progress prompt 45%; that plus a line at the top of the system text 55%; say listed first 78%; a described `text` field 55%; line at the top of the system text 56%; the combination of first, described field and top line 62%, and with the "never compose" wording 84%. The spread on these sample sizes is about +-11 points, and the one high number did not survive its ablation, so no wording or placement change was kept.
- **tool_choice.** The API accepts `tool_choice: any` for Haiku 5.5 with its default thinking (the plan assumed it would not; the request is retried without it, and remembered per model, if a model refuses). Full 24-probe suite, Haiku, 3 runs, no grading: say on 72 of 72 runs against 52 of 72 on auto, input tokens per probe +5%, output tokens -38%, cost per probe unchanged (about $0.030). Costs and risks seen in that run: after a `handoff` a forced call made the Bot narrate the delegation ("I've asked Pip ..., I'll tell you what they came up with"), so forcing stops once the run has handed off; a scheduled run must be able to end with a plain-text blocker, because an answer records success; in group chats more members spoke (not measured with the grader). So the requirement is applied only to Haiku models in a user-visible DM that is not scheduled (`forceSayFor`). It is never applied to the reserved repair, the final-only follow-up, or compaction.
- **Open:** Sonnet and Opus were not forced (they meet the bar and the API acceptance was not measured for them); OpenAI has the equivalent (`required`) but it was not measured; groups on Haiku stay on auto.

### Numbers

Offline eval, 24 probes, 2 runs per model, grader claude-opus-5-5, flag on, final code of this branch (results: `eval/results/structured-say-phase3/`; the directory is git-ignored, like the previous run's). "Last run" is the Phase 2 after-review-fixes run in `eval/results/structured-say-haiku-sonnet/`.

| | Haiku 5.5 last run | Haiku 5.5 Phase 3 | Sonnet 5.5 last run | Sonnet 5.5 Phase 3 |
|---|---|---|---|---|
| say adherence (runs owing an answer) | 81% (52/64) | 88% (56/64) | 97% | 97% |
| final_origin counts | say 52, implicit 9, none 3 | say 56, implicit 5, none 3 | say 61, implicit 2, none 1 | say 64, implicit 2 |
| DM probes only | 85% (34/40) | 100% (40/40) | 95% (38/40) | 95% (38/40) |
| visible count ok | 98% | 100% | 100% | 100% |
| exact duplicates | 0 | 0 | 0 | 0 |
| step narration leak | 4% | 2% | 6% | 4% |
| coordination leak | 4% | 2% | 2% | 6% |
| delegation requirements in text | 0% | 0% | 0% | 2% |
| handoff brief visible | 0 of 4 | 0 of 4 | 0 of 4 | 0 of 4 |
| closing offer | 4% | 6% | 11% | 6% |
| avg score (0-2) | 1.800 | 1.829 | 1.793 | 1.810 |
| probe cost per probe (estimate) | $0.0300 | $0.0279 | $0.0610 | $0.0596 |
| probe tokens in / out | 1,294,868 / 29,029 | 1,239,972 / 20,291 | 1,374,067 / 18,072 | 1,335,086 / 19,085 |

Reading it:

- Haiku DM adherence is 100% (40 of 40 DM runs that owe an answer; was 85%). Overall Haiku stays under the 95% bar because groups are not forced: `group_debate` 33% and `group_delegate` 50% say, the same misses as before.
- Group forcing was measured and rejected. Haiku, group probes only, 3 runs, graded, forced vs auto: say 86% vs 38%, but coordination leaks 28% vs 11%, delegation requirements in text 6% vs 0%, narration 11% vs 6%, average score 1.59 vs 1.78 (`group_unaddressed` 1.70 vs 1.93). Not shipped.
- Sonnet is unchanged on adherence (its two misses are the lookup probe, a text-only final after tool work). Its coordination leak rose from 2% to 6% (3 of 48 runs: two unaddressed-question turns where a member said it would leave the analogy to another, and one debate turn). Haiku's fell from 4% to 2%, so this is either run noise at n=48 or an effect of the Phase 3 group prompt, which now says a mention in text only refers to a member; it needs a re-measure before the rc.
- The cost of Haiku probes did not rise: against the last run, probe input tokens are 4% lower and output tokens 30% lower (the input difference is within run-to-run noise; on the full suite with forcing everywhere, input was +5% and output -38%).
- One run on the way here showed why the full suite matters: with a required tool call and no release, a Haiku onboarding turn whose `say(answer)` was rejected by the completion review repeated the identical call 157 times (the recovery guard refuses an identical retry of a refused call) and spent about 10M input tokens before the run budget ended it. The loop now releases the requirement after a round in which every call failed (`TestForceToolUseIsReleasedAfterAFailedRound`); the final run above includes that fix and its setup usage is back to 283k tokens.

### Still open after Phase 3

- Haiku in groups: 33-50% say on debate and delegation turns. The only measured lever (required tool calls) costs quality there; a prompt-only lever was not found for DMs either. Options: leave as is (the text is published as `implicit`), or measure forcing only on turns addressed to the Bot.
- Sonnet and Opus: required tool calls not measured (adherence already meets the bar); OpenAI `tool_choice: required` not implemented.
- Sonnet's coordination leak (above) needs a re-measure; the 2% bar is not met in this run.
- `nothing` and `wait` remain on `respond`; Phase 4 retires the flag, the scanner and the `stream_assistant_turns` writes.
- The `final_origin` aggregation on Settings → Usage and the "View activity" internal text turns are still Phase 4.

### Follow-up: review findings and live findings

Verified against the code, each with a test that fails when the fix is reverted:

1. **Budget wrap-up (high, confirmed).** The force condition lacked `!budgetWrapUp`, and the forced-termination branch dropped a lone `say(answer)` and published the "[Run stopped ...]" placeholder. The wrap-up request is no longer forced, and a wrap-up answered with one `say(answer)` call uses its text (`TestForceToolUseStopsAtTheBudgetWrapUp`; fails on both halves).
2. **Identical answer after the review refusal (medium, confirmed; the cause of the 157-call runaway).** The review returned a plain error, the runtime classified it Permanent/`tool_rejected`, and the recovery guard barred the same arguments hash for the rest of the run. It now returns a Transient `answer_review` outcome with next action `retry`, which the guard lets through. Tested through the real runtime with a scripted model: `TestIdenticalSayAnswerAfterTheReviewRefusalIsRecorded` (second identical call recorded, origin `say`, three model calls). The release after a failed round stays as a bound.
3. **Scheduled answers unreviewed (medium, confirmed).** `state.review` was set only for runs that were not scheduled, so the receipt was never reviewed. Scheduled runs now get the same one-time review after tool work, with the scheduled reminder (`TestScheduledSayAnswerAfterToolWorkIsReviewedOnce`).
4. **Flag-on scheduled coverage.** Added: the approval -> restart -> HTTP delivery acceptance under say (`TestScheduledMCPApprovalRestartHTTPDeliveryWithSay`, all three outcomes), a scheduled run resumed after its answer was recorded (one message, delivered, no model call), and a group wake after a resumed answered run (exactly one mention run). The comment pointing at a missing file is fixed.
5. **tool_choice fallback.** It logs when the per-model memo is stored and matches the refusal wording ("forces tool use", "may not be enabled", not supported, ...) instead of any 400 that names the field.
6. **Status answer after a handoff (live).** Principle in the say description: after delegating, speak only to add substance the person has yet to read. Structural rule, deterministic and with no text matching: when the run has already posted a visible message and has handed off, `say(answer)` is refused with a retryable outcome and the run ends quietly (the colleague's result resumes the Bot). A handoff with nothing posted yet keeps its short answer (the Phase 2 behaviour). Side effect on the eval: a lead that posts and delegates ends that run with origin `none`, which the adherence metric counts as a miss (the follow-up run that integrates the result is the one that says).
7. **Greeting answered with the old topic (live).** Cause: the unaddressed-group-chat text told every member to "add your own useful perspective", with the finished debate in the history window, and nothing told the model that the latest message is what is answered. The structured group text now says to contribute what the latest message calls for and that a finished topic stays closed (replacing wording, no extra budget), and a line beside the request of an unaddressed group message says to refer to an earlier topic only when that message does. On Haiku, new probe `group_greeting_after_debate` (6 runs each, grader flag `revived_prior_topic`): system text only 6 of 6 revived the debate; first beside-the-request wording 4 of 6; final wording 0 of 6, with two of the six still naming the debate in passing ("the two have it in hand"). Sonnet did not reproduce it before or after.
8. **Sonnet coordination leak.** Re-measured on the final code: 4% (2 of 50 runs; was 6% in the Phase 3 run and 2% before). Still above the 2% bar. Both flagged runs are group turns (`group_unaddressed`, `group_debate`); at n=50 this cannot be told from noise.

Eval, final code, 25 probes (24 plus the greeting probe), 2 runs per model (`eval/results/structured-say-phase3b/`; Phase 3 was 24 probes):

| | Haiku Phase 3 | Haiku 3b | Sonnet Phase 3 | Sonnet 3b |
|---|---|---|---|---|
| say adherence | 88% (56/64) | 86% (57/66) | 97% | 95% |
| DM probes only | 100% (40/40) | 100% (40/40) | 95% (38/40) | 93% (37/40) |
| final_origin counts | say 56, implicit 5, none 3 | say 57, implicit 8, none 3 | say 64, implicit 2 | say 66, implicit 3 |
| visible count ok | 100% | 100% | 100% | 100% |
| exact duplicates | 0 | 0 | 0 | 0 |
| step narration leak | 2% | 2% | 4% | 4% |
| coordination leak | 2% | 4% | 6% | 4% |
| delegation requirements in text | 0% | 0% | 2% | 0% |
| topic revival (new flag) | not measured | 0% | not measured | 0% |
| closing offer | 6% | 8% | 6% | 10% |
| avg score (0-2) | 1.829 | 1.824 | 1.810 | 1.830 |
| cost per probe (estimate) | $0.0279 | $0.0299 | $0.0596 | $0.0609 |

Reading it: Haiku DMs are still 100% say; the overall 86% is the group turns (the posted-then-delegated leads now end `none`, as noted). Sonnet's three `implicit` runs are the lookup probe (text-only final after tool work) and one more; its adherence moved 97% to 95% on 3 runs of 66 and is inside run noise. Cost per probe rose about 7% and 2%: the extra probe is a two-turn group probe, and the review refusal adds a call on tool-work turns.

Still open: Haiku in groups (debate and delegate turns) is not forced and stays well under 95% say; Sonnet's coordination leak is at 4% against a 2% bar; forcing for Sonnet/Opus and OpenAI `required` are unmeasured.
