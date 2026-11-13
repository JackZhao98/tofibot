import { useDropzone } from "react-dropzone";
import * as Collapsible from "@radix-ui/react-collapsible";
import * as RadioGroup from "@radix-ui/react-radio-group";
import {useCallback, useEffect, useId, useRef, useState, type ReactNode} from "react";
import * as Switch from "@radix-ui/react-switch";
import {TofiIcon} from "../icons";
import {useTranslation} from "../i18n";
import { Disclosure } from "../InteractionPrimitives";
import "./settings-components.css";

/** Section: a 16px display heading, an optional count or hint, an optional description. */
export function SettingsSection({title, hint, description, children, className = "", id: sectionId}: {title?: ReactNode; hint?: ReactNode; description?: ReactNode; children: ReactNode; className?: string; /** A stable id a link can scroll to (`data-section`; the ids are listed in settings/sections.ts). */ id?: string}) {
  const id = useId();
  return <section className={`settings-sec ${className}`.trim()} data-section={sectionId} aria-labelledby={title ? id : undefined}>
    {(title || hint) && <div className="settings-sec-head">{title && <h4 id={id}>{title}</h4>}{hint && <span className="settings-sec-hint">{hint}</span>}</div>}
    {description && <p className="settings-sec-desc">{description}</p>}
    {children}
  </section>;
}

/** Gives a block that is not a SettingsSection a stable id a link can scroll to. */
export function SectionAnchor({id, children, wide = false}: {id: string; children: ReactNode; /** Opts this block out of the 640px content column (a table that cannot fit it). */ wide?: boolean}) {
  return <div className={`settings-anchor${wide ? " st-wide" : ""}`} data-section={id}>{children}</div>;
}

/** Card (a group): one filled block, no border and no shadow; direct children are separated by 1px lines that stop short of the edges. */
export function SettingsCard({children, className = ""}: {children: ReactNode; className?: string}) {
  return <div className={`settings-card ${className}`.trim()}>{children}</div>;
}

/**
 * Row: name 14px/500, one-line description 12.5px muted, at most one status, one control and one chevron on the right.
 * `link` makes the whole row a button that opens a subpage: the current value in grey plus a chevron.
 * On mobile the control drops below, except switches and segmented controls (`inline`).
 */
export function SettingsRow({label, description, control, inline = false, labelFor, status, link}: {label: ReactNode; description?: ReactNode; control?: ReactNode; /** Keeps the control beside the text on mobile (switches, segmented controls). */ inline?: boolean; labelFor?: string; /** A StatusBadge (dot and word) shown before the control. */ status?: ReactNode; /** Turns the row into a subpage link. */ link?: {onOpen: () => void; value?: ReactNode; label?: string}}) {
  const copy = <div className="settings-row-copy">{labelFor && !link ? <label htmlFor={labelFor}>{label}</label> : <strong>{label}</strong>}{description && <p>{description}</p>}</div>;
  if (link) {
    return <button type="button" className="settings-row-v2 is-link is-inline" aria-label={link.label} onClick={link.onOpen}>
      {copy}
      {status && <span className="settings-row-status">{status}</span>}
      {link.value !== undefined && link.value !== "" && <span className="settings-row-value">{link.value}</span>}
      <TofiIcon name="chevron-right" size={14} aria-hidden="true"/>
    </button>;
  }
  return <div className={`settings-row-v2${inline ? " is-inline" : ""}`}>
    {copy}
    {status && <span className="settings-row-status">{status}</span>}
    {control && <div className="settings-row-control">{control}</div>}
  </div>;
}

/** "Saved" for 1.5 seconds after a control that saves on change. Local state only: flipping a switch never re-renders the page. */
export function useSavedFlash(): [boolean, () => void] {
  const [saved, setSaved] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const flash = useCallback(() => {
    setSaved(true);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setSaved(false), 1500);
  }, []);
  return [saved, flash];
}

/** The inline confirmation a saving control shows next to itself. Announced politely, empty when idle. */
export function SavedNote({show}: {show: boolean}) {
  const {t} = useTranslation("settings");
  return <span className="settings-saved" aria-live="polite">{show ? t("saved") : ""}</span>;
}

/**
 * Switch (Radix): 36x22, off = 20% ink, on = lagoon. Only for "on / off and it takes effect now". `onChange` may return a promise:
 * "Saved" shows for 1.5 s once it resolves; a rejection shows nothing (the caller reports the failure).
 */
export function SettingsSwitch({checked, onChange, label, labelledBy, disabled = false, busy = false, id, className = "", showSaved = true}: {checked: boolean; onChange: (next: boolean) => void | Promise<unknown>; /** The accessible name when no visible label points at the switch. */ label?: string; labelledBy?: string; disabled?: boolean; busy?: boolean; id?: string; className?: string; showSaved?: boolean}) {
  const [saved, flash] = useSavedFlash();
  const change = (next: boolean) => {
    const result = onChange(next);
    if (!showSaved) return;
    if (result && typeof (result as Promise<unknown>).then === "function") void (result as Promise<unknown>).then(value => { if (value !== false) flash(); }, () => {});
    else flash();
  };
  return <span className={`st-switch-wrap ${className}`.trim()}>
    {showSaved && <SavedNote show={saved}/>}
    <Switch.Root id={id} className="st-switch" checked={checked} disabled={disabled} aria-label={label} aria-labelledby={labelledBy} aria-busy={busy || undefined} onCheckedChange={change}><Switch.Thumb className="st-switch-thumb"/></Switch.Root>
  </span>;
}

/** A value that differs from its default: "Changed · default X · Reset", in honey text, no pill. */
export function ChangedNote({defaultValue, onReset, disabled = false, resetLabel}: {defaultValue: ReactNode; onReset: () => void; disabled?: boolean; /** A fuller accessible name for the Reset action ("Reset to default"); the visible word stays "Reset". */ resetLabel?: string}) {
  const {t} = useTranslation("settings");
  return <span className="st-changed">{t("changed.note", {value: defaultValue})} · <button type="button" className="st-link" disabled={disabled} aria-label={resetLabel} onClick={onReset}>{t("changed.reset")}</button></span>;
}

export type BadgeState = "ok" | "need" | "bad" | "asleep" | "testing";
/** Status is a 7px dot and a word, never color alone: solid green = fine, solid honey = needs you, hollow = off, red = failed. */
export function StatusBadge({state, children}: {state: BadgeState; children?: ReactNode}) {
  const {t} = useTranslation("settings");
  return <span className={`status-badge is-${state}`} data-state={state}><i className="status-dot" aria-hidden="true"/>{children ?? t(`status.${state}`)}</span>;
}

const bannerIcon = {error: "error", warn: "alert", info: "info"} as const;
/** Banner: a bold "what happened", one line "what to do", at most one action. Only errors announce themselves. */
export function Banner({tone, title, children, action, details}: {tone: "error" | "warn" | "info"; title: ReactNode; children?: ReactNode; action?: {label: ReactNode; onClick: () => void; disabled?: boolean}; details?: string}) {
  const {t} = useTranslation("settings");
  const [open, setOpen] = useState(false);
  const detailsId = useId();
  return <div className={`settings-banner is-${tone}`} role={tone === "error" ? "alert" : undefined}>
    <TofiIcon name={bannerIcon[tone]} size={18} aria-hidden="true"/>
    <div className="settings-banner-copy"><strong>{title}</strong>{children && <p>{children}</p>}
      {details && <Collapsible.Root open={open} onOpenChange={setOpen}><Collapsible.Trigger className="settings-banner-more">{t("banner.details")}</Collapsible.Trigger><Collapsible.Content className="collapsible-content"><pre id={detailsId} className="settings-banner-details">{details}</pre></Collapsible.Content></Collapsible.Root>}
    </div>
    {action && <button type="button" className="st-button settings-banner-action" disabled={action.disabled} onClick={action.onClick}>{action.label}</button>}
  </div>;
}

/** Danger zone: a plain section titled by the caller ("Danger zone"), last on the page. Only the action's text is red (`st-button is-danger`); no frame. */
export function DangerZone({title, children}: {title: ReactNode; children: ReactNode}) {
  const id = useId();
  return <section className="settings-sec danger-zone" aria-labelledby={id}><div className="settings-sec-head"><h4 id={id}>{title}</h4></div>{children}</section>;
}

/** Segmented control: a radiogroup over a short list. */
export function Segmented<T extends string>({value, options, onChange, label, disabled = false}: {value: T; options: readonly {value: T; label: ReactNode}[]; onChange: (value: T) => void; label: string; disabled?: boolean}) {
  return <RadioGroup.Root value={value} onValueChange={onChange} disabled={disabled} className="segmented" aria-label={label}>
    {options.map(option => <RadioGroup.Item key={option.value} value={option.value}>{option.label}</RadioGroup.Item>)}
  </RadioGroup.Root>;
}

/** Short help text stays visible; the long version sits behind a "Details" disclosure. */
export function SettingsDetails({children}: {children: ReactNode}) {
  const {t} = useTranslation("settings");
  return <Disclosure title={t("disclosure.details")}>{children}</Disclosure>;
}

/** Styled file picker: a button, the chosen file name as a chip, and a drop zone. The native input stays hidden. */
export function FilePicker({accept, label, fileName, onFile, disabled = false, dropHint, labelledBy}: {accept?: string; label: ReactNode; fileName?: string; onFile: (file: File) => void; disabled?: boolean; dropHint?: ReactNode; labelledBy?: string}) {
  const input = useRef<HTMLInputElement>(null);
  const { getRootProps, isDragActive } = useDropzone({ noClick: true, noKeyboard: true, disabled,
    onDrop: files => { if (files[0]) onFile(files[0]); },
  });
  return <div {...getRootProps({ className: `file-picker${isDragActive ? " is-dragging" : ""}`, role: labelledBy ? "group" : undefined, "aria-labelledby": labelledBy })}>
    <input ref={input} type="file" hidden accept={accept} onChange={event => { const file = event.currentTarget.files?.[0]; event.currentTarget.value = ""; if (file) onFile(file); }}/>
    <button type="button" className="st-button" disabled={disabled} onClick={() => input.current?.click()}><TofiIcon name="folder-open" size={16} aria-hidden="true"/>{label}</button>
    {fileName ? <span className="file-chip" title={fileName}><TofiIcon name="file" size={14} aria-hidden="true"/><span>{fileName}</span></span> : dropHint && <span className="file-picker-hint">{dropHint}</span>}
  </div>;
}

/**
 * A subpage inside a settings page: back, title, optional actions and focus return. Focus goes to the back button when the subpage opens
 * and is handed back to whatever opened it (`opener`, or the element that had focus) when it closes.
 */
export function SettingsSubpage({title, note, lead, actions, onBack, children, className = "", headClassName = "", titleClassName = "", backClassName = "", backLabel, opener, attrs}: {title: ReactNode; note?: ReactNode; /** A mark shown before the title (a service logo). */ lead?: ReactNode; actions?: ReactNode; onBack: () => void; children: ReactNode; className?: string; headClassName?: string; titleClassName?: string; backClassName?: string; backLabel?: string; /** The element focus returns to on close. */ opener?: HTMLElement | null; /** Extra attributes for the root element (`data-kind`, `aria-label`). */ attrs?: Record<string, string>}) {
  const {t} = useTranslation("settings");
  const back = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const origin = opener ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    back.current?.focus({preventScroll: true});
    return () => { if (origin && origin.isConnected) origin.focus({preventScroll: true}); };
  }, [opener]);
  return <div className={`st-subpage ${className}`.trim()} {...attrs}>
    <header className={`st-subpage-head ${headClassName}`.trim()}>
      <button ref={back} type="button" className={`st-subpage-back ${backClassName}`.trim()} aria-label={backLabel ?? t("shell.back")} onClick={onBack}><TofiIcon name="chevron-left" size={20} aria-hidden="true"/></button>
      {lead}
      <div className={`st-subpage-title ${titleClassName}`.trim()}><h3>{title}</h3>{note && <p>{note}</p>}</div>
      {actions}
    </header>
    {children}
  </div>;
}
