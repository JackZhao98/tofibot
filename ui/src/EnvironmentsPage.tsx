import {useEffect, useRef, useState, type ReactNode, type RefObject} from "react";
import {ApiError, request} from "./api";
import {ComputerAttach} from "./ComputerAttach";
import {ComputerOptions} from "./ComputerOptions";
import {useHasComputer, useHostHasComputer} from "./computerCapability";
import {ActionConfirm, ConfigureDialog, DeniedBotsDialog, E2BDialog, reasonKey, VmCommandDialog, type ConfirmSpec} from "./EnvironmentDialogs";
import {RemoteControlBlock} from "./RemoteControl";
import {useRemoteControl} from "./useRemoteControl";
import {useEnvironments, type Environment, type EnvironmentAction, type EnvironmentState, type EnvironmentsView} from "./environments";
import {useTranslation} from "./i18n";
import {formatRelativeTime} from "./i18n/format";
import {TofiIcon, type TofiIconName} from "./icons";
import {useOwnerSession} from "./OwnerSession";
import {usePolicy} from "./capabilityPolicy";
import {arrowOpens, OverlayMenu} from "./overlays/OverlayHost";
import {useSettingsActive} from "./settingsActivity";
import {Banner, SectionAnchor, SettingsCard, SettingsRow, SettingsSection, StatusBadge, type BadgeState} from "./settings/components";
import type {SettingsTab} from "./SettingsShell";
import "./extensions-system.css";
import "./environments.css";
import "./remote-control.css";
import "./policy-member.css";

const kindIcon: Record<Environment["kind"], TofiIconName> = {vm: "server", e2b: "cloud", mac: "laptop"};
const badgeFor: Record<EnvironmentState, BadgeState> = {ready: "ok", online: "ok", starting: "testing", resuming: "testing", preparing: "testing", asleep: "asleep", offline: "asleep", stopped: "asleep", standby: "asleep", unknown: "asleep", failed: "bad"};
/** Actions that change something for good go in the row's menu, behind a confirmation; the rest are buttons. */
const menuActions: EnvironmentAction[] = ["remove", "disconnect"];

/** The organization provides this account's Bot computer (user groups): the row says so and carries no key or switch controls. */
export type OrganizationComputer = {via: string};

export type EnvironmentRowHandlers = {
  busy: string;
  onAction: (action: EnvironmentAction, item: Environment) => void;
};

/** The state word is a short badge; what went wrong is a named reason under the row, never the upstream text. */
function useEnvironmentWords() {
  const {t} = useTranslation("computer");
  return {
    name: (item: Environment) => item.name || t(`environments.name.${item.kind}`),
    state: (state: EnvironmentState) => t(`environments.state.${state}`),
    access: (item: Environment) => t(`environments.access.${item.access}`),
  };
}

function RowMenu({item, name, items, onAction, disabled}: {item: Environment; name: string; items: EnvironmentAction[]; onAction: EnvironmentRowHandlers["onAction"]; disabled: boolean}) {
  const {t} = useTranslation("computer");
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  return <>
    <button ref={trigger} type="button" className="secondary-button env-more" disabled={disabled} aria-haspopup="menu" aria-expanded={open} aria-label={t("environments.more", {name})} data-hint={t("environments.more", {name})}
      onClick={() => setOpen(value => !value)} onKeyDown={arrowOpens(setOpen)}><TofiIcon name="more" size={16} aria-hidden="true"/></button>
    {open && <OverlayMenu open onOpenChange={setOpen} className="mcp-menu env-menu" label={t("environments.more", {name})} trigger={trigger as RefObject<HTMLElement | null>} anchor={trigger as RefObject<HTMLElement | null>} align="end" sideOffset={6}>
      {items.map(action => <button key={action} type="button" className="mcp-menu-danger" data-action={action} onClick={() => { setOpen(false); onAction(action, item); }}>{t(`environments.action.${action}`)}</button>)}
    </OverlayMenu>}
  </>;
}

/** One environment: kind icon, name, a "Bot computer" tag on the one Bots use, who may use it, a named reason when it failed. Controls come only from `actions`. */
export function EnvironmentRow({item, e2bError, handlers, provided = false, templatePhase = ""}: {item: Environment; e2bError?: string; handlers: EnvironmentRowHandlers; provided?: boolean; templatePhase?: string}) {
  const {t} = useTranslation("computer");
  const words = useEnvironmentWords();
  const name = words.name(item);
  const buttons = item.actions.filter(action => !menuActions.includes(action));
  const menu = item.actions.filter(action => menuActions.includes(action));
  const working = handlers.busy === item.id;
  const lastSeen = item.kind === "mac" && item.state === "offline" && item.last_seen ? formatRelativeTime(item.last_seen) : "";
  const label = <span className="env-name"><TofiIcon name={kindIcon[item.kind]} size={18} aria-hidden="true"/><span className="env-name-text">{name}</span>{(item.bot_computer || provided) && <span className="env-tag">{t("environments.bot_computer")}</span>}{provided && <span className="env-tag env-tag-org" data-tag="organization"><TofiIcon name="lock" size={11} aria-hidden="true"/>{t("environments.provided")}</span>}</span>;
  const required = item.error_code === "guest_outdated_required";
  const imageBuilding = item.kind === "e2b" && templatePhase === "preparing" && !required;
  const description = <>
    {!(provided && item.access === "none") && <span className="env-line">{words.access(item)}{lastSeen && ` · ${t("environments.last_seen", {time: lastSeen})}`}</span>}
    {item.guest_outdated && !required && <span className="env-line env-outdated" data-outdated="true">{t("environments.outdated")}</span>}
    {imageBuilding && <span className="env-line env-building" data-template="preparing">{t(item.state === "preparing" ? "environments.image_preparing" : "environments.image_building")}</span>}
    {item.error_code && <span className="env-line env-reason" data-error-code={item.error_code}>{t(`environments.error.${reasonKey(item.error_code)}` as "environments.error.generic")}</span>}
    {e2bError && <span className="env-line env-reason-detail">{e2bError}</span>}
  </>;
  const control = <div className="env-controls">
    <StatusBadge state={badgeFor[item.state]}>{words.state(item.state)}</StatusBadge>
    {buttons.map(action => <button key={action} type="button" className="secondary-button" data-action={action} disabled={working} onClick={() => handlers.onAction(action, item)}>{t(action === "configure" && provided ? "environments.action.details" : `environments.action.${action}`)}</button>)}
    {menu.length > 0 && <RowMenu item={item} name={name} items={menu} onAction={handlers.onAction} disabled={working}/>}
  </div>;
  return <div className="env-row" data-environment={item.id} data-kind={item.kind} data-state={item.state}><SettingsRow label={label} description={description} control={control}/></div>;
}

/** The list without the requests, so every state can be drawn from data. */
export function EnvironmentList({view, handlers, children, organization, thisMacID}: {view: EnvironmentsView; handlers: EnvironmentRowHandlers; children?: ReactNode; organization?: OrganizationComputer; /** The Mac app's own device: shown in the remote-control block (one thing in one place), not again here. */ thisMacID?: string | null}) {
  const {t} = useTranslation("computer");
  const macs = view.environments.filter(item => item.kind === "mac" && item.id !== thisMacID);
  const others = view.environments.filter(item => item.kind !== "mac");
  // The organization's computer is the one of its kind, also while it is still being prepared (no Bot computer yet).
  const orgKind = organization ? (organization.via === "e2b" ? "e2b" : "vm") : "";
  const isOrg = (item: Environment) => Boolean(organization) && item.kind === orgKind;
  const row = (item: Environment) => <EnvironmentRow key={item.id} item={item} e2bError={item.kind === "e2b" && !organization ? view.e2b?.error : undefined} handlers={handlers} provided={isOrg(item)} templatePhase={item.kind === "e2b" ? view.e2b?.template_phase : ""}/>;
  // Provided but nothing listed yet (the shared computer is not there): the page still says whose Bot computer it will be.
  const waiting = organization && !view.environments.some(isOrg);
  const empty = !others.length && !macs.length && !waiting;
  return <SettingsCard className="env-list">
    {waiting && <div className="env-row" data-environment="organization" data-kind={organization.via || "organization"} data-state="provided"><SettingsRow
      label={<span className="env-name"><TofiIcon name={organization.via === "e2b" ? "cloud" : "server"} size={18} aria-hidden="true"/><span className="env-name-text">{t(organization.via === "e2b" ? "environments.name.e2b" : "environments.name.vm")}</span><span className="env-tag">{t("environments.bot_computer")}</span><span className="env-tag env-tag-org" data-tag="organization"><TofiIcon name="lock" size={11} aria-hidden="true"/>{t("environments.provided")}</span></span>}
      description={<span className="env-line">{t("environments.provided_note")}</span>}/></div>}
    {others.map(row)}
    {macs.map(row)}
    {empty && <p className="settings-card-note" role="status">{t("environments.empty")}</p>}
    {children}
  </SettingsCard>;
}

type Dialog = {kind: "e2b"} | {kind: "denied"} | {kind: "vm"} | {kind: "configure"} | {kind: "confirm"; action: "use" | "remove" | "disconnect" | "upgrade"; item: Environment};

/** Settings > Environments (tab id `computer`): which environments exist, which Bots can use them, and the cloud ones' controls. */
export function EnvironmentsPage({openTab}: {openTab: (tab: SettingsTab) => void}) {
  const {t} = useTranslation("computer");
  const active = useSettingsActive();
  const {data, failed, retry, replace, reload} = useEnvironments(active);
  const has = useHasComputer(), host = useHostHasComputer(), session = useOwnerSession();
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const addButton = useRef<HTMLButtonElement>(null);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const canAttach = host && (!session?.multi_account || session.owner?.role === "admin");
  const rc = useRemoteControl(active);
  // Files and local execution (in development): the slim Mac settings page, reached only from the this-Mac row's menu.
  const openFiles = rc.available && window.tofiDesktop?.openDesktopSettings ? () => { void window.tofiDesktop?.openDesktopSettings?.().catch(() => {}); } : null;
  // The list follows the switch: a Mac that appeared or left is read once, not polled.
  const thisMacID = rc.status?.enabled ? rc.status.deviceID : null;
  const seenMac = useRef<string | null | undefined>(undefined);
  useEffect(() => {
    if (seenMac.current !== undefined && seenMac.current !== thisMacID) void reload();
    seenMac.current = thisMacID;
  }, [thisMacID, reload]);

  // The host command dialog stays open while the Bot computer is not there: the list reads again every 3 s and the dialog closes by itself.
  const vmOpen = dialog?.kind === "vm";
  const vmPresent = Boolean(data?.environments.some(item => item.kind === "vm"));
  useEffect(() => {
    if (!vmOpen) return;
    if (vmPresent) { setDialog(null); return; }
    const timer = window.setInterval(() => void reload(), 3000);
    return () => window.clearInterval(timer);
  }, [vmOpen, vmPresent, reload]);

  const reasonOf = (cause: unknown) => cause instanceof ApiError && cause.code ? t(`environments.error.${reasonKey(cause.code)}` as "environments.error.generic") : t("environments.error.generic");
  async function run(id: string, work: () => Promise<unknown>, after?: (body: unknown) => void) {
    setBusy(id); setError("");
    try { const body = await work(); if (!alive.current) return; if (after) after(body); else await reload(); }
    catch (cause) { if (alive.current) setError(reasonOf(cause)); }
    finally { if (alive.current) { setBusy(""); setDialog(null); } }
  }
  function onAction(action: EnvironmentAction, item: Environment) {
    if (action === "configure") setDialog({kind: "configure"});
    else if (action === "retry") void run(item.id, () => request("/api/computers/firecracker/retry", {method: "POST"}));
    else if (action === "wake") void run(item.id, () => request("/api/computers/firecracker/wake", {method: "POST"}));
    else setDialog({kind: "confirm", action, item});
  }
  function confirmed(action: "use" | "remove" | "disconnect" | "upgrade", item: Environment) {
    if (action === "upgrade") void run(item.id, () => request("/api/computers/firecracker/upgrade", {method: "POST"}));
    else if (action === "use") void run(item.id, () => request<unknown>(`/api/environments/${encodeURIComponent(item.id)}/use`, {method: "POST"}), replace);
    else if (action === "remove") void run(item.id, () => request<unknown>("/api/environments/e2b", {method: "DELETE"}), replace);
    else void run(item.id, () => request(`/api/computers/${encodeURIComponent(item.id)}`, {method: "DELETE"}));
  }
  const words = useEnvironmentWords();
  function spec(action: "use" | "remove" | "disconnect" | "upgrade", item: Environment): ConfirmSpec {
    const name = words.name(item);
    return {title: t(`environments.confirm.${action}.title`, {name}), body: t(`environments.confirm.${action}.body`, {name}), action: t(`environments.confirm.${action}.action`), danger: action === "upgrade"};
  }

  const add = data?.add;
  const addItems = [
    add?.e2b ? {id: "e2b" as const, icon: "cloud" as const, label: t("environments.add_menu.e2b")} : null,
    add?.vm_command ? {id: "vm" as const, icon: "server" as const, label: t("environments.add_menu.vm")} : null,
  ].filter(item => item !== null);
  const addControl = addItems.length > 0 && <>
    <button ref={addButton} type="button" className="primary-button env-add" aria-haspopup="menu" aria-expanded={menuOpen} onClick={() => setMenuOpen(value => !value)} onKeyDown={arrowOpens(setMenuOpen)}><TofiIcon name="plus" size={16} aria-hidden="true"/>{t("environments.add")}</button>
    {menuOpen && <OverlayMenu open onOpenChange={setMenuOpen} className="mcp-menu env-menu env-add-menu" label={t("environments.add")} trigger={addButton as RefObject<HTMLElement | null>} anchor={addButton as RefObject<HTMLElement | null>} align="end" sideOffset={6}>
      {addItems.map(item => <button key={item.id} type="button" data-add={item.id} onClick={() => { setMenuOpen(false); setDialog({kind: item.id}); }}><TofiIcon name={item.icon} size={16} aria-hidden="true"/>{item.label}</button>)}
    </OverlayMenu>}
  </>;

  const policy = usePolicy();
  const computerPolicy = data?.policy ?? policy.of("computer");
  const computerMode = computerPolicy.mode;
  const organization = computerMode === "provided" ? {via: computerPolicy.via ?? ""} : undefined;
  const noComputer = computerMode === "none";
  const noBackendEmpty = !has && data && data.environments.length === 0 && !canAttach && !organization && !noComputer;
  let body: ReactNode;
  if (failed && !data) body = <Banner tone="error" title={t("environments.unavailable")} action={{label: t("environments.retry"), onClick: retry}}/>;
  else if (!data) body = <p className="field-note" role="status">{t("environments.loading")}</p>;
  else if (noBackendEmpty) body = <ComputerOptions onAddE2B={add?.e2b ? () => setDialog({kind: "e2b"}) : undefined} />;
  else body = <>
    {noComputer && <Banner tone="info" title={t("environments.none_title")}>{t("environments.none_body")}</Banner>}
    <SectionAnchor id="environment"><SettingsSection className="env-section">
      <div className="env-head"><p>{t("environments.description")}</p>{addControl}</div>
      <EnvironmentList view={data} handlers={{busy, onAction}} organization={organization} thisMacID={rc.available ? rc.status?.deviceID : null}/>
    </SettingsSection></SectionAnchor>
    {!rc.available && add?.mac && <p className="rc-browser-note" data-add-mac-note="">{t("environments.rc.browser")}</p>}
    {failed && <Banner tone="warn" title={t("environments.stale")} action={{label: t("environments.retry"), onClick: retry}}/>}
  </>;

  const base = session?.multi_account && session.owner?.id ? `/api/admin/accounts/${encodeURIComponent(session.owner.id)}/computer` : "/api/admin/computer";
  return <div className="settings-stack environments-page">
    {error && <Banner tone="error" title={error}/>}
    {data && rc.available && <RemoteControlBlock rc={rc} offered={add?.mac !== false} onOpenFiles={openFiles} onOpenDenied={() => setDialog({kind: "denied"})}/>}
    {body}
    {data && !has && canAttach && computerMode === "host" && <SettingsSection id="attach" title={t("environments.attach_title")}><ComputerAttach base={base}/></SettingsSection>}
    {dialog?.kind === "e2b" && <E2BDialog onClose={() => setDialog(null)} onSaved={body => { replace(body); setDialog(null); }}/>}
    {dialog?.kind === "denied" && <DeniedBotsDialog bots={rc.denied} onUndo={id => void rc.undoDeny(id)} onClose={() => setDialog(null)}/>}
    {dialog?.kind === "vm" && add?.vm_command && <VmCommandDialog command={add.vm_command} onClose={() => setDialog(null)}/>}
    {dialog?.kind === "configure" && <ConfigureDialog provided={Boolean(organization)} onClose={() => setDialog(null)} onOpenAdmin={() => openTab("admin")}/>}
    {dialog?.kind === "confirm" && <ActionConfirm spec={spec(dialog.action, dialog.item)} busy={busy !== ""} onCancel={() => setDialog(null)} onConfirm={() => confirmed(dialog.action, dialog.item)}/>}
  </div>;
}
