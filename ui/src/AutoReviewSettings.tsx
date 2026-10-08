import {useEffect, useState} from "react";
import {request} from "./api";
import {useSettingsDraft} from "./settingsDraft";

type Mode = "off" | "shadow" | "auto";
type Settings = {mode: Mode; review_scope: "all_external_tools"};

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
  return <section className="settings-section"><h3>AutoReview</h3><p className="settings-description">默认关闭。审查范围：所有外部工具。审查建议与执行权限分别判断。</p>
    {settings ? <><label>当前账号的审批模式<select value={mode} disabled={busy} onChange={event => {setMode(event.target.value as Mode); setStatus("");}}><option value="off">关闭 · 保留原有审批规则</option><option value="shadow">观察 · 记录建议，保留原有执行路径</option><option value="auto">自动 · 审查后按执行策略决定</option></select></label><p className="field-note" data-autoreview-mode={mode}>{mode === "off" ? "关闭：不请求审查，保留原有人工审批及可信只读豁免。" : mode === "shadow" ? "观察：异步记录所有外部工具的建议，不新增等待或执行权限；原有人工审批及可信只读路径继续生效。" : "自动：审查所有外部工具；已授权的低或中风险提案可自动领取一次执行权；高风险、需要确认的提案仍须由你批准。拒绝、上下文缺口及无效绑定不能执行。"}关闭后，尚未领取执行权的自动批准立即失效。</p><span role="status">{status}</span></> : !error && <p className="muted">读取配置…</p>}
    {error && <p className="error-text" role="alert">{error}{!settings && <button className="text-button" onClick={() => setVersion(current => current + 1)}>重试</button>}</p>}
  </section>;
}
