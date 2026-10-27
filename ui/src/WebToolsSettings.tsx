import {useCallback, useEffect, useId, useRef, useState, type FormEvent, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent} from "react";
import {api, ApiError} from "./api";
import {ConfirmAction} from "./InteractionSystem";
import {ServiceMark} from "./ServiceMark";
import {TofiIcon} from "./icons";
import {GripGlyph} from "./widgetGrip";
import {useTranslation} from "./i18n";
import {Banner, SettingsCard, SettingsSection, StatusBadge, type BadgeState} from "./settings/components";
import type {WebSourceEntry, WebToolsView} from "./types";
import "./extensions-system.css";
import "./settings/automation.css";
import "./web-tools-settings.css";

type Chain = "search" | "fetch";
type Outcome = {ok: boolean; code: string};

const badge: Record<WebSourceEntry["status"], BadgeState> = {ready: "ok", always: "ok", needs_key: "need", needs_address: "need", error: "bad", off: "asleep", limit: "need"};
const placeholders: Record<string, string> = {tavily: "tvly-…", brave: "BSA…", firecrawl: "fc-…", brightdata: ""};

/** Settings section: where Bots search and read the web. Two ordered lists; a source that fails hands over to the next by error code. */
export function WebToolsSettings({initial}: {initial?: WebToolsView}) {
 const {t} = useTranslation("settings");
 const [view, setView] = useState<WebToolsView | null>(initial ?? null);
 const [error, setError] = useState("");
 const [outcomes, setOutcomes] = useState<Record<string, Outcome>>({});
 const [testing, setTesting] = useState<string | null>(null);
 const [announce, setAnnounce] = useState("");
 useEffect(() => {
  if (initial) return;
  const controller = new AbortController();
  api.webTools(controller.signal).then(setView).catch(() => { if (!controller.signal.aborted) setError(t("webtools.load_failed")); });
  return () => controller.abort();
 }, [initial, t]);
 const adopt = useCallback((next: WebToolsView) => setView({...next, test: undefined}), []);
 async function run(action: () => Promise<WebToolsView>, failure = t("action.save_failed")) {
  setError("");
  try { adopt(await action()); return true; }
  catch (cause) { setError(cause instanceof ApiError && cause.message ? cause.message : failure); return false; }
 }
 async function test(id: string) {
  setTesting(id); setError("");
  try {
   const next = await api.testWebProvider(id);
   if (next.test) setOutcomes(current => ({...current, [id]: {ok: next.test!.ok, code: next.test!.code}}));
   adopt(next);
  } catch { setOutcomes(current => ({...current, [id]: {ok: false, code: "network"}})); }
  finally { setTesting(null); }
 }
 async function move(chain: Chain, from: number, to: number) {
  if (!view || from === to) return;
  const list = view[chain];
  const fixed = list.filter(item => item.fixed), movable = list.filter(item => !item.fixed);
  if (to < 0 || to >= movable.length) return;
  const next = [...movable];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  setView({...view, [chain]: [...fixed, ...next]});
  setAnnounce(t("webtools.moved", {name: item.label, position: to + 1, total: next.length}));
  if (!await run(() => api.setWebToolOrder(chain, next.map(entry => entry.id)))) void api.webTools().then(setView).catch(() => undefined);
 }
 return <SettingsSection className="web-tools" title={t("webtools.title")} description={t("webtools.description")}>
  {view && !view.enabled && <Banner tone="warn" title={t("webtools.master_off_title")}>{t("webtools.master_off_body")}</Banner>}
  {view && view.enabled && !view.search_offered && <Banner tone="warn" title={t("webtools.no_search_title")}>{t("webtools.no_search_body")}</Banner>}
  {error && <p className="error-text" role="alert">{error}</p>}
  {!view && !error && <p className="field-note" role="status">{t("providers.status_loading")}</p>}
  {view && (["search", "fetch"] as const).map(chain => <ChainCard key={chain} chain={chain} entries={view[chain]} outcomes={outcomes} testing={testing} onMove={(from, to) => void move(chain, from, to)}
   onEnabled={(id, enabled) => void run(() => api.setWebToolEnabled(chain, id, enabled))}
   onSave={(id, body) => run(() => api.saveWebProvider(id, body), t("webtools.save_failed"))}
   onRemove={id => run(() => api.removeWebProvider(id), t("webtools.remove_failed"))}
   onTest={id => void test(id)} />)}
  {view?.limits && <SettingsCard className="web-limits">
   <div className="web-chain-head"><h5>{t("webtools.limits_title")}</h5><p>{t("webtools.limits_note")}</p></div>
   {(["search", "fetch"] as const).map(chain => <div className="web-limit-row" key={chain}>
    <label htmlFor={`web-limit-${chain}`}>{t(`webtools.limit_${chain}`)}</label>
    <select id={`web-limit-${chain}`} value={String(view.limits![chain])} onChange={event => void run(() => api.setWebToolLimits({[chain]: Number(event.target.value)}))}>
     {[...new Set([50, 100, 300, 1000, view.limits![chain]].filter(value => value !== -1))].sort((a, b) => a - b).map(value => <option key={value} value={value}>{t("webtools.limit_option", {count: value})}</option>)}
     <option value="-1">{t("webtools.limit_unlimited")}</option>
    </select>
   </div>)}
  </SettingsCard>}
  <p className="sr-only" role="status" aria-live="polite">{announce}</p>
 </SettingsSection>;
}

function ChainCard({chain, entries, outcomes, testing, onMove, onEnabled, onSave, onRemove, onTest}: {chain: Chain; entries: WebSourceEntry[]; outcomes: Record<string, Outcome>; testing: string | null; onMove: (from: number, to: number) => void; onEnabled: (id: string, enabled: boolean) => void; onSave: (id: string, body: {key?: string; zone?: string; base_url?: string}) => Promise<boolean>; onRemove: (id: string) => Promise<boolean>; onTest: (id: string) => void}) {
 const {t} = useTranslation("settings");
 const titleId = useId();
 const list = useRef<HTMLOListElement>(null);
 const [dragging, setDragging] = useState<{from: number; over: number} | null>(null);
 const movable = entries.filter(item => !item.fixed);
 const fixedCount = entries.length - movable.length;
 const overIndex = (clientY: number) => {
  const rows = list.current ? Array.from(list.current.querySelectorAll<HTMLElement>("[data-movable='true']")) : [];
  let best = rows.length - 1;
  for (const [index, row] of rows.entries()) { const box = row.getBoundingClientRect(); if (clientY < box.top + box.height / 2) { best = index; break; } }
  return Math.max(0, best);
 };
 function press(event: ReactPointerEvent<HTMLButtonElement>, from: number) {
  if (event.button !== 0 && event.pointerType === "mouse") return;
  event.preventDefault();
  const target = event.currentTarget;
  target.setPointerCapture(event.pointerId);
  setDragging({from, over: from});
 }
 function dragMove(event: ReactPointerEvent<HTMLButtonElement>) {
  if (dragging) setDragging({from: dragging.from, over: overIndex(event.clientY)});
 }
 function release(event: ReactPointerEvent<HTMLButtonElement>) {
  if (!dragging) return;
  const {from, over} = dragging;
  if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  setDragging(null);
  if (over !== from) onMove(from, over);
 }
 function key(event: ReactKeyboardEvent<HTMLButtonElement>, index: number) {
  const step = event.key === "ArrowUp" ? -1 : event.key === "ArrowDown" ? 1 : 0;
  if (!step) return;
  event.preventDefault();
  onMove(index, index + step);
  const id = movable[index].id;
  requestAnimationFrame(() => list.current?.querySelector<HTMLElement>(`[data-id='${id}'] .web-grip`)?.focus());
 }
 return <SettingsCard className="web-chain">
  <div className="web-chain-head"><h5 id={titleId}>{t(`webtools.${chain}_title`)}</h5><p>{t(`webtools.${chain}_note`)}</p></div>
  <ol ref={list} className="web-chain-list" aria-labelledby={titleId}>
   {entries.map(entry => {
    const index = movable.indexOf(entry);
    return <SourceRow key={entry.id} entry={entry} chain={chain} index={index} count={movable.length} first={fixedCount} outcome={outcomes[entry.id]} testing={testing === entry.id} busy={testing !== null}
     dragging={dragging?.from === index && !entry.fixed} over={dragging !== null && dragging.over === index && dragging.from !== index && !entry.fixed}
     grip={{onPointerDown: event => press(event, index), onPointerMove: dragMove, onPointerUp: release, onPointerCancel: () => setDragging(null), onKeyDown: event => key(event, index)}}
     onMove={step => onMove(index, index + step)} onEnabled={enabled => onEnabled(entry.id, enabled)} onSave={body => onSave(entry.id, body)} onRemove={() => onRemove(entry.id)} onTest={() => onTest(entry.id)} />;
   })}
  </ol>
 </SettingsCard>;
}

type GripHandlers = {onPointerDown: (event: ReactPointerEvent<HTMLButtonElement>) => void; onPointerMove: (event: ReactPointerEvent<HTMLButtonElement>) => void; onPointerUp: (event: ReactPointerEvent<HTMLButtonElement>) => void; onPointerCancel: () => void; onKeyDown: (event: ReactKeyboardEvent<HTMLButtonElement>) => void};

function SourceRow({entry, chain, index, count, outcome, testing, busy, dragging, over, grip, onMove, onEnabled, onSave, onRemove, onTest}: {entry: WebSourceEntry; chain: Chain; index: number; count: number; first: number; outcome?: Outcome; testing: boolean; busy: boolean; dragging: boolean; over: boolean; grip: GripHandlers; onMove: (step: -1 | 1) => void; onEnabled: (enabled: boolean) => void; onSave: (body: {key?: string; zone?: string; base_url?: string}) => Promise<boolean>; onRemove: () => Promise<boolean>; onTest: () => void}) {
 const {t} = useTranslation("settings");
 const [editing, setEditing] = useState(false);
 const switchId = useId();
 const state: BadgeState = testing ? "testing" : badge[entry.status];
 const code = entry.status === "error" ? entry.error_code : outcome && !outcome.ok ? outcome.code : "";
 const needsSetup = entry.status === "needs_key" || entry.status === "needs_address";
 const configurable = Boolean(entry.fields?.length);
 const statusText = testing ? t("webtools.testing") : entry.status === "error" ? t("webtools.status.error") : outcome?.ok && entry.status === "ready" ? t("webtools.test_ok") : t(`webtools.status.${entry.status}` as "webtools.status.ready");
 const detail = entry.kind === "self_hosted" ? entry.base_url : [entry.key_hint ? t("providers.configured_hint", {hint: entry.key_hint}) : "", entry.used_today ? t("webtools.used_today", {count: entry.used_today}) : ""].filter(Boolean).join(" · ");
 return <li className="web-row" data-id={entry.id} data-chain={chain} data-movable={!entry.fixed} data-status={entry.status} data-dragging={dragging || undefined} data-over={over || undefined}>
  <div className="web-row-main">
   {entry.fixed ? <span className="web-grip-spacer" aria-hidden="true" /> : <button type="button" className="web-grip" aria-label={t("webtools.move_label", {name: entry.label})} aria-keyshortcuts="ArrowUp ArrowDown" title={t("webtools.move_hint")} {...grip}><GripGlyph size={16} /></button>}
   <span className="web-row-mark">{entry.kind === "native" ? <span className="service-mark service-mark-generic" aria-hidden="true"><TofiIcon name="sparkles" size={22} /></span> : entry.kind === "builtin" ? <span className="service-mark service-mark-generic" aria-hidden="true"><TofiIcon name="globe" size={22} /></span> : entry.kind === "computer" ? <span className="service-mark service-mark-generic" aria-hidden="true"><TofiIcon name="monitor" size={22} /></span> : <ServiceMark name={entry.label} id={entry.id} />}</span>
   <div className="web-row-copy">
    <div className="web-row-title"><strong>{entry.kind === "native" ? t("webtools.native_label") : entry.kind === "builtin" ? t("webtools.builtin_label") : entry.kind === "computer" ? t("webtools.computer_label") : entry.label}</strong>{entry.paid && <span className="settings-tag" data-tag="paid">{t("webtools.paid")}</span>}</div>
    <p>{t(`webtools.about.${entry.id}` as "webtools.about.tavily")}</p>
    <div className="web-row-status" role="status"><StatusBadge state={state}>{statusText}</StatusBadge>{code && <span className="web-row-code" data-code={code}>{t(`webtools.code.${code}` as "webtools.code.network", {defaultValue: code})}</span>}{detail && <span className="web-row-detail">{detail}</span>}</div>
   </div>
   <div className="web-row-actions">
    {!entry.fixed && <span className="web-row-move"><button type="button" className="icon-button" aria-label={t("webtools.move_up", {name: entry.label})} disabled={index <= 0} onClick={() => onMove(-1)}><TofiIcon name="arrow-up" size={16} /></button><button type="button" className="icon-button" aria-label={t("webtools.move_down", {name: entry.label})} disabled={index >= count - 1} onClick={() => onMove(1)}><TofiIcon name="arrow-down" size={16} /></button></span>}
    {entry.testable && <button type="button" className="secondary-button" disabled={busy || needsSetup} onClick={onTest}>{testing ? t("webtools.testing") : t("webtools.test")}</button>}
    {configurable && <button type="button" className="secondary-button" aria-expanded={editing} onClick={() => setEditing(value => !value)}>{needsSetup ? t("webtools.set_up") : t("webtools.edit")}</button>}
    {!entry.fixed && <label className="debug-toggle auto-switch" htmlFor={switchId}><input id={switchId} type="checkbox" role="switch" aria-label={t("webtools.enable_label", {name: entry.label})} checked={entry.enabled} onChange={event => onEnabled(event.target.checked)} /></label>}
   </div>
  </div>
  {editing && configurable && <ProviderForm entry={entry} onSave={async body => { if (await onSave(body)) { setEditing(false); return true; } return false; }} onRemove={async () => { if (await onRemove()) { setEditing(false); return true; } return false; }} />}
 </li>;
}

function ProviderForm({entry, onSave, onRemove}: {entry: WebSourceEntry; onSave: (body: {key?: string; zone?: string; base_url?: string}) => Promise<boolean>; onRemove: () => Promise<boolean>}) {
 const {t} = useTranslation("settings");
 const id = useId();
 const address = entry.kind === "self_hosted";
 const [value, setValue] = useState(address ? entry.base_url ?? "" : "");
 const [zone, setZone] = useState(entry.zone ?? "");
 const [saving, setSaving] = useState(false);
 const wantsZone = entry.fields?.includes("zone") ?? false;
 const configured = entry.status !== "needs_key" && entry.status !== "needs_address";
 async function submit(event: FormEvent) {
  event.preventDefault();
  if (!value.trim() || saving || (wantsZone && !zone.trim())) return;
  setSaving(true);
  const ok = await onSave(address ? {base_url: value.trim()} : {key: value.trim(), ...(wantsZone ? {zone: zone.trim()} : {})});
  setSaving(false);
  if (ok) setValue(address ? value.trim() : "");
 }
 return <form className="web-row-form" onSubmit={event => void submit(event)}>
  <label htmlFor={id}>{address ? t("webtools.address_label") : configured ? t("webtools.replace_key_label") : t("webtools.key_label")}</label>
  <div className="provider-key-row">
   <input id={id} name={`${entry.id}-${address ? "address" : "key"}`} type={address ? "url" : "password"} autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder={address ? "http://searxng.local:8080" : placeholders[entry.id]} value={value} disabled={saving} onChange={event => setValue(event.target.value)} />
   {!wantsZone && <button type="submit" className="primary-button" disabled={saving || !value.trim()}>{t("providers.save")}</button>}
  </div>
  {wantsZone && <><label htmlFor={`${id}-zone`}>{t("webtools.zone_label")}</label><input id={`${id}-zone`} name={`${entry.id}-zone`} type="text" autoComplete="off" spellCheck={false} placeholder="web_unlocker1" value={zone} disabled={saving} onChange={event => setZone(event.target.value)} /><button type="submit" className="primary-button" disabled={saving || !value.trim() || !zone.trim()}>{t("providers.save")}</button></>}
  <div className="web-row-form-foot"><p className="field-note">{address ? t("webtools.address_note") : t("webtools.key_note")}</p>{configured && <ConfirmAction label={t("providers.remove")} question={t("providers.remove_question", {provider: entry.label})} onConfirm={async () => { if (!await onRemove()) throw new Error(t("webtools.remove_failed")); }} />}</div>
 </form>;
}
