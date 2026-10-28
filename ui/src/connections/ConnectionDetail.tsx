import { useEffect, useRef, useState, type ReactNode } from "react";
import { request } from "../api";
import { BotAvatar } from "../BotAvatar";
import type { StandingAllowance } from "../StandingAllowances";
import { Disclosure } from "../InteractionSystem";
import { TofiIcon } from "../icons";
import { ServiceMark } from "../ServiceMark";
import { useTranslation } from "../i18n";
import { arrowOpens, OverlayMenu } from "../overlays/OverlayHost";
import { Segmented } from "../settings/components";
import type { ConnectionBots } from "./bots";
import { MoreMenu, PrimaryButton, type RowMenuItem, type RowPrimary } from "./RowView";
import { RepoLink } from "./repoStars";
import { message, normalizeTiers, TIERS, transportLabels, toolsEndpoint, type MCPTransport, type ToolTier, type ToolTierList, type ToolTierView } from "./shared";
import type { ConnectionKind } from "./status";

/** Facts the Advanced section lists. Each is optional: a section row appears only for what this connection has. */
export type AdvancedFacts = {
  source?: string; repo?: { name: string; stars?: number }; address?: string; transport?: MCPTransport; headers?: string[];
  allow?: string[]; deny?: string[]; scopes?: string[]; redirect?: "loopback"; pkg?: string; project?: string; previewNote?: boolean; communityNote?: boolean;
};
export type DetailConfirm = { text: string; submit: string; onCancel: () => void; onConfirm: () => void };
export type DetailDanger = { key: string; label: string; onSelect: () => void; remove?: boolean }[];

export type ConnectionDetailViewProps = {
  name: string; mark: string; source: string; serverName: string;
  kind: ConnectionKind; statusText: string; detail?: string; checkedText?: string; error?: string; note?: string;
  primary: RowPrimary | null; menu: RowMenuItem[]; busy: boolean;
  /** Account, services and project lines of a Google connection. */
  facts: string[];
  tiers: ToolTierList | null; onTiers: (value: ToolTierList) => void; community: boolean; toolsError?: string;
  advanced: AdvancedFacts; help?: ReactNode;
  /** A tool to scroll to and flash once the list is in (the approval card's "Manage" link). */
  focusTool?: string;
  /** The "Always allowed" rows for this connection and how to revoke one; `onDropAllowance` removes a chip after its exit. */
  /** Which Bots may use this connection (step 4). */
  bots: ConnectionBots; onBotsAll: () => void; onBotsChosen: (ids: string[]) => void;
  allowances: StandingAllowance[]; onRevoke: (item: StandingAllowance) => Promise<void>; onDropAllowance: (id: string) => void;
  danger: DetailDanger; confirm: DetailConfirm | null;
  onBack: () => void; onRefresh: () => void; refreshing: boolean;
};

/** The permission of one tool: Radix menu through the overlay host, not a native select. */
function TierMenu({ tool, busy, saving, onPick }: { tool: ToolTierView; busy: boolean; saving: boolean; onPick: (value: ToolTier | "auto") => void }) {
  const { t } = useTranslation("extensions");
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const current = tool.override ?? "auto";
  const label = t("tools.select_label", { name: tool.name });
  const pick = (value: ToolTier | "auto") => { setOpen(false); if (value !== current) onPick(value); };
  const options: (ToolTier | "auto")[] = ["auto", ...TIERS];
  return <div className="mcp-tier-anchor">
    <button ref={trigger} type="button" className="mcp-tier-trigger" aria-label={label} aria-haspopup="menu" aria-expanded={open} disabled={busy || saving} data-tier={tool.tier}
      onClick={() => setOpen(value => !value)} onKeyDown={arrowOpens(setOpen)}>
      <span>{t(`tools.tier.${tool.tier}`)}</span><TofiIcon name="chevron-down" size={14} aria-hidden="true" />
    </button>
    {open && <OverlayMenu open onOpenChange={setOpen} className="mcp-menu mcp-tier-menu" label={label} trigger={trigger} anchor={trigger} align="end" sideOffset={6}>
      {options.map(value => <button key={value} type="button" data-value={value} aria-current={current === value ? "true" : undefined} onClick={() => pick(value)}>
        <span>{t(`tools.tier.${value}`)}</span>{current === value && <TofiIcon name="check" size={14} aria-hidden="true" />}
      </button>)}
      <p className="mcp-menu-note">{t("tools.raise_note")}</p>
    </OverlayMenu>}
  </div>;
}

/** Tools grouped by permission, a menu per tool, community rule stated. */
function ToolsSection({ serverName, tiers, onTiers, busy, community, refreshing, loadError, focusTool, allowances, onRevoke, onDrop }: { serverName: string; tiers: ToolTierList | null; onTiers: (value: ToolTierList) => void; busy: boolean; community: boolean; refreshing: boolean; loadError?: string; focusTool?: string; allowances: StandingAllowance[]; onRevoke: (item: StandingAllowance) => Promise<void>; onDrop: (id: string) => void }) {
  const { t } = useTranslation("extensions");
  const [saving, setSaving] = useState("");
  const [error, setError] = useState("");
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  async function setTier(tool: ToolTierView, value: ToolTier | "auto") {
    setSaving(tool.name); setError("");
    try {
      const result = await request<{ tool: ToolTierView; summary: ToolTierList }>(`${toolsEndpoint(serverName)}/${encodeURIComponent(tool.name)}`, { method: "PUT", body: JSON.stringify({ tier: value === "auto" ? "" : value }) });
      if (alive.current) onTiers(normalizeTiers(result.summary));
    } catch (e) { if (alive.current) setError(message(e)); }
    finally { if (alive.current) setSaving(""); }
  }
  // "Always allowed" chips: the same rows as Settings > Permissions, filtered to this connection (MCPRow reads them).
  const [leaving, setLeaving] = useState<string[]>([]);
  async function revoke(item: StandingAllowance) {
    setError("");
    try {
      await onRevoke(item);
      if (!alive.current) return;
      // The chip leaves with a short fade; reduced motion has no animation to wait for.
      if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) onDrop(item.id);
      else setLeaving(list => [...list, item.id]);
    } catch (e) { if (alive.current) setError(message(e)); }
  }
  // Arriving from an approval card: bring the tool into view and flash it once.
  const list = useRef<HTMLDivElement>(null);
  const [flash, setFlash] = useState("");
  useEffect(() => {
    if (!focusTool || !tiers) return;
    const row = [...(list.current?.querySelectorAll<HTMLElement>("[data-tool]") ?? [])].find(node => node.dataset.tool === focusTool);
    if (!row) return;
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    row.scrollIntoView({ block: "center", behavior: reduced ? "auto" : "smooth" });
    setFlash(focusTool);
    const timer = window.setTimeout(() => setFlash(""), 1600);
    return () => window.clearTimeout(timer);
  }, [focusTool, tiers]);
  const groups = tiers ? TIERS.map(tier => ({ tier, tools: tiers.tools.filter(tool => tool.tier === tier) })) : [];
  return <section className="conn-section" data-section="tools" aria-labelledby="conn-tools-title" aria-busy={refreshing} ref={list}>
    <h4 id="conn-tools-title">{t("tools.title", { count: tiers?.tools.length ?? 0 })}</h4>
    <p className="conn-lead">{t("tools.explain")}</p>
    {community && <p className="conn-lead" data-capability="community">{t("capabilities.community_note")}</p>}
    {(error || loadError) && <p className="mcp-service-error" role="alert">{error || loadError}</p>}
    {tiers && tiers.tools.length === 0 && <p className="conn-lead">{t("tools.empty")}</p>}
    {groups.map(group => group.tools.length > 0 && <div key={group.tier} className="mcp-tool-group" data-tier={group.tier} role="group" aria-label={t(`tools.group.${group.tier}`)}>
      <h5>{t(`tools.group.${group.tier}`)}<span className="mcp-tool-count">{group.tools.length}</span></h5>
      <ul>{group.tools.map(tool => {
        const marker = tool.source === "owner" ? t("tools.source.owner") : tool.source === "provider" ? t("tools.source.provider") : "";
        const granted = allowances.filter(item => item.tool === tool.name);
        return <li key={tool.name} className="mcp-tool" data-tool={tool.name} data-source={tool.source} data-changed={tool.changed ? "true" : undefined} data-flash={flash === tool.name ? "true" : undefined}>
          <div className="mcp-tool-copy"><code>{tool.name}</code>{tool.description && <p>{tool.description}</p>}<span className="mcp-tool-marks">{marker && <small className="mcp-tier-mark">{marker}</small>}{tool.duplicated && <small className="mcp-tier-mark">{t("tools.duplicated")}</small>}{tool.changed && <small className="mcp-tier-mark is-changed">{t("tools.changed")}</small>}</span>
            {granted.length > 0 && <ul className="mcp-allowances" aria-label={t("tools.allowance.always")}>{granted.map(item => <li key={item.id} className="mcp-allowance" data-state={item.state} data-leaving={leaving.includes(item.id) ? "true" : undefined}
              onAnimationEnd={() => { if (leaving.includes(item.id)) { onDrop(item.id); setLeaving(list => list.filter(id => id !== item.id)); } }}>
              <BotAvatar id={item.bot_id} mini />
              <span>{item.state === "active" ? t("tools.allowance.always") : t("tools.allowance.stale")} · {item.bot_name}</span>
              <button type="button" aria-label={t("tools.allowance.revoke", { bot: item.bot_name, tool: tool.name })} disabled={leaving.includes(item.id)} onClick={() => void revoke(item)}><TofiIcon name="close" size={12} aria-hidden="true" /></button>
            </li>)}</ul>}</div>
          <TierMenu tool={tool} busy={busy || refreshing} saving={saving === tool.name} onPick={value => void setTier(tool, value)} />
        </li>;
      })}</ul>
    </div>)}
  </section>;
}

/** "Which Bots can use this connection": All Bots (the default) or a checked list; the list folds open and closed. */
function BotsSection({ name, bots, onAll, onChosen }: { name: string; bots: ConnectionBots; onAll: () => void; onChosen: (ids: string[]) => void }) {
  const { t } = useTranslation("extensions");
  const [chosenView, setChosenView] = useState(false);
  const mode = bots.all && !chosenView ? "all" : "chosen";
  const allowed = bots.bots.filter(bot => bot.enabled).map(bot => bot.bot_id);
  const toggle = (id: string, next: boolean) => onChosen(next ? [...allowed, id] : allowed.filter(entry => entry !== id));
  return <section className="conn-section" data-section="bots" aria-labelledby="conn-bots-title" aria-busy={bots.saving}>
    <h4 id="conn-bots-title">{t("detail.bots.title")}</h4>
    <p className="conn-lead">{t("detail.bots.lead", { name })}</p>
    {bots.state === "loading" && <p className="conn-lead" role="status">{t("detail.bots.loading")}</p>}
    {bots.error && <p className="mcp-service-error" role="alert">{bots.error}</p>}
    {bots.state !== "loading" && bots.bots.length > 0 && <>
      <Segmented label={t("detail.bots.choose")} value={mode} disabled={bots.saving}
        options={[{ value: "all", label: t("detail.bots.all") }, { value: "chosen", label: t("detail.bots.chosen") }]}
        onChange={value => { if (value === "all") { setChosenView(false); onAll(); } else setChosenView(true); }} />
      <div className="conn-bots-fold" data-open={mode === "chosen" ? "true" : undefined} inert={mode !== "chosen" || undefined}>
        <div>
          <ul className="conn-bots" aria-label={t("detail.bots.choose")}>{bots.bots.map(bot => <li key={bot.bot_id} data-bot-id={bot.bot_id}>
            <label><input type="checkbox" aria-label={t("detail.bots.check", { bot: bot.bot_name, name })} checked={bot.enabled} disabled={bots.saving} onChange={event => toggle(bot.bot_id, event.target.checked)} /><BotAvatar id={bot.bot_id} mini /><span>{bot.bot_name}</span></label>
          </li>)}</ul>
          {allowed.length === 0 && <p className="conn-lead">{t("detail.bots.none")}</p>}
          <p className="conn-lead">{t("detail.bots.new_note")}</p>
        </div>
      </div>
    </>}
  </section>;
}

function AdvancedSection({ facts }: { facts: AdvancedFacts }) {
  const { t } = useTranslation("extensions");
  const rows: [string, ReactNode][] = [];
  if (facts.source) rows.push([t("detail.advanced.source"), facts.source]);
  if (facts.address) rows.push([t("detail.advanced.address"), <code key="a">{facts.address}</code>]);
  if (facts.transport) rows.push([t("form.transport"), transportLabels[facts.transport]]);
  if (facts.headers?.length) rows.push([t("form.headers"), facts.headers.join(", ")]);
  if (facts.allow?.length) rows.push([t("form.allowlist"), facts.allow.join(", ")]);
  if (facts.deny?.length) rows.push([t("form.denylist"), facts.deny.join(", ")]);
  if (facts.scopes?.length) rows.push([t("form.scopes"), facts.scopes.join(", ")]);
  if (facts.redirect) rows.push([t("detail.advanced.signin"), t("detail.advanced.loopback")]);
  if (facts.pkg) rows.push([t("detail.advanced.package"), facts.pkg]);
  if (facts.project) rows.push([t("detail.advanced.project"), facts.project]);
  if (facts.repo) rows.push([t("detail.advanced.repository"), <RepoLink key="r" repo={facts.repo.name} stars={facts.repo.stars} />]);
  return <section className="conn-section" data-section="advanced"><div className="conn-card">
    <Disclosure title={t("detail.advanced.title")}>
      <dl className="conn-facts">{rows.map(([term, value]) => <div key={term}><dt>{term}</dt><dd>{value}</dd></div>)}</dl>
      {facts.communityNote && <p className="conn-lead">{t("connections.details.community_review")}</p>}
      {facts.previewNote && <p className="conn-lead">{t("connections.preview_note")}</p>}
    </Disclosure>
  </div></section>;
}

/**
 * One connection's page (docs/design/tools-and-connections.md section 3.3): Status, Tools, Advanced, Danger. Pure presentation
 * over what MCPRow already knows; MCPRow keeps the state machine so a connection is probed once, however it is shown.
 */
export function ConnectionDetailView(props: ConnectionDetailViewProps) {
  const { t } = useTranslation("extensions");
  const back = useRef<HTMLButtonElement>(null);
  useEffect(() => { back.current?.focus({ preventScroll: true }); }, []);
  const { kind } = props;
  return <div className="mcp-detail" data-kind={kind} aria-label={props.name}>
    <header className="mcp-detail-head">
      <button ref={back} type="button" className="mcp-detail-back" aria-label={t("detail.back")} onClick={props.onBack}><TofiIcon name="chevron-left" size={20} aria-hidden="true" /></button>
      <ServiceMark name={props.mark} />
      <div className="mcp-detail-title"><h3>{props.name}</h3><p>{props.source}</p></div>
      <div className="mcp-detail-actions">
        <button type="button" className="mcp-button" disabled={props.busy || props.refreshing} onClick={props.onRefresh}>{props.refreshing ? t("tools.refreshing") : t("tools.refresh")}</button>
        {props.menu.length > 0 && <MoreMenu name={props.name} menu={props.menu} busy={props.busy} />}
      </div>
    </header>
    <section className="conn-section" data-section="status" aria-labelledby="conn-status-title">
      <h4 id="conn-status-title">{t("detail.status")}</h4>
      <div className="conn-card conn-status" data-kind={kind}>
        <div className="conn-status-line">
          <span className="mcp-service-status" role="status"><i aria-hidden="true" />{props.statusText}{props.detail && <small className="mcp-service-detail">{props.detail}</small>}{props.checkedText && <small className="mcp-service-checked">{props.checkedText}</small>}</span>
          {props.primary && <PrimaryButton primary={props.primary} />}
        </div>
        {props.error && <p className="mcp-service-error" role="alert">{props.error}</p>}
        {props.note && <p className="conn-lead" role="status">{props.note}</p>}
        {props.facts.map(fact => <p key={fact} className="conn-lead">{fact}</p>)}
        {props.help}
      </div>
    </section>
    <ToolsSection serverName={props.serverName} tiers={props.tiers} onTiers={props.onTiers} busy={props.busy} community={props.community} refreshing={props.refreshing} loadError={props.toolsError} focusTool={props.focusTool} allowances={props.allowances} onRevoke={props.onRevoke} onDrop={props.onDropAllowance} />
    <BotsSection name={props.name} bots={props.bots} onAll={props.onBotsAll} onChosen={props.onBotsChosen} />
    <AdvancedSection facts={props.advanced} />
    <section className="conn-section conn-danger" data-section="danger" aria-labelledby="conn-danger-title">
      <h4 id="conn-danger-title">{t("detail.danger")}</h4>
      {props.confirm
        ? <div className="mcp-confirm" role="alertdialog" aria-label={props.confirm.text}><span>{props.confirm.text}</span><button type="button" className="mcp-button" disabled={props.busy} onClick={props.confirm.onCancel}>{t("action.cancel")}</button><button type="button" className="mcp-button mcp-danger" disabled={props.busy} onClick={props.confirm.onConfirm}>{props.confirm.submit}</button></div>
        : <div className="conn-danger-actions">{props.danger.map(item => <button key={item.key} type="button" className={`mcp-button${item.remove ? " mcp-danger" : ""}`} disabled={props.busy} onClick={item.onSelect}>{item.label}</button>)}</div>}
    </section>
  </div>;
}
