import { openUiModules } from "./ui-modules.mjs";

// Load through Vite so modules that use the i18n catalogs resolve like the app.
// Assertions pin the shipped zh-CN copy.
const ui = await openUiModules({ language: "zh-CN" });

try {
  const { activeToolForRun, buildToolRunAnchors, buildToolSummaryAnchors, buildToolTimeline, elapsedToolSeconds, orderToolActivities, toolActionLabel, toolArgumentPreview, toolAttemptIssues, toolDisplayLabel, toolDisplayState, toolStepDetail, toolStepTitle } = await ui.load("/src/toolTimeline.ts");
  const { mergeMessageTimeline, precedingBlockBotIds, previousRenderedIndex } = await ui.load("/src/messageTimeline.ts");
  const { taskPhaseVisible } = await ui.load("/src/taskIssuePresentation.ts");
  const { foldCompletedProgress } = await ui.load("/src/runProgress.ts");
  const base = { conversation_id: "c", bot_id: "b", arguments: "", result: "", status: "completed", truncated: false, updated_at: "2026-09-17T00:00:09Z" };
  const message = (id, seq, created_at, run_id = "r") => ({ id, seq, created_at, run_id, conversation_id: "c", role: "assistant", content: id });
  const activity = (call_id, started_at, updated_at = "2026-09-17T00:00:10Z", run_id = "r") => ({ ...base, call_id, started_at, updated_at, run_id, name: call_id });
  const assert = (condition, reason) => { if (!condition) throw new Error(reason); };
  assert(toolAttemptIssues([{ status: "failed" }, { status: "completed" }]) === "1 次失败", "a successful retry must retain the failed attempt without declaring the task incomplete");
  assert(toolAttemptIssues([{ status: "completed" }]) === "", "successful attempts must have no issue label");
  assert(toolAttemptIssues([{ status: "interrupted" }, { status: "running" }]) === "1 次中断 · 1 次待结束", "interrupted and unfinished attempts must remain distinguishable");
  const expired = {status:"failed",outcome:{status:"approval_expired",code:"approval_window_expired",execution_certainty:"not_executed"}};
  assert(toolDisplayLabel(expired) === "已过期" && toolDisplayState(expired) === "expired", "expiry has its own presentation");
  assert(toolAttemptIssues([expired]) === "", "expired approval is not an execution-error alert");
  assert(toolDisplayLabel({...expired,status:"interrupted"}) === "未执行", "object-level not-executed certainty takes precedence over interrupted transport");
  assert(toolDisplayLabel({status:"failed",outcome:{code:"batch_skipped",execution_certainty:"not_executed"}}) === "未执行", "stale batch tails did not execute");
  const approvedStep = {status:"completed",outcome:{status:"approval_recorded",code:"approval_recorded",execution_certainty:"not_executed"}};
  assert(toolDisplayState(approvedStep) === "approved" && toolDisplayLabel(approvedStep) === "已批准", "recorded approval is a resolved step, not unknown");
  assert(toolDisplayState({status:"completed",outcome:{status:"permanent_failure",execution_certainty:"not_executed"}}) === "unknown", "other completed-but-not-executed records stay unverified");
  const progress = { ...message("progress", 2, "2026-09-17T00:00:01Z"), kind: "progress" };
  const finalReply = message("final-reply", 3, "2026-09-17T00:00:03Z");
  const completed = foldCompletedProgress([progress, finalReply], [{ id: "r", status: "done" }]);
  assert(completed.visible.map(item => item.id).join(",") === "final-reply" && completed.progressByFinalId.get("final-reply")?.[0].id === "progress", "completed progress must fold under the durable final reply");
  for (const status of ["running", "waiting", "failed", "cancelled", "interrupted"]) {
    const retained = foldCompletedProgress([progress, finalReply], [{ id: "r", status }]);
    assert(retained.visible.length === 2 && retained.progressByFinalId.size === 0, `${status} progress must remain visible`);
  }
  assert(foldCompletedProgress([progress], [{ id: "r", status: "done" }]).visible.length === 1, "progress without a loaded final reply must remain visible");
  assert(foldCompletedProgress([progress, { ...finalReply, run_id: "other" }], [{ id: "r", status: "done" }]).visible.length === 2, "another run's reply must not fold this run");
  assert(toolActionLabel({ name: "computer_action", arguments: '{"action":"browser.navigate"}' }) === "正在浏览网页", "browser action must identify browsing");
  assert(toolActionLabel({ name: "computer_action", arguments: '{"action":"desktop.click"}' }) === "正在操作 Bot 电脑", "desktop action must identify computer use");
  assert(toolActionLabel({ name: "tofi_read", arguments: "{}" }) === "正在检查文档", "read tool must identify document work");
  assert(toolActionLabel({ name: "tofi_shell", arguments: "{}" }) === "正在运行命令", "shell action must identify command work");
  assert(toolActionLabel({ name: "computer_action", arguments: "broken" }) === "正在操作 Bot 电脑", "malformed action must not guess specifics");
  assert(toolActionLabel({ name: "search_mcp_tools", arguments: "{}" }) === "正在查找可用工具", "tool discovery is not web browsing");
  assert(toolActionLabel({ name: "call_mcp_tool", arguments: '{"name":"mcp__web__search"}' }) === "正在浏览网页", "resolved connected web tool identifies browsing");
  assert(toolActionLabel({ name: "call_mcp_tool", arguments: '{"name":"mcp__crm__lookup"}' }) === "正在使用连接的工具", "an unrecognised remote tool is named by its role");
  assert(toolActionLabel({ name: "call_mcp_tool", arguments: '{"name":"mcp_notion__notion-fetch"}' }) === "正在使用连接的工具", "a connected tool's verb (fetch) is not browsing the web");
  assert(toolStepTitle({ name: "call_mcp_tool", arguments: '{"name":"mcp_notion__notion-fetch"}' }) === "使用连接的工具" && toolStepDetail({ name: "call_mcp_tool", arguments: '{"name":"mcp_notion__notion-fetch"}' }) === "notion-fetch", "call_mcp_tool rows read as the connected tool plus its name");
  assert(toolDisplayState({ status: "failed", outcome: undefined }) === "not_executed", "a failed row with no outcome is not-executed, never uncertain");
  assert(toolDisplayState({ status: "failed", outcome: { status: "uncertain_effect", code: "mcp_result_unknown", execution_certainty: "unknown" } }) === "unknown", "an explicit unknown certainty still shows as unverified");
  assert(toolActionLabel({ name: "custom_widget", arguments: "{}" }) === "正在调用 custom_widget", "an unknown local tool keeps its own name");
  assert(toolStepTitle({ name: "custom_widget", arguments: "{}" }) === "调用 custom_widget" && toolStepTitle({ name: "tofi_shell", arguments: "{}" }) === "运行命令", "record titles drop the live progress wording");
  assert(toolStepDetail({ name: "computer_action", arguments: '{"action":"desktop.capture"}' }) === "截屏", "desktop actions get a plain word");
  assert(toolActionLabel({ name: "tofi_shell", arguments: "{}" }, "en") === "Running a command" && toolStepTitle({ name: "tofi_shell", arguments: "{}" }, "en") === "Run a command", "English tool wording comes from the en catalog");
  assert(toolArgumentPreview({ arguments: '{"url":"https://user:pass@example.com/docs?token=secret#private"}' }) === "example.com/docs", "timeline preview must drop URL credentials, query and fragment");
  assert(toolArgumentPreview({ arguments: '{"command":"echo secret"}' }) === undefined, "timeline preview must not expose raw command");
  assert(activeToolForRun([{ ...activity("one", "2026-09-17T00:00:01Z"), status: "running" }, { ...activity("two", "2026-09-17T00:00:02Z"), status: "running" }], "r")?.call_id === "two", "latest running tool drives work label");
  assert(elapsedToolSeconds({ ...activity("timed", "2026-09-17T00:00:00Z", "2026-09-17T00:00:03Z"), status: "completed" }, Date.parse("2026-09-17T00:01:00Z")) === 3, "completed tool duration remains fixed");

  const beforeFinal = buildToolTimeline([message("final", 1, "2026-09-17T00:00:00.000000900Z")], [activity("shell", "2026-09-17T00:00:00.000000500Z")]);
  assert(beforeFinal.beforeMessageId.get("final")?.[0].call_id === "shell", "pre-final tool must render before final bubble");
  const multiTurn = buildToolTimeline([
    message("one", 1, "2026-09-17T00:00:00.000000100Z"),
    message("two", 2, "2026-09-17T00:00:00.000000300Z"),
    message("three", 3, "2026-09-17T00:00:00.000000500Z"),
  ], [activity("a", "2026-09-17T00:00:00.000000200Z"), activity("b", "2026-09-17T00:00:00.000000400Z")]);
  assert(multiTurn.beforeMessageId.get("one")?.map(item => item.call_id).join(",") === "a,b", "one disclosure per run even across assistant turns");
  assert(multiTurn.afterMessageId.size === 0, "no repeated disclosure after later turns");
  const liveReports = buildToolTimeline([
    { ...message("report-one", 1, "2026-09-17T00:00:00.000000100Z"), kind: "progress" },
    { ...message("report-two", 2, "2026-09-17T00:00:00.000000300Z"), kind: "progress" },
  ], [activity("first", "2026-09-17T00:00:00.000000200Z"), activity("second", "2026-09-17T00:00:00.000000400Z")], [{ id: "r", status: "running" }]);
  assert(liveReports.afterMessageId.get("report-one")?.[0].call_id === "first" && liveReports.afterMessageId.get("report-two")?.[0].call_id === "second", "live tool rounds must stay between their public progress reports");
  assert(liveReports.beforeMessageId.size === 0, "live tool rounds must not jump above their prior report");
  const tie = orderToolActivities([activity("z", "2026-09-17T00:00:00.123456789Z"), activity("a", "2026-09-17T00:00:00.123456789Z")]);
  assert(tie.map((item) => item.call_id).join(",") === "a,z", "nanosecond tie must use call id");
  const stable = orderToolActivities([activity("late", "2026-09-17T00:00:00.100000000Z", "2026-09-17T00:00:11Z"), activity("early", "2026-09-17T00:00:00.200000000Z", "2026-09-17T00:00:01Z")]);
  assert(stable.map((item) => item.call_id).join(",") === "late,early", "updated_at must not reorder");
  const orphan = buildToolTimeline([], [activity("orphan", "2026-09-17T00:00:00Z")]);
  assert(orphan.fallback.length === 0, "unanchored historical activity must not appear as new activity");
  const trigger = { ...message("old-request", 10, "2026-09-16T12:00:00Z"), role: "user", run_id: undefined };
  const cancelled = { id: "old", status: "cancelled", trigger_message_id: trigger.id };
  const oldTool = activity("old-call", "2026-09-16T12:00:04Z", undefined, "old");
  const history = buildToolTimeline([trigger, message("reply", 24, "2026-09-17T00:00:05Z")], [oldTool], [cancelled]);
  assert(history.afterMessageId.get(trigger.id)?.[0].call_id === "old-call", "cancelled run without reply remains at original request");
  assert(history.fallback.length === 0, "cancelled run cannot float below recent replies");
  const paged = buildToolTimeline([message("reply", 24, "2026-09-17T00:00:05Z")], [oldTool], [cancelled]);
  assert(paged.fallback.length === 0 && paged.afterMessageId.size === 0, "history outside loaded page stays outside page");
  const boundedSummary = { run_id: "r", bot_id: "b", tool_count: 15, completed_count: 13, failed_count: 1, interrupted_count: 1, pending_count: 0, started_at: "2026-09-17T00:00:00Z", updated_at: "2026-09-17T00:00:05Z" };
  const summaryAnchor = buildToolSummaryAnchors([progress, finalReply], [boundedSummary], []);
  assert(summaryAnchor.beforeMessageId.get("final-reply")?.[0]?.tool_count === 15, "a loaded historical run must retain its exact summary when global tool rows are absent");
  assert(buildToolSummaryAnchors([progress, finalReply], [boundedSummary], [activity("loaded", "2026-09-17T00:00:01Z")]).beforeMessageId.get("final-reply")?.[0]?.tool_count === 15, "loaded detail rows must stay under the exact summary disclosure");
  const triggerOnly = { ...trigger, id: "terminal-trigger" };
  const terminalAnchor = buildToolRunAnchors([triggerOnly], [{ run_id: "terminal" }], [{ id: "terminal", trigger_message_id: triggerOnly.id }]);
  assert(terminalAnchor.afterMessageId.get(triggerOnly.id)?.[0]?.run_id === "terminal", "a terminal run without a final reply must remain reachable from its loaded trigger");
  const scheduledProgressOne = { ...message("scheduled-progress-one", 30, "2026-09-17T00:00:10Z", "scheduled"), kind: "progress" };
  const scheduledProgressTwo = { ...message("scheduled-progress-two", 31, "2026-09-17T00:00:12Z", "scheduled"), kind: "progress" };
  const scheduledSummary = { ...boundedSummary, run_id: "scheduled", tool_count: 2, completed_count: 1, failed_count: 1, interrupted_count: 0, started_at: "2026-09-17T00:00:09Z", updated_at: "2026-09-17T00:00:13Z" };
  const scheduledAnchors = buildToolSummaryAnchors([scheduledProgressOne, scheduledProgressTwo], [scheduledSummary], [activity("scheduled-one", "2026-09-17T00:00:09Z", undefined, "scheduled"), activity("scheduled-two", "2026-09-17T00:00:11Z", undefined, "scheduled")], [{ id: "scheduled", status: "failed" }]);
  const scheduledAggregateCount = [...scheduledAnchors.beforeMessageId.values(), ...scheduledAnchors.afterMessageId.values()].flat().filter(summary => summary.run_id === "scheduled").length;
  assert(scheduledAnchors.afterMessageId.get("scheduled-progress-two")?.[0]?.tool_count === 2 && scheduledAggregateCount === 1, "a failed scheduled run without a final reply or trigger must keep one aggregate after its latest visible progress report");
  const queued = buildToolTimeline([
    { ...trigger, id: "q1", seq: 22 }, { ...trigger, id: "q2", seq: 23 },
    message("a1", 24, "2026-09-17T00:00:05Z", "r1"), message("a2", 25, "2026-09-17T00:00:07Z", "r2"),
  ], [activity("c1", "2026-09-17T00:00:04Z", undefined, "r1"), activity("c2", "2026-09-17T00:00:06Z", undefined, "r2")]);
  assert(queued.beforeMessageId.get("a1")?.[0].call_id === "c1" && queued.beforeMessageId.get("a2")?.[0].call_id === "c2", "overlapping requests retain separate run ownership");
  const live = buildToolTimeline([], [activity("live", "2026-09-17T00:00:04Z")], [{ id: "r", status: "running" }]);
  assert(live.fallback.length === 1, "live tools remain visible before message snapshot");
  const duplicate = buildToolTimeline([message("reply", 24, "2026-09-17T00:00:05Z")], [activity("same", "2026-09-17T00:00:04Z"), activity("same", "2026-09-17T00:00:04Z")]);
  assert(duplicate.beforeMessageId.get("reply")?.length === 1, "snapshot and event copies count once");
  const speaking = { message_id: "streaming", run_id: "r", conversation_id: "c", bot_id: "b", seq: 2, status: "active", content: "Started reply", created_at: "2026-09-17T00:00:01Z" };
  const interrupt = { ...message("interrupt", 3, "2026-09-17T00:00:02Z"), role: "user" };
  const liveMessages = mergeMessageTimeline([interrupt], [speaking]);
  assert(liveMessages.map(item => item.id).join(",") === "streaming,interrupt", "user interjection stays below already speaking draft");
  const committed = { ...message("streaming", 2, speaking.created_at), content: "Completed reply" };
  assert(mergeMessageTimeline([interrupt, committed], [speaking]).map(item => item.id).join(",") === "streaming,interrupt", "publication preserves position and deduplicates draft");
  assert(mergeMessageTimeline([interrupt], [{ ...speaking, seq: 0, content: "" }]).length === 1, "working-only draft must not create an empty bubble");
  assert(!taskPhaseVisible({ status: "done" }), "a finished turn shows no status text (announced to screen readers only)");
  assert(!taskPhaseVisible({ status: "running" }) && !taskPhaseVisible({ status: "queued" }) && !taskPhaseVisible({ status: "waiting" }), "live state belongs to the Activity row, not a phase line");
  assert(taskPhaseVisible({ status: "cancelled" }) && taskPhaseVisible({ status: "interrupted" }), "an abnormal stop stays visible");
  const { buildTaskOwners } = await ui.load("/src/taskIssuePresentation.ts");
  const run = (id, bot_id, trigger_message_id, kind = "") => ({ id, conversation_id: "g", bot_id, kind, trigger_message_id, status: "done", created_at: "2026-09-17T03:41:00Z", updated_at: "2026-09-17T03:41:49Z" });
  const msg = (id, seq, role, sender_bot_id, run_id) => ({ id, seq, conversation_id: "g", role, sender_bot_id, run_id, content: id, created_at: "2026-09-17T03:41:49Z" });
  const ownerInput = owners => owners.map(owner => ({ anchor: owner.anchor, botId: owner.family.latest.bot_id }));
  // User message -> root run (tools) -> one final message from the same Bot: the block hangs off the user trigger.
  const userTrigger = msg("u1", 1, "user", undefined, undefined), final = msg("f1", 2, "assistant", "ledger", "r1");
  const rootOwners = buildTaskOwners([run("r1", "ledger", "u1")], [userTrigger, final], [], []);
  assert(rootOwners[0].anchor?.id === "u1", "a root run is anchored to its user trigger, above its final message");
  assert(precedingBlockBotIds({ kind: "message", id: "u1" }, ownerInput(rootOwners)).includes("ledger"), "user message -> block -> final: the final message does not repeat the name");
  // Two Bots answer one user message: the earlier block's Bot is not last, yet still named above.
  const twoOwners = buildTaskOwners([run("r1", "ledger", "u1"), run("r2", "news", "u1")], [userTrigger, final, msg("f2", 3, "assistant", "news", "r2")], [], []);
  const twoIds = precedingBlockBotIds({ kind: "message", id: "u1" }, ownerInput(twoOwners));
  assert(twoIds.includes("ledger") && twoIds.includes("news"), "every Bot labelled under one trigger is named once, not only the last block");
  // group_followup: trigger is another Bot's message.
  const followOwners = buildTaskOwners([run("r3", "metaphor", "f1", "group_followup")], [userTrigger, final, msg("f3", 3, "assistant", "metaphor", "r3")], [], []);
  assert(precedingBlockBotIds({ kind: "message", id: "f1" }, ownerInput(followOwners)).join() === "metaphor", "group follow-up block names its Bot once");
  assert(precedingBlockBotIds({ kind: "message", id: "x" }, ownerInput(rootOwners)).length === 0 && precedingBlockBotIds(undefined, []).length === 0, "no adjacent block, no grouping");
  // Synthetic copy of a production group view: user mention -> root run (7 tools, 49s) -> one final message.
  const { buildQuestionTimeline } = await ui.load("/src/questionTimeline.ts");
  const shapeRun = { id: "r9", conversation_id: "g", bot_id: "ledger", kind: null, parent_run_id: null, trigger_message_id: "u9", status: "done", created_at: "2026-10-13T10:41:20.630Z", updated_at: "2026-10-13T10:42:09.929Z" };
  const shapeUser = { id: "u9", seq: 60, conversation_id: "g", role: "user", run_id: "r9", content: "mention", created_at: "2026-10-13T10:41:20.630Z" };
  const shapeFinal = { id: "f9", seq: 61, conversation_id: "g", role: "assistant", sender_bot_id: "ledger", run_id: "r9", content: "final", created_at: "2026-10-13T10:42:09.906Z" };
  const nameAbove = (questions) => {
    const owners = buildTaskOwners([shapeRun], [shapeUser, shapeFinal], questions, []);
    const items = buildQuestionTimeline([shapeUser, shapeFinal], questions, false, []);
    const owned = item => item.kind === "question" && owners.some(owner => owner.family.attempts.some(r => r.id === item.question.run_id));
    const anchorOf = item => item?.kind === "message" ? { kind: "message", id: item.message.id } : item?.kind === "question" ? { kind: "question", id: item.question.question_id } : undefined;
    const above = items[previousRenderedIndex(items, items.length - 1, item => owned(item) && !precedingBlockBotIds(anchorOf(item), owners.map(o => ({ anchor: o.anchor, botId: o.family.latest.bot_id }))).length)];
    return precedingBlockBotIds(anchorOf(above), owners.map(o => ({ anchor: o.anchor, botId: o.family.latest.bot_id })));
  };
  assert(nameAbove([]).join() === "ledger", "production shape: final message right under its block does not repeat the name");
  const approval = { question_id: "q9", run_id: "r9", conversation_id: "g", bot_id: "ledger", question_type: "approval", status: "answered", created_at: "2026-10-13T10:41:40Z" };
  assert(nameAbove([approval]).join() === "ledger", "an approval card owned by the run draws nothing, so it must not hide the block above the final message");
  // Activity hidden (the default): only a live run, a pending decision/draft or a run-level issue draws a block.
  const { taskBlockVisible } = await ui.load("/src/TaskRunBlock.tsx");
  const hideRun = (id, status, extra = {}) => ({ id, conversation_id: "g", bot_id: "ledger", kind: "", trigger_message_id: "u1", status, created_at: "2026-10-13T10:41:20Z", updated_at: "2026-10-13T10:42:09Z", ...extra });
  const hideInput = (status, { questions = [], drafts = [], tools = [], hideActivity = true, revealed = false, extra } = {}) => {
    const latest = hideRun("rh", status, extra);
    return { owner: buildTaskOwners([latest], [userTrigger], questions, drafts)[0], tools, questions, drafts, messages: [], summaries: [], details: {}, connected: true, hideActivity, revealed };
  };
  assert(taskBlockVisible(hideInput("done")) === false, "a finished run that raised no card draws nothing when Activity is hidden");
  assert(taskBlockVisible(hideInput("done", { tools: [{ ...activity("t1", "2026-10-13T10:41:30Z", undefined, "rh"), conversation_id: "g", bot_id: "ledger" }] })) === false, "tool steps alone never draw a block when Activity is hidden");
  assert(taskBlockVisible(hideInput("done", { hideActivity: false })) === true, "Activity on: the finished Activity row is drawn");
  assert(taskBlockVisible(hideInput("done", { revealed: true })) === true, "a run opened from its message's menu draws its Activity");
  assert(taskBlockVisible(hideInput("running")) === true && taskBlockVisible(hideInput("queued")) === true, "a working run keeps its one live status line");
  assert(taskBlockVisible(hideInput("failed", { extra: { error: "boom" } })) === true, "a failed run's issue card stays visible");
  assert(taskBlockVisible(hideInput("cancelled")) === true, "an abnormal stop stays visible");
  const pendingApproval = { question_id: "qh", run_id: "rh", conversation_id: "g", bot_id: "ledger", question_type: "approval", status: "pending", question: "Send?", created_at: "2026-10-13T10:41:40Z" };
  assert(taskBlockVisible(hideInput("done", { questions: [pendingApproval] })) === true, "a pending approval card is never hidden with Activity");
  const answeredApproval = { ...pendingApproval, status: "answered", answer: true };
  assert(taskBlockVisible(hideInput("done", { questions: [answeredApproval] })) === false, "an answered approval is bookkeeping inside Activity");

  // One linear timeline: whatever is drawn between two calls of a run closes the stretch above it.
  {
    const { placeOwners } = await ui.load("/src/activityPlacements.ts");
    const { buildTaskOwners } = await ui.load("/src/taskIssuePresentation.ts");
    const { splitActivitySegments } = await ui.load("/src/toolTimeline.ts");
    const t = n => `2026-10-28T10:00:${String(n).padStart(2, "0")}Z`;
    const msg = (id, seq, at, role = "user", extra = {}) => ({ id, seq, created_at: at, conversation_id: "c", role, content: id, ...extra });
    const call = (id, at, name = "computer_action", run_id = "r1") => ({ ...base, call_id: id, run_id, name, arguments: "{}", started_at: t(at), updated_at: t(at + 1) });
    const r1 = { id: "r1", conversation_id: "c", bot_id: "b", status: "running", created_at: t(1), updated_at: t(30), trigger_message_id: "u1" };
    const owners = buildTaskOwners([r1], [msg("u1", 1, t(0))], [], []);
    const ownerAnchor = o => o.anchor && `${o.anchor.kind}:${o.anchor.id}`;
    const placed = (messages, tools, notes = [], runs = [r1]) => placeOwners({ owners: buildTaskOwners(runs, messages, [], []), tools, messages, notes, anchorTime: anchor => messages.find(m => m.id === anchor.id)?.created_at });
    const ids = placement => placement.segment.tools.map(tool => tool.call_id).join(",");

    const quiet = placed([msg("u1", 1, t(0))], [call("a", 2), call("b", 4), call("c", 6)]);
    assert(quiet.length === 1 && !quiet[0].segment && ownerAnchor(quiet[0].owner) === "message:u1", "an uninterrupted run stays one block, with no segment");

    const split = placed([msg("u1", 1, t(0)), msg("u2", 2, t(5))], [call("a", 2), call("b", 4), call("c", 6), call("d", 8)]);
    assert(split.length === 2 && split.every(p => p.segment), "a user message between two calls splits the run into two segments");
    assert(ids(split[0]) === "a,b" && ids(split[1]) === "c,d", "segments keep the calls in time order on each side of the message");
    assert(split[0].anchor?.id === "u1" && split[1].anchor?.id === "u2", "the first stretch stays at the trigger, the next sits under the message that closed it");
    assert(split[0].segment.index === 0 && split[0].segment.primary === false && split[1].segment.primary === true && split[1].segment.count === 2, "only the latest segment is primary (live line, cards)");
    assert(split[0].key !== split[1].key, "segments have distinct keys");

    const many = placed([msg("u1", 1, t(0)), msg("u2", 2, t(5)), msg("u3", 3, t(9)), msg("notice", 4, t(13), "assistant", { kind: "notice" })], [call("a", 2), call("b", 4), call("c", 6), call("d", 8), call("e", 10), call("f", 14)]);
    assert(many.map(ids).join("|") === "a,b|c,d|e|f", "each interruption starts the next stretch");
    assert(many.map(p => p.anchor.id).join(",") === "u1,u2,u3,notice", "stretches sit under their own closing message, in order");

    const sayNote = placed([msg("u1", 1, t(0))], [call("a", 2), call("b", 6)], [msg("n1", 2, t(4), "assistant", { kind: "progress", run_id: "r1" })]);
    assert(sayNote.length === 2 && sayNote[1].anchor?.id === "u1" && sayNote[1].segment.from === t(6) && sayNote[0].segment.until === t(6), "the Bot's own progress note closes the stretch and stays inside the one it ends");

    const other = placed([msg("u1", 1, t(0)), msg("ticket", 2, t(3), "assistant", { kind: "scheduled_task", run_id: "r2" })], [call("a", 2), call("b", 5)], [], [r1, { ...r1, id: "r2", created_at: t(3), trigger_message_id: "ticket" }]);
    assert(other.filter(p => p.owner.key.endsWith(":r1")).length === 2 && other.some(p => p.anchor?.id === "ticket" && p.segment), "another run's ticket splits this run too");

    const beforeHead = splitActivitySegments([call("a", 2), call("b", 4)], [{ at: t(0), anchor: "u1" }, { at: t(1), anchor: "u0" }], t(0));
    assert(beforeHead.length === 1 && beforeHead[0].anchor === "u0", "a message between the trigger and the first call puts the calls under it, with no empty leading segment");
    const headOnly = splitActivitySegments([call("a", 2), call("b", 4)], [{ at: t(0), anchor: "u1" }], t(0));
    assert(headOnly.length === 1 && headOnly[0].anchor === undefined, "the trigger itself is not a boundary for its own run");
    assert(splitActivitySegments([], [{ at: t(0), anchor: "u" }], t(0)).length === 0, "no calls, no segments");

    // Long runs: hundreds of calls cut by dozens of messages in one pass, nowhere near a frame.
    const at = n => new Date(Date.parse("2026-10-28T10:00:00Z") + n * 1000).toISOString();
    const long = Array.from({ length: 600 }, (_, i) => ({ ...call(`l${String(i).padStart(4, "0")}`, 0), started_at: at(2 + i), updated_at: at(3 + i) }));
    const longMessages = [msg("u1", 1, t(0)), ...Array.from({ length: 40 }, (_, i) => msg(`m${i}`, 2 + i, at(15 + i * 15)))];
    const startedAt = performance.now();
    let longPlaced;
    for (let run = 0; run < 20; run += 1) longPlaced = placed(longMessages, long);
    const perCall = (performance.now() - startedAt) / 20;
    assert(longPlaced.length > 30 && longPlaced.reduce((n, p) => n + p.segment.tools.length, 0) === 600, "a 600-call run is cut into segments without losing or repeating a call");
    assert(perCall < 25, `placing a 600-call run must stay cheap (took ${perCall.toFixed(1)} ms)`);
    console.log(`  placement of a 600-call run in ${longPlaced.length} segments: ${perCall.toFixed(2)} ms`);
  }
  console.log("tool timeline checks: PASS (Activity hiding, run ownership, cancelled history, pagination, overlapping requests, live activity, deduplication, precise ordering)");
} finally {
  await ui.close();
}
