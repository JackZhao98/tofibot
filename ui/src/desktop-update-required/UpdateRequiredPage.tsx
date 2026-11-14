import { useRef, useState } from "react";
import { useTranslation } from "../i18n";
import { SettingsCard, SettingsRow } from "../settings/components";
import { PageShell, StateNotice, usePageState } from "../desktop-pages/shared";

export type UpdateRequiredState = {reason: "server_too_old" | "app_too_old"; appVersion: string; uiContract: number; serverContract: number | null; minClient: string | null; serverURL: string; updaterAvailable: boolean};
export function UpdateRequiredPage() {
  const {t} = useTranslation("settings");
  const {state, error} = usePageState<UpdateRequiredState>("/__desktop/update-required/state");
  const lock = useRef(false);
  const [busy, setBusy] = useState(false);
  const [install, setInstall] = useState(false);
  const [status, setStatus] = useState("");
  async function update() {
    if (lock.current) return;
    lock.current = true; setBusy(true); setStatus(t(install ? "desktop_pages.update.restarting" : "desktop_pages.update.checking"));
    try {
      // appUpdate has no check/download method: retain the old same-origin check route.
      // New shells can use the existing native install confirmation; old shells keep the route.
      if (install && window.tofiDesktop?.appUpdate) {
        await window.tofiDesktop.appUpdate.install();
        setStatus("");
        return;
      }
      const response = await fetch("/__desktop/update", {method: "POST", headers: {"x-tofi-connection-info": "1", "content-type": "application/json"}, body: JSON.stringify({action: install ? "install" : "check"})});
      if (!response.ok) throw new Error();
      const result = await response.json();
      setStatus(typeof result.message === "string" ? result.message : "");
      if (result.state === "downloaded") setInstall(true);
    } catch { setStatus(t("desktop_pages.update.failed")); }
    finally { lock.current = false; setBusy(false); }
  }
  return <PageShell title={t("desktop_pages.update.title")}>
    {!state ? <StateNotice error={error}/> : <>
      <h2>{t(state.reason === "server_too_old" ? "desktop_pages.update.server_old" : "desktop_pages.update.app_old")}</h2>
      <p>{t(state.reason === "server_too_old" ? "desktop_pages.update.server_help" : "desktop_pages.update.app_help")}</p>
      <SettingsCard>{(["appVersion", "uiContract", "serverContract", "minClient"] as const).map(key => state[key] !== null && state[key] !== "" && <SettingsRow key={key} label={t(`desktop_pages.update.${key}`)} control={key === "minClient" ? t("desktop_pages.update.or_newer", {version: state[key]}) : String(state[key])}/>)}</SettingsCard>
      {state.reason === "app_too_old" && (state.updaterAvailable ? <><button className="primary-button" disabled={busy} onClick={() => void update()}>{t(install ? "desktop_pages.update.install" : "desktop_pages.update.update")}</button><p role="status">{status}</p></> : <p>{t("desktop_pages.update.manual")}</p>)}
      <div className="desktop-actions">{/^https?:\/\//.test(state.serverURL) && <a className="st-button" href={state.serverURL} target="_blank" rel="noreferrer">{t("desktop_pages.update.browser")}</a>}
      <a className="st-link" href="/__desktop/setup">{t("desktop_pages.update.change")}</a></div>
    </>}
  </PageShell>;
}
