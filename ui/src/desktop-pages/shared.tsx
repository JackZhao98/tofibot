import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "../i18n";
import { Banner } from "../settings/components";

export const detail = (cause: unknown) => String((cause as {message?: unknown})?.message || cause || "").slice(0, 2000);
export function PageShell({ title, children, back = false }: {title: string; children: ReactNode; back?: boolean}) {
  const { t } = useTranslation("settings");
  useEffect(() => { document.title = `${title} · Tofi`; }, [title]);
  return <div className="workspace mac-settings desktop-page"><div className="mac-settings-titlebar" aria-hidden="true" />
    <main className="settings-page-body"><div className="mac-settings-inner"><header className="mac-settings-head"><h1>{title}</h1>
      {back && <a className="st-button mac-settings-back" href="/__desktop/settings">{t("desktop_pages.back")}</a>}</header>{children}</div></main></div>;
}
export function usePageState<T>(url: string) {
  const [state, setState] = useState<T | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    void fetch(url, {signal: controller.signal, cache: "no-store", credentials: "same-origin"}).then(response => {
      if (!response.ok) throw new Error(String(response.status));
      return response.json() as Promise<T>;
    }).then(setState).catch(cause => { if (!controller.signal.aborted) setError(detail(cause)); });
    return () => controller.abort();
  }, [url]);
  return {state, error};
}
export function StateNotice({error}: {error: string}) {
  const {t} = useTranslation("settings");
  return error ? <Banner tone="error" title={t("desktop_pages.load_failed", {message: error})} /> : <p role="status">{t("mac.loading")}</p>;
}
