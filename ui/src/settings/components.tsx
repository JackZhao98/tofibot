import {useId, useState, type ReactNode} from "react";
import {TofiIcon, type TofiIconName} from "../icons";
import {useTranslation} from "../i18n";
import "./settings-components.css";

/** Section: a 16px display heading, an optional count or hint, an optional description. */
export function SettingsSection({title, hint, description, children, className = ""}: {title?: ReactNode; hint?: ReactNode; description?: ReactNode; children: ReactNode; className?: string}) {
  const id = useId();
  return <section className={`settings-sec ${className}`.trim()} aria-labelledby={title ? id : undefined}>
    {(title || hint) && <div className="settings-sec-head">{title && <h4 id={id}>{title}</h4>}{hint && <span className="settings-sec-hint">{hint}</span>}</div>}
    {description && <p className="settings-sec-desc">{description}</p>}
    {children}
  </section>;
}

/** Card: 1px soft border, 18px radius; direct children are separated by 1px lines. */
export function SettingsCard({children, className = ""}: {children: ReactNode; className?: string}) {
  return <div className={`settings-card ${className}`.trim()}>{children}</div>;
}

/** Row: label 14px/600, description 12.5px muted, control right-aligned. On mobile the control drops below, except switches and segmented controls. */
export function SettingsRow({label, description, control, inline = false, labelFor}: {label: ReactNode; description?: ReactNode; control?: ReactNode; /** Keeps the control beside the text on mobile (switches, segmented controls). */ inline?: boolean; labelFor?: string}) {
  return <div className={`settings-row-v2${inline ? " is-inline" : ""}`}>
    <div className="settings-row-copy">{labelFor ? <label htmlFor={labelFor}>{label}</label> : <strong>{label}</strong>}{description && <p>{description}</p>}</div>
    {control && <div className="settings-row-control">{control}</div>}
  </div>;
}

export type BadgeState = "ok" | "need" | "bad" | "asleep" | "testing";
const badgeIcon: Record<BadgeState, TofiIconName> = {ok: "check-circle", need: "alert", bad: "error", asleep: "moon", testing: "loading"};
/** Status is icon plus text, never color alone. */
export function StatusBadge({state, children}: {state: BadgeState; children?: ReactNode}) {
  const {t} = useTranslation("settings");
  return <span className={`status-badge is-${state}`} data-state={state}><TofiIcon name={badgeIcon[state]} size={14} aria-hidden="true"/>{children ?? t(`status.${state}`)}</span>;
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
      {details && <><button type="button" className="settings-banner-more" aria-expanded={open} aria-controls={detailsId} onClick={() => setOpen(value => !value)}>{t("banner.details")}</button>{open && <pre id={detailsId} className="settings-banner-details">{details}</pre>}</>}
    </div>
    {action && <button type="button" className="secondary-button settings-banner-action" disabled={action.disabled} onClick={action.onClick}>{action.label}</button>}
  </div>;
}

/** Danger zone: dashed danger border, mono uppercase title. */
export function DangerZone({title, children}: {title: ReactNode; children: ReactNode}) {
  const id = useId();
  return <section className="danger-zone" aria-labelledby={id}><h4 id={id}>{title}</h4>{children}</section>;
}

/** Segmented control: a radiogroup over a short list. */
export function Segmented<T extends string>({value, options, onChange, label, disabled = false}: {value: T; options: readonly {value: T; label: ReactNode}[]; onChange: (value: T) => void; label: string; disabled?: boolean}) {
  return <div className="segmented" role="radiogroup" aria-label={label}>
    {options.map(option => <button key={option.value} type="button" role="radio" aria-checked={value === option.value} disabled={disabled} onClick={() => onChange(option.value)}>{option.label}</button>)}
  </div>;
}
