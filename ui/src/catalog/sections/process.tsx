// The process line (components.md section 1, "Process line"): what a Bot is doing right now as one quiet line that opens
// into the tool steps. Task block (live line, Activity summary), web tool row and the compact live line, on synthetic
// runs and tool steps; the clock of a live run starts when the frame opens.
import { CompactLiveLine, RunStartContext, WebToolActivityRun } from "../../App";
import { TaskRunBlock } from "../../TaskRunBlock";
import { buildTaskOwners } from "../../taskIssuePresentation";
import type { Message, Run, ToolActivity, ToolActivityRunSummary } from "../../types";
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
const summary = (runId: string, startedSecondsAgo: number, endedSecondsAgo: number): ToolActivityRunSummary =>
  ({ run_id: runId, bot_id: BOT, tool_count: 3, completed_count: 3, failed_count: 0, interrupted_count: 0, pending_count: 0, started_at: ago(startedSecondsAgo), updated_at: ago(endedSecondsAgo) });
const answer = (runId: string): Message =>
  ({ id: `m_${runId}`, conversation_id: CONVERSATION, seq: 3, role: "assistant", content: "The review is at 15:00 in the small room.", created_at: ago(2), sender_bot_id: BOT, sender_bot_name: "Mochi", run_id: runId });

const noop = () => {};
const none = async () => {};
function block(runId: string, status: Run["status"], options: { live: boolean; liveStatus?: string; hideActivity: boolean }) {
  const current = status === "done" ? run(runId, "done", 48, 2) : run(runId, status, 12);
  const messages = status === "done" ? [answer(runId)] : [];
  const [owner] = buildTaskOwners([current], messages, [], []);
  return <TaskRunBlock owner={owner} tools={steps(runId, options.live)} questions={[]} drafts={[]} messages={messages} summaries={status === "done" ? [summary(runId, 48, 2)] : []} details={{ [runId]: { loaded: 3, toolCount: 3, hasMore: false, loading: false } }}
    onOpenTools={noop} onRefresh={none} onLoadDetails={none} liveStatus={options.liveStatus} hideActivity={options.hideActivity} renderQuestion={() => null} renderDraft={() => null} />;
}
function web(runId: string, live: boolean) {
  const items = steps(runId, live);
  return <RunStartContext.Provider value={new Map([[runId, Date.now() - 12_000]])}>
    <WebToolActivityRun runId={runId} items={items} notes={[]} summary={live ? undefined : summary(runId, 40, 6)} live={live} now={Date.now()} timezone="UTC" botName="Mochi" detail={{ loaded: 3, toolCount: 3, hasMore: false, loading: false }} onLoadDetails={none} />
  </RunStartContext.Provider>;
}

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
  { id: "process-web-live", label: "Web tool row, live", note: "Web build: the same line on the tool row", ...ctx, render: () => web("run_w1", true) },
  { id: "process-web-live-open", label: "Web tool row, live, opened", ...ctx, render: () => web("run_w2", true), drive: expand(".web-tool-toggle") },
  { id: "process-web-complete", label: "Web tool row, finished", ...ctx, render: () => web("run_w3", false) },
  { id: "process-web-complete-open", label: "Web tool row, finished, opened", ...ctx, render: () => web("run_w4", false), drive: expand(".web-tool-toggle") },
  { id: "process-compact", label: "Compact live line (nothing to open yet)", note: "a run that has not called a tool: the same line, not a button", ...ctx, render: () => <CompactLiveLine label="Thinking · 4s" botId={BOT} botName="Mochi" at={ago(4)} showName={false} ariaLabel="Mochi is working" /> },
  { id: "process-compact-open", label: "Compact live line with steps behind it", note: "activity hidden, a tool is running: a button", ...ctx, render: () => <CompactLiveLine label="Browsing the web · 12s" botId={BOT} botName="Mochi" at={ago(12)} showName={false} ariaLabel="Mochi is working" onOpen={noop} /> },
];
