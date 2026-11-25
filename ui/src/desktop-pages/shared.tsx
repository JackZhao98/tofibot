import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "../i18n";
import { Banner } from "../settings/components";
import { LinkButton } from "./LinkButton";
import { MacFrame } from "./MacFrame";

export { LinkButton };
export const detail = (cause: unknown) => String((cause as {message?: unknown})?.message || cause || "").slice(0, 2000);
export function PageShell({ title, children, back = false }: {title: string; children: ReactNode; back?: boolean}) {
  const { t } = useTranslation("settings");
  useEffect(() => { document.title = `${title} · Tofi`; }, [title]);
  return <MacFrame title={title} back={back ? <LinkButton className="mac-settings-back" href="/__desktop/settings">{t("desktop_pages.back")}</LinkButton> : undefined}>{children}</MacFrame>;
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
