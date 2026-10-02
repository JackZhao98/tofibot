import { TofiIcon } from "./icons";
import { useCallback, useEffect, useMemo, useState } from "react";
import type { DesktopPermissionKind, DesktopPermissionSnapshot } from "./desktop";
import "./permission-coach.css";

type Props = {
  permissions: DesktopPermissionKind[];
  onDismiss?: () => void;
  force?: boolean;
  open?: boolean;
};

const labels: Record<DesktopPermissionKind, { name: string; description: string; setting: string }> = {
  microphone: { name: "麦克风", description: "把你的语音转换成 Dictate 文字。", setting: "打开麦克风设置" },
  screen: { name: "屏幕录制", description: "让 Tofi 读取这台 Mac 的屏幕画面。", setting: "打开屏幕录制设置" },
  accessibility: { name: "辅助功能", description: "让 Tofi 在你允许时点击、输入和操作桌面。", setting: "打开辅助功能设置" },
};

function isGranted(snapshot: DesktopPermissionSnapshot | null, kind: DesktopPermissionKind) {
  if (!snapshot) return false;
  return kind === "accessibility" ? snapshot.accessibility : snapshot[kind] === "granted";
}

function statusText(snapshot: DesktopPermissionSnapshot | null, kind: DesktopPermissionKind) {
  if (isGranted(snapshot, kind)) return "已授权";
  if (!snapshot) return "正在检查…";
  return kind === "accessibility" || snapshot[kind] === "denied" ? "需要到系统设置开启" : "等待授权";
}

export function PermissionCoach({ permissions, onDismiss, force = false, open = false }: Props) {
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
      setError(cause instanceof Error ? cause.message : "无法读取本机权限状态。 ");
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
      setError(cause instanceof Error ? cause.message : "授权请求没有完成。 ");
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
      setError(cause instanceof Error ? cause.message : "无法打开系统设置。 ");
    } finally {
      setBusy(null);
    }
  }

  return <aside className="permission-coach" role="dialog" aria-labelledby="permission-coach-title" aria-describedby="permission-coach-description">
    <div className="permission-coach-header">
      <div className="permission-coach-icon" aria-hidden="true"><TofiIcon name="shield-check" size={20} /></div>
      <div><h3 id="permission-coach-title">给 Tofi 一点权限</h3><p id="permission-coach-description">只在你开启对应功能时使用。授权后会自动回来。</p></div>
      <button type="button" className="permission-coach-close" aria-label="稍后处理" onClick={() => { setVisible(false); onDismiss?.(); }}><TofiIcon name="close" size={20} style={{ verticalAlign: "middle" }} /></button>
    </div>
    <div className="permission-coach-list">
      {permissions.map((kind) => {
        const definition = labels[kind];
        const granted = isGranted(snapshot, kind);
        const showDrop = kind === "screen" || kind === "accessibility";
        return <section className={`permission-coach-item${granted ? " is-granted" : ""}`} key={kind}>
          <div className="permission-coach-item-copy"><strong>{definition.name}</strong><span>{definition.description}</span></div>
          <span className="permission-coach-status">{granted ? <><TofiIcon name="check" size={16} variant="filled" style={{ verticalAlign: "middle" }} /> 已授权</> : statusText(snapshot, kind)}</span>
          {!granted && <div className="permission-coach-actions"><button type="button" className="primary-button" disabled={busy !== null} onClick={() => void request(kind)}>{busy === kind ? "处理中…" : "允许"}</button><button type="button" className="secondary-button" disabled={busy !== null} onClick={() => void openSettings(kind)}>{definition.setting}</button></div>}
          {!granted && showDrop && <div className="permission-guide-hint"><span>打开系统设置后，把浮层中的 App 拖到上面的列表</span><small>浮层位于设置窗口下方；若未显示，请把设置窗口向上移，留出空位。拖入后仍需手动开启权限。</small></div>}
        </section>;
      })}
    </div>
    {error && <p className="permission-coach-error" role="alert">{error}</p>}
    <div className="permission-coach-footer"><span>完成后无需重启 Tofi</span><button type="button" className="text-button" onClick={() => void refresh()}>重新检查</button></div>
  </aside>;
}
