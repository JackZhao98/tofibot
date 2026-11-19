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

type Bridge = NonNullable<NonNullable<Window["tofiDesktop"]>["notifications"]>;

/** The Mac app's notifier state and its three switches (null until the first read). Only a Mac app with the bridge has one. */
function useDeviceNotifier(bridge: Bridge | undefined) {
  const [status, setStatus] = useState<DesktopNotifierStatus | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    if (!bridge) return;
    let alive = true;
    bridge.status().then(next => { if (alive) setStatus(next); }, () => { if (alive) setFailed(true); });
    const off = bridge.onStatus(next => { if (alive) setStatus(next); });
    return () => { alive = false; off(); };
  }, [bridge]);
  async function update(patch: Partial<DesktopNotifierSettings>) {
    setFailed(false);
    try { if (bridge) setStatus(await bridge.update(patch)); return true; } catch { setFailed(true); return false; }
  }
  return {status, failed, update};
}

/** Settings > General > Chats: what the chat list shows, the notifications (folded) and the server address (folded). Each switch applies at once and is stored on the account. */
export function ChatsSettings() {
  const {t} = useTranslation(["settings", "chat"]);
  const preference = useUserTimezone();
  const [busy, setBusy] = useState<NotifyKey | null>(null);
  const [failed, setFailed] = useState(false);
  const [permission, setPermission] = useState<string>(() => typeof window !== "undefined" && "Notification" in window ? Notification.permission : "unsupported");
  const bridge = typeof window !== "undefined" ? window.tofiDesktop?.notifications : undefined;
  const notifier = useBrowserNotifierState();
  const device = useDeviceNotifier(bridge);
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
  const anyOn = rows.some(row => row.on);
  // A browser without the Mac app: its own permission. A Mac app reports its own state.
  const blocked = !bridge && anyOn && permission !== "granted" && permission !== "default";
  const unsupported = !bridge && notifier === "server_unsupported";
  const deviceState = device.status?.state;
  const deviceProblem = Boolean(bridge && device.status && deviceState !== "on" && (device.status.settings.enabled || deviceState === "blocked_by_system"));
  const problem = blocked || unsupported || deviceProblem;
  // The summary counts every switch that decides whether something arrives: the four of the account, and "on this Mac" with the app.
  const total = rows.length + (bridge ? 1 : 0);
  const onCount = rows.filter(row => row.on).length + (bridge && device.status?.settings.enabled ? 1 : 0);
  // Nothing is claimed before the preferences (and, on the Mac, the app's state) have arrived.
  const loaded = preference.ready && (!bridge || device.status !== null);
  const summary = !loaded ? undefined : problem ? t("settings:chats.summary_attention") : onCount === total ? t("settings:chats.summary_all_on") : onCount === 0 ? t("settings:chats.summary_all_off") : t("settings:chats.summary_some", {on: onCount, total});
  const deviceText = !device.status ? t("settings:chats.device_status_unknown")
    : deviceState === "offline" && device.status.lastSuccessAt ? t("settings:chats.device_status_offline_since", {time: new Date(device.status.lastSuccessAt).toLocaleTimeString([], {hour: "2-digit", minute: "2-digit"})})
    : t(`settings:chats.device_status_${deviceState}`, {defaultValue: t("settings:chats.device_status_unknown")});
  return <SettingsSection title={t("settings:general.chat")}>
    <ShowActivitySetting/>
    <ExpandRow label={t("settings:chats.notifications")} summary={summary} attention={problem} reveal={{tab: "general", section: "chats"}} data-setting="notifications">
      {rows.map(row => <SettingsRow key={row.kind} label={row.label} data-setting={`notify-${row.kind}`}><SettingsSwitch label={row.label} checked={row.on} disabled={busy !== null || !preference.ready} busy={busy === row.kind} onChange={on => change(row.kind, on)}/></SettingsRow>)}
      {failed && <p className="gen-note gen-note--danger" role="alert">{t("settings:general.activity_save_failed")}</p>}
      {bridge && <DeviceNotifications status={device.status} failed={device.failed} update={device.update} text={deviceText}/>}
    </ExpandRow>
    {/* A problem is said outside the fold: the person must not have to open the row to learn nothing arrives. */}
    {unsupported && <p className="gen-note gen-note--danger" role="status" data-setting="notify-server-unsupported">{t("settings:chats.server_unsupported")}</p>}
    {blocked && <p className="gen-note gen-note--danger" role="status" data-setting="notify-blocked">{permission === "unsupported" ? t("chat:notifications.unsupported") : t("chat:notifications.off")}</p>}
    {deviceProblem && <p className="gen-note gen-note--danger" role="status" data-setting="notify-device-attention">{deviceText}</p>}
    <ServerAddressRow/>
  </SettingsSection>;
}

const KNOWN_REASONS = ["connecting", "too_many_waiters", "wait_ignored", "cursor_stalled", "bad_response", "session_expired", "notifications_unsupported"] as const;

/** "This device": the Mac app's notifier state and its three switches, in the open Notifications row. */
function DeviceNotifications({status, failed, update, text}: {status: DesktopNotifierStatus | null; failed: boolean; update: (patch: Partial<DesktopNotifierSettings>) => Promise<boolean>; text: string}) {
  const {t} = useTranslation(["settings"]);
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
