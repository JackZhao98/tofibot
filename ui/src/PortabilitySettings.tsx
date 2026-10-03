import { useEffect, useState } from "react";
import { request } from "./api";
import type { Bot } from "./types";
import { getBotAvatarConfig, saveBotAvatarConfig } from "./avatarStore";
import type { AvatarConfig } from "./lib/tofi-avatar/index.js";
import { decryptPortable, encryptPortable, isEncryptedPortable, MAX_PORTABLE_FILE_BYTES } from "./portabilityCrypto";
import "./portability.css";
import { useUserTimezone } from "./UserTimezone";

const categories = ["bot_config", "chats", "memories", "schedules", "settings"] as const;
const labels: Record<string, string> = { bot_config: "Bot 配置与当前浏览器头像", conversations: "会话", chats: "聊天正文", memories: "记忆", schedules: "定时任务", settings: "时区与模型设置" };
type PortableBot = { id: string; name: string; avatar?: AvatarConfig };
type Bundle = { format: string; version: number; kind?: string; included: string[]; bots?: PortableBot[]; bot?: { name: string; avatar?: AvatarConfig }; counts?: Record<string, number> };
type Preview = { preview_id: string; counts: Record<string, number>; bots: PortableBot[]; conflicts: string[]; warnings: string[]; excluded: string[]; expires_at: string; estimated_bytes: number; dependencies: string[] };

function download(source: string, name: string) {
  const url = URL.createObjectURL(new Blob([source], { type: "application/json" }));
  const a = document.createElement("a"); a.href = url; a.download = name; a.click();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
function message(error: unknown) { return error instanceof Error ? error.message : "操作失败，请重试。"; }

export function PortabilitySettings({ bots, initialFile, initialBotID = "", onInitialFileConsumed }: { bots: Bot[]; initialFile?: File; initialBotID?: string; onInitialFileConsumed?: () => void }) {
  const timezone = useUserTimezone();
  const [kind, setKind] = useState<"account" | "bot">(initialBotID ? "bot" : "account");
  const [botIDs, setBotIDs] = useState<string[]>(initialBotID ? [initialBotID] : bots.map(b => b.id));
  const [exportBots, setExportBots] = useState(bots);
  const [included, setIncluded] = useState<string[]>(["bot_config", "chats", "memories", "schedules"]);
  const [exportPassword, setExportPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [importPassword, setImportPassword] = useState("");
  const [fileSource, setFileSource] = useState("");
  const [fileName, setFileName] = useState("");
  const [bundle, setBundle] = useState<Bundle>();
  const [bundleSource, setBundleSource] = useState("");
  const [importCategories, setImportCategories] = useState<string[]>([]);
  const [importBotIDs, setImportBotIDs] = useState<string[]>([]);
  const [preview, setPreview] = useState<Preview>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  async function loadFile(file: File) {
    setPreview(undefined); setBundle(undefined); setBundleSource(""); setFileSource(""); setError(""); setDone(""); setImportPassword(""); setFileName(file.name);
    if (file.size > MAX_PORTABLE_FILE_BYTES) { setError("文件超过 24 MiB，请减少导出内容。"); return; }
    try {
      const source = await file.text();
      setFileSource(source);
      if (!isEncryptedPortable(source)) loadBundle(source);
    } catch { setError("无法读取 JSON 数据包；压缩包和整盘备份不受支持。"); }
  }
  function loadBundle(source: string) {
    const parsed = JSON.parse(source) as Bundle;
    if (!parsed || !["tofi.bundle", "tofi.bot"].includes(parsed.format) || parsed.version !== 1 || !Array.isArray(parsed.included)) throw new Error("文件格式或版本不受支持。");
    setBundle(parsed); setBundleSource(source); setImportCategories(parsed.included.filter(c => c !== "settings")); setImportBotIDs(parsed.bots?.map(b => b.id) ?? []); setPreview(undefined);
  }
  useEffect(() => { if (initialFile) void loadFile(initialFile).finally(() => onInitialFileConsumed?.()); }, [initialFile]);
  useEffect(() => { if (initialBotID) { setKind("bot"); setBotIDs([initialBotID]); setIncluded(c => c.filter(x => x !== "settings")); } }, [initialBotID]);
  useEffect(() => {
    let active = true;
    void request<{ bots: Bot[] | null }>("/api/bots?include_archived=true").then(result => {
      if (active) { const all = result.bots ?? []; setExportBots(all); setBotIDs(initialBotID ? [initialBotID] : all.map(bot => bot.id)); }
    }).catch(cause => { if (active) setError(message(cause)); });
    return () => { active = false; };
  }, [initialBotID]);
  const toggle = (values: string[], value: string) => values.includes(value) ? values.filter(x => x !== value) : [...values, value];
  function invalidate() { setPreview(undefined); setDone(""); setError(""); }
  function importBody(previewID?: string) {
    // Preserve the original JSON tokens so the server can reject duplicate
    // fields. loadBundle has already required one valid JSON value.
    const options = JSON.stringify({ selection: { categories: importCategories, bot_ids: importBotIDs }, ...(previewID ? { preview_id: previewID } : {}) });
    return `{"bundle":${bundleSource},${options.slice(1)}`;
  }

  async function exportBundle() {
    if (busy) return; setBusy(true); setError(""); setDone("");
    try {
      if (exportPassword !== confirmPassword) throw new Error("两次口令不一致。");
      if (exportPassword.length < 12) throw new Error("请使用至少 12 个字符的加密口令。");
      const exported = await request<Bundle>("/api/portability/export", { method: "POST", body: JSON.stringify({ kind, selection: { categories: included, bot_ids: botIDs } }) });
      exported.bots?.forEach(bot => { bot.avatar = getBotAvatarConfig(bot.id); });
      const encrypted = await encryptPortable(JSON.stringify(exported), exportPassword);
      download(encrypted, kind === "bot" ? "tofi-bot.tofi.json" : "tofi-account.tofi.json");
      setExportPassword(""); setConfirmPassword(""); setDone("加密数据包已下载。请妥善保存口令；无法找回。");
    } catch (cause) { setError(message(cause)); } finally { setBusy(false); }
  }
  async function decrypt() {
    setBusy(true); setError("");
    try { loadBundle(await decryptPortable(fileSource, importPassword)); setImportPassword(""); } catch (cause) { setError(message(cause)); } finally { setBusy(false); }
  }
  async function inspect() {
    if (!bundle || busy) return; setBusy(true); setError(""); setDone(""); setPreview(undefined);
    try {
      const result = await request<Preview>("/api/portability/preview", { method: "POST", body: importBody() });
      setPreview(result);
    } catch (cause) { setError(message(cause)); } finally { setBusy(false); }
  }
  async function apply() {
    if (!bundle || !preview || busy) return; setBusy(true); setError("");
    try {
      const result = await request<{ id_map: Record<string, string>; counts: Record<string, number> }>("/api/portability/apply", { method: "POST", body: importBody(preview.preview_id) });
      preview.bots.forEach(bot => { if (bot.avatar && result.id_map[bot.id]) saveBotAvatarConfig(result.id_map[bot.id], bot.avatar); });
      setPreview(undefined); setBundle(undefined); setBundleSource(""); setFileSource(""); setDone(`已导入 ${result.counts.bot_config} 个 Bot。定时任务全部暂停，现有数据保留。`);
      window.dispatchEvent(new Event("tofi:portability-imported"));
      if (importCategories.includes("settings")) void timezone.refresh();
      void request<{ bots: Bot[] | null }>("/api/bots?include_archived=true").then(next => setExportBots(next.bots ?? [])).catch(() => {});
    } catch (cause) { setError(message(cause)); } finally { setBusy(false); }
  }
  const encryptedFile = fileSource && isEncryptedPortable(fileSource);
  return <section className="settings-section portability-section">
    <h3>数据迁移</h3>
    <p className="settings-description">选择账号数据或单个 Bot，导入前检查内容并创建副本。账号包与 Bot 包使用相同格式，默认加密。</p>
    <p className="field-note">当前支持配置、聊天正文、记忆、定时任务和时区／模型设置。附件、密钥、登录、插件、技能、工作事项、邮件草稿、运行记录、虚拟电脑文件与整盘暂不支持。这是所选数据包，尚非完整账号备份。聊天或指令中粘贴的秘密仍会随正文导出。</p>
    <fieldset disabled={busy}>
      <legend>导出</legend>
      <label>范围<select value={kind} onChange={event => { const next = event.target.value as "account" | "bot"; setKind(next); if (next === "bot") { setBotIDs(exportBots[0] ? [exportBots[0].id] : []); setIncluded(c => c.filter(x => x !== "settings")); } }}><option value="account">账号数据</option><option value="bot">独立 Bot</option></select></label>
      <div className="portability-choices">{exportBots.map(bot => <label key={bot.id}><input type={kind === "bot" ? "radio" : "checkbox"} name="export-bots" checked={botIDs.includes(bot.id)} onChange={() => setBotIDs(kind === "bot" ? [bot.id] : toggle(botIDs, bot.id))}/>{bot.name}{bot.archived ? "（已归档）" : ""}</label>)}</div>
      <div className="portability-choices">{categories.filter(c => kind === "account" || c !== "settings").map(c => <label key={c}><input type="checkbox" checked={included.includes(c)} disabled={c === "bot_config"} onChange={() => setIncluded(toggle(included, c))}/>{labels[c]}</label>)}</div>
      <p className="field-note">历史引用所需的 Bot 配置与空私聊结构会作为依赖附带；不会恢复旧群成员，也不会附带其额外聊天正文。</p>
      <label>加密口令<input type="password" autoComplete="new-password" value={exportPassword} onChange={e => setExportPassword(e.target.value)} /></label>
      <label>再次输入口令<input type="password" autoComplete="new-password" value={confirmPassword} onChange={e => setConfirmPassword(e.target.value)} /></label>
      <button type="button" disabled={!botIDs.length || exportPassword.length < 12 || exportPassword !== confirmPassword} onClick={() => void exportBundle()}>下载加密数据包</button>
      {kind === "bot" && <p className="field-note">单 Bot 包只带该 Bot 的私聊；群聊不会随包导出。</p>}
    </fieldset>
    <fieldset disabled={busy}>
      <legend>导入</legend>
      <label>选择数据包<input type="file" accept=".json,.tofi,application/json" onChange={e => { const file = e.currentTarget.files?.[0]; e.currentTarget.value = ""; if (file) void loadFile(file); }} /></label>
      {fileName && <p className="field-note">{fileName}</p>}
      {encryptedFile && !bundle && <><label>解密口令<input type="password" autoComplete="off" value={importPassword} onChange={e => setImportPassword(e.target.value)} /></label><button type="button" onClick={() => void decrypt()}>解密并查看</button></>}
      {bundle && <>
        <p>来源格式：{bundle.format} · 版本 {bundle.version}</p>
        <div className="portability-choices">{bundle.bots?.map(bot => <label key={bot.id}><input type="checkbox" checked={importBotIDs.includes(bot.id)} onChange={() => { setImportBotIDs(toggle(importBotIDs, bot.id)); invalidate(); }} />{bot.name}</label>)}</div>
        <div className="portability-choices">{bundle.included.map(c => <label key={c}><input type="checkbox" checked={importCategories.includes(c)} disabled={c === "bot_config"} onChange={() => { setImportCategories(toggle(importCategories, c)); invalidate(); }} />{labels[c] ?? c}{c === "settings" ? "（勾选后替换现有设置）" : ""}</label>)}</div>
        <button type="button" disabled={Boolean(bundle.bots?.length && !importBotIDs.length)} onClick={() => void inspect()}>预览导入</button>
      </>}
      {preview && <div className="portability-preview" aria-live="polite">
        <h4>导入预览</h4>
        <p>预估数据库空间：约 {Math.ceil(preview.estimated_bytes / 1024)} KiB。实际占用由 SQLite 决定；空间不足会整体回滚。</p>
        <dl>{Object.entries(preview.counts).map(([name, count]) => <div key={name}><dt>{labels[name] ?? name}</dt><dd>{count}</dd></div>)}</dl>
        <p>将创建的 Bot：{preview.bots.map(bot => bot.name).join("、") || "无"}</p>
        <p>Bot 和所有关联记录将获得新 ID；现有内容保留。未选内容会跳过；历史引用所需的 Bot 配置会作为依赖保留，不恢复旧群成员或额外私聊正文。群聊需要选中全部现有成员。预览有效期 15 分钟。</p>
        {preview.conflicts.length > 0 && <><h4>冲突与设置变更</h4><ul>{preview.conflicts.map((c, i) => <li key={i}>{c}</li>)}</ul></>}
        <h4>关联依赖</h4><ul>{preview.dependencies.map((dependency, i) => <li key={i}>{dependency}</li>)}</ul>
        <h4>敏感内容与缺失项</h4><ul>{preview.warnings.map((warning, i) => <li key={i}>{warning}</li>)}</ul>
        <button type="button" onClick={() => void apply()}>确认创建副本</button>
      </div>}
    </fieldset>
    {busy && <p role="status">处理中…</p>}{error && <p className="error-text" role="alert">{error}</p>}{done && <p role="status">{done}</p>}
  </section>;
}
