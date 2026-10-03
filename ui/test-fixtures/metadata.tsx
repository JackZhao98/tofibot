// Synthetic acceptance entry only; production components use the real isolated API.
import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { api } from "../src/api";
import { MemoryPanel } from "../src/MemoryPanel";
import { WorkPanel, WorkPreview } from "../src/WorkPanel";
import { ScheduledTaskRow } from "../src/ScheduledTaskRow";
import { TimezoneProvider } from "../src/UserTimezone";
import type { Bot, Conversation, Memory, Message, Run, Schedule } from "../src/types";
import type { ScheduleOccurrence } from "../src/scheduleOccurrences";
import "../src/styles.css";
import "../src/interaction-system.css";
import "../src/v2-foundations.css";
import "../src/v2-app.css";

type Fixture = { bot: Bot; conversation: Conversation; memories: Memory[]; schedules: Schedule[]; messages: Message[]; runs: Run[]; occurrences: ScheduleOccurrence[] };
declare global { interface Window { metadataFixture: Fixture } }
const fixture = window.metadataFixture;

function Acceptance() {
  const [tab, setTab] = useState("runs");
  const [memories, setMemories] = useState(fixture.memories);
  const [refreshToken, setRefreshToken] = useState(0);
  useEffect(() => {
    const refresh = () => { void api.memories(fixture.conversation.id).then(result => { setMemories(result.memories); setRefreshToken(current => current + 1); }); };
    refresh();
    // Deterministic equivalent of Workspace memory events and agenda refresh.
    window.addEventListener("metadata-acceptance-refresh", refresh);
    return () => window.removeEventListener("metadata-acceptance-refresh", refresh);
  }, []);
  return <TimezoneProvider><main className="workspace" style={{ display: "block", maxWidth: 700, padding: 20, margin: "auto", height: "auto", minHeight: "100vh" }}>
    <p>Synthetic fixture · no live account or model</p>
    <nav style={{ display: "flex", flexWrap: "wrap", gap: 8, marginBottom: 18 }}>{[["runs", "执行卡"], ["schedules", "日程列表"], ["memories", "记忆列表"], ["preview", "预览"]].map(([key, label]) => <button key={key} onClick={() => setTab(key)}>{label}</button>)}</nav>
    {tab === "memories" && <div id="memories" data-revision={memories[0]?.revision}><MemoryPanel conversationId={fixture.conversation.id} memories={memories} onClose={() => setTab("runs")} onCreate={async input => { const memory = await api.createMemory(fixture.conversation.id, input); setMemories(current => [...current, memory]); }} onUpdate={async (id, input) => { const memory = await api.updateMemory(id, input); setMemories(current => current.map(item => item.id === id ? memory : item)); }} onDelete={async id => { await api.deleteMemory(id); setMemories(current => current.filter(item => item.id !== id)); }} /></div>}
    {tab === "schedules" && <div id="schedules"><WorkPanel conversation={fixture.conversation} conversations={[fixture.conversation]} bots={[fixture.bot]} refreshToken={refreshToken} onClose={() => setTab("runs")} onNavigate={() => {}} /></div>}
    {tab === "preview" && <div id="preview"><WorkPreview scope={{ type: "bots", id: fixture.bot.id }} refreshToken={refreshToken} onOpen={() => setTab("schedules")} /></div>}
    {tab === "runs" && <div id="runs" style={{ display: "grid", gap: 14 }}>{fixture.messages.filter(message => message.kind === "scheduled_task").map(message => {
      const occurrence = fixture.occurrences.find(item => item.root_run_id === message.run_id);
      return <ScheduledTaskRow key={message.id} message={message} run={fixture.runs.find(item => item.id === message.run_id)} occurrence={occurrence} schedule={fixture.schedules.find(item => item.id === occurrence?.schedule_id)} timezone="UTC" onRetry={async id => { await api.retryRun(id); }} />;
    })}</div>}
  </main></TimezoneProvider>;
}
createRoot(document.getElementById("root")!).render(<Acceptance />);
