import * as Collapsible from "@radix-ui/react-collapsible";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { request } from "./api";
import { BotSkills } from "./BotSkills";
import { Button } from "./ds/Button/Button";
import { CollapsibleContent } from "./ds/Collapsible/Collapsible";
import { IconButton } from "./ds/IconButton/IconButton";
import { SettingsSection } from "./ds/SettingsSection/SettingsSection";
import { displayName, markName, presetFor } from "./connections/names";
import { message, type MCP } from "./connections/shared";
import { storedKind, type ConnectionKind } from "./connections/status";
import { useTranslation } from "./i18n";
import { TofiIcon } from "./icons";
import { Select } from "./Select";
import { ServiceMark } from "./ServiceMark";
import { openConnectionDeepLink } from "./settings/deepLinks";
import { SettingsSwitch } from "./settings/components";
import "./extensions-system.css";
import "./bot-tools.css";

/**
 * Bot settings > Tools: what this Bot can use and how often it asks. Connections are open or closed for this Bot; each open one lists
 * its tools with this Bot's own tier (it follows the connection's tier until the owner sets one); Skills are switched here too. All of
 * it saves on change and applies from the next run. Lazy chunk `botSettings`.
 */
export type BotConnectionRow = {
  server: string; name: string; mark: string;
  /** From the stored check; null before any check ran (no status is shown). */
  kind: ConnectionKind | null; tools?: number;
  /** This Bot may use it. A paused connection is closed to every Bot whatever this says. */
  enabled: boolean;
};

export type BotTierValue = "read_only" | "write" | "high_risk";
/** One tool as this Bot sees it: `tier` is what applies, `global_tier` what the connection says, `bot_tier` this Bot's own choice. */
export type BotToolTier = { name: string; tier: BotTierValue; global_tier?: BotTierValue; bot_tier?: BotTierValue; bot_changed?: boolean };
/** null while loading. */
export type BotTierState = { tools: BotToolTier[] | null; error: boolean; saving: string };

export type BotToolsViewProps = {
  botName: string;
  /** null while loading. */
  connections: BotConnectionRow[] | null; connectionsError: string; saving: string;
  onToggle: (server: string, next: boolean) => void; onOpenConnection: (server: string) => void; onAddConnection: () => void;
  /** Tier lists by connection; a connection with no entry has not been opened yet. */
  tiers: Record<string, BotTierState>; onOpenTiers: (server: string) => void; onSetTier: (server: string, tool: string, tier: BotTierValue | "") => void;
  skills: ReactNode; onOpenSkills: () => void;
  /** Starts open (the catalog draws every state). */
  openTiers?: string[];
};

const TIER_WORD = { read_only: "extensions:tools.tier.read_only", write: "extensions:tools.tier.write", high_risk: "extensions:tools.tier.high_risk" } as const;

/** The status words are the Connections page's own. */
function kindKey(kind: ConnectionKind) {
  switch (kind) {
    case "ready": return "extensions:status.connected" as const;
    case "needs_auth": return "extensions:status.needs_auth" as const;
    case "api_disabled": return "extensions:status.setup" as const;
    case "error": return "extensions:status.failed" as const;
    case "paused": return "extensions:status.paused" as const;
    default: return "extensions:status.checking" as const;
  }
}

function TierRows({ server, botName, state, onSetTier }: { server: string; botName: string; state: BotTierState; onSetTier: BotToolsViewProps["onSetTier"] }) {
  const { t } = useTranslation(["bots", "extensions"]);
  if (state.error) return <p className="error-text" role="alert">{t("bots:tools.tier.error")}</p>;
  if (state.tools === null) return <p className="field-note" role="status">{t("bots:tools.connections.loading")}</p>;
  if (state.tools.length === 0) return <p className="field-note">{t("bots:tools.tier.empty")}</p>;
  return <ul className="bt-tools">
    {state.tools.map(tool => {
      const global = tool.global_tier ?? tool.tier;
      return <li key={tool.name} className="bt-tool" data-tool={tool.name} data-own={tool.bot_tier ? "true" : undefined}>
        <span className="bt-tool-copy"><code>{tool.name}</code>{tool.bot_changed && <small className="bt-warning">{t("bots:tools.tier.changed")}</small>}</span>
        <Select size="sm" className="bt-tier" aria-label={t("bots:tools.tier.label", { tool: tool.name, bot: botName })} disabled={state.saving === tool.name} value={tool.bot_tier ?? ""}
          onValueChange={value => onSetTier(server, tool.name, value as BotTierValue | "")}
          options={[{ value: "", label: t("bots:tools.tier.follow", { tier: t(TIER_WORD[global]) }) }, ...(Object.keys(TIER_WORD) as BotTierValue[]).map(value => ({ value, label: t(TIER_WORD[value]) }))]} />
      </li>;
    })}
  </ul>;
}

/** Presentational: everything arrives as props so every state can be drawn without a server. */
export function BotToolsView(p: BotToolsViewProps) {
  const { t } = useTranslation(["bots", "extensions"]);
  return <div className="bot-tools">
    <SettingsSection id="connections" title={t("bots:tools.connections.title")} description={t("bots:tools.connections.hint")}>
      {p.connections === null && <p className="field-note bt-pad" role="status">{t("bots:tools.connections.loading")}</p>}
      {p.connectionsError && <p className="error-text bt-pad" role="alert">{p.connectionsError}</p>}
      {p.connections !== null && p.connections.length === 0 && <div className="bt-empty"><p>{t("bots:tools.connections.none")}</p><Button size="sm" onClick={p.onAddConnection}>{t("bots:tools.connections.add")}</Button></div>}
      {p.connections?.map(row => {
        const paused = row.kind === "paused";
        const off = !row.enabled || paused;
        const summary = [row.kind ? t(kindKey(row.kind)) : "", row.tools ? t("extensions:connections.row.tools", { count: row.tools }) : ""].filter(Boolean).join(" · ");
        const state = p.tiers[row.server];
        return <Collapsible.Root key={row.server} className="bt-conn" data-server={row.server} data-off={off ? "true" : undefined} data-paused={paused ? "true" : undefined} defaultOpen={p.openTiers?.includes(row.server)} onOpenChange={open => { if (open) p.onOpenTiers(row.server); }}>
          <div className="bt-conn-head">
            <ServiceMark name={row.mark} />
            <span className="bt-row-copy"><strong>{row.name}</strong>{summary && <small>{summary}</small>}</span>
            <SettingsSwitch label={t("bots:tools.connections.switch", { connection: row.name, bot: p.botName })} checked={row.enabled && !paused} disabled={paused || p.saving === row.server} busy={p.saving === row.server} showSaved={false} onChange={next => p.onToggle(row.server, next)} />
            {!off && <Collapsible.Trigger className="bt-tiers-trigger" aria-label={t("bots:tools.tier.show", { connection: row.name })} data-hint={t("bots:tools.tier.show", { connection: row.name })}><TofiIcon name="chevron-down" size={16} aria-hidden="true" /></Collapsible.Trigger>}
            <IconButton size="sm" icon="chevron-right" label={t("bots:tools.connections.open", { connection: row.name })} onClick={() => p.onOpenConnection(row.server)} />
          </div>
          {!off && <CollapsibleContent><div className="bt-tiers">{state ? <TierRows server={row.server} botName={p.botName} state={state} onSetTier={p.onSetTier} /> : null}</div></CollapsibleContent>}
        </Collapsible.Root>;
      })}
    </SettingsSection>
    <SettingsSection id="skills" title={t("bots:skills.title")} description={t("bots:skills.hint")} actions={<Button size="sm" onClick={p.onOpenSkills}>{t("bots:tools.skills.manage")}</Button>}>
      <div className="bt-pad">{p.skills}</div>
    </SettingsSection>
  </div>;
}

/** Bot settings > Tools, wired: the connections and this Bot's access, each connection's tool tiers when opened, and the Skills. */
export function BotTools({ bot, refreshToken = 0, leaveTo, onOpenSettings }: {
  bot: { id: string; name: string }; refreshToken?: number;
  /** Runs `go` once the cover may close (unsaved changes are asked about first). */
  leaveTo: (go: () => void) => void;
  onOpenSettings: (tab: "skills" | "models" | "computer" | "permissions" | "connections") => void;
}) {
  const { t } = useTranslation("bots");
  const [servers, setServers] = useState<MCP[] | null>(null);
  const [access, setAccess] = useState<Record<string, boolean>>({});
  const [error, setError] = useState<"load" | "save" | string>("");
  const [saving, setSaving] = useState("");
  const [tiers, setTiers] = useState<Record<string, BotTierState>>({});
  const opened = useRef<Set<string>>(new Set());
  const alive = useRef(true);
  const id = encodeURIComponent(bot.id);
  const load = useCallback(async () => {
    const [list, mine] = await Promise.allSettled([
      request<{ servers: MCP[] }>("/api/extensions/mcp"),
      request<{ connections: { server: string; enabled: boolean }[] }>(`/api/bots/${id}/connections`),
    ]);
    if (!alive.current) return;
    if (list.status === "fulfilled" && mine.status === "fulfilled") {
      setServers(list.value.servers ?? []);
      setAccess(Object.fromEntries((mine.value.connections ?? []).map(row => [row.server, row.enabled])));
      setError("");
    } else { setServers(current => current ?? []); setError("load"); }
  }, [id]);
  const loadTiers = useCallback(async (server: string) => {
    try {
      const value = await request<{ tools?: BotToolTier[] }>(`/api/bots/${id}/tool-tiers/${encodeURIComponent(server)}`);
      if (alive.current) setTiers(current => ({ ...current, [server]: { tools: value.tools ?? [], error: false, saving: "" } }));
    } catch { if (alive.current) setTiers(current => ({ ...current, [server]: { tools: current[server]?.tools ?? null, error: true, saving: "" } })); }
  }, [id]);
  // A new Bot starts from nothing. A refresh keeps what is on screen (so an open tool list stays open) and only swaps the data.
  useEffect(() => { setServers(null); setTiers({}); opened.current = new Set(); }, [id]);
  useEffect(() => {
    alive.current = true; void load();
    const focus = () => { if (!document.hidden) { void load(); opened.current.forEach(server => void loadTiers(server)); } };
    const changed = () => { void load(); opened.current.forEach(server => void loadTiers(server)); };
    window.addEventListener("focus", focus);
    window.addEventListener("tofi:connections-changed", changed);
    return () => { alive.current = false; window.removeEventListener("focus", focus); window.removeEventListener("tofi:connections-changed", changed); };
  }, [load, loadTiers, refreshToken]);

  async function toggle(server: string, next: boolean) {
    if (saving) return;
    setSaving(server); setError("");
    setAccess(current => ({ ...current, [server]: next }));
    try { await request(`/api/bots/${id}/connections/${encodeURIComponent(server)}`, { method: "PUT", body: JSON.stringify({ enabled: next }) }); }
    catch (cause) { if (alive.current) { setAccess(current => ({ ...current, [server]: !next })); setError(message(cause) || t("tools.connections.save_failed")); } }
    finally { if (alive.current) setSaving(""); }
  }
  function openTiers(server: string) {
    if (opened.current.has(server)) return;
    opened.current.add(server);
    setTiers(current => ({ ...current, [server]: { tools: null, error: false, saving: "" } }));
    void loadTiers(server);
  }
  async function setTier(server: string, tool: string, tier: BotTierValue | "") {
    setTiers(current => current[server] ? { ...current, [server]: { ...current[server], saving: tool } } : current);
    try {
      await request(`/api/bots/${id}/tool-tiers/${encodeURIComponent(server)}/${encodeURIComponent(tool)}`, { method: "PUT", body: JSON.stringify({ tier }) });
      await loadTiers(server);
    } catch { if (alive.current) setTiers(current => current[server] ? { ...current, [server]: { ...current[server], saving: "", error: true } } : current); }
  }

  const rows: BotConnectionRow[] | null = servers === null ? null : servers.map(server => {
    const name = displayName(server, presetFor(server));
    return { server: server.name, name, mark: markName(server, name), kind: storedKind(server), tools: server.status?.tool_count, enabled: access[server.name] !== false };
  });
  return <BotToolsView botName={bot.name} connections={rows} connectionsError={error === "load" ? t("tools.connections.error") : error} saving={saving}
    onToggle={(server, next) => void toggle(server, next)} onOpenConnection={server => leaveTo(() => openConnectionDeepLink({ server }))} onAddConnection={() => leaveTo(() => onOpenSettings("connections"))}
    tiers={tiers} onOpenTiers={openTiers} onSetTier={(server, tool, tier) => void setTier(server, tool, tier)}
    skills={<BotSkills botId={bot.id} refreshToken={refreshToken} bare />} onOpenSkills={() => leaveTo(() => onOpenSettings("skills"))} />;
}
