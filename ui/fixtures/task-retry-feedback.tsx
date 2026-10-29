// Synthetic page for test:retry-feedback: one failed scheduled run in a TaskRunBlock; Retry is answered by the test.
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { i18nReady } from "../src/i18n";
import "../src/styles.css";
import "../src/v2-foundations.css";
import "../src/v2-app.css";
import "../src/web-tool-steps.css";
import { TaskRunBlock } from "../src/TaskRunBlock";
import { buildTaskOwners } from "../src/taskIssuePresentation";
import type { Message, Run } from "../src/types";

const at = "2026-10-28T09:00:00Z";
const failed = { id: "run-failed", conversation_id: "chat", bot_id: "bot", kind: "schedule", status: "failed", model: "claude-sonnet", created_at: at, updated_at: at, error: "main-model server_is_overloaded", failure: { code: "execution_failed", source: "runtime", message: "" } } as unknown as Run;
const child = { ...failed, id: "run-retry", status: "running", parent_run_id: failed.id, error: undefined, failure: undefined, created_at: "2026-10-28T09:05:00Z", updated_at: "2026-10-28T09:05:00Z" } as unknown as Run;

const trigger = { id: "msg-1", conversation_id: "chat", run_id: "run-failed", role: "user", content: "Daily digest", seq: 1, created_at: at } as unknown as Message;
type Gate = { resolve: () => void; reject: (message: string) => void };
declare global { interface Window { retryGate?: Gate; retryCalls: number } }
window.retryCalls = 0;

function Page() {
  const [runs, setRuns] = useState<Run[]>([failed]);
  const owners = buildTaskOwners(runs, [trigger], [], []);
  const childOf = new Map(runs.filter(run => run.parent_run_id).map(run => [run.parent_run_id!, run]));
  const retry = () => new Promise<void>((resolve, reject) => {
    window.retryCalls++;
    window.retryGate = { resolve: () => { setRuns(current => [...current, child]); resolve(); }, reject: message => reject(new Error(message)) };
  });
  return <main style={{ maxWidth: 640, margin: "0 auto", padding: 16 }}>
    {owners.map(owner => <TaskRunBlock key={owner.key} owner={owner} locale="en" tools={[]} questions={[]} drafts={[]} messages={[trigger]} summaries={[]} details={{}} onRetryRun={retry} retriedBy={childOf.get(owner.family.latest.id)} onOpenTools={() => {}} onRefresh={async () => {}} renderQuestion={() => null} renderDraft={() => null} />)}
  </main>;
}
i18nReady.then(() => createRoot(document.getElementById("root")!).render(<Page />));
