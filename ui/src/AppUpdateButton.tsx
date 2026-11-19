import { useEffect, useState } from "react";
import type { AppUpdateStatus } from "./desktop";
import { Banner } from "./ds/Banner/Banner";
import { Button } from "./ds/Button/Button";
import { IconButton } from "./ds/IconButton/IconButton";
import { useTranslation } from "./i18n";

/** The native updater owns downloads and confirmation; the workspace only reflects its status, as a kit Banner (info) above the account
 *  rows, or as one icon button when the sidebar is the collapsed rail. */
export function AppUpdateButton({ compact = false }: { compact?: boolean }) {
  const bridge = window.tofiDesktop?.appUpdate;
  const { t } = useTranslation("auth");
  const [status, setStatus] = useState<AppUpdateStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!bridge) return;
    let active = true, received = false;
    const unsubscribe = bridge.onStatus(next => { received = true; if (active) { setStatus(next); setError(""); } });
    void bridge.status().then(next => { if (active && !received) setStatus(next); }, () => { if (active && !received) setError(t("app_update.status_failed")); });
    return () => { active = false; unsubscribe(); };
  }, [bridge, t]);
  const visible = status && ["available", "downloading", "downloaded"].includes(status.state);
  const ready = status?.state === "downloaded";
  const label = ready ? status.version ? t("app_update.restart", { version: status.version }) : t("app_update.restart_without_version") : t("app_update.downloading");
  const title = status?.version ? t("app_update.title", { version: status.version }) : t("app_update.title_plain");
  const note = error ? <span className="error-text" role="alert">{error}</span> : ready ? t("app_update.note_ready") : t("app_update.note_downloading");
  const install = () => {
    if (!ready || !bridge || busy) return;
    setBusy(true); setError("");
    void bridge.install().catch(() => setError(t("app_update.install_failed"))).finally(() => setBusy(false));
  };
  const state = status?.state;
  return <>
    {visible && (compact
      ? <IconButton className="sidebar-app-update" icon="download" label={label} data-state={state} aria-busy={busy || state === "downloading" || undefined} disabled={busy} onClick={install} />
      : <Banner className="sidebar-app-update-banner" tone="info" title={title}>{note}<span className="sidebar-app-update-action"><Button className="sidebar-app-update" variant="secondary" size="sm" icon="download" data-state={state} aria-label={label} data-hint={label} title={label} busy={busy || state === "downloading"} disabled={busy} onClick={install}>{t("app_update.action")}</Button></span></Banner>)}
    {(!visible || compact) && error && <span className="error-text" role="alert">{error}</span>}
  </>;
}
