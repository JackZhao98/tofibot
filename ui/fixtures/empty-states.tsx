import { createRoot } from "react-dom/client";
import { useState, type ReactNode } from "react";
import { i18nReady, setLanguage } from "../src/i18n";
import "../src/fonts.css";
import "../src/styles.css";
import "../src/interaction-system.css";
import "../src/settings-system.css";
import "../src/desktop-system.css";
import "../src/conversation-workspace.css";
import "../src/v2-foundations.css";
import "../src/v2-app.css";
import "../src/settings/settings-components.css";
import "../src/web-tool-steps.css";
import "../src/web-mention.css";
import "../src/web-file-drop.css";
import "../src/web-sidebar-list.css";
import "../src/chat-header.css";
import "../src/context-card.css";
import { FirstRunHero } from "../src/FirstRunHero";
import { MemoryPanel } from "../src/MemoryPanel";
import { ArchivePanel } from "../src/ArchivePanel";
import { UsagePanel } from "../src/UsagePanel";
import { WorkPanel } from "../src/WorkPanel";
import { MCPSettings } from "../src/MCPSettings";
import { IntegrationBrowser } from "../src/IntegrationBrowser";
import { ExtensionPanel } from "../src/ExtensionPanel";
import { ComputerCredentials } from "../src/ComputerCredentials";
import { TerminalPanel } from "../src/TerminalPanel";
import { ViewOnlyChat } from "../src/ViewOnlyChat";
import { OwnerSessionGate } from "../src/OwnerSession";
import { CATS, EmptyCat, EmptyState } from "../src/EmptyCat";
import { TimezoneProvider } from "../src/UserTimezone";

// Synthetic only: every /api read answers with empty collections, and no other origin is contacted.
const params = new URLSearchParams(location.search);
const state = params.get("state") ?? "first-run";
localStorage.setItem("tofi:appearance", params.get("theme") === "dark" ? "dark" : "light");
const empty = { conversations: [], bots: [], agents: [], calls: [], contexts: [], schedules: [], goals: [], tasks: [], items: [], work_items: [], servers: [], skills: [], keys: [], credentials: [], messages: [], memories: [], terminals: [], sessions: [], drafts: [], questions: [] };
const originalFetch = window.fetch.bind(window);
window.fetch = async (input, init) => {
  const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, location.origin);
  if (url.origin !== location.origin) throw new Error("Synthetic fixture forbids external requests");
  if (state === "gate-offline" && url.pathname === "/api/server-info") return new Response("{}", { status: 503 });
  if (!url.pathname.startsWith("/api/")) return originalFetch(input, init);
  const body = { ...empty, ok: true, result: { ...empty, terminals: [], terminal_id: "", sessions: [] }, summary: {}, totals: {}, catalog: null };
  return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
};

const noop = () => {};
const conversation = { id: "c1", kind: "dm", name: "Synthetic", bot_id: "b1", bot_ids: ["b1"], updated_at: "2026-10-01T00:00:00Z" } as never;
const oneMemory = [{ id: "m1", conversation_id: "c1", title: "Synthetic", description: "", content: "Synthetic memory", created_at: "2026-10-01T00:00:00Z", updated_at: "2026-10-01T00:00:00Z" }] as never;

function Frame({ children }: { children: ReactNode }) {
  return <div className="workspace" style={{ padding: 16, maxWidth: 720, margin: "0 auto" }}>{children}</div>;
}

function Case() {
  const [query, setQuery] = useState(state === "integrations-no-match" ? "zzzz" : "");
  switch (state) {
    case "first-run": return <div style={{ minHeight: "100dvh", display: "grid" }}><FirstRunHero onCreate={noop} busy={false} error="" /></div>;
    case "first-run-error": return <div style={{ minHeight: "100dvh", display: "grid" }}><FirstRunHero onCreate={noop} busy={false} error="Synthetic error" /></div>;
    case "archive-empty": return <Frame><ArchivePanel onClose={noop} onOpen={noop} onLoaded={noop} onChanged={async () => {}} onDelete={noop} /></Frame>;
    case "memory-empty": return <Frame><MemoryPanel memories={[]} conversationId="c1" scope="bot" onClose={noop} onCreate={async () => {}} onUpdate={async () => {}} onDelete={async () => {}} /></Frame>;
    case "memory-no-match": return <Frame><MemoryPanel memories={oneMemory} conversationId="c1" scope="bot" onClose={noop} onCreate={async () => {}} onUpdate={async () => {}} onDelete={async () => {}} /></Frame>;
    case "usage-no-agents": return <Frame><UsagePanel timezone="UTC" /></Frame>;
    case "work-empty": return <Frame><WorkPanel conversation={conversation} conversations={[conversation]} bots={[]} refreshToken={0} onClose={noop} onNavigate={noop} /></Frame>;
    case "mcp-empty": return <Frame><MCPSettings /></Frame>;
    case "integrations-no-match": return <Frame><IntegrationBrowser query={query} servers={[]} onQueryChange={setQuery} onBack={noop} onSelect={noop} onCustom={noop} /></Frame>;
    case "skills-empty": return <Frame><ExtensionPanel bots={[]} kind="skills" /></Frame>;
    case "credentials-env-empty": return <Frame><ComputerCredentials bots={[]} /></Frame>;
    case "terminal-empty": return <Frame><div style={{ height: 420, display: "grid" }}><TerminalPanel botId="b1" botName="Synthetic" onClose={noop} /></div></Frame>;
    case "view-only-empty": return <Frame><ViewOnlyChat target={{ source: conversation, target: conversation }} bots={[]} onClose={noop} card /></Frame>;
    case "gate-offline": return <OwnerSessionGate><p>app</p></OwnerSessionGate>;
    case "many": return <Frame><div className="many">{Array.from({ length: 14 }, (_, index) => <EmptyState key={index} name={`many-${index}`} look={CATS.mochi} pose="curious"><p>Synthetic</p></EmptyState>)}</div></Frame>;
    case "scroll": return <div style={{ padding: 16 }}><EmptyCat look={CATS.mochi} pose="awake" name="top" /><div style={{ height: 3000 }} /><EmptyCat look={CATS.miso} pose="awake" name="bottom" /></div>;
    default: return <p>unknown state</p>;
  }
}

void i18nReady.then(async () => {
  const lang = params.get("lang");
  if (lang) await setLanguage(lang as never);
  document.documentElement.lang = lang ?? "en";
  createRoot(document.getElementById("root")!).render(<TimezoneProvider><Case /></TimezoneProvider>);
});
