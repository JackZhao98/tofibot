import { useEffect, useState, type ReactNode } from "react";
import { request } from "./api";
import type { Bot } from "./types";
import { getBotAvatarConfig, saveBotAvatarConfig } from "./avatarStore";
import type { AvatarConfig } from "./lib/tofi-avatar/index.js";
import { decryptPortable, encryptPortable, isEncryptedPortable, MAX_PORTABLE_FILE_BYTES } from "./portabilityCrypto";
import { ENVIRONMENT_CATEGORY, portabilityLabel as label, exportDefaultCategories, importDefaultCategories, checkPortableInput } from "./portabilitySelection";
import "./portability.css";
import { Banner, FilePicker, SettingsDetails, SettingsRow } from "./settings/components";
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
  const choice = (key: string, input: ReactNode, text: ReactNode) => <label className="check-row" key={key}>{input}<span>{text}</span></label>;
  const exportDisabled = (!botIDs.length && (exportBots.length > 0 || !included.includes(ENVIRONMENT_CATEGORY))) || (included.includes(ENVIRONMENT_CATEGORY) && !exportEnvironmentIDs.length) || exportPassword.length < 12 || exportPassword !== confirmPassword;
  // One "Details" per card, at its bottom-left: the full explanations behind the short help text.
  const detailsRow = (notes: string[]) => <div className="portability-details"><SettingsDetails>{notes.map(note => <p key={note}>{note}</p>)}</SettingsDetails></div>;
  return <section className="portability-section" aria-label={t("portability.title")}>
    <p className="portability-summary">{t("portability.summary")}</p>
    <fieldset className="settings-card portability-card" disabled={busy}>
      <div className="portability-head"><h5>{t("portability.export")}</h5></div>
      <SettingsRow label={t("portability.scope")} labelFor="portability-scope" control={<select id="portability-scope" value={kind} onChange={event => { const next = event.target.value as "account" | "bot"; setKind(next); if (next === "bot") { setBotIDs(exportBots[0] ? [exportBots[0].id] : []); setIncluded(c => c.filter(x => x !== "settings" && x !== ENVIRONMENT_CATEGORY)); } }}><option value="account">{t("portability.scope_account")}</option><option value="bot">{t("portability.scope_bot")}</option></select>}/>
      <div className="portability-block">
        <div className="portability-choices">{exportBots.map(bot => choice(bot.id, <input type={kind === "bot" ? "radio" : "checkbox"} name="export-bots" checked={botIDs.includes(bot.id)} onChange={() => setBotIDs(kind === "bot" ? [bot.id] : toggle(botIDs, bot.id))}/>, bot.archived ? t("portability.bot_archived", { name: bot.name }) : bot.name))}</div>
      </div>
      <div className="portability-block">
        <div className="portability-choices">{categories.filter(c => kind === "account" || c !== "settings" && c !== ENVIRONMENT_CATEGORY).map(c => choice(c, <input type="checkbox" checked={included.includes(c)} disabled={c === "bot_config"} onChange={() => setIncluded(toggle(included, c))}/>, label(c)))}</div>
      </div>
      {kind === "account" && <SettingsRow label={t("portability.environment_label")} description={t("portability.environment_help")} control={<button type="button" className="secondary-button" onClick={() => void loadEnvironment()}>{t("portability.environment_load")}</button>}/>}
      {kind === "account" && included.includes(ENVIRONMENT_CATEGORY) && environmentRecords.length > 0 && <div className="portability-block"><div className="portability-choices">{environmentRecords.map(x => choice(`${x.source_category}:${x.id}`, <input type="checkbox" checked={exportEnvironmentIDs.includes(x.id)} onChange={() => setExportEnvironmentIDs(toggle(exportEnvironmentIDs, x.id))} />, `${x.name || x.target} · ${x.target} · ${x.source_category === "active_vault" ? t("portability.source_active") : t("portability.source_inactive")}`))}</div></div>}
      <SettingsRow label={t("portability.password")} labelFor="portability-password" control={<input id="portability-password" type="password" autoComplete="new-password" value={exportPassword} onChange={e => setExportPassword(e.target.value)} />}/>
      <SettingsRow label={t("portability.password_confirm")} labelFor="portability-password-confirm" control={<input id="portability-password-confirm" type="password" autoComplete="new-password" value={confirmPassword} onChange={e => setConfirmPassword(e.target.value)} />}/>
      <div className="portability-actions"><button type="button" className="primary-button" disabled={exportDisabled} onClick={() => void exportBundle()}>{t("portability.download")}</button></div>
      {detailsRow([t("portability.description"), t("portability.scope_note"), t("portability.security_note"), ...(kind === "account" ? [t("portability.environment_note")] : [t("portability.bot_note")]), t("portability.dependency_note")])}
    </fieldset>
    <fieldset className="settings-card portability-card" disabled={busy}>
      <div className="portability-head"><h5>{t("portability.import")}</h5></div>
      <div className="portability-block"><FilePicker accept=".json,.tofi,application/json" label={t("portability.choose_file")} fileName={fileName} dropHint={t("portability.drop_hint")} disabled={busy} onFile={file => void loadFile(file)}/></div>
      {encryptedFile && !bundle && <>
        <SettingsRow label={t("portability.decrypt_password")} labelFor="portability-decrypt" control={<input id="portability-decrypt" type="password" autoComplete="off" value={importPassword} onChange={e => setImportPassword(e.target.value)} onKeyDown={event => { if (event.key === "Enter" && importPassword && !busy) { event.preventDefault(); void decrypt(); } }} />}/>
        <div className="portability-actions"><button type="button" className="secondary-button" onClick={() => void decrypt()}>{t("portability.decrypt")}</button></div>
      </>}
      {bundle && <>
        <div className="portability-block"><p className="portability-help">{t("portability.source", { format: bundle.format, version: bundle.version })}</p></div>
        {Boolean(bundle.bots?.length) && <div className="portability-block"><div className="portability-choices">{bundle.bots?.map(bot => choice(bot.id, <input type="checkbox" checked={importBotIDs.includes(bot.id)} onChange={() => { setImportBotIDs(toggle(importBotIDs, bot.id)); invalidate(); }} />, bot.name))}</div></div>}
        <div className="portability-block"><div className="portability-choices">{bundle.included.map(c => choice(c, <input type="checkbox" checked={importCategories.includes(c)} disabled={c === "bot_config"} onChange={() => { setImportCategories(toggle(importCategories, c)); invalidate(); }} />, c === "settings" ? t("portability.category_replaces", { label: label(c) }) : label(c)))}</div></div>
        {bundle.included.includes(ENVIRONMENT_CATEGORY) && <div className="portability-block"><p className="portability-help">{t("portability.import_environment_help")}</p><div className="portability-choices">{bundle.vault_environment?.map(x => choice(x.id, <input type="checkbox" disabled={!importCategories.includes(ENVIRONMENT_CATEGORY)} checked={importEnvironmentIDs.includes(x.id)} onChange={() => { setImportEnvironmentIDs(toggle(importEnvironmentIDs, x.id)); invalidate(); }} />, `${x.name || x.target} · ${x.target} · ${x.source_category === "inactive_recovery" ? t("portability.source_inactive") : t("portability.source_active")}`))}</div></div>}
        <div className="portability-actions"><button type="button" className="secondary-button" disabled={Boolean(bundle.bots?.length && !importBotIDs.length) || (importCategories.includes(ENVIRONMENT_CATEGORY) && !importEnvironmentIDs.length)} onClick={() => void inspect()}>{t("portability.preview_button")}</button></div>
      </>}
      {preview && <div className="portability-block portability-preview" aria-live="polite">
        <h6>{t("portability.preview_title")}</h6>
        <p>{t("portability.attachment_space", { size: Math.ceil(preview.attachment_bytes / 1024) })}</p>
        <p>{t("portability.database_space", { size: Math.ceil(preview.estimated_bytes / 1024) })}</p>
        <dl>{Object.entries(preview.counts).map(([name, count]) => <div key={name}><dt>{label(name)}</dt><dd>{count}</dd></div>)}</dl>
        <p>{t("portability.bots_created", { names: preview.bots.map(bot => bot.name).join(t("portability.list_separator")) || t("portability.none") })}</p>
        {preview.conflicts.length > 0 && <><h6>{t("portability.conflicts")}</h6><ul>{preview.conflicts.map((c, i) => <li key={i}>{c}</li>)}</ul></>}
        <h6>{t("portability.dependencies")}</h6><ul>{preview.dependencies.map((dependency, i) => <li key={i}>{dependency}</li>)}</ul>
        {preview.vault_environment && <><h6>{t("portability.environment_title")}</h6><p>{t("portability.environment_summary", { count: preview.vault_environment.length, bytes: preview.vault_environment_bytes })}</p><ul>{preview.vault_environment.map(x => <li key={x.id}>{x.name || x.target} · {x.target} · {t("portability.inactive_restore")}</li>)}</ul></>}
        <h6>{t("portability.warnings")}</h6><ul>{preview.warnings.map((warning, i) => <li key={i}>{warning}</li>)}</ul>
      </div>}
      {preview && <div className="portability-actions"><button type="button" className="primary-button" disabled={preview.can_apply === false} onClick={() => void apply()}>{t("portability.apply")}</button></div>}
      {bundle && detailsRow([...(bundle.included.includes(ENVIRONMENT_CATEGORY) ? [t("portability.import_environment_note")] : []), t("portability.preview_note")])}
    </fieldset>
    {environmentLoaded && <div className="settings-card portability-card"><div className="portability-head"><h5>{t("portability.recovery_title")}</h5></div><div className="portability-block"><p className="portability-help">{t("portability.recovery_help")}</p></div>{environmentRecords.filter(x => x.source_category === "inactive_recovery").map(x => <div className="portability-recovery-row" key={x.id}><span>{x.name || x.target} · {x.target} · {t("portability.bytes", { count: x.bytes ?? 0 })} · {t("portability.inactive")}</span><button type="button" className="danger-link" disabled={busy} onClick={() => void deleteRecovery(x.id)}>{t("portability.recovery_delete")}</button></div>)}{detailsRow([t("portability.recovery_note")])}</div>}
    {busy && <p className="portability-status" role="status">{t("action.processing")}</p>}
    {error && <Banner tone="error" title={error}/>}
    {done && <div role="status"><Banner tone="info" title={done}/></div>}
  </section>;
}
