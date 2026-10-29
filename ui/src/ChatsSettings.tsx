import {useState} from "react";
import {useTranslation} from "./i18n";
import {ShowActivitySetting, useUserTimezone} from "./UserTimezone";
import {SettingsCard} from "./settings/components";

type Kind = "chats" | "tasks";

/** Settings > General > Chats: what the chat list and the browser show. Each switch applies at once and is stored on the account. */
export function ChatsSettings() {
  const {t} = useTranslation(["settings", "chat"]);
  const preference = useUserTimezone();
  const [busy, setBusy] = useState<Kind | null>(null);
  const [failed, setFailed] = useState(false);
  const [permission, setPermission] = useState<string>(() => typeof window !== "undefined" && "Notification" in window ? Notification.permission : "unsupported");
  async function change(kind: Kind, on: boolean) {
    setBusy(kind); setFailed(false);
    // The browser asks once; a refusal is shown below the switches.
    if (on && "Notification" in window && Notification.permission === "default") setPermission(await Notification.requestPermission());
    if (!await preference.saveNotify(kind, on)) setFailed(true);
    setBusy(null);
  }
  const rows: {kind: Kind; label: string; on: boolean}[] = [
    {kind: "tasks", label: t("settings:chats.task_notifications"), on: preference.notifyTasks},
    {kind: "chats", label: t("settings:chats.chat_notifications"), on: preference.notifyChats},
  ];
  const blocked = (preference.notifyChats || preference.notifyTasks) && permission !== "granted" && permission !== "default";
  return <>
    <SettingsCard>
      <ShowActivitySetting/>
      {rows.map(row => <label key={row.kind} className="debug-toggle" data-setting={`notify-${row.kind}`}><span>{row.label}</span><input type="checkbox" role="switch" checked={row.on} disabled={busy !== null || !preference.ready} onChange={event => void change(row.kind, event.target.checked)}/></label>)}
    </SettingsCard>
    {failed && <p className="timezone-error" role="alert">{t("settings:general.activity_save_failed")}</p>}
    {blocked && <p className="field-note" role="status">{permission === "unsupported" ? t("chat:notifications.unsupported") : t("chat:notifications.off")}</p>}
  </>;
}
