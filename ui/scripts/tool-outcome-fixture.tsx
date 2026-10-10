// Stub-data fixture for scripts/shot-tool-outcome.mjs. Synthetic data only.
import { createRoot } from "react-dom/client";
import { TaskRunBlock } from "../src/TaskRunBlock";
import { buildTaskOwners } from "../src/taskIssuePresentation";
import { i18nReady } from "../src/i18n";
import "../src/fonts.css";
import "../src/styles.css";
import "../src/interaction-system.css";
import "../src/settings-system.css";
import "../src/desktop-system.css";
import "../src/conversation-workspace.css";
import "../src/v2-foundations.css";
import "../src/v2-app.css";
import "../src/settings/settings-components.css";
import "../src/settings/settings-polish.css";
import "../src/web-tool-steps.css";
import "../src/web-mention.css";
import "../src/web-file-drop.css";
import "../src/web-sidebar-list.css";
import "../src/chat-header.css";
import "../src/context-card.css";

const at = "2026-10-10T08:53:00Z";
const base = { conversation_id: "c1", bot_id: "b1", run_id: "r1" };
const run = { id: "r1", conversation_id: "c1", bot_id: "b1", status: "done", trigger_message_id: "m1", model: "synthetic", created_at: at, updated_at: "2026-10-10T08:53:09Z" };
const request = { id: "m1", conversation_id: "c1", role: "user", content: "Call yourself tofi.", seq: 1, created_at: at };
const answer = { id: "m2", conversation_id: "c1", run_id: "r1", sender_bot_id: "b1", role: "assistant", content: "Done. I am tofi now.", seq: 2, created_at: "2026-10-10T08:53:09Z" };
const tool = (call_id: string, started_at: string, over: object) => ({ ...base, call_id, name: "set_bot_profile", arguments: '{"name":"tofi"}', result: "", status: "failed", truncated: false, started_at, updated_at: started_at, ...over });
const uncertain = { code: "unclassified_tool_failure", status: "uncertain_effect", execution_certainty: "unknown", message: "", next_action: "verify_effect" };
const definite = { code: "invalid_arguments", status: "validation_error", execution_certainty: "not_executed", message: "", next_action: "repair_arguments" };
const ok = { status: "completed", result: "{}" };

const scenes: Record<string, { tools: object[]; liveStatus?: string; running?: boolean }> = {
  // What the owner saw on v0.1.1: both calls reported as uncertain.
  incident: { tools: [tool("a", "2026-10-10T08:53:03Z", { outcome: uncertain }), tool("b", "2026-10-10T08:53:07Z", { arguments: '{"name":"tofi","instructions":"x"}', outcome: uncertain })] },
  // After the fix: a definite refusal, then the corrected call succeeds.
  recovered: { tools: [tool("a", "2026-10-10T08:53:03Z", { outcome: definite }), tool("b", "2026-10-10T08:53:07Z", { arguments: '{"name":"tofi","instructions":"x"}', ...ok })] },
  // A genuinely uncertain external action.
  uncertain: { tools: [tool("a", "2026-10-10T08:53:03Z", { name: "call_mcp_tool", arguments: '{"name":"crm__create_record","arguments":{}}', outcome: { ...uncertain, code: "mcp_result_unknown" } })] },
  live: { tools: [], running: true, liveStatus: new URLSearchParams(location.search).get("status") ?? "Thinking" },
};

async function main() {
  await i18nReady;
  const params = new URLSearchParams(location.search);
  const scene = scenes[params.get("scene") ?? "incident"];
  document.documentElement.dataset.theme = params.get("theme") ?? "light";
  const current = scene.running ? { ...run, status: "running" } : run;
  const messages = scene.running ? [request] : [request, answer];
  const owner = buildTaskOwners([current as never], messages as never, [], [])[0];
  createRoot(document.getElementById("root")!).render(
    <div className="workspace" style={{ padding: 24, maxWidth: 760, margin: "0 auto" }}>
      <p style={{ font: "var(--type-body)", margin: "0 0 12px" }}>Call yourself tofi.</p>
      <TaskRunBlock owner={owner} tools={scene.tools as never} questions={[]} drafts={[]} messages={messages as never} summaries={[]} details={{ r1: { hasMore: false } }} locale="en" liveStatus={scene.liveStatus} onOpenTools={() => {}} onRefresh={async () => {}} renderQuestion={() => null} renderDraft={() => null} />
    </div>,
  );
}
void main();
