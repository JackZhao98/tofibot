import {useRef, useState, type RefObject} from "react";
import {useTranslation} from "./i18n";
import {TofiIcon} from "./icons";
import {arrowOpens, OverlayMenu} from "./overlays/OverlayHost";
import {thisMacState, type RemoteControl, type ThisMacState} from "./useRemoteControl";
import type {RemoteControlReason} from "./desktop";
import "./remote-control.css";

/**
 * Settings > Environments, Mac app only: the one switch "Remote control this Mac" and, below it while it is on, the this-Mac
 * row (docs/design/environments-simplify.md 4.1-4.3). One filled block (settings-simplify.md 5: `--surface-2`, 14px radius,
 * no border lines); the state is a dot and a word with one sentence for the reason; the only inline action is a text action.
 * Everything here is asked of the app's main process: the page can request the switch, and the macOS dialog decides.
 */
const keptReasons: RemoteControlReason[] = ["revoked", "connection_changed", "removal_pending"];

function Switch({checked, label, disabled, busy, onChange}: {checked: boolean; label: string; disabled: boolean; busy: boolean; onChange: (value: boolean) => void}) {
  return <label className="debug-toggle auto-switch rc-switch"><input type="checkbox" role="switch" aria-label={label} aria-busy={busy || undefined} checked={checked} disabled={disabled} onChange={event => onChange(event.target.checked)}/></label>;
}

function ThisMacMenu({name, denied, onOpenFiles, onOpenDenied}: {name: string; denied: number; onOpenFiles: (() => void) | null; onOpenDenied: () => void}) {
  const {t} = useTranslation("computer");
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const label = t("environments.more", {name});
  if (!onOpenFiles && !denied) return null;
  return <>
    <button ref={trigger} type="button" className="rc-more" aria-haspopup="menu" aria-expanded={open} aria-label={label} data-hint={label} onClick={() => setOpen(value => !value)} onKeyDown={arrowOpens(setOpen)}><TofiIcon name="more" size={16} aria-hidden="true"/></button>
    {open && <OverlayMenu open onOpenChange={setOpen} className="mcp-menu env-menu" label={label} trigger={trigger as RefObject<HTMLElement | null>} anchor={trigger as RefObject<HTMLElement | null>} align="end" sideOffset={6}>
      {onOpenFiles && <button type="button" className="mcp-menu-danger" data-action="files" onClick={() => { setOpen(false); onOpenFiles(); }}>{t("environments.rc.menu.files")}</button>}
      {denied > 0 && <button type="button" className="mcp-menu-danger" data-action="denied" onClick={() => { setOpen(false); onOpenDenied(); }}>{t("environments.rc.menu.denied")}</button>}
    </OverlayMenu>}
  </>;
}

/** The this-Mac row: icon, "This Mac" with the device name, the state, one inline action, the menu. */
export function ThisMacRow({rc, state, onOpenFiles, onOpenDenied}: {rc: RemoteControl; state: ThisMacState; onOpenFiles: (() => void) | null; onOpenDenied: () => void}) {
  const {t} = useTranslation("computer");
  const status = rc.status!;
  const working = rc.busy !== "";
  const reasonText = state.reason ? t(`environments.rc.reason.${state.reason}`) : state.word === "in_use" ? (status.botName ? t("environments.rc.in_use_by", {name: status.botName}) : t("environments.rc.in_use")) : "";
  const act = () => {
    if (state.action === "turn_on_both") void rc.set(true);
    else if (state.action === "authorize" || state.action === "retry") void rc.authorize();
    else if (state.action === "relaunch") void rc.relaunch();
    else if (state.action === "stop") void rc.stop();
  };
  const name = t("environments.rc.this_mac");
  return <div className="rc-row rc-mac" data-environment="this-mac" data-state={state.word} data-reason={state.reason ?? undefined}>
    <TofiIcon name="laptop" size={18} aria-hidden="true"/>
    <div className="rc-copy">
      <strong>{name}</strong>
      <p className="rc-device">{status.name}</p>
      {reasonText && <p className="rc-reason" data-reason-text="">{reasonText}</p>}
      {!status.hotkeyOk && state.word !== "unavailable" && <p className="rc-reason">{t("environments.rc.hotkey_taken")}</p>}
    </div>
    <div className="rc-side">
      <span className={`rc-state is-${state.word}`} role="status">{t(`environments.rc.state.${state.word}`)}</span>
      {state.action && <button type="button" className="rc-action" data-action={state.action} disabled={working} onClick={act}>{t(`environments.rc.action.${state.action}`)}</button>}
      <ThisMacMenu name={name} denied={rc.denied.length} onOpenFiles={onOpenFiles} onOpenDenied={onOpenDenied}/>
    </div>
  </div>;
}

/** The switch row, its one-line note, and the this-Mac row while the switch is on. `offered` is false on a server without personal computers. */
export function RemoteControlBlock({rc, offered, onOpenFiles, onOpenDenied}: {rc: RemoteControl; offered: boolean; onOpenFiles: (() => void) | null; onOpenDenied: () => void}) {
  const {t} = useTranslation("computer");
  const status = rc.status;
  if (!status) return null;
  const enabled = status.enabled;
  const locked = rc.busy !== "" || (!offered && !enabled);
  const kept = !enabled && status.reason && keptReasons.includes(status.reason) ? status.reason : null;
  const note = rc.failure ? {text: t(`environments.rc.reason.${rc.failure}`), alert: true} : kept ? {text: t(`environments.rc.reason.${kept}`), alert: false} : !offered && !enabled ? {text: t("environments.rc.no_personal"), alert: false} : null;
  return <section className="rc-block" data-remote-control="" data-enabled={enabled}>
    <div className="rc-row rc-switch-row">
      <div className="rc-copy"><strong>{t("environments.rc.title")}</strong><p>{t(enabled ? "environments.rc.on" : "environments.rc.off")}</p></div>
      <Switch checked={enabled} label={t("environments.rc.title")} disabled={locked} busy={rc.busy === "set"} onChange={value => void rc.set(value)}/>
    </div>
    {note && <p className={`rc-note${note.alert ? " is-error" : ""}`} role={note.alert ? "alert" : "status"} data-failure={rc.failure ?? undefined}>{note.text}</p>}
    {enabled && <ThisMacRow rc={rc} state={thisMacState(status)} onOpenFiles={onOpenFiles} onOpenDenied={onOpenDenied}/>}
  </section>;
}
