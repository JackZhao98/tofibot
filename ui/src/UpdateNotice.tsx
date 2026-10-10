import { useCallback, useEffect, useState } from "react";
import "./UpdateNotice.css";
import { request } from "./api";
import { Trans, useTranslation } from "./i18n";
import { formatDateTime } from "./i18n/format";
import { TofiIcon } from "./icons";
import { useOwnerSession } from "./OwnerSession";
import { useUserTimezone } from "./UserTimezone";
import { SettingsCard, SettingsRow, StatusBadge } from "./settings/components";

export type SystemUpdate = { current: string; latest: string; update_available: boolean; notes_url: string; checked_at: string; auto_update: "patch" | "off" };

const dismissKey = "tofi.update-notice.dismissed";
const versionPattern = /^v(\d+)\.(\d+)\.(\d+)(?:-rc\.(\d+))?$/;

/** The server also enforces this: multi-account mode needs an Admin, single-owner mode the signed-in owner. */
export function useIsUpdateAdmin(): boolean {
  const session = useOwnerSession();
  if (!session) return false;
  if (session.multi_account) return session.owner?.role === "admin";
  return !session.enabled || session.authenticated;
}

/** A fix release: same major.minor, higher patch, and not a release candidate. This is what TOFI_AUTO_UPDATE=patch installs. */
export function isPatchBump(current: string, latest: string): boolean {
  const a = versionPattern.exec(current), b = versionPattern.exec(latest);
  return Boolean(a && b && !b[4] && a[1] === b[1] && a[2] === b[2] && Number(b[3]) > Number(a[3]));
}

function readDismissed(): string {
  try { return window.localStorage.getItem(dismissKey) ?? ""; } catch { return ""; }
}

/** Reads /api/system/update. The server answers from its cache, so a first read can be empty; ask again shortly. */
export function useSystemUpdate(enabled: boolean): SystemUpdate | null {
  const [info, setInfo] = useState<SystemUpdate | null>(null);
  useEffect(() => {
    if (!enabled) { setInfo(null); return; }
    let disposed = false;
    let timer = 0;
    const load = (attempt: number) => {
      request<SystemUpdate>("/api/system/update", { cache: "no-store" }).then(value => {
        if (disposed) return;
        setInfo(value);
        if (!value.checked_at && attempt < 4) timer = window.setTimeout(() => load(attempt + 1), 3000);
      }).catch(() => {});
    };
    load(0);
    return () => { disposed = true; window.clearTimeout(timer); };
  }, [enabled]);
  return info;
}

function safeNotes(url: string): string {
  return url.startsWith("https://") ? url : "";
}

/** Dismissible strip above the workspace. Dismissal is remembered per version. */
export function UpdateBanner() {
  const { t } = useTranslation("settings");
  const admin = useIsUpdateAdmin();
  const info = useSystemUpdate(admin);
  const [dismissed, setDismissed] = useState(readDismissed);
  const dismiss = useCallback((version: string) => {
    try { window.localStorage.setItem(dismissKey, version); } catch { /* storage unavailable: hide for this page view only */ }
    setDismissed(version);
  }, []);
  if (!admin || !info?.update_available || dismissed === info.latest) return null;
  const notes = safeNotes(info.notes_url);
  const automatic = info.auto_update === "patch" && isPatchBump(info.current, info.latest);
  return <div className="update-banner" role="status" data-update-banner>
    <TofiIcon name="info" size={16} aria-hidden="true" />
    <p>{automatic
      ? t("update.banner.automatic", { version: info.latest })
      : <Trans t={t} i18nKey="update.banner.available" values={{ version: info.latest }} components={{ code: <code /> }} />}</p>
    {notes && <a className="update-banner-link" href={notes} target="_blank" rel="noopener noreferrer">{t("update.banner.whats_new")}</a>}
    <button type="button" className="update-banner-dismiss" aria-label={t("update.banner.dismiss")} onClick={() => dismiss(info.latest)}><TofiIcon name="close" size={16} /></button>
  </div>;
}

/** Settings -> Advanced -> Version. Auto-update is toggled on the server, so the card shows the command. */
export function VersionCard() {
  const { t } = useTranslation("settings");
  const { timezone } = useUserTimezone();
  const admin = useIsUpdateAdmin();
  const info = useSystemUpdate(admin);
  if (!admin) return null;
  const known = Boolean(info?.latest);
  const notes = info ? safeNotes(info.notes_url) : "";
  const status = !info || !known ? null : info.update_available ? <StatusBadge state="need">{t("update.card.status_available")}</StatusBadge> : <StatusBadge state="ok">{t("update.card.status_current")}</StatusBadge>;
  return <SettingsCard className="version-card">
    <SettingsRow label={t("update.card.current")} control={<span className="version-value"><code>{info?.current ?? "-"}</code>{status}</span>} />
    <SettingsRow label={t("update.card.latest")} description={info?.update_available ? <Trans t={t} i18nKey="update.card.install_hint" components={{ code: <code /> }} /> : undefined}
      control={<span className="version-value">{known ? <code>{info!.latest}</code> : <span className="version-muted">{t("update.card.unknown")}</span>}{notes && <a href={notes} target="_blank" rel="noopener noreferrer">{t("update.card.notes")}</a>}</span>} />
    <SettingsRow label={t("update.card.checked")} control={<span className="version-value">{info?.checked_at ? formatDateTime(info.checked_at, { dateStyle: "medium", timeStyle: "short", ...(timezone ? { timeZone: timezone } : {}) }) : <span className="version-muted">{t("update.card.never")}</span>}</span>} />
    <SettingsRow label={t("update.card.auto")} description={<Trans t={t} i18nKey="update.card.auto_hint" components={{ on: <code />, off: <code /> }} />}
      control={<span className="version-value" data-auto-update={info?.auto_update ?? "off"}>{info?.auto_update === "patch" ? t("update.card.auto_on") : t("update.card.auto_off")}</span>} />
  </SettingsCard>;
}
