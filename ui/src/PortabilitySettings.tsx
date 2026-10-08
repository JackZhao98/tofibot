import { useEffect, useState } from "react";
import { request } from "./api";
import type { Bot } from "./types";
import { getBotAvatarConfig, saveBotAvatarConfig } from "./avatarStore";
import type { AvatarConfig } from "./lib/tofi-avatar/index.js";
import { decryptPortable, encryptPortable, isEncryptedPortable, MAX_PORTABLE_FILE_BYTES } from "./portabilityCrypto";
import { ENVIRONMENT_CATEGORY, portabilityLabel as label, exportDefaultCategories, importDefaultCategories, checkPortableInput } from "./portabilitySelection";
import "./portability.css";
import { useUserTimezone } from "./UserTimezone";
import { i18n, useTranslation } from "./i18n";

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
function message(error: unknown) { return error instanceof Error ? error.message : i18n.t("settings:action.failed"); }

export function PortabilitySettings({ bots, initialFile, initialBotID = "", onInitialFileConsumed }: { bots: Bot[]; initialFile?: File; initialBotID?: string; onInitialFileConsumed?: () => void }) {
  const { t } = useTranslation("settings");
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
    if (file.size > MAX_PORTABLE_FILE_BYTES) { setError(t("portability.error.file_too_large")); return; }
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
    catch { setError(t("portability.error.environment_unavailable")); }
  }
  async function deleteRecovery(id: string) {
    setBusy(true); setError("");
    try { await request(`/api/portability/recovery/${encodeURIComponent(id)}`, { method: "DELETE" }); setExportEnvironmentIDs(ids => ids.filter(x => x !== id)); await loadEnvironment(); setDone(t("portability.recovery_deleted")); }
    catch { setError(t("portability.recovery_delete_failed")); } finally { setBusy(false); }
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
      if (exportPassword !== confirmPassword) throw new Error(t("portability.error.password_mismatch"));
      if (exportPassword.length < 12) throw new Error(t("portability.error.password_short"));
      const exported = await request<Bundle>("/api/portability/export", { method: "POST", body: JSON.stringify({ kind, selection: { categories: included, bot_ids: botIDs, vault_environment_ids: included.includes(ENVIRONMENT_CATEGORY) ? environmentRecords.filter(x => x.source_category === "active_vault" && exportEnvironmentIDs.includes(x.id)).map(x => x.id) : [], recovered_environment_ids: included.includes(ENVIRONMENT_CATEGORY) ? environmentRecords.filter(x => x.source_category === "inactive_recovery" && exportEnvironmentIDs.includes(x.id)).map(x => x.id) : [] } }) });
      exported.bots?.forEach(bot => { bot.avatar = getBotAvatarConfig(bot.id); });
      const encrypted = await encryptPortable(JSON.stringify(exported), exportPassword);
      const filename = `tofi-${kind}-${new Date().toISOString().replace(/[:.]/g, "-")}.tofi.json`;
      download(encrypted, filename);
      setExportPassword(""); setConfirmPassword(""); setDone(exported.counts?.missing_attachments ? t("portability.exported_missing", { filename, count: exported.counts.missing_attachments }) : t("portability.exported", { filename }));
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
      setPreview(undefined); setBundle(undefined); setBundleSource(""); setFileSource(""); setImportEnvironmentIDs([]); setDone(t("portability.imported", { count: result.counts.bot_config, environments: result.counts.vault_environment ?? 0 })); if (environmentLoaded) void loadEnvironment();
      window.dispatchEvent(new Event("tofi:portability-imported"));
      if (importCategories.includes("settings")) void timezone.refresh();
      void request<{ bots: Bot[] | null }>("/api/bots?include_archived=true").then(next => setExportBots(next.bots ?? [])).catch(() => {});
    } catch (cause) { setError(message(cause)); } finally { setBusy(false); }
  }
  const encryptedFile = fileSource && isEncryptedPortable(fileSource);
  return <section className="settings-section portability-section">
    <h3>{t("portability.title")}</h3>
    <p className="settings-description">{t("portability.description")}</p>
    <p className="field-note">{t("portability.scope_note")}</p>
    <p className="field-note">{t("portability.security_note")}</p>
    <fieldset disabled={busy}>
      <legend>{t("portability.export")}</legend>
      <label>{t("portability.scope")}<select value={kind} onChange={event => { const next = event.target.value as "account" | "bot"; setKind(next); if (next === "bot") { setBotIDs(exportBots[0] ? [exportBots[0].id] : []); setIncluded(c => c.filter(x => x !== "settings" && x !== ENVIRONMENT_CATEGORY)); } }}><option value="account">{t("portability.scope_account")}</option><option value="bot">{t("portability.scope_bot")}</option></select></label>
      <div className="portability-choices">{exportBots.map(bot => <label key={bot.id}><input type={kind === "bot" ? "radio" : "checkbox"} name="export-bots" checked={botIDs.includes(bot.id)} onChange={() => setBotIDs(kind === "bot" ? [bot.id] : toggle(botIDs, bot.id))}/>{bot.archived ? t("portability.bot_archived", { name: bot.name }) : bot.name}</label>)}</div>
      <div className="portability-choices">{categories.filter(c => kind === "account" || c !== "settings" && c !== ENVIRONMENT_CATEGORY).map(c => <label key={c}><input type="checkbox" checked={included.includes(c)} disabled={c === "bot_config"} onChange={() => setIncluded(toggle(included, c))}/>{label(c)}</label>)}</div>
      {kind === "account" && <div className="portability-sensitive">
        <p className="field-note">{t("portability.environment_note")}</p>
        <button type="button" onClick={() => void loadEnvironment()}>{t("portability.environment_load")}</button>
        {included.includes(ENVIRONMENT_CATEGORY) && <div className="portability-choices">{environmentRecords.map(x => <label key={`${x.source_category}:${x.id}`}><input type="checkbox" checked={exportEnvironmentIDs.includes(x.id)} onChange={() => setExportEnvironmentIDs(toggle(exportEnvironmentIDs, x.id))} />{x.name || x.target} · {x.target} · {x.source_category === "active_vault" ? t("portability.source_active") : t("portability.source_inactive")}</label>)}</div>}
      </div>}
      <p className="field-note">{t("portability.dependency_note")}</p>
      <label>{t("portability.password")}<input type="password" autoComplete="new-password" value={exportPassword} onChange={e => setExportPassword(e.target.value)} /></label>
      <label>{t("portability.password_confirm")}<input type="password" autoComplete="new-password" value={confirmPassword} onChange={e => setConfirmPassword(e.target.value)} /></label>
      <button type="button" disabled={(!botIDs.length && (exportBots.length > 0 || !included.includes(ENVIRONMENT_CATEGORY))) || (included.includes(ENVIRONMENT_CATEGORY) && !exportEnvironmentIDs.length) || exportPassword.length < 12 || exportPassword !== confirmPassword} onClick={() => void exportBundle()}>{t("portability.download")}</button>
      {kind === "bot" && <p className="field-note">{t("portability.bot_note")}</p>}
    </fieldset>
    <fieldset disabled={busy}>
      <legend>{t("portability.import")}</legend>
      <label>{t("portability.choose_file")}<input type="file" accept=".json,.tofi,application/json" onChange={e => { const file = e.currentTarget.files?.[0]; e.currentTarget.value = ""; if (file) void loadFile(file); }} /></label>
      {fileName && <p className="field-note">{fileName}</p>}
      {encryptedFile && !bundle && <><label>{t("portability.decrypt_password")}<input type="password" autoComplete="off" value={importPassword} onChange={e => setImportPassword(e.target.value)} /></label><button type="button" onClick={() => void decrypt()}>{t("portability.decrypt")}</button></>}
      {bundle && <>
        <p>{t("portability.source", { format: bundle.format, version: bundle.version })}</p>
        <div className="portability-choices">{bundle.bots?.map(bot => <label key={bot.id}><input type="checkbox" checked={importBotIDs.includes(bot.id)} onChange={() => { setImportBotIDs(toggle(importBotIDs, bot.id)); invalidate(); }} />{bot.name}</label>)}</div>
        <div className="portability-choices">{bundle.included.map(c => <label key={c}><input type="checkbox" checked={importCategories.includes(c)} disabled={c === "bot_config"} onChange={() => { setImportCategories(toggle(importCategories, c)); invalidate(); }} />{c === "settings" ? t("portability.category_replaces", { label: label(c) }) : label(c)}</label>)}</div>
        {bundle.included.includes(ENVIRONMENT_CATEGORY) && <div className="portability-sensitive"><p className="field-note">{t("portability.import_environment_note")}</p><div className="portability-choices">{bundle.vault_environment?.map(x => <label key={x.id}><input type="checkbox" disabled={!importCategories.includes(ENVIRONMENT_CATEGORY)} checked={importEnvironmentIDs.includes(x.id)} onChange={() => { setImportEnvironmentIDs(toggle(importEnvironmentIDs, x.id)); invalidate(); }} />{x.name || x.target} · {x.target} · {x.source_category === "inactive_recovery" ? t("portability.source_inactive") : t("portability.source_active")}</label>)}</div></div>}
        <button type="button" disabled={Boolean(bundle.bots?.length && !importBotIDs.length) || (importCategories.includes(ENVIRONMENT_CATEGORY) && !importEnvironmentIDs.length)} onClick={() => void inspect()}>{t("portability.preview_button")}</button>
      </>}
      {preview && <div className="portability-preview" aria-live="polite">
        <h4>{t("portability.preview_title")}</h4>
        <p>{t("portability.attachment_space", { size: Math.ceil(preview.attachment_bytes / 1024) })}</p>
        <p>{t("portability.database_space", { size: Math.ceil(preview.estimated_bytes / 1024) })}</p>
        <dl>{Object.entries(preview.counts).map(([name, count]) => <div key={name}><dt>{label(name)}</dt><dd>{count}</dd></div>)}</dl>
        <p>{t("portability.bots_created", { names: preview.bots.map(bot => bot.name).join(t("portability.list_separator")) || t("portability.none") })}</p>
        <p>{t("portability.preview_note")}</p>
        {preview.conflicts.length > 0 && <><h4>{t("portability.conflicts")}</h4><ul>{preview.conflicts.map((c, i) => <li key={i}>{c}</li>)}</ul></>}
        <h4>{t("portability.dependencies")}</h4><ul>{preview.dependencies.map((dependency, i) => <li key={i}>{dependency}</li>)}</ul>
        {preview.vault_environment && <><h4>{t("portability.environment_title")}</h4><p>{t("portability.environment_summary", { count: preview.vault_environment.length, bytes: preview.vault_environment_bytes })}</p><ul>{preview.vault_environment.map(x => <li key={x.id}>{x.name || x.target} · {x.target} · {t("portability.inactive_restore")}</li>)}</ul></>}
        <h4>{t("portability.warnings")}</h4><ul>{preview.warnings.map((warning, i) => <li key={i}>{warning}</li>)}</ul>
        <button type="button" disabled={preview.can_apply === false} onClick={() => void apply()}>{t("portability.apply")}</button>
      </div>}
    </fieldset>
    {environmentLoaded && <div className="portability-sensitive"><h4>{t("portability.recovery_title")}</h4><p className="field-note">{t("portability.recovery_note")}</p>{environmentRecords.filter(x => x.source_category === "inactive_recovery").map(x => <div className="portability-recovery-row" key={x.id}><span>{x.name || x.target} · {x.target} · {t("portability.bytes", { count: x.bytes ?? 0 })} · {t("portability.inactive")}</span><button type="button" disabled={busy} onClick={() => void deleteRecovery(x.id)}>{t("portability.recovery_delete")}</button></div>)}</div>}
    {busy && <p role="status">{t("action.processing")}</p>}{error && <p className="error-text" role="alert">{error}</p>}{done && <p role="status">{done}</p>}
  </section>;
}
