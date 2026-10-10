import {useEffect, useId, useRef, useState, type ReactNode} from "react";
import {createPortal} from "react-dom";
import {TofiIcon} from "./icons";
import {useTranslation} from "./i18n";
import {StatusBadge} from "./settings/components";
import type {AdminAccount} from "./adminApi";

/** Modal sheet shared by the admin flows: portal, focus trap, Escape closes unless `locked`. */
export function AdminSheet({title, children, onClose, locked = false, className = ""}: {title: ReactNode; children: ReactNode; onClose: () => void; /** Blocks Escape and outside clicks (work in flight, or a secret that must be acknowledged). */ locked?: boolean; className?: string}) {
  const pane = useRef<HTMLDivElement>(null);
  const headingId = useId();
  const lockedRef = useRef(locked);
  lockedRef.current = locked;
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    pane.current?.focus();
    const keys = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.defaultPrevented) {
        event.preventDefault(); event.stopPropagation();
        if (!lockedRef.current) onClose();
        return;
      }
      if (event.key !== "Tab") return;
      const targets = Array.from(pane.current?.querySelectorAll<HTMLElement>("button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href]") ?? []).filter(element => element.getClientRects().length);
      const index = targets.indexOf(document.activeElement as HTMLElement);
      if (index < 0 || (!event.shiftKey && index === targets.length - 1) || (event.shiftKey && index === 0)) {
        event.preventDefault();
        (event.shiftKey ? targets.at(-1) : targets[0])?.focus();
      }
    };
    document.addEventListener("keydown", keys, true);
    return () => { document.removeEventListener("keydown", keys, true); previous?.focus(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return createPortal(<div className="sheet-overlay" onMouseDown={event => { if (event.target === event.currentTarget && !locked) onClose(); }}>
    <div ref={pane} className={`sheet admin-sheet ${className}`.trim()} role="dialog" aria-modal="true" aria-labelledby={headingId} tabIndex={-1}>
      <h3 id={headingId}>{title}</h3>
      {children}
    </div>
  </div>, document.body);
}

/** Copy button that says "Copied" for a moment. Falls back to selecting the text when the clipboard is blocked. */
export function CopyButton({value, label, onFail}: {value: string; label?: string; onFail?: () => void}) {
  const {t} = useTranslation("settings");
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1800);
    return () => window.clearTimeout(timer);
  }, [copied]);
  async function copy() {
    try { await navigator.clipboard.writeText(value); setCopied(true); } catch { onFail?.(); }
  }
  return <button type="button" className="secondary-button admin-copy" onClick={() => void copy()}>
    <TofiIcon name={copied ? "check" : "copy"} size={16} aria-hidden="true"/>
    <span aria-live="polite">{copied ? t("admin.copied") : label ?? t("admin.copy")}</span>
  </button>;
}

/** A value the admin has to copy (link, passphrase, one-time password): monospace block plus Copy. */
export function SecretField({label, value, note}: {label: string; value: string; note?: string}) {
  const id = useId();
  return <div className="admin-secret">
    <label htmlFor={id}>{label}</label>
    <div className="admin-secret-line">
      <input id={id} readOnly value={value} onFocus={event => event.currentTarget.select()} spellCheck={false} autoComplete="off"/>
      <CopyButton value={value}/>
    </div>
    {note && <p>{note}</p>}
  </div>;
}

/** Role and status of an account, as chips. Status is icon plus text. */
export function AccountBadges({account}: {account: AdminAccount}) {
  const {t} = useTranslation("settings");
  return <span className="admin-badges">
    <span className="admin-role">{account.role === "admin" ? t("admin.role.admin") : t("admin.role.user")}</span>
    {account.deleting
      ? <StatusBadge state={account.delete_error ? "bad" : "testing"}>{account.delete_error ? t("admin.status.delete_failed") : t("admin.status.deleting")}</StatusBadge>
      : account.disabled
        ? <StatusBadge state="asleep">{t("admin.status.deactivated")}</StatusBadge>
        : account.must_change_password
          ? <StatusBadge state="need">{t("admin.status.must_change")}</StatusBadge>
          : <StatusBadge state="ok">{t("admin.status.active")}</StatusBadge>}
  </span>;
}
