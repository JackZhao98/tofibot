import { useState } from "react";
import { request } from "./api";

export function WorkspacePurgeSettings() {
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function purge() {
    if (busy || value.trim() !== "PURGE") return;
    setBusy(true);
    setError("");
    try {
      await request<{ purged: boolean }>("/api/admin/purge", { method: "POST", body: JSON.stringify({ confirm: "PURGE" }) });
      window.location.reload();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "重置失败，请稍后重试。");
      setBusy(false);
    }
  }
  return <section className="settings-section purge-section">
    <h3>从头开始</h3>
    <p className="settings-description">清空所有 Bot、对话、任务、附件、配对设备，以及共享 Linux 电脑的整个 /workspace。服务器安装、Codex 连接和其他服务配置会保留。</p>
    <p className="field-note">执行前 VM 会自动保留一个可恢复 checkpoint；新创建的第一个账号会成为 admin。</p>
    <label>输入 PURGE 确认<input value={value} onChange={event => setValue(event.target.value)} spellCheck={false} autoCapitalize="characters" disabled={busy} /></label>
    {error && <p className="error-text" role="alert">{error}</p>}
    <button type="button" className="purge-button" disabled={busy || value.trim() !== "PURGE"} onClick={() => void purge()}>{busy ? "正在清空…" : "清空全部数据"}</button>
  </section>;
}
