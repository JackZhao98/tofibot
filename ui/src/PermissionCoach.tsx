import { TofiIcon } from "./icons";
import { useCallback, useEffect, useMemo, useState } from "react";
import type { DesktopPermissionKind, DesktopPermissionSnapshot } from "./desktop";
import "./permission-coach.css";
import { i18n, useTranslation } from "./i18n";

type Props = {
  permissions: DesktopPermissionKind[];
  onDismiss?: () => void;
  force?: boolean;
  open?: boolean;
};

function labels(kind: DesktopPermissionKind) {
  return { name: i18n.t(`settings:permissions.${kind}.name`), description: i18n.t(`settings:permissions.${kind}.description`), setting: i18n.t(`settings:permissions.${kind}.setting`) };
}

function isGranted(snapshot: DesktopPermissionSnapshot | null, kind: DesktopPermissionKind) {
  if (!snapshot) return false;
  return kind === "accessibility" ? snapshot.accessibility : snapshot[kind] === "granted";
}

function statusText(snapshot: DesktopPermissionSnapshot | null, kind: DesktopPermissionKind) {
  if (isGranted(snapshot, kind)) return i18n.t("settings:permissions.granted");
  if (!snapshot) return i18n.t("settings:permissions.checking");
  return kind === "accessibility" || snapshot[kind] === "denied" ? i18n.t("settings:permissions.needs_settings") : i18n.t("settings:permissions.waiting");
}

export function PermissionCoach({ permissions, onDismiss, force = false, open = false }: Props) {
  const { t } = useTranslation("settings");
  const desktop = window.tofiDesktop;
  const [snapshot, setSnapshot] = useState<DesktopPermissionSnapshot | null>(null);
  const [busy, setBusy] = useState<DesktopPermissionKind | null>(null);
  const [error, setError] = useState("");
  const [visible, setVisible] = useState(true);

  const refresh = useCallback(async () => {
    if (!desktop) return;
    try {
      setSnapshot(await desktop.getPermissionStatus());
      setError("");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : i18n.t("settings:permissions.read_failed"));
    }
  }, [desktop]);

  useEffect(() => {
    if (!desktop) return;
    void refresh();
    const timer = window.setInterval(() => void refresh(), 1800);
    const onFocus = () => void refresh();
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onFocus);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onFocus);
    };
  }, [desktop, refresh]);

  useEffect(() => {
    if (open) setVisible(true);
  }, [open]);

  const pending = useMemo(() => permissions.filter((kind) => !isGranted(snapshot, kind)), [permissions, snapshot]);
  if (!desktop || !visible || (!force && !open && snapshot && pending.length === 0)) return null;

  async function request(kind: DesktopPermissionKind) {
    if (!desktop) return;
    setBusy(kind);
    setError("");
    try {
      await desktop.requestPermission(kind);
      await refresh();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("permissions.request_failed"));
    } finally {
      setBusy(null);
    }
  }

  async function openSettings(kind: DesktopPermissionKind) {
    if (!desktop) return;
    setBusy(kind);
    setError("");
    try {
      await desktop.openPrivacySettings(kind);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("permissions.open_failed"));
    } finally {
      setBusy(null);
    }
  }

  return <aside className="permission-coach" role="dialog" aria-labelledby="permission-coach-title" aria-describedby="permission-coach-description">
    <div className="permission-coach-header">
      <div className="permission-coach-icon" aria-hidden="true"><TofiIcon name="shield-check" size={20} /></div>
      <div><h3 id="permission-coach-title">{t("permissions.title")}</h3><p id="permission-coach-description">{t("permissions.description")}</p></div>
      <button type="button" className="permission-coach-close" aria-label={t("permissions.later")} onClick={() => { setVisible(false); onDismiss?.(); }}><TofiIcon name="close" size={20} style={{ verticalAlign: "middle" }} /></button>
    </div>
    <div className="permission-coach-list">
      {permissions.map((kind) => {
        const definition = labels(kind);
        const granted = isGranted(snapshot, kind);
        const showDrop = kind === "screen" || kind === "accessibility";
        return <section className={`permission-coach-item${granted ? " is-granted" : ""}`} key={kind}>
          <div className="permission-coach-item-copy"><strong>{definition.name}</strong><span>{definition.description}</span></div>
          <span className="permission-coach-status">{granted ? <><TofiIcon name="check" size={16} variant="filled" style={{ verticalAlign: "middle" }} /> {t("permissions.granted")}</> : statusText(snapshot, kind)}</span>
          {!granted && <div className="permission-coach-actions"><button type="button" className="primary-button" disabled={busy !== null} onClick={() => void request(kind)}>{busy === kind ? t("action.processing") : t("permissions.allow")}</button><button type="button" className="secondary-button" disabled={busy !== null} onClick={() => void openSettings(kind)}>{definition.setting}</button></div>}
          {!granted && showDrop && <div className="permission-guide-hint"><span>{t("permissions.drag_hint")}</span><small>{t("permissions.drag_detail")}</small></div>}
        </section>;
      })}
    </div>
    {error && <p className="permission-coach-error" role="alert">{error}</p>}
    <div className="permission-coach-footer"><span>{t("permissions.no_restart")}</span><button type="button" className="text-button" onClick={() => void refresh()}>{t("permissions.recheck")}</button></div>
  </aside>;
}
