import {useEffect, useRef, useState, type ReactNode} from "react";
import {useTranslation} from "./i18n";
import {StatusBadge, type BadgeState} from "./settings/components";

/** Avoid a busy-label flash when provider verification completes immediately. */
export function useDelayedBusy(busy: boolean) {
 const [visible, setVisible] = useState(false);
 useEffect(() => {
  if (!busy) { setVisible(false); return; }
  const timer = window.setTimeout(() => setVisible(true), 150);
  return () => window.clearTimeout(timer);
 }, [busy]);
 return busy && visible;
}

/** One provider row; credentials and connection details are disclosed on demand. */
export function CardHead({id, title, state, action, status, tag}: {id: string; title: string; state: BadgeState; action?: ReactNode; status?: string; tag?: string}) {
 return <div className="provider-card-head"><h3 id={id}>{title}</h3>{tag || status ? <span className="provider-card-marks"><StatusBadge state={state}>{status}</StatusBadge>{tag && <span className="settings-tag">{tag}</span>}</span> : <StatusBadge state={state}/>}{action}</div>;
}

/** Provider confirmations use the same padded secondary buttons as the rest of Settings. */
export function ProviderConfirm({label, question, disabled, onConfirm}: {label: string; question: string; disabled?: boolean; onConfirm: () => Promise<void>}) {
 const {t} = useTranslation("settings");
 return <SwitchMethod label={label} question={question} confirm={t("confirm.action", {action: label})} disabled={disabled} onConfirm={onConfirm}/>;
}

/** A secondary action with an inline confirmation for a connection change. */
export function SwitchMethod({label, question, confirm, disabled, onConfirm}: {label: string; question: string; confirm: string; disabled?: boolean; onConfirm: () => Promise<void>}) {
 const {t} = useTranslation("settings");
 const [asking, setAsking] = useState(false);
 const trigger = useRef<HTMLButtonElement>(null);
 const wasAsking = useRef(false);
 useEffect(() => { if (!asking && wasAsking.current) trigger.current?.focus(); wasAsking.current = asking; }, [asking]);
 const [pending, setPending] = useState(false);
 const [error, setError] = useState("");
 async function run() {
  setPending(true); setError("");
  try { await onConfirm(); setAsking(false); }
  catch (cause) { setError(cause instanceof Error ? cause.message : t("action.failed")); }
  finally { setPending(false); }
 }
 if (!asking) return <button ref={trigger} type="button" className="st-button" disabled={disabled} onClick={() => setAsking(true)}>{label}</button>;
 return <span className="inline-confirmation" role="group" aria-label={question} onKeyDown={event => { if (event.key === "Escape") { event.stopPropagation(); if (!pending) setAsking(false); } }}>
  <span>{question}</span>
  <button type="button" className="st-button" disabled={disabled || pending} onClick={() => void run()}>{pending ? t("action.processing") : confirm}</button>
  <button type="button" className="st-button" disabled={disabled || pending} autoFocus onClick={() => setAsking(false)}>{t("action.cancel")}</button>
  {error && <span className="error-text" role="alert">{error}</span>}
 </span>;
}

