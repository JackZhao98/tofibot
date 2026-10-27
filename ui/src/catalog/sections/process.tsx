// The process line (components.md section 1, "Process line"): what a Bot is doing right now as one quiet line that opens
// into the tool steps. Task block (live line, Activity summary), web tool row and the compact live line, on synthetic
// runs and tool steps; the clock of a live run starts when the frame opens.
import { CompactLiveLine, RunStartContext, WebToolActivityRun } from "../../App";
import { DeliveryNote } from "../../SendReceipt";
import { RunContextBody, type RunContextState } from "../../RunContext";
import { TaskRunBlock } from "../../TaskRunBlock";
import { buildTaskOwners } from "../../taskIssuePresentation";
import type { Message, Run, RunContextSnapshot, ToolActivity, ToolActivityRunSummary } from "../../types";
import type { CatalogState } from "../types";

const ago = (seconds: number) => new Date(Date.now() - seconds * 1000).toISOString();
const CONVERSATION = "dm_mochi";
const BOT = "bot_mochi";

const run = (id: string, status: Run["status"], startedSecondsAgo: number, endedSecondsAgo = 0): Run =>
  ({ id, conversation_id: CONVERSATION, bot_id: BOT, status, model: "synthetic-model", created_at: ago(startedSecondsAgo), updated_at: ago(endedSecondsAgo) });
const MCP_BROWSER = JSON.stringify({ name: "mcp_browser__open_page", arguments: { url: "https://docs.example.test/launch" } });
const tool = (runId: string, n: number, name: string, status: ToolActivity["status"], startedSecondsAgo: number, endedSecondsAgo: number, args = "{}", result = "Synthetic result"): ToolActivity =>
  ({ conversation_id: CONVERSATION, bot_id: BOT, run_id: runId, call_id: `call_${n}`, name, arguments: args, result: status === "completed" ? result : "", status, truncated: false, started_at: ago(startedSecondsAgo), updated_at: ago(endedSecondsAgo) });
const steps = (runId: string, live: boolean): ToolActivity[] => [
  tool(runId, 1, "search_history", "completed", 40, 36, JSON.stringify({ query: "launch review notes" })),
  tool(runId, 2, "call_mcp_tool", "completed", 35, 22, MCP_BROWSER),
  tool(runId, 3, "call_mcp_tool", live ? "running" : "completed", 20, live ? 0 : 6, MCP_BROWSER),
];
// A longer run: a failed call and a run of identical calls, for the compact Activity list.
const richSteps = (runId: string, live: boolean): ToolActivity[] => {
  const send = (n: number, ago0: number, ago1: number, status: ToolActivity["status"] = "completed") => tool(runId, n, "say", status, ago0, ago1, JSON.stringify({ text: `Update ${n}` }));
  return [
    tool(runId, 1, "search_history", "completed", 17, 16.9, JSON.stringify({ query: "launch review notes" })),
    tool(runId, 2, "workspace_capabilities", "completed", 16.8, 16.7),
    { ...tool(runId, 3, "call_mcp_tool", "failed", 16.6, 14, MCP_BROWSER), result: "" },
    send(4, 14, 13.9), send(5, 13.8, 13.7), send(6, 13.6, live ? 0 : 13.5, live ? "running" : "completed"),
  ];
};
const richSummary = (runId: string): ToolActivityRunSummary =>
  ({ run_id: runId, bot_id: BOT, tool_count: 6, completed_count: 5, failed_count: 1, interrupted_count: 0, pending_count: 0, started_at: ago(18), updated_at: ago(1) });
const summary = (runId: string, startedSecondsAgo: number, endedSecondsAgo: number): ToolActivityRunSummary =>
  ({ run_id: runId, bot_id: BOT, tool_count: 3, completed_count: 3, failed_count: 0, interrupted_count: 0, pending_count: 0, started_at: ago(startedSecondsAgo), updated_at: ago(endedSecondsAgo) });
const answer = (runId: string): Message =>
  ({ id: `m_${runId}`, conversation_id: CONVERSATION, seq: 3, role: "assistant", content: "The review is at 15:00 in the small room.", created_at: ago(2), sender_bot_id: BOT, sender_bot_name: "Mochi", run_id: runId });

// A message sent while the Bot works: the quiet note in the failed-line slot under the bubble (SendReceipt.tsx).
const sentWhileWorking = (state: "queued" | "read") => <article className="message message-user"><div className="message-body"><div className="message-content"><p>Also check the projector.</p></div><DeliveryNote state={state} /></div></article>;
// Parallel Bots in one group: one line each, keyed by run, stacked.
const twoLiveLines = () => <div>
  <CompactLiveLine label="Browsing the web · 12s" botId={BOT} botName="Mochi" at={ago(12)} showName ariaLabel="Mochi is working" onOpen={noop} />
  <CompactLiveLine label="Thinking · 4s" botId="bot_miso" botName="Miso" at={ago(4)} showName ariaLabel="Miso is working" />
</div>;

const noop = () => {};
const none = async () => {};
function block(runId: string, status: Run["status"], options: { live: boolean; liveStatus?: string; hideActivity: boolean; rich?: boolean }) {
  const current = status === "done" ? run(runId, "done", 48, 2) : run(runId, status, 12);
  const messages = status === "done" ? [answer(runId)] : [];
  const [owner] = buildTaskOwners([current], messages, [], []);
  return <TaskRunBlock owner={owner} tools={options.rich ? richSteps(runId, options.live) : steps(runId, options.live)} questions={[]} drafts={[]} messages={messages} summaries={status === "done" ? [options.rich ? richSummary(runId) : summary(runId, 48, 2)] : []} details={{ [runId]: { loaded: options.rich ? 6 : 3, toolCount: options.rich ? 6 : 3, hasMore: false, loading: false } }}
    onOpenTools={noop} onRefresh={none} onLoadDetails={none} liveStatus={options.liveStatus} hideActivity={options.hideActivity} renderQuestion={() => null} renderDraft={() => null} />;
}
function web(runId: string, live: boolean) {
  const items = steps(runId, live);
  return <RunStartContext.Provider value={new Map([[runId, Date.now() - 12_000]])}>
    <WebToolActivityRun runId={runId} items={items} notes={[]} summary={live ? undefined : summary(runId, 40, 6)} live={live} now={Date.now()} timezone="UTC" botName="Mochi" detail={{ loaded: 3, toolCount: 3, hasMore: false, loading: false }} onLoadDetails={none} />
  </RunStartContext.Provider>;
}

// "Context" inside View activity (D53): synthetic snapshots; the body is the production component, the fetch is not made.
const ctxMessage = (id: string, role: "user" | "assistant", text: string, minutesAgo: number, extra: Record<string, unknown> = {}) =>
  ({ kind: "history_message", role, chars: text.length, tokens: Math.ceil(text.length / 4), text, message: { id, created_at: ago(minutesAgo * 60), ...extra } });
const CONTEXT_SNAPSHOT: RunContextSnapshot = {
  version: 1, run_id: "run_ctx", conversation_id: CONVERSATION, bot_id: BOT, model: "synthetic-model", captured_at: ago(60),
  parts: [
    { kind: "system_guidance", role: "system", chars: 4200, tokens: 1050, text: "You are Bot Mochi (id=bot_mochi). Speak in your Bot instructions' persona and voice in every reply.\nPromise future work only after scheduling; no self-renewing loops." },
    { kind: "bot_instructions", role: "system", chars: 640, tokens: 160, text: "Bot instructions:\nAnswer briefly. Keep the launch checklist current.\nEnd of Bot instructions." },
    { kind: "tools", role: "system", chars: 21000, tokens: 5400, text: "[{\"name\":\"say\",\"description\":\"Tell the person something\"}]", items: [{ name: "browser_open", chars: 2400, tokens: 610 }, { name: "say", chars: 900, tokens: 230 }] },
    { kind: "tools_on_demand", role: "system", chars: 3000, tokens: 780, on_demand: true, text: "[{\"name\":\"schedule_create\"}]" },
    { kind: "memory", role: "user", chars: 120, tokens: 30, text: "[memory data]\nThe cat is called Pixel.\n[/memory data]" },
    { kind: "summary", role: "user", chars: 900, tokens: 225, text: "[derived history data]\nLaunch review moved to Thursday; venue booked.\n[/derived history data]" },
    { kind: "earlier_turns_note", role: "user", chars: 170, tokens: 42, text: "[earlier conversation]\nThis conversation began before the turns shown here.\n[/earlier conversation]" },
    ctxMessage("m1", "user", "Can you check the venue booking?", 52),
    ctxMessage("m2", "assistant", "Booked for Thursday 15:00.", 51, { gisted: true }),
    ctxMessage("m3", "user", "Thanks. What is on the launch checklist today?", 3, { request: true }),
    { kind: "run_context", role: "user", chars: 70, tokens: 18, text: "[run context]\nCurrent time: 2026-10-22 10:30 UTC\n[/run context]" },
    { kind: "request_note", role: "user", chars: 160, tokens: 40, text: "\nUse say(answer) for the final reply." },
  ],
  history: { considered: 3, included: 3, skipped: 0, skipped_by_summary: 0 },
  totals: { chars: 30000, tokens: 7600, on_demand_chars: 3000, on_demand_tokens: 780, dropped_texts: 0, by_kind: [
    { kind: "tools", parts: 1, chars: 21000, tokens: 5400 }, { kind: "system_guidance", parts: 1, chars: 4200, tokens: 1050 }, { kind: "summary", parts: 1, chars: 900, tokens: 225 },
    { kind: "bot_instructions", parts: 1, chars: 640, tokens: 160 }, { kind: "history_message", parts: 3, chars: 150, tokens: 40 }, { kind: "memory", parts: 1, chars: 120, tokens: 30 },
    { kind: "earlier_turns_note", parts: 1, chars: 170, tokens: 42 }, { kind: "run_context", parts: 1, chars: 70, tokens: 18 }, { kind: "request_note", parts: 1, chars: 160, tokens: 40 },
  ] },
};
const CONTEXT_CUT: RunContextSnapshot = {
  ...CONTEXT_SNAPSHOT,
  parts: CONTEXT_SNAPSHOT.parts.map(part => part.kind === "memory" ? { ...part, text: undefined, text_dropped: true } : part.kind === "summary" ? { ...part, redactions: 2 } : part),
  history: { considered: 41, included: 3, skipped: 38, skipped_by_summary: 30, earlier_unfetched: true },
  totals: { ...CONTEXT_SNAPSHOT.totals, dropped_texts: 1 },
};
const contextBody = (state: RunContextState) => <div className="run-context-catalog"><RunContextBody state={state} timezone="UTC" /></div>;
const openPart = (index: number): CatalogState["drive"] => async step => { await step.click(`.run-context-parts > .run-context-part:nth-child(${index}) > summary`); await step.wait(200); };

const expand = (selector: string): CatalogState["drive"] => async step => { await step.click(selector); await step.wait(400); };
const LONG = "Working out which of the available tools fits best for the launch review notes and the venue";
const ctx = { kind: "inline" as const, context: ["catalog-column"] };

export const processStates: CatalogState[] = [
  { id: "process-live", label: "Process line, live (Activity hidden)", note: "loading mark, what the Bot is doing, seconds; one line; a button that opens the steps", ...ctx, render: () => block("run_p1", "running", { live: true, liveStatus: "Browsing the web", hideActivity: true }) },
  { id: "process-live-open", label: "Process line, live, opened", note: "the same line as the Activity summary, the steps below; focus stays on the line", ...ctx, render: () => block("run_p2", "running", { live: true, liveStatus: "Browsing the web", hideActivity: true }), drive: expand(".task-run-live-line button") },
  { id: "process-live-long", label: "Process line, live, long text", note: "never wraps: it ends in an ellipsis; opened, the whole text is the first row", ...ctx, render: () => block("run_p3", "running", { live: true, liveStatus: LONG, hideActivity: true }) },
  { id: "process-live-long-open", label: "Process line, live, long text, opened", ...ctx, render: () => block("run_p4", "running", { live: true, liveStatus: LONG, hideActivity: true }), drive: expand(".task-run-live-line button") },
  { id: "process-live-focus", label: "Process line, live, keyboard focus", ...ctx, render: () => block("run_p5", "running", { live: true, liveStatus: "Browsing the web", hideActivity: true }), force: [{ selector: ".task-run-live-line button", state: "focus" }] },
  { id: "process-complete", label: "Process line, finished", note: "no mark; \"Activity · 3 tools · 46s\"", ...ctx, render: () => block("run_p6", "done", { live: false, hideActivity: false }) },
  { id: "process-complete-open", label: "Process line, finished, opened", ...ctx, render: () => block("run_p7", "done", { live: false, hideActivity: false }), drive: expand("details.task-activity > summary") },
  { id: "process-complete-rich-open", label: "Activity, finished, opened, with a failed call", note: "compact rows: status icon, no timeline rail; the failed row stays red; runs of the same call fold into one row with a count", ...ctx, render: () => block("run_p8", "done", { live: false, hideActivity: false, rich: true }), drive: expand("details.task-activity > summary") },
  { id: "process-live-rich-open", label: "Activity, live, opened, with a failed call", ...ctx, render: () => block("run_p9", "running", { live: true, liveStatus: "Sending a message", hideActivity: false, rich: true }), drive: expand("details.task-activity > summary") },
  { id: "process-web-live", label: "Web tool row, live", note: "Web build: the same line on the tool row", ...ctx, render: () => web("run_w1", true) },
  { id: "process-web-live-open", label: "Web tool row, live, opened", ...ctx, render: () => web("run_w2", true), drive: expand(".web-tool-toggle") },
  { id: "process-web-complete", label: "Web tool row, finished", ...ctx, render: () => web("run_w3", false) },
  { id: "process-web-complete-open", label: "Web tool row, finished, opened", ...ctx, render: () => web("run_w4", false), drive: expand(".web-tool-toggle") },
  { id: "context-ready", label: "Context in View activity", note: "summary line, share of the context by kind, then the parts in order; history messages are compact rows", ...ctx, render: () => contextBody({ status: "ready", snapshot: CONTEXT_SNAPSHOT }) },
  { id: "context-part-open", label: "Context, one part opened", note: "the original text in mono, with Copy", ...ctx, render: () => contextBody({ status: "ready", snapshot: CONTEXT_SNAPSHOT }), drive: openPart(1) },
  { id: "context-cut", label: "Context, history cut by the window", note: "skipped messages, older history never read, a part whose text was left out, secret-like values replaced", ...ctx, render: () => contextBody({ status: "ready", snapshot: CONTEXT_CUT }) },
  { id: "context-loading", label: "Context, loading", ...ctx, render: () => contextBody({ status: "loading" }) },
  { id: "context-unavailable", label: "Context, no snapshot", note: "context_snapshot_unavailable: an older run, or one pruned", ...ctx, render: () => contextBody({ status: "unavailable" }) },
  { id: "context-error", label: "Context, could not load", ...ctx, render: () => contextBody({ status: "error", message: "The request timed out." }) },
  { id: "process-compact", label: "Compact live line (nothing to open yet)", note: "a run that has not called a tool: the same line, not a button", ...ctx, render: () => <CompactLiveLine label="Thinking · 4s" botId={BOT} botName="Mochi" at={ago(4)} showName={false} ariaLabel="Mochi is working" /> },
  { id: "process-compact-open", label: "Compact live line with steps behind it", note: "activity hidden, a tool is running: a button", ...ctx, render: () => <CompactLiveLine label="Browsing the web · 12s" botId={BOT} botName="Mochi" at={ago(12)} showName={false} ariaLabel="Mochi is working" onOpen={noop} /> },
  { id: "delivery-queued", label: "Queued note under a sent message", note: "sent while the Bot works: \"Queued, read at next step\" until its run reads it", ...ctx, render: () => sentWhileWorking("queued") },
  { id: "delivery-read", label: "Read note under a sent message", note: "shown for a few seconds after the run read it, then it goes", ...ctx, render: () => sentWhileWorking("read") },
  { id: "process-two-live", label: "Two Bots working in a group", note: "one live line each (name, status, seconds), stacked, keyed by run", ...ctx, render: twoLiveLines },
];
