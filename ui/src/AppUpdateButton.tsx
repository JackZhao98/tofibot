import { useEffect, useState } from "react";
import type { AppUpdateStatus } from "./desktop";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";

/** The native updater owns downloads and confirmation; the workspace only reflects its status. */
export function AppUpdateButton() {
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
  return <>
    {visible && <button className="sidebar-account-settings sidebar-app-update" data-state={status.state} aria-label={label} data-hint={label} title={label} aria-busy={busy || status.state === "downloading" || undefined} disabled={busy} onClick={() => {
      if (!ready || !bridge || busy) return;
      setBusy(true); setError("");
      void bridge.install().catch(() => setError(t("app_update.install_failed"))).finally(() => setBusy(false));
    }}><TofiIcon name="download" size={18}/></button>}
    {error && <span className="sidebar-app-update-error error-text" role="alert">{error}</span>}
  </>;
}
