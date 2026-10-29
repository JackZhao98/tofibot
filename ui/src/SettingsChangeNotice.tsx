import { useState } from "react";
import { ApiError, request } from "./api";
import { useTranslation } from "./i18n";
import type { Message } from "./types";

/** The name of a setting in the person's language, from the catalog key the server sent ("chat:botPanel.name"); empty when this build does not have it. */
export function useSettingLabel(labelKey: string | undefined): string {
  const { i18n } = useTranslation();
  return labelKey && i18n.exists(labelKey) ? (i18n.t as (key: string) => string)(labelKey) : "";
}

/**
 * The chat line a direct settings change leaves ("Timezone: A → B · Undo"). The server posts it as a system notice; the words are
 * put together here so they follow the person's language. A lazy chunk: the line is rare and stays out of the entry bundle.
 */
export default function SettingsChangeNotice({ notice, fallback, title }: { notice: NonNullable<Message["notice"]>; fallback: string; title?: string }) {
  const { t } = useTranslation("chat");
  const label = useSettingLabel(notice.label_key);
  const [state, setState] = useState<"idle" | "busy" | "undone" | "failed" | "stale">("idle");
  const undone = state === "undone" || notice.reverted;
  async function undo() {
    if (!notice.change_id || state === "busy") return;
    setState("busy");
    try {
      await request(`/api/settings-changes/${encodeURIComponent(notice.change_id)}/revert`, { method: "POST" });
      setState("undone");
    } catch (cause) {
      setState(cause instanceof ApiError && cause.code === "settings_changed_since" ? "stale" : "failed");
    }
  }
  const line = label ? t("message.settings_change", { label, old: notice.old_value || t("message.settings_change_empty"), new: notice.new_value || t("message.settings_change_empty") }) : fallback;
  return <article className="message message-notice" title={title}><div data-settings-change={notice.change_id}>
    <span>{line}</span>
    {undone ? <span className="settings-change-state"> · {t("message.settings_change_undone")}</span>
      : notice.revertable && notice.change_id ? <> · <button type="button" disabled={state === "busy"} onClick={() => void undo()}>{t("message.settings_change_undo")}</button></>
      : null}
    {state === "failed" && <span role="alert" className="settings-change-state"> · {t("message.settings_change_undo_failed")}</span>}
    {state === "stale" && <span role="alert" className="settings-change-state"> · {t("message.settings_change_stale")}</span>}
  </div></article>;
}
