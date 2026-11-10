import {useCallback, useEffect, useRef, useState, type FormEvent} from "react";
import {ApiError, request} from "./api";
import {ComputerAttach} from "./ComputerAttach";
import {ComputerResources} from "./ComputerResources";
import {CopyFeedbackIcon} from "./CopyFeedbackIcon";
import {useTranslation} from "./i18n";
import {useOwnerSession} from "./OwnerSession";
import {ConfirmDialog, ModalDialog} from "./overlays/ModalDialog";
import "./computer-resources.css";
import "./environments.css";

/**
 * The dialogs behind Settings > Environments. Lazy with the Settings panel: only this file (and ComputerResources/ComputerAttach)
 * reaches the Radix dialog, never the entry bundle. Every dialog reuses the Bot computer restart dialog's markup and classes
 * (`resource-restart-*`), so it looks like the confirmations the person already knows.
 */
const E2B_KEYS_URL = "https://e2b.dev/dashboard?tab=keys";

/** The dialogs sit inside the page body, so the settings styles that scope to `.settings-page-body` still reach them. */
const pageContainer = () => document.querySelector<HTMLElement>('.settings-content [data-page="computer"]');

const knownErrors = ["invalid_key", "e2b_unreachable", "e2b_limit", "image_missing", "template_build_failed", "sandbox_failed", "guest_unreachable", "sandbox_gone", "token_unreadable", "org_resource_missing", "not_ready", "guest_outdated_required", "computer_upgrade_failed", "not_outdated", "computer_busy"] as const;
export const reasonKey = (code: string) => (knownErrors as readonly string[]).includes(code) ? code : "generic";

/** Copy button with the same glyph the rest of the app uses; clipboard failures stay silent (the text is selectable). */
function CopyButton({text, label}: {text: string; label: string}) {
  const {t} = useTranslation("computer");
  const [copied, setCopied] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  return <button type="button" className="secondary-button env-copy" aria-label={label} onClick={() => {
    void navigator.clipboard?.writeText(text).then(() => { setCopied(true); window.clearTimeout(timer.current); timer.current = window.setTimeout(() => setCopied(false), 1600); }, () => {});
  }}><CopyFeedbackIcon copied={copied} size={16}/>{copied ? t("environments.copied") : t("environments.copy")}</button>;
}

/** Add an E2B cloud computer: paste a key. The server checks it and starts preparing the computer in the background. */
export function E2BDialog({onClose, onSaved}: {onClose: () => void; onSaved: (body: unknown) => void}) {
  const {t} = useTranslation("computer");
  const [key, setKey] = useState("");
  const [template, setTemplate] = useState("");
  const [showTemplate, setShowTemplate] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const field = useRef<HTMLInputElement>(null);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (busy || !key.trim()) return;
    setBusy(true); setError("");
    try {
      const body = await request<unknown>("/api/environments/e2b", {method: "PUT", body: JSON.stringify(template.trim() ? {api_key: key.trim(), template_id: template.trim()} : {api_key: key.trim()})});
      if (!alive.current) return;
      onSaved(body);
    } catch (cause) {
      if (alive.current) { setError(t(`environments.error.${reasonKey(cause instanceof ApiError ? cause.code ?? "" : "")}` as "environments.error.generic")); setBusy(false); field.current?.focus(); }
    }
  }
  return <ModalDialog onClose={onClose} locked={busy} initialFocus={field} overlayClassName="resource-restart-overlay" container={pageContainer()}>
    <form className="resource-restart-dialog env-dialog" aria-labelledby="env-e2b-title" aria-describedby="env-e2b-cost" onSubmit={event => void submit(event)} noValidate>
      <h3 id="env-e2b-title">{t("environments.e2b.title")}</h3>
      <div className="env-field-row"><label className="env-field" htmlFor="env-e2b-key">{t("environments.e2b.key")}</label><a className="env-field-link" href={E2B_KEYS_URL} target="_blank" rel="noopener noreferrer">{t("environments.e2b.get_key")}</a></div>
      <input ref={field} id="env-e2b-key" name="e2b-api-key" type="password" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder="e2b_…" value={key} disabled={busy} aria-invalid={error ? true : undefined} aria-describedby={error ? "env-e2b-error" : undefined} onChange={event => { setKey(event.target.value); if (error) setError(""); }}/>
      {error && <p id="env-e2b-error" className="env-dialog-error" role="alert">{error}</p>}
      <p id="env-e2b-cost" className="env-dialog-note">{t("environments.e2b.cost")}</p>
      {showTemplate ? <>
        <label className="env-field" htmlFor="env-e2b-template">{t("environments.e2b.template_id")}</label>
        <input id="env-e2b-template" name="e2b-template-id" type="text" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" value={template} disabled={busy} aria-describedby="env-e2b-template-hint" onChange={event => setTemplate(event.target.value)}/>
        <p id="env-e2b-template-hint" className="env-dialog-note">{t("environments.e2b.template_hint")}</p>
      </> : <button type="button" className="text-button env-template-toggle" aria-expanded={false} onClick={() => setShowTemplate(true)}>{t("environments.e2b.template_toggle")}</button>}
      <div className="resource-restart-actions"><button type="button" className="secondary-button" disabled={busy} onClick={onClose}>{t("environments.cancel")}</button><button type="submit" className="primary-button" disabled={busy || !key.trim()}>{busy ? t("environments.e2b.saving") : t("environments.save")}</button></div>
    </form>
  </ModalDialog>;
}

/** Bots the person denied on this Mac before (a local veto). Allowing one again asks in a native macOS dialog, so this page cannot lift it alone. */
export function DeniedBotsDialog({bots, onUndo, onClose}: {bots: {bot_id: string; bot_name: string}[]; onUndo: (botID: string) => void; onClose: () => void}) {
  const {t} = useTranslation("computer");
  const close = useRef<HTMLButtonElement>(null);
  return <ModalDialog onClose={onClose} initialFocus={close} overlayClassName="resource-restart-overlay" container={pageContainer()}>
    <section className="resource-restart-dialog env-dialog" aria-labelledby="env-denied-title" aria-describedby="env-denied-body">
      <h3 id="env-denied-title">{t("environments.rc.denied.title")}</h3>
      <p id="env-denied-body" className="env-dialog-note">{t("environments.rc.denied.body")}</p>
      {bots.length === 0 ? <p className="env-dialog-note" role="status">{t("environments.rc.denied.empty")}</p> : <ul className="rc-denied-list">
        {bots.map(bot => <li key={bot.bot_id} data-bot={bot.bot_id}><span>{bot.bot_name || t("environments.rc.denied.unnamed")}</span><button type="button" className="rc-action" onClick={() => onUndo(bot.bot_id)}>{t("environments.rc.denied.allow")}</button></li>)}
      </ul>}
      <div className="resource-restart-actions"><button ref={close} type="button" className="secondary-button" onClick={onClose}>{t("environments.close")}</button></div>
    </section>
  </ModalDialog>;
}

/** Add the Bot computer on this server: the one command the host runs. The page picks the computer up by itself (it reads the list while this is open). */
export function VmCommandDialog({command, onClose}: {command: string; onClose: () => void}) {
  const {t} = useTranslation("computer");
  const close = useRef<HTMLButtonElement>(null);
  return <ModalDialog onClose={onClose} initialFocus={close} overlayClassName="resource-restart-overlay" container={pageContainer()}>
    <section className="resource-restart-dialog env-dialog" aria-labelledby="env-vm-title" aria-describedby="env-vm-after">
      <h3 id="env-vm-title">{t("environments.vm.title")}</h3>
      <p className="env-dialog-note">{t("environments.vm.run")}</p>
      <div className="env-code-row"><code className="env-command" data-computer-command="">{command}</code><CopyButton text={command} label={t("environments.vm.copy_aria")}/></div>
      <p id="env-vm-after" className="env-dialog-note">{t("environments.vm.after")}</p>
      <div className="resource-restart-actions"><button ref={close} type="button" className="primary-button" onClick={onClose}>{t("environments.close")}</button></div>
    </section>
  </ModalDialog>;
}

/** The VM's size and attach controls, moved off the page. `ComputerResources` is the same component the page used to render. */
export function ConfigureDialog({onClose, onOpenAdmin, provided = false}: {onClose: () => void; onOpenAdmin: () => void; provided?: boolean}) {
  const {t} = useTranslation("computer");
  const session = useOwnerSession();
  const close = useRef<HTMLButtonElement>(null);
  const isAdmin = Boolean(session?.multi_account && session.owner?.role === "admin");
  const selfId = session?.owner?.id;
  return <ModalDialog onClose={onClose} initialFocus={close} overlayClassName="resource-restart-overlay" container={pageContainer()}>
    <section className="resource-restart-dialog env-dialog is-wide" aria-labelledby="env-config-title">
      <h3 id="env-config-title">{t(provided ? "environments.configure.provided_title" : "environments.configure.title")}</h3>
      <div className="env-config-body">
        <ComputerResources bare onOpenAdmin={() => { onClose(); onOpenAdmin(); }}/>
        {isAdmin && selfId && !provided && <ComputerAttach base={`/api/admin/accounts/${encodeURIComponent(selfId)}/computer`}/>}
      </div>
      <div className="resource-restart-actions"><button ref={close} type="button" className="primary-button" onClick={onClose}>{t("environments.done")}</button></div>
    </section>
  </ModalDialog>;
}

export type ConfirmSpec = {title: string; body: string; action: string; /** A destructive confirmation: the action button takes the danger tone. */ danger?: boolean};

/** A confirmation with one named effect. Cancel is focused first; a press outside never confirms. */
export function ActionConfirm({spec, busy, onCancel, onConfirm}: {spec: ConfirmSpec; busy: boolean; onCancel: () => void; onConfirm: () => void}) {
  const {t} = useTranslation("computer");
  const cancel = useRef<HTMLButtonElement>(null);
  const confirm = useCallback(() => { if (!busy) onConfirm(); }, [busy, onConfirm]);
  return <ConfirmDialog onClose={onCancel} locked={busy} initialFocus={cancel} overlayClassName="resource-restart-overlay" container={pageContainer()}>
    <div className="resource-restart-dialog env-dialog" aria-labelledby="env-confirm-title" aria-describedby="env-confirm-body">
      <h3 id="env-confirm-title">{spec.title}</h3>
      <p id="env-confirm-body">{spec.body}</p>
      <div className="resource-restart-actions"><button ref={cancel} type="button" className="secondary-button" disabled={busy} onClick={onCancel}>{t("environments.cancel")}</button><button type="button" className={spec.danger ? "env-danger-button" : "primary-button"} disabled={busy} onClick={confirm}>{spec.action}</button></div>
    </div>
  </ConfirmDialog>;
}
