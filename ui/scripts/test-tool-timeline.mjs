import { openUiModules } from "./ui-modules.mjs";

// Load through Vite so modules that use the i18n catalogs resolve like the app.
// Assertions pin the shipped zh-CN copy.
const ui = await openUiModules({ language: "zh-CN" });

try {
  const { activeToolForRun, buildToolRunAnchors, buildToolSummaryAnchors, buildToolTimeline, elapsedToolSeconds, orderToolActivities, toolActionLabel, toolArgumentPreview, toolAttemptIssues, toolDisplayLabel, toolDisplayState, toolStepDetail, toolStepTitle } = await ui.load("/src/toolTimeline.ts");
  const { mergeMessageTimeline } = await ui.load("/src/messageTimeline.ts");
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
  assert(toolActionLabel({ name: "computer_action", arguments: '{"action":"desktop.click"}' }) === "正在操作电脑", "desktop action must identify computer use");
  assert(toolActionLabel({ name: "tofi_read", arguments: "{}" }) === "正在检查文档", "read tool must identify document work");
  assert(toolActionLabel({ name: "tofi_shell", arguments: "{}" }) === "正在运行命令", "shell action must identify command work");
  assert(toolActionLabel({ name: "computer_action", arguments: "broken" }) === "正在操作电脑", "malformed action must not guess specifics");
  assert(toolActionLabel({ name: "search_mcp_tools", arguments: "{}" }) === "正在查找可用工具", "tool discovery is not web browsing");
  assert(toolActionLabel({ name: "call_mcp_tool", arguments: '{"name":"mcp__web__search"}' }) === "正在浏览网页", "resolved connected web tool identifies browsing");
  assert(toolActionLabel({ name: "call_mcp_tool", arguments: '{"name":"mcp__crm__lookup"}' }) === "正在使用连接的工具", "an unrecognised remote tool is named by its role");
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
  console.log("tool timeline checks: PASS (run ownership, cancelled history, pagination, overlapping requests, live activity, deduplication, precise ordering)");
} finally {
  await ui.close();
}
