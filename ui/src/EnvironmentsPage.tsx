import {EnvironmentFeatures} from "./EnvironmentFeatures";
import {AutomationRows} from "./AutomationSettings";
import {useEffect, useRef, useState, type ReactNode, type RefObject} from "react";
import {ApiError, request} from "./api";
import {ComputerAttach} from "./ComputerAttach";
import {useHasComputer, useHostHasComputer} from "./computerCapability";
import {ActionConfirm, ConfigureDialog, DeniedBotsDialog, E2BDialog, reasonKey, VmCommandDialog, type ConfirmSpec} from "./EnvironmentDialogs";
import {RemoteControlBlock} from "./RemoteControl";
import {MacFolders} from "./MacFolders";
import type {Bot} from "./types";
import {useRemoteControl} from "./useRemoteControl";
import {useEnvironments, type Environment, type EnvironmentAction, type EnvironmentFeature, type EnvironmentState, type EnvironmentsView} from "./environments";
import {useTranslation} from "./i18n";
import {formatRelativeTime} from "./i18n/format";
import {TofiIcon, type TofiIconName} from "./icons";
import {useOwnerSession} from "./OwnerSession";
import {usePolicy} from "./capabilityPolicy";
import {arrowOpens, OverlayMenu, pressToggle } from "./overlays/OverlayHost";
import {useSettingsActive} from "./settingsActivity";
import {Banner, SectionAnchor, SettingsCard, SettingsRow, SettingsSection, StatusBadge, type BadgeState} from "./settings/components";
import type {SettingsTab} from "./SettingsShell";
import "./extensions-system.css";
import "./environments.css";
import "./remote-control.css";
import "./policy-member.css";
import { Button } from "./ds/Button/Button";
import { EmptyState } from "./ds/EmptyState/EmptyState";
import { Notice } from "./ds/Notice/Notice";

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
    name: (item: Environment) => item.kind === "mac" ? item.name || t("environments.name.mac") : t(`devices.kind.${item.kind}`),
    state: (state: EnvironmentState) => t(`environments.state.${state}`),
      };
}

function RowMenu({item, name, items, onAction, disabled}: {item: Environment; name: string; items: EnvironmentAction[]; onAction: EnvironmentRowHandlers["onAction"]; disabled: boolean}) {
  const {t} = useTranslation("computer");
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  return <>
    <button ref={trigger} type="button" className="st-button env-more" disabled={disabled} aria-haspopup="menu" aria-expanded={open} aria-label={t("environments.more", {name})} data-hint={t("environments.more", {name})}
      {...pressToggle(() => setOpen(value => !value))} onKeyDown={arrowOpens(setOpen)}><TofiIcon name="more" size={16} aria-hidden="true"/></button>
    {open && <OverlayMenu open onOpenChange={setOpen} className="mcp-menu env-menu" label={t("environments.more", {name})} trigger={trigger as RefObject<HTMLElement | null>} anchor={trigger as RefObject<HTMLElement | null>} align="end" sideOffset={6}>
      {items.map(action => <button key={action} type="button" className="mcp-menu-danger is-danger" data-action={action} onClick={() => { setOpen(false); onAction(action, item); }}>{t(`environments.action.${action}`)}</button>)}
    </OverlayMenu>}
  </>;
}

/** The words a row shows for a Mac's features: remote control (desktop), the browser, and folders (files or shell). */
const capOf: Record<EnvironmentFeature["key"], "remote" | "browser" | "folders"> = {desktop: "remote", browser: "browser", files: "folders", shell: "folders"};
const quietStates: EnvironmentState[] = ["ready", "online"];

/** One row: kind icon, name, a subtitle, a badge only when the state is not the normal one, and the controls `actions` allows. */
export function EnvironmentRow({item, e2bError, handlers, provided = false, templatePhase = "", keyHint = ""}: {item: Environment; e2bError?: string; handlers: EnvironmentRowHandlers; provided?: boolean; templatePhase?: string; keyHint?: string}) {
  const {t} = useTranslation("computer");
  const words = useEnvironmentWords();
  const name = words.name(item);
  const buttons = item.actions.filter(action => !menuActions.includes(action) && action !== "use");
  const menu = item.actions.filter(action => menuActions.includes(action));
  const working = handlers.busy === item.id;
  const lastSeen = item.kind === "mac" && item.state === "offline" && item.last_seen ? formatRelativeTime(item.last_seen) : "";
  const label = <span className="env-name"><TofiIcon name={kindIcon[item.kind]} size={18} aria-hidden="true"/><span className="env-name-text">{name}</span></span>;
  const required = item.error_code === "guest_outdated_required";
  const imageBuilding = item.kind === "e2b" && templatePhase === "preparing" && !required;
  const caps = [...new Set(item.features.filter(feature => feature.status === "connected").map(feature => capOf[feature.key]))];
  const subtitle = provided ? t("devices.bot.managed")
    : item.kind === "vm" ? t("devices.kind.vm_sub")
    : item.kind === "e2b" ? (keyHint ? t("devices.kind.e2b_sub", {hint: keyHint}) : t("devices.kind.e2b_billed"))
    : lastSeen ? t("devices.mac.last_seen", {time: lastSeen})
    : caps.length ? caps.map(cap => t(`devices.cap.${cap}`)).join(" · ") : t("devices.mac.no_access");
  const description = <>
    <span className="env-line">{subtitle}</span>
    {item.guest_outdated && !required && <span className="env-line env-outdated" data-outdated="true">{t("environments.outdated")}</span>}
    {imageBuilding && <span className="env-line env-building" data-template="preparing">{t(item.state === "preparing" ? "environments.image_preparing" : "environments.image_building")}</span>}
    {item.error_code && <span className="env-line env-reason" data-error-code={item.error_code}>{t(`environments.error.${reasonKey(item.error_code)}` as "environments.error.generic")}</span>}
    {e2bError && <span className="env-line env-reason-detail">{e2bError}</span>}
  </>;
  const control = <div className="env-controls">
    {!quietStates.includes(item.state) && <StatusBadge state={badgeFor[item.state]}>{words.state(item.state)}</StatusBadge>}
    {buttons.map(action => <Button key={action} type="button" size="sm" data-action={action} disabled={working} onClick={() => handlers.onAction(action, item)}>{t(action === "configure" && provided ? "environments.action.details" : `environments.action.${action}`)}</Button>)}
    {menu.length > 0 && <RowMenu item={item} name={name} items={menu} onAction={handlers.onAction} disabled={working}/>}
  </div>;
  return <div className="env-row" data-environment={item.id} data-kind={item.kind} data-state={item.state} data-bot-computer={item.bot_computer || undefined}><SettingsRow label={label} description={description} control={control}/></div>;
}

/** What the empty Bot computer section offers: one row per kind, in the names the filled rows use. */
export type BotComputerOffers = {onSetupVM?: () => void; attach?: ReactNode; vmMember?: boolean; onAddE2B?: () => void; unassigned?: boolean};

/** Devices: the Bot computer (one per account) and the person's own devices. Drawn from data, so the catalog shows every state. */
export function EnvironmentList({view, handlers, children, organization, thisMacID, offers = {}}: {view: EnvironmentsView; handlers: EnvironmentRowHandlers; children?: ReactNode; organization?: OrganizationComputer; /** The Mac app's own device: shown in the remote-control block (one thing in one place), not again here. */ thisMacID?: string | null; offers?: BotComputerOffers}) {
  const {t} = useTranslation("computer");
  const macs = view.environments.filter(item => item.kind === "mac" && item.id !== thisMacID);
  const computers = view.environments.filter(item => item.kind !== "mac");
  // One Bot computer: the one Bots use, else the one being set up. A second one ready beside it is a note with Switch.
  const primary = computers.find(item => item.bot_computer) ?? computers[0];
  // A second computer that is ready but idle is a note with Switch; one still being set up (or failed) keeps its own row.
  const spare = computers.find(item => item !== primary && item.actions.includes("use") && !["preparing", "failed", "starting"].includes(item.state));
  const settingUp = computers.filter(item => item !== primary && item !== spare);
  const canAddE2B = Boolean(offers.onAddE2B) && !computers.some(item => item.kind === "e2b");
  const orgKind = organization ? (organization.via === "e2b" ? "e2b" : "vm") : "";
  const isOrg = (item: Environment) => Boolean(organization) && item.kind === orgKind;
  const waiting = organization && !view.environments.some(isOrg);
  const row = (item: Environment) => <EnvironmentRow key={item.id} item={item} e2bError={item.kind === "e2b" && !organization ? view.e2b?.error : undefined} handlers={handlers} provided={isOrg(item)} templatePhase={item.kind === "e2b" ? view.e2b?.template_phase : ""} keyHint={item.kind === "e2b" && !organization ? view.e2b?.key_hint : ""}/>;
  let bot: ReactNode;
  if (primary) bot = <SettingsCard className="env-list">{row(primary)}{settingUp.map(row)}
    {canAddE2B && <div className="env-row" data-offer="e2b"><SettingsRow label={<span className="env-name"><TofiIcon name="cloud" size={18} aria-hidden="true"/><span className="env-name-text">{t("devices.kind.e2b")}</span></span>} description={<span className="env-line">{t("devices.add.e2b_body")}</span>} control={<Button size="sm" data-add="e2b" onClick={offers.onAddE2B}>{t("devices.action.add")}</Button>}/></div>}
    {spare && <Notice className="env-spare" action={<><Button size="sm" data-action="use" onClick={() => handlers.onAction("use", spare)}>{t("devices.second.switch")}</Button>{spare.actions.includes("remove") && <Button size="sm" variant="ghost" data-action="remove" onClick={() => handlers.onAction("remove", spare)}>{t("environments.action.remove")}</Button>}</>}>{t("devices.second.ready", {name: t(`devices.kind.${spare.kind === "e2b" ? "e2b" : "vm"}`)})}</Notice>}
  </SettingsCard>;
  else if (waiting) bot = <SettingsCard className="env-list"><div className="env-row" data-environment="organization" data-kind={organization.via || "organization"} data-state="provided"><SettingsRow
    label={<span className="env-name"><TofiIcon name={organization.via === "e2b" ? "cloud" : "server"} size={18} aria-hidden="true"/><span className="env-name-text">{t(organization.via === "e2b" ? "devices.kind.e2b" : "devices.kind.vm")}</span></span>}
    description={<span className="env-line">{t("devices.bot.managed")}</span>}/></div></SettingsCard>;
  else if (offers.unassigned) bot = <EmptyState icon="server" title={t("devices.bot.unassigned_title")}>{t("devices.bot.unassigned_body")}</EmptyState>;
  else bot = <SettingsCard className="env-list env-offers">
    <div className="env-row" data-offer="vm"><SettingsRow label={<span className="env-name"><TofiIcon name="server" size={18} aria-hidden="true"/><span className="env-name-text">{t("devices.kind.vm")}</span></span>}
      description={<span className="env-line">{offers.vmMember ? t("devices.add.vm_member") : t("devices.add.vm_body")}</span>}
      control={offers.onSetupVM ? <Button size="sm" data-add="vm" onClick={offers.onSetupVM}>{t("devices.action.setup")}</Button> : undefined}/>{offers.attach}</div>
    <div className="env-row" data-offer="e2b"><SettingsRow label={<span className="env-name"><TofiIcon name="cloud" size={18} aria-hidden="true"/><span className="env-name-text">{t("devices.kind.e2b")}</span></span>}
      description={<span className="env-line">{t("devices.add.e2b_body")}</span>}
      control={offers.onAddE2B ? <Button size="sm" data-add="e2b" onClick={offers.onAddE2B}>{t("devices.action.add")}</Button> : undefined}/></div>
  </SettingsCard>;
  return <div className="settings-stack env-devices">
    <SectionAnchor id="environment"><SettingsSection className="env-section" title={t("devices.bot.title")} description={t("devices.bot.description")}>{bot}</SettingsSection></SectionAnchor>
    <SectionAnchor id="devices"><SettingsSection className="env-section" title={t("devices.mine.title")} description={t("devices.mine.description")}>
      {macs.length ? <SettingsCard className="env-list">{macs.map(row)}</SettingsCard>
        : !thisMacID && <EmptyState icon="laptop" title={t("devices.mine.empty_title")}>{t(view.add.mac ? "devices.mine.empty_body" : "devices.mine.disabled")}</EmptyState>}
      {children}
    </SettingsSection></SectionAnchor>
  </div>;
}

type Dialog = {kind: "e2b"} | {kind: "denied"} | {kind: "vm"} | {kind: "configure"} | {kind: "confirm"; action: "use" | "remove" | "disconnect" | "upgrade"; item: Environment};

/** Settings > Environments (tab id `computer`): which environments exist, which Bots can use them, and the cloud ones' controls. */
export function EnvironmentsPage({openTab, bots = []}: {openTab: (tab: SettingsTab) => void; /** The account's active Bots, for the folder grant menu. */ bots?: Bot[]}) {
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
  // The Mac this app runs on: its row in the list is left out (it is shown once, on the Bot browser row below).
  const thisMac = data?.environments.find(item => item.kind === "mac" && item.id === rc.status?.deviceID);
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
  const policy = usePolicy();
  const computerPolicy = data?.policy ?? policy.of("computer");
  const computerMode = computerPolicy.mode;
  const organization = computerMode === "provided" ? {via: computerPolicy.via ?? ""} : undefined;
  const base = session?.multi_account && session.owner?.id ? `/api/admin/accounts/${encodeURIComponent(session.owner.id)}/computer` : "/api/admin/computer";
  const offers: BotComputerOffers = {
    unassigned: computerMode === "none",
    onSetupVM: add?.vm_command ? () => setDialog({kind: "vm"}) : undefined,
    attach: data && !has && canAttach && computerMode === "host" ? <ComputerAttach base={base}/> : undefined,
    vmMember: !add?.vm_command && !canAttach,
    onAddE2B: add?.e2b ? () => setDialog({kind: "e2b"}) : undefined,
  };
  let body: ReactNode;
  if (failed && !data) body = <Banner tone="error" title={t("environments.unavailable")} action={{label: t("environments.retry"), onClick: retry}}/>;
  else if (!data) body = <p className="field-note" role="status">{t("environments.loading")}</p>;
  else body = <>
    <EnvironmentList view={data} handlers={{busy, onAction}} organization={organization} thisMacID={rc.available ? rc.status?.deviceID : null} offers={offers}/>
    {failed && <Banner tone="warn" title={t("environments.stale")} action={{label: t("environments.retry"), onClick: retry}}/>}
  </>;

  return <div className="settings-stack environments-page">
    {error && <Banner tone="error" title={error}/>}
    {data && rc.available && <RemoteControlBlock rc={rc} offered={add?.mac !== false} onOpenFiles={openFiles} onOpenDenied={() => setDialog({kind: "denied"})}/>}
    <MacFolders bots={bots}/>
    {/* This Mac's features (built-in browser, desktop control, ...) show here whether or not remote control is on. */}
    <SettingsRow label={t("browser.source")} description={!window.tofiDesktop ? t("browser.in_app") : !window.tofiDesktop.botBrowser ? t("browser.status.browser_client_unsupported") : thisMac ? <EnvironmentFeatures kind="mac" features={thisMac.features}/> : undefined} control={window.tofiDesktop?.botBrowser && window.tofiDesktop.openDesktopSettings ? <Button type="button" size="sm" onClick={() => { void window.tofiDesktop?.openDesktopSettings?.(); }}>{t("browser.settings")}</Button> : undefined}/>
    {body}
    {data && data.environments.some(item => item.bot_computer || item.kind === "mac") && <AutomationRows groups={["environment"]} hasEnvironment/>}
    {dialog?.kind === "e2b" && <E2BDialog onClose={() => setDialog(null)} onSaved={body => { replace(body); setDialog(null); }}/>}
    {dialog?.kind === "denied" && <DeniedBotsDialog bots={rc.denied} onUndo={id => void rc.undoDeny(id)} onClose={() => setDialog(null)}/>}
    {dialog?.kind === "vm" && add?.vm_command && <VmCommandDialog command={add.vm_command} onClose={() => setDialog(null)}/>}
    {dialog?.kind === "configure" && <ConfigureDialog provided={Boolean(organization)} onClose={() => setDialog(null)} onOpenAdmin={() => openTab("admin")}/>}
    {dialog?.kind === "confirm" && <ActionConfirm spec={spec(dialog.action, dialog.item)} busy={busy !== ""} onCancel={() => setDialog(null)} onConfirm={() => confirmed(dialog.action, dialog.item)}/>}
  </div>;
}
