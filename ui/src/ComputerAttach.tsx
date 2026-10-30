import {useCallback, useEffect, useRef, useState, type ReactNode} from "react";
import {ApiError, request} from "./api";
import {useTranslation} from "./i18n";
import {ConfirmDialog} from "./overlays/ModalDialog";
import {SettingsRow} from "./settings/components";
import "./computer-resources.css";

/** The administrator's view of a server's VM: `GET .../computer/state`, and the answer to attach. */
type Summary = {attached: boolean; id?: string; kind: string; status: string; backend_available: boolean};
type DetachAnswer = {computer: Summary; detach?: {drained: boolean; interrupted: number; outbox: {synced: number; deferred: number; refused: number}}};
type Phase = "idle" | "attaching" | "draining" | "syncing";

/** Seconds a detach waits for running computer calls before it ends them. */
const DETACH_WAIT_SECONDS = 60;
/** The server answers once, after both steps; the card moves from "draining" to "syncing" on this timer. */
const DRAIN_SHOWN_MS = 2500;
const knownErrors = ["computer_backend_unavailable", "computer_attach_failed", "computer_detach_failed", "forbidden", "unavailable"] as const;

/**
 * Hot attach and detach of an account's VM computer (admin only). `base` is `/api/admin/accounts/{id}/computer`
 * on a gateway and `/api/admin/computer` on a single-account server; `running` says whether an attached computer is on.
 */
export function ComputerAttach({base, running, disabled, children}: {base: string; running?: boolean; disabled?: boolean; /** More rows of the same card (the size sliders). */ children?: ReactNode}) {
  const {t} = useTranslation("computer");
  const [summary, setSummary] = useState<Summary | null>(null);
  const [failed, setFailed] = useState(false);
  const [phase, setPhase] = useState<Phase>("idle");
  const [error, setError] = useState("");
  const [result, setResult] = useState("");
  const [confirming, setConfirming] = useState(false);
  const alive = useRef(true);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => { alive.current = true; return () => { alive.current = false; window.clearTimeout(timer.current); }; }, []);
  useEffect(() => {
    let active = true;
    setSummary(null); setFailed(false); setError(""); setResult(""); setPhase("idle");
    request<Summary>(`${base}/state`).then(next => { if (active) setSummary(next); }).catch(() => { if (active) setFailed(true); });
    return () => { active = false; };
  }, [base]);
  const words = useCallback((cause: unknown) => cause instanceof ApiError && cause.code && (knownErrors as readonly string[]).includes(cause.code) ? t(`attach.error.${cause.code}` as "attach.error.generic") : t("attach.error.generic"), [t]);

  if (failed) return <div className="settings-card computer-attach" data-testid="computer-attach"><p className="settings-card-note" role="alert">{t("attach.error.read")}</p></div>;
  if (!summary) return <div className="settings-card computer-attach" data-testid="computer-attach"><p className="settings-card-note" role="status">{t("attach.loading")}</p></div>;

  const on = summary.status === "attached" || summary.status === "lost" || summary.status === "detaching";
  const stateKey = summary.status === "attached" ? (running ? "attached_running" : "attached_stopped")
    : summary.status === "lost" ? "lost" : summary.status === "detaching" ? "detaching"
    : summary.backend_available || summary.id ? "detached" : "none";
  const busy = phase !== "idle";

  async function attach() {
    setError(""); setResult(""); setPhase("attaching");
    try { const next = await request<Summary>(`${base}/attach`, {method: "POST"}); if (!alive.current) return; setSummary(next); setResult(t("attach.done.attach")); }
    catch (cause) { if (alive.current) setError(words(cause)); }
    finally { if (alive.current) setPhase("idle"); }
  }
  async function detach() {
    setConfirming(false); setError(""); setResult(""); setPhase("draining");
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => { if (alive.current) setPhase(current => current === "draining" ? "syncing" : current); }, DRAIN_SHOWN_MS);
    try {
      const answer = await request<DetachAnswer>(`${base}/detach`, {method: "POST", body: JSON.stringify({wait_seconds: DETACH_WAIT_SECONDS})});
      if (!alive.current) return;
      setSummary(answer.computer);
      const report = answer.detach;
      const parts = [t("attach.done.detach")];
      if (report) {
        parts.push(t("attach.done.copied", {count: report.outbox.synced}));
        if (report.interrupted > 0) parts.push(t("attach.done.interrupted", {count: report.interrupted}));
        if (report.outbox.deferred + report.outbox.refused > 0) parts.push(t("attach.done.left", {count: report.outbox.deferred + report.outbox.refused}));
      }
      setResult(parts.join(" "));
    } catch (cause) { if (alive.current) setError(words(cause)); }
    finally { window.clearTimeout(timer.current); if (alive.current) setPhase("idle"); }
  }

  const tone = stateKey === "attached_running" ? "on" : stateKey === "lost" || stateKey === "detaching" ? "warn" : "off";
  const action = stateKey === "none" ? null : on
    ? <button type="button" className="secondary-button is-danger" disabled={disabled || busy} onClick={() => setConfirming(true)}>{t("attach.action.detach")}</button>
    : <button type="button" className="secondary-button" disabled={disabled || busy} onClick={() => void attach()}>{t("attach.action.attach")}</button>;
  return <div className="settings-card computer-attach" data-testid="computer-attach">
    <SettingsRow inline label={<span className="computer-status" data-tone={tone} data-testid="computer-attach-state"><span className="computer-status-dot" aria-hidden="true"/>{t(`attach.state.${stateKey}` as "attach.state.none")}</span>}
      description={t(`attach.help.${on ? "attached" : stateKey === "none" ? "none" : "detached"}` as "attach.help.none")} control={action}/>
    {phase !== "idle" && <p className="settings-card-note" role="status">{t(`attach.progress.${phase}` as "attach.progress.attaching")}</p>}
    {phase === "idle" && result && <p className="settings-card-note" role="status">{result}</p>}
    {error && <p className="settings-card-note" role="alert">{error}</p>}
    {children}
    {confirming && <ConfirmDialog onClose={() => setConfirming(false)} overlayClassName="resource-restart-overlay"><div className="resource-restart-dialog" aria-labelledby="attach-detach-title" aria-describedby="attach-detach-impact">
      <h3 id="attach-detach-title">{t("attach.confirm.title")}</h3>
      <p id="attach-detach-impact">{t("attach.confirm.impact")}</p>
      <div className="resource-restart-actions">
        <button type="button" className="secondary-button" onClick={() => setConfirming(false)}>{t("attach.confirm.cancel")}</button>
        <button type="button" className="primary-button" onClick={() => void detach()}>{t("attach.confirm.action")}</button>
      </div>
    </div></ConfirmDialog>}
  </div>;
}
