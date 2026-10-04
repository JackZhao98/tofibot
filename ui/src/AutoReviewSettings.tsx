import {useEffect, useState} from "react";
import {request} from "./api";
import {useSettingsDraft} from "./settingsDraft";

type Mode = "off" | "shadow" | "auto";
type Settings = {mode: Mode; eligible_tool_count: number};

export function AutoReviewSettings() {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [mode, setMode] = useState<Mode>("off");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [version, setVersion] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    request<Settings>("/api/auto-review-settings", {signal: controller.signal}).then(value => {setSettings(value); setMode(value.mode); setError("");}).catch(cause => {if (!controller.signal.aborted) setError(cause.message);});
    return () => controller.abort();
  }, [version]);
  async function save() {
    if (busy || !settings) return false;
    setBusy(true); setError(""); setStatus("");
    try {
      await request("/api/auto-review-settings", {method: "PUT", body: JSON.stringify({mode})});
      setSettings({...settings, mode}); setStatus("已保存当前账号的 AutoReview 设置。"); return true;
    } catch (cause) {setError(cause instanceof Error ? cause.message : "保存失败，请重试。"); return false;}
    finally {setBusy(false);}
  }
  useSettingsDraft({label: "AutoReview", dirty: Boolean(settings && settings.mode !== mode), busy, save, discard: () => {if (settings) setMode(settings.mode); setError(""); setStatus("");}});
  return <section className="settings-section"><h3>AutoReview</h3><p className="settings-description">默认关闭。仅审查经过独立核实的公开只读工具；其余操作需要你批准。</p>
    {settings ? <><label>当前账号的审批模式<select value={mode} disabled={busy} onChange={event => {setMode(event.target.value as Mode); setStatus("");}}><option value="off">关闭 · 人工审批</option><option value="shadow">观察 · 记录建议，仍由你批准</option><option value="auto">自动审批 · 仅限已核实工具</option></select></label><p className="field-note">已核实工具：{settings.eligible_tool_count}。{settings.eligible_tool_count === 0 ? "当前没有工具可自动批准。" : "自动决定仅对这一次完整提案有效。"}关闭后，尚未领取执行权的自动批准立即失效。</p><span role="status">{status}</span></> : !error && <p className="muted">读取配置…</p>}
    {error && <p className="error-text" role="alert">{error}{!settings && <button className="text-button" onClick={() => setVersion(current => current + 1)}>重试</button>}</p>}
  </section>;
}
