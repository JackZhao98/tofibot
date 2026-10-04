import { useEffect, useState } from "react";
import { request } from "./api";
import type { Bot } from "./types";
import { getBotAvatarConfig, saveBotAvatarConfig } from "./avatarStore";
import type { AvatarConfig } from "./lib/tofi-avatar/index.js";
import { decryptPortable, encryptPortable, isEncryptedPortable, MAX_PORTABLE_FILE_BYTES } from "./portabilityCrypto";
import { ENVIRONMENT_CATEGORY, portabilityLabels as labels, exportDefaultCategories, importDefaultCategories, checkPortableInput } from "./portabilitySelection";
import "./portability.css";
import { useUserTimezone } from "./UserTimezone";

const categories = ["bot_config", "chats", "memories", "schedules", "attachments", "settings", "vault_environment"] as const;
type PortableBot = { id: string; name: string; avatar?: AvatarConfig };
type Bundle = { format: string; version: number; kind?: string; included: string[]; bots?: PortableBot[]; bot?: { name: string; avatar?: AvatarConfig }; counts?: Record<string, number>; vault_environment?: EnvironmentRecord[] };
type EnvironmentRecord = { id: string; name: string; target: string; source_category: "active_vault" | "inactive_recovery"; status?: string; bytes?: number };
type Preview = { preview_id: string; counts: Record<string, number>; bots: PortableBot[]; conflicts: string[]; warnings: string[]; excluded: string[]; expires_at: string; estimated_bytes: number; attachment_bytes: number; can_apply: boolean; dependencies: string[]; vault_environment?: EnvironmentRecord[]; vault_environment_bytes?: number };

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
  const [included, setIncluded] = useState<string[]>(exportDefaultCategories);
  const [environmentRecords, setEnvironmentRecords] = useState<EnvironmentRecord[]>([]);
  const [exportEnvironmentIDs, setExportEnvironmentIDs] = useState<string[]>([]);
  const [importEnvironmentIDs, setImportEnvironmentIDs] = useState<string[]>([]);
  const [environmentLoaded, setEnvironmentLoaded] = useState(false);
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
      if (!isEncryptedPortable(source)) loadBundle(source, false);
    } catch (cause) { setFileSource(""); setError(message(cause)); }
  }
  function loadBundle(source: string, encrypted: boolean) {
    checkPortableInput(source, encrypted);
    const parsed = JSON.parse(source) as Bundle;
    setBundle(parsed); setBundleSource(source); setImportCategories(importDefaultCategories(parsed.included)); setImportEnvironmentIDs([]); setImportBotIDs(parsed.bots?.map(b => b.id) ?? []); setPreview(undefined);
  }
  async function loadEnvironment() {
    setError("");
    try { const result = await request<{ records: EnvironmentRecord[] }>("/api/portability/environment"); setEnvironmentRecords(result.records); setEnvironmentLoaded(true); }
    catch { setError("环境凭据元数据不可用。请登录并使用 HTTPS 或已启用的本机入口。"); }
  }
  async function deleteRecovery(id: string) {
    setBusy(true); setError("");
    try { await request(`/api/portability/recovery/${encodeURIComponent(id)}`, { method: "DELETE" }); setExportEnvironmentIDs(ids => ids.filter(x => x !== id)); await loadEnvironment(); setDone("已删除未启用的恢复副本；现有凭据和电脑环境保留。"); }
    catch { setError("无法删除恢复副本。"); } finally { setBusy(false); }
  }

  useEffect(() => { if (initialFile) void loadFile(initialFile).finally(() => onInitialFileConsumed?.()); }, [initialFile]);
  useEffect(() => { if (initialBotID) { setKind("bot"); setBotIDs([initialBotID]); setIncluded(c => c.filter(x => x !== "settings" && x !== ENVIRONMENT_CATEGORY)); } }, [initialBotID]);
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
    const options = JSON.stringify({ selection: { categories: importCategories, bot_ids: importBotIDs, vault_environment_ids: importCategories.includes(ENVIRONMENT_CATEGORY) ? importEnvironmentIDs : [] }, ...(previewID ? { preview_id: previewID } : {}) });
    return `{"bundle":${bundleSource},${options.slice(1)}`;
  }

  async function exportBundle() {
    if (busy) return; setBusy(true); setError(""); setDone("");
    try {
      if (exportPassword !== confirmPassword) throw new Error("两次口令不一致。");
      if (exportPassword.length < 12) throw new Error("请使用至少 12 个字符的加密口令。");
      const exported = await request<Bundle>("/api/portability/export", { method: "POST", body: JSON.stringify({ kind, selection: { categories: included, bot_ids: botIDs, vault_environment_ids: included.includes(ENVIRONMENT_CATEGORY) ? environmentRecords.filter(x => x.source_category === "active_vault" && exportEnvironmentIDs.includes(x.id)).map(x => x.id) : [], recovered_environment_ids: included.includes(ENVIRONMENT_CATEGORY) ? environmentRecords.filter(x => x.source_category === "inactive_recovery" && exportEnvironmentIDs.includes(x.id)).map(x => x.id) : [] } }) });
      exported.bots?.forEach(bot => { bot.avatar = getBotAvatarConfig(bot.id); });
      const encrypted = await encryptPortable(JSON.stringify(exported), exportPassword);
      const filename = `tofi-${kind}-${new Date().toISOString().replace(/[:.]/g, "-")}.tofi.json`;
      download(encrypted, filename);
      setExportPassword(""); setConfirmPassword(""); setDone(`加密数据包已下载：${filename}。${exported.counts?.missing_attachments ? `其中 ${exported.counts.missing_attachments} 个附件未包含，请在导入预览中查看原因。` : ""}请妥善保存口令；无法找回。`);
    } catch (cause) { setError(message(cause)); } finally { setBusy(false); }
  }
  async function decrypt() {
    setBusy(true); setError("");
    try { loadBundle(await decryptPortable(fileSource, importPassword), true); setImportPassword(""); } catch (cause) { setError(message(cause)); } finally { setBusy(false); }
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
      setPreview(undefined); setBundle(undefined); setBundleSource(""); setFileSource(""); setImportEnvironmentIDs([]); setDone(`已导入 ${result.counts.bot_config} 个 Bot，恢复 ${result.counts.vault_environment ?? 0} 个未启用的环境凭据。定时任务全部暂停，现有数据保留。`); if (environmentLoaded) void loadEnvironment();
      window.dispatchEvent(new Event("tofi:portability-imported"));
      if (importCategories.includes("settings")) void timezone.refresh();
      void request<{ bots: Bot[] | null }>("/api/bots?include_archived=true").then(next => setExportBots(next.bots ?? [])).catch(() => {});
    } catch (cause) { setError(message(cause)); } finally { setBusy(false); }
  }
  const encryptedFile = fileSource && isEncryptedPortable(fileSource);
  return <section className="settings-section portability-section">
    <h3>数据迁移</h3>
    <p className="settings-description">选择账号数据或单个 Bot，导入前检查内容并创建副本。账号包与 Bot 包使用相同格式，默认加密。</p>
    <p className="field-note">当前支持配置、聊天正文、记忆、定时任务、附件和时区／模型设置。附件每个最多 4 MiB、合计最多 8 MiB；缺失或超限文件会明确列出。可另行明确选择持久保险库环境凭据；只恢复未启用的副本。SSH 私钥、登录、插件、技能、工作事项、邮件草稿、运行记录、虚拟电脑文件与整盘暂不支持。这是所选数据包，尚非完整账号备份。聊天或指令中粘贴的秘密仍会随正文导出。</p>
    <p className="field-note">导出口令只留在浏览器。持有数据包和口令的人可以读取内容。导入会将解密的数据包发送到目标服务器，选择决定写入哪些记录。服务器处理明文；账号管理者重设密码可导致账号接管，主机管理员也可读取运行数据。这不是对运营方保密的托管。保险库与数据库分别捕获快照。</p>
    <fieldset disabled={busy}>
      <legend>导出</legend>
      <label>范围<select value={kind} onChange={event => { const next = event.target.value as "account" | "bot"; setKind(next); if (next === "bot") { setBotIDs(exportBots[0] ? [exportBots[0].id] : []); setIncluded(c => c.filter(x => x !== "settings" && x !== ENVIRONMENT_CATEGORY)); } }}><option value="account">账号数据</option><option value="bot">独立 Bot</option></select></label>
      <div className="portability-choices">{exportBots.map(bot => <label key={bot.id}><input type={kind === "bot" ? "radio" : "checkbox"} name="export-bots" checked={botIDs.includes(bot.id)} onChange={() => setBotIDs(kind === "bot" ? [bot.id] : toggle(botIDs, bot.id))}/>{bot.name}{bot.archived ? "（已归档）" : ""}</label>)}</div>
      <div className="portability-choices">{categories.filter(c => kind === "account" || c !== "settings" && c !== ENVIRONMENT_CATEGORY).map(c => <label key={c}><input type="checkbox" checked={included.includes(c)} disabled={c === "bot_config"} onChange={() => setIncluded(toggle(included, c))}/>{labels[c]}</label>)}</div>
      {kind === "account" && <div className="portability-sensitive">
        <p className="field-note">环境凭据默认不选；还须逐项勾选记录。最多 64 项，每项 64 KiB，合计 1 MiB。此范围不包括电脑中已安装的环境变量、SSH 或服务登录。</p>
        <button type="button" onClick={() => void loadEnvironment()}>查看可迁移的环境凭据</button>
        {included.includes(ENVIRONMENT_CATEGORY) && <div className="portability-choices">{environmentRecords.map(x => <label key={`${x.source_category}:${x.id}`}><input type="checkbox" checked={exportEnvironmentIDs.includes(x.id)} onChange={() => setExportEnvironmentIDs(toggle(exportEnvironmentIDs, x.id))} />{x.name || x.target} · {x.target} · {x.source_category === "active_vault" ? "保险库原记录" : "未启用恢复副本"}</label>)}</div>}
      </div>}
      <p className="field-note">历史引用所需的 Bot 配置与空私聊结构会作为依赖附带；不会恢复旧群成员，也不会附带其额外聊天正文。</p>
      <label>加密口令<input type="password" autoComplete="new-password" value={exportPassword} onChange={e => setExportPassword(e.target.value)} /></label>
      <label>再次输入口令<input type="password" autoComplete="new-password" value={confirmPassword} onChange={e => setConfirmPassword(e.target.value)} /></label>
      <button type="button" disabled={(!botIDs.length && (exportBots.length > 0 || !included.includes(ENVIRONMENT_CATEGORY))) || (included.includes(ENVIRONMENT_CATEGORY) && !exportEnvironmentIDs.length) || exportPassword.length < 12 || exportPassword !== confirmPassword} onClick={() => void exportBundle()}>下载加密数据包</button>
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
        {bundle.included.includes(ENVIRONMENT_CATEGORY) && <div className="portability-sensitive"><p className="field-note">环境凭据默认不选。选中类别后逐项勾选，只创建未启用的恢复副本，不覆盖同名目标，也不连接服务或安装到电脑。</p><div className="portability-choices">{bundle.vault_environment?.map(x => <label key={x.id}><input type="checkbox" disabled={!importCategories.includes(ENVIRONMENT_CATEGORY)} checked={importEnvironmentIDs.includes(x.id)} onChange={() => { setImportEnvironmentIDs(toggle(importEnvironmentIDs, x.id)); invalidate(); }} />{x.name || x.target} · {x.target} · {x.source_category === "inactive_recovery" ? "未启用恢复副本" : "保险库原记录"}</label>)}</div></div>}
        <button type="button" disabled={Boolean(bundle.bots?.length && !importBotIDs.length) || (importCategories.includes(ENVIRONMENT_CATEGORY) && !importEnvironmentIDs.length)} onClick={() => void inspect()}>预览导入</button>
      </>}
      {preview && <div className="portability-preview" aria-live="polite">
        <h4>导入预览</h4>
        <p>附件空间：约 {Math.ceil(preview.attachment_bytes / 1024)} KiB，使用目标账号文件存储配额。</p>
        <p>预估数据库空间：约 {Math.ceil(preview.estimated_bytes / 1024)} KiB。实际占用由 SQLite 决定；空间不足会整体回滚。</p>
        <dl>{Object.entries(preview.counts).map(([name, count]) => <div key={name}><dt>{labels[name] ?? name}</dt><dd>{count}</dd></div>)}</dl>
        <p>将创建的 Bot：{preview.bots.map(bot => bot.name).join("、") || "无"}</p>
        <p>Bot 和所有关联记录将获得新 ID；现有内容保留。未选内容会跳过；历史引用所需的 Bot 配置会作为依赖保留，不恢复旧群成员或额外私聊正文。群聊需要选中全部现有成员。预览有效期 15 分钟。</p>
        {preview.conflicts.length > 0 && <><h4>冲突与设置变更</h4><ul>{preview.conflicts.map((c, i) => <li key={i}>{c}</li>)}</ul></>}
        <h4>关联依赖</h4><ul>{preview.dependencies.map((dependency, i) => <li key={i}>{dependency}</li>)}</ul>
        {preview.vault_environment && <><h4>环境凭据：未启用恢复</h4><p>共 {preview.vault_environment.length} 项，{preview.vault_environment_bytes} 字节；不会显示值，也不会自动启用。</p><ul>{preview.vault_environment.map(x => <li key={x.id}>{x.name || x.target} · {x.target} · 未启用恢复</li>)}</ul></>}
        <h4>敏感内容与缺失项</h4><ul>{preview.warnings.map((warning, i) => <li key={i}>{warning}</li>)}</ul>
        <button type="button" disabled={preview.can_apply === false} onClick={() => void apply()}>确认创建副本</button>
      </div>}
    </fieldset>
    {environmentLoaded && <div className="portability-sensitive"><h4>未启用的环境凭据恢复副本</h4><p className="field-note">仅显示元数据。可以在上方明确选择并加密再导出，或删除恢复副本。启用与安装是后续独立操作，目前不提供。</p>{environmentRecords.filter(x => x.source_category === "inactive_recovery").map(x => <div className="portability-recovery-row" key={x.id}><span>{x.name || x.target} · {x.target} · {x.bytes} 字节 · 未启用</span><button type="button" disabled={busy} onClick={() => void deleteRecovery(x.id)}>删除恢复副本</button></div>)}</div>}
    {busy && <p role="status">处理中…</p>}{error && <p className="error-text" role="alert">{error}</p>}{done && <p role="status">{done}</p>}
  </section>;
}
