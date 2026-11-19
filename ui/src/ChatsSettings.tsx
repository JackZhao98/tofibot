import {useEffect, useState} from "react";
import {useTranslation} from "./i18n";
import {useUserTimezone, type NotifyKey} from "./UserTimezone";
import {ShowActivitySetting} from "./TimezoneSettings";
import {SettingsRow} from "./ds/SettingsRow/SettingsRow";
import {SettingsSection} from "./ds/SettingsSection/SettingsSection";
import {SettingsSwitch} from "./settings/components";
import {ExpandRow} from "./settings/ExpandRow";
import {ServerAddressRow} from "./ConnectionInfo";
import {useBrowserNotifierState} from "./attentionNotifier";
import type {DesktopNotifierSettings, DesktopNotifierStatus} from "./desktop";
import "./settings/general.css";

/** Settings > General > Chats: what the chat list shows, the notifications (folded) and the server address (folded). Each switch applies at once and is stored on the account. */
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
    const ok = await preference.saveNotify(kind, on);
    if (!ok) setFailed(true);
    setBusy(null);
    return ok;
  }
  const rows: {kind: NotifyKey; label: string; on: boolean}[] = [
    {kind: "tasks", label: t("settings:chats.task_notifications"), on: preference.notifyTasks},
    {kind: "chats", label: t("settings:chats.chat_notifications"), on: preference.notifyChats},
    {kind: "needs_you", label: t("settings:chats.needs_you_notifications"), on: preference.notifyNeedsYou},
    {kind: "spend_alerts", label: t("settings:chats.spend_alert_notifications"), on: preference.notifySpendAlerts},
  ];
  const onCount = rows.filter(row => row.on).length;
  const anyOn = onCount > 0;
  const summary = onCount === rows.length ? t("settings:chats.summary_all_on") : onCount === 0 ? t("settings:chats.summary_all_off") : t("settings:chats.summary_some", {on: onCount, total: rows.length});
  // A browser without the Mac app: its own permission. A Mac app reports its own state below.
  const blocked = !bridge && anyOn && permission !== "granted" && permission !== "default";
  return <SettingsSection title={t("settings:general.chat")}>
    <ShowActivitySetting/>
    <ExpandRow label={t("settings:chats.notifications")} summary={summary} data-setting="notifications">
      {rows.map(row => <SettingsRow key={row.kind} label={row.label} data-setting={`notify-${row.kind}`}><SettingsSwitch label={row.label} checked={row.on} disabled={busy !== null || !preference.ready} busy={busy === row.kind} onChange={on => change(row.kind, on)}/></SettingsRow>)}
      {failed && <p className="gen-note gen-note--danger" role="alert">{t("settings:general.activity_save_failed")}</p>}
      {!bridge && notifier === "server_unsupported" && <p className="gen-note" role="status" data-setting="notify-server-unsupported">{t("settings:chats.server_unsupported")}</p>}
      {blocked && <p className="gen-note" role="status">{permission === "unsupported" ? t("chat:notifications.unsupported") : t("chat:notifications.off")}</p>}
      {bridge && <DeviceNotifications bridge={bridge}/>}
    </ExpandRow>
    <ServerAddressRow/>
  </SettingsSection>;
}

const KNOWN_REASONS = ["connecting", "too_many_waiters", "wait_ignored", "cursor_stalled", "bad_response", "session_expired", "notifications_unsupported"] as const;


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
    try { setStatus(await bridge.update(patch)); return true; } catch { setFailed(true); return false; }
  }
  const time = status?.lastSuccessAt ? new Date(status.lastSuccessAt).toLocaleTimeString([], {hour: "2-digit", minute: "2-digit"}) : "";
  const text = !status ? t("settings:chats.device_status_unknown")
    : status.state === "offline" && time ? t("settings:chats.device_status_offline_since", {time})
    : t(`settings:chats.device_status_${status.state}`, {defaultValue: t("settings:chats.device_status_unknown")});
  // Only reasons the Mac app is known to send are worded; any other value is left out.
  const reasonKey = KNOWN_REASONS.find(known => known === status?.reason);
  const switches: {key: keyof DesktopNotifierSettings; label: string}[] = [
    {key: "enabled", label: t("settings:chats.device_enabled")},
    {key: "content", label: t("settings:chats.device_content")},
    {key: "openAtLogin", label: t("settings:chats.device_login")},
  ];
  return <section aria-label={t("settings:chats.device_title")} data-setting="notify-device">
    <h3 className="gen-subhead">{t("settings:chats.device_title")}</h3>
    {switches.map(item => <SettingsRow key={item.key} label={item.label} data-setting={`notify-device-${item.key}`}><SettingsSwitch label={item.label} checked={status?.settings[item.key] ?? false} disabled={!status} onChange={on => update({[item.key]: on})}/></SettingsRow>)}
    <p className="gen-note" role="status" data-setting="notify-device-status" data-state={status?.state ?? "unknown"}>{text}</p>
    {reasonKey && <p className="gen-note" data-setting="notify-device-reason" data-reason={reasonKey}>{t(`settings:chats.device_reason_${reasonKey}`)}</p>}
    <p className="gen-note">{t("settings:chats.device_login_hint")}</p>
    {failed && <p className="gen-note gen-note--danger" role="alert">{t("settings:general.activity_save_failed")}</p>}
  </section>;
}
