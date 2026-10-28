import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { api, request } from "./api";
import { BotAvatar } from "./BotAvatar";
import { BotInspector } from "./BotInspector";
import { BotSkills } from "./BotSkills";
import { useHasComputer } from "./computerCapability";
import { displayName, markName, presetFor } from "./connections/names";
import { message, type MCP } from "./connections/shared";
import { storedKind, type ConnectionKind } from "./connections/status";
import { Disclosure } from "./InteractionSystem";
import { useTranslation } from "./i18n";
import { formatDateTime } from "./i18n/format";
import { TofiIcon } from "./icons";
import { ServiceMark } from "./ServiceMark";
import { openConnectionDeepLink } from "./settings/deepLinks";
import type { StandingAllowance } from "./StandingAllowances";
import type { WebSourceEntry, WebToolsView } from "./types";
import "./extensions-system.css";
import "./bot-tools.css";

/**
 * Bot settings > Tools (docs/design/tools-and-connections.md section 3.4): what this Bot can use, in one place. Connections and
 * Skills are the owner's to switch here; Web, Computer and Built in state what follows workspace settings; the last group lists what
 * this Bot may do without asking. Lazy chunk `botSettings`.
 */
export type BotConnectionRow = {
  server: string; name: string; mark: string;
  /** From the stored check; null before any check ran (no status is shown). */
  kind: ConnectionKind | null; tools?: number; allowed: number;
  /** This Bot may use it. A paused connection is closed to every Bot whatever this says. */
  enabled: boolean;
};

export type BotToolsViewProps = {
  botName: string;
  /** null while loading. */
  connections: BotConnectionRow[] | null; connectionsError: string; saving: string;
  onToggle: (server: string, next: boolean) => void; onOpenConnection: (server: string) => void; onAddConnection: () => void;
  skills: ReactNode; onOpenSkills: () => void;
  web: WebToolsView | null; onOpenWeb: () => void;
  computer: boolean; onOpenComputer: () => void;
  inspector: ReactNode;
  allowances: StandingAllowance[] | null; leaving: string[]; onGone: (id: string) => void; remembered: number; revoking: string; permissionsError: string; serverName: (server: string) => string;
  onRevoke: (item: StandingAllowance) => void; onOpenPermissions: () => void;
};

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

/** "Claude native → Tavily → Brave": the sources the chain will try, in order. */
function chainText(entries: WebSourceEntry[]) {
  return entries.filter(entry => entry.enabled && (entry.status === "ready" || entry.status === "always")).map(entry => entry.label).join(" → ");
}

function Group({ id, title, hint, action, children }: { id: string; title: string; hint?: string; action?: ReactNode; children: ReactNode }) {
  return <section className="bt-group" data-group={id} aria-label={title}>
    <header className="bt-group-head"><div><h3>{title}</h3>{hint && <p>{hint}</p>}</div>{action}</header>
    {children}
  </section>;
}

function LinkButton({ children, onClick }: { children: ReactNode; onClick: () => void }) {
  return <button type="button" className="bt-link" onClick={onClick}>{children}<TofiIcon name="chevron-right" size={14} aria-hidden="true" /></button>;
}

/** Presentational: everything arrives as props so every state can be drawn without a server. */
export function BotToolsView(p: BotToolsViewProps) {
  const { t } = useTranslation(["bots", "extensions", "settings"]);
  const searchLine = !p.web ? "" : !p.web.search_offered ? t("bots:tools.web.search_none") : t("bots:tools.web.on", { chain: chainText(p.web.search) });
  const fetchLine = !p.web ? "" : t("bots:tools.web.on", { chain: chainText(p.web.fetch) });
  const when = (value: string) => formatDateTime(value);
  return <div className="bot-tools">
    <Group id="connections" title={t("bots:tools.connections.title")} hint={t("bots:tools.connections.hint")}>
      {p.connections === null && <p className="field-note" role="status">{t("bots:tools.connections.loading")}</p>}
      {p.connectionsError && <p className="error-text" role="alert">{p.connectionsError}</p>}
      {p.connections !== null && p.connections.length === 0 && <div className="bt-empty"><p>{t("bots:tools.connections.none")}</p><LinkButton onClick={p.onAddConnection}>{t("bots:tools.connections.add")}</LinkButton></div>}
      {p.connections !== null && p.connections.length > 0 && <ul className="bt-list">
        {p.connections.map(row => {
          const paused = row.kind === "paused";
          const summary = [row.kind ? t(kindKey(row.kind)) : "", row.tools ? t("extensions:connections.row.tools", { count: row.tools }) : "", row.allowed > 0 ? t("bots:tools.connections.allowed", { count: row.allowed }) : ""].filter(Boolean).join(" · ");
          return <li key={row.server} className="bt-row bt-conn" data-server={row.server} data-off={!row.enabled || paused ? "true" : undefined} data-paused={paused ? "true" : undefined}>
            <ServiceMark name={row.mark} />
            <span className="bt-row-copy"><strong>{row.name}</strong>{summary && <small>{summary}</small>}</span>
            <label className="debug-toggle auto-switch">
              <input type="checkbox" role="switch" aria-label={t("bots:tools.connections.switch", { connection: row.name, bot: p.botName })} checked={row.enabled && !paused} disabled={paused || p.saving === row.server} onChange={event => p.onToggle(row.server, event.target.checked)} />
            </label>
            <button type="button" className="bt-open" aria-label={t("bots:tools.connections.open", { connection: row.name })} data-hint={t("bots:tools.connections.open", { connection: row.name })} onClick={() => p.onOpenConnection(row.server)}><TofiIcon name="chevron-right" size={16} aria-hidden="true" /></button>
          </li>;
        })}
      </ul>}
    </Group>

    <Group id="skills" title={t("bots:skills.title")} action={<LinkButton onClick={p.onOpenSkills}>{t("bots:tools.skills.manage")}</LinkButton>}>
      {p.skills}
    </Group>

    <Group id="web" title={t("bots:tools.web.title")} action={<LinkButton onClick={p.onOpenWeb}>{t("bots:tools.web.configure")}</LinkButton>}>
      {p.web === null ? <p className="field-note" role="status">{t("bots:tools.connections.loading")}</p> : <dl className="bt-facts">
        <div><dt>{t("bots:tools.web.search")}</dt><dd>{searchLine}</dd></div>
        <div><dt>{t("bots:tools.web.fetch")}</dt><dd>{fetchLine}</dd></div>
      </dl>}
    </Group>

    <Group id="computer" title={t("bots:tools.computer.title")} action={<LinkButton onClick={p.onOpenComputer}>{t("bots:tools.computer.open")}</LinkButton>}>
      <p className="bt-line">{p.computer ? t("bots:tools.computer.shared") : t("bots:tools.computer.none")}</p>
    </Group>

    <Group id="builtin" title={t("bots:tools.builtin.title")}>
      <Disclosure title={t("bots:tools.builtin.summary")}>
        <p className="bt-line">{t("bots:tools.builtin.items")}</p>
        {p.inspector}
      </Disclosure>
    </Group>

    <Group id="permissions" title={t("bots:tools.permissions.title")} action={<LinkButton onClick={p.onOpenPermissions}>{t("bots:tools.permissions.manage")}</LinkButton>}>
      {p.allowances === null && <p className="field-note" role="status">{t("bots:tools.connections.loading")}</p>}
      {p.permissionsError && <p className="error-text" role="alert">{p.permissionsError}</p>}
      {p.allowances !== null && p.allowances.length === 0 && <p className="bt-line">{t("bots:tools.permissions.empty")}</p>}
      {p.allowances !== null && p.allowances.length > 0 && <ul className="bt-list">
        {p.allowances.map(item => <li key={item.id} className="bt-allow" data-allowance-id={item.id} data-state={item.state} data-leaving={p.leaving.includes(item.id) ? "true" : undefined}
          onTransitionEnd={event => { if (event.target === event.currentTarget && p.leaving.includes(item.id)) p.onGone(item.id); }}>
          <div className="bt-collapse"><div className="bt-row">
            <span className="bt-allow-avatar"><BotAvatar id={item.bot_id} mini /></span>
            <span className="bt-row-copy">
              <strong><code>{item.tool}</code> <span className="bt-server">{p.serverName(item.server)}</span></strong>
              <small>{t("bots:tools.permissions.allowed", { time: when(item.created_at) })}{item.use_count > 0 ? ` · ${t("bots:tools.permissions.used", { count: item.use_count })}` : ""}</small>
              {item.state !== "active" && <small className="bt-warning">{t(`settings:permissionsPage.standing.${item.state}`)}</small>}
            </span>
            <button type="button" className="bs-button" disabled={Boolean(p.revoking) || p.leaving.includes(item.id)} onClick={() => p.onRevoke(item)}>{p.revoking === item.id ? t("settings:permissionsPage.standing.revoking") : t("settings:permissionsPage.standing.revoke")}</button>
          </div></div>
        </li>)}
      </ul>}
      {p.allowances !== null && <p className="bt-line bt-remembered">{p.remembered > 0 ? t("bots:tools.permissions.remembered", { count: p.remembered }) : t("bots:tools.permissions.remembered_none")}</p>}
    </Group>
  </div>;
}

/** Bot settings > Tools, wired: reads the connections, this Bot's access, the web chains and its allowances. */
export function BotTools({ bot, refreshToken = 0, leaveTo, onOpenSettings }: {
  bot: { id: string; name: string }; refreshToken?: number;
  /** Runs `go` once the cover may close (unsaved changes are asked about first). */
  leaveTo: (go: () => void) => void;
  onOpenSettings: (tab: "skills" | "models" | "computer" | "permissions" | "connections") => void;
}) {
  const { t } = useTranslation("bots");
  const hasComputer = useHasComputer();
  const [servers, setServers] = useState<MCP[] | null>(null);
  const [access, setAccess] = useState<Record<string, boolean>>({});
  const [error, setError] = useState("");
  const [saving, setSaving] = useState("");
  const [web, setWeb] = useState<WebToolsView | null>(null);
  const [allowances, setAllowances] = useState<StandingAllowance[] | null>(null);
  const [remembered, setRemembered] = useState(0);
  const [permissionsError, setPermissionsError] = useState("");
  const [revoking, setRevoking] = useState("");
  const [leaving, setLeaving] = useState<string[]>([]);
  const alive = useRef(true);
  const id = encodeURIComponent(bot.id);
  const load = useCallback(async () => {
    const [list, mine, sources, granted] = await Promise.allSettled([
      request<{ servers: MCP[] }>("/api/extensions/mcp"),
      request<{ connections: { server: string; enabled: boolean }[] }>(`/api/bots/${id}/connections`),
      api.webTools(),
      request<{ allowances?: StandingAllowance[]; remembered?: unknown[] }>(`/api/standing-allowances?bot=${id}`),
    ]);
    if (!alive.current) return;
    if (list.status === "fulfilled" && mine.status === "fulfilled") {
      setServers(list.value.servers ?? []);
      setAccess(Object.fromEntries((mine.value.connections ?? []).map(row => [row.server, row.enabled])));
      setError("");
    } else { setServers(current => current ?? []); setError(t("tools.connections.error")); }
    if (sources.status === "fulfilled" && Array.isArray(sources.value.search) && Array.isArray(sources.value.fetch)) setWeb(sources.value);
    if (granted.status === "fulfilled") { setAllowances(granted.value.allowances ?? []); setRemembered(granted.value.remembered?.length ?? 0); setPermissionsError(""); }
    else { setAllowances(current => current ?? []); setPermissionsError(t("tools.permissions.error")); }
  }, [id, t]);
  useEffect(() => {
    alive.current = true; setServers(null); setAllowances(null); void load();
    const focus = () => { if (!document.hidden) void load(); };
    const changed = () => void load();
    window.addEventListener("focus", focus);
    window.addEventListener("tofi:standing-allowances-changed", changed);
    window.addEventListener("tofi:connections-changed", changed);
    return () => { alive.current = false; window.removeEventListener("focus", focus); window.removeEventListener("tofi:standing-allowances-changed", changed); window.removeEventListener("tofi:connections-changed", changed); };
  }, [load, refreshToken]);

  async function toggle(server: string, next: boolean) {
    if (saving) return;
    setSaving(server); setError("");
    setAccess(current => ({ ...current, [server]: next }));
    try { await request(`/api/bots/${id}/connections/${encodeURIComponent(server)}`, { method: "PUT", body: JSON.stringify({ enabled: next }) }); }
    catch (cause) { if (alive.current) { setAccess(current => ({ ...current, [server]: !next })); setError(message(cause) || t("tools.connections.save_failed")); } }
    finally { if (alive.current) setSaving(""); }
  }
  async function revoke(item: StandingAllowance) {
    if (revoking) return;
    setRevoking(item.id); setPermissionsError("");
    try {
      await request(`/api/standing-allowances/${encodeURIComponent(item.id)}`, { method: "DELETE" });
      if (!alive.current) return;
      // The row folds away over --m-panel-out; with reduced motion there is nothing to wait for.
      if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) setAllowances(list => (list ?? []).filter(entry => entry.id !== item.id));
      else setLeaving(list => [...list, item.id]);
    }
    catch (cause) { if (alive.current) setPermissionsError(message(cause)); }
    finally { if (alive.current) setRevoking(""); }
  }

  const gone = (id: string) => { setAllowances(list => (list ?? []).filter(entry => entry.id !== id)); setLeaving(list => list.filter(entry => entry !== id)); };
  const names = new Map((servers ?? []).map(server => [server.name, displayName(server, presetFor(server))]));
  const rows: BotConnectionRow[] | null = servers === null ? null : servers.map(server => {
    const name = displayName(server, presetFor(server));
    return { server: server.name, name, mark: markName(server, name), kind: storedKind(server), tools: server.status?.tool_count, enabled: access[server.name] !== false, allowed: (allowances ?? []).filter(item => item.server === server.name && item.state === "active" && !leaving.includes(item.id)).length };
  });
  return <BotToolsView botName={bot.name} connections={rows} connectionsError={error} saving={saving}
    onToggle={(server, next) => void toggle(server, next)} onOpenConnection={server => leaveTo(() => openConnectionDeepLink({ server }))} onAddConnection={() => leaveTo(() => onOpenSettings("connections"))}
    skills={<BotSkills botId={bot.id} refreshToken={refreshToken} bare />} onOpenSkills={() => leaveTo(() => onOpenSettings("skills"))}
    web={web} onOpenWeb={() => leaveTo(() => onOpenSettings("models"))} computer={hasComputer} onOpenComputer={() => leaveTo(() => onOpenSettings("computer"))}
    inspector={<BotInspector botId={bot.id} tools />}
    allowances={allowances} leaving={leaving} onGone={gone} remembered={remembered} revoking={revoking} permissionsError={permissionsError} serverName={server => names.get(server) ?? server}
    onRevoke={item => void revoke(item)} onOpenPermissions={() => leaveTo(() => onOpenSettings("permissions"))} />;
}
