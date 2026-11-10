import {useEffect, useState} from "react";
import {useTranslation} from "./i18n";
import {useUserTimezone, type NotifyKey} from "./UserTimezone";
import {ShowActivitySetting} from "./TimezoneSettings";
import {SettingsCard} from "./settings/components";
import {useBrowserNotifierState} from "./attentionNotifier";
import type {DesktopNotifierSettings, DesktopNotifierStatus} from "./desktop";

/** Settings > General > Chats: what the chat list and notifications show. Each switch applies at once and is stored on the account. */
export function ChatsSettings() {
  const {t} = useTranslation(["settings", "chat"]);
  const preference = useUserTimezone();
  const [busy, setBusy] = useState<NotifyKey | null>(null);
  const [failed, setFailed] = useState(false);
  const [permission, setPermission] = useState<string>(() => typeof window !== "undefined" && "Notification" in window ? Notification.permission : "unsupported");
  const bridge = typeof window !== "undefined" ? window.tofiDesktop?.notifications : undefined;
  const notifier = useBrowserNotifierState();
  async function change(kind: NotifyKey, on: boolean) {
    setBusy(kind); setFailed(false);
    // The browser asks once; a refusal is shown below the switches. The Mac app asks macOS itself.
    if (on && !bridge && "Notification" in window && Notification.permission === "default") setPermission(await Notification.requestPermission());
    if (!await preference.saveNotify(kind, on)) setFailed(true);
    setBusy(null);
  }
  const rows: {kind: NotifyKey; label: string; on: boolean}[] = [
    {kind: "tasks", label: t("settings:chats.task_notifications"), on: preference.notifyTasks},
    {kind: "chats", label: t("settings:chats.chat_notifications"), on: preference.notifyChats},
    {kind: "needs_you", label: t("settings:chats.needs_you_notifications"), on: preference.notifyNeedsYou},
    {kind: "spend_alerts", label: t("settings:chats.spend_alert_notifications"), on: preference.notifySpendAlerts},
  ];
  const anyOn = preference.notifyChats || preference.notifyTasks || preference.notifyNeedsYou || preference.notifySpendAlerts;
  // A browser without the Mac app: its own permission. A Mac app reports its own state below.
  const blocked = !bridge && anyOn && permission !== "granted" && permission !== "default";
  return <>
    <SettingsCard>
      <ShowActivitySetting/>
      {rows.map(row => <label key={row.kind} className="debug-toggle" data-setting={`notify-${row.kind}`}><span>{row.label}</span><input type="checkbox" role="switch" checked={row.on} disabled={busy !== null || !preference.ready} onChange={event => void change(row.kind, event.target.checked)}/></label>)}
    </SettingsCard>
    {failed && <p className="timezone-error" role="alert">{t("settings:general.activity_save_failed")}</p>}
    {bridge && <DeviceNotifications bridge={bridge}/>}
    {!bridge && notifier === "server_unsupported" && <p className="field-note" role="status" data-setting="notify-server-unsupported">{t("settings:chats.server_unsupported")}</p>}
    {blocked && <p className="field-note" role="status">{permission === "unsupported" ? t("chat:notifications.unsupported") : t("chat:notifications.off")}</p>}
  </>;
}

/** "This device": the Mac app's notifier state and its three switches. Only a Mac app with the bridge renders this. */
function DeviceNotifications({bridge}: {bridge: NonNullable<Window["tofiDesktop"]>["notifications"] & object}) {
  const {t} = useTranslation(["settings"]);
  const [status, setStatus] = useState<DesktopNotifierStatus | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let alive = true;
    bridge.status().then(next => { if (alive) setStatus(next); }, () => { if (alive) setFailed(true); });
    const off = bridge.onStatus(next => { if (alive) setStatus(next); });
    return () => { alive = false; off(); };
  }, [bridge]);
  async function update(patch: Partial<DesktopNotifierSettings>) {
    setFailed(false);
    try { setStatus(await bridge.update(patch)); } catch { setFailed(true); }
  }
  const time = status?.lastSuccessAt ? new Date(status.lastSuccessAt).toLocaleTimeString([], {hour: "2-digit", minute: "2-digit"}) : "";
  const text = !status ? t("settings:chats.device_status_unknown")
    : status.state === "offline" && time ? t("settings:chats.device_status_offline_since", {time})
    : t(`settings:chats.device_status_${status.state}`, {defaultValue: t("settings:chats.device_status_unknown")});
  const switches: {key: keyof DesktopNotifierSettings; label: string}[] = [
    {key: "enabled", label: t("settings:chats.device_enabled")},
    {key: "content", label: t("settings:chats.device_content")},
    {key: "openAtLogin", label: t("settings:chats.device_login")},
  ];
  return <section className="device-notifications" aria-label={t("settings:chats.device_title")} data-setting="notify-device">
    <h3 className="field-note device-notifications-title">{t("settings:chats.device_title")}</h3>
    <SettingsCard>
      {switches.map(item => <label key={item.key} className="debug-toggle" data-setting={`notify-device-${item.key}`}><span>{item.label}</span><input type="checkbox" role="switch" checked={status?.settings[item.key] ?? false} disabled={!status} onChange={event => void update({[item.key]: event.target.checked})}/></label>)}
    </SettingsCard>
    <p className="field-note" role="status" data-setting="notify-device-status" data-state={status?.state ?? "unknown"}>{text}</p>
    <p className="field-note">{t("settings:chats.device_login_hint")}</p>
    {failed && <p className="timezone-error" role="alert">{t("settings:general.activity_save_failed")}</p>}
  </section>;
}
