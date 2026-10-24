import { useCallback, useEffect, useRef, useState } from "react";
import { api, request } from "../api";
import { ConfirmAction, Disclosure } from "../InteractionSystem";
import { TofiIcon } from "../icons";
import { skillCatalog } from "../skillCatalog";
import { Banner } from "./components";
import { i18n, useTranslation } from "../i18n";
import { formatDateTime } from "../i18n/format";
import { CATS, EmptyState } from "../EmptyCat";
import { installedLine, readSkillFolder, type LibraryBundle, type LibraryListing, type LibrarySkill, type SkillVersion } from "../skillLibrary";
import "../extensions-system.css";
import "../skill-library.css";

const post = (body: unknown) => ({ method: "POST", body: JSON.stringify(body) });

/** Which Bots get a new Skill (decision 0058): every active Bot, a chosen few, or none. */
export type EnableChoice = { mode: "all" } | { mode: "bots"; ids: string[] };
export type BotRef = { id: string; name: string };
const enableField = (choice: EnableChoice): "all" | string[] => choice.mode === "all" ? "all" : choice.ids;

/** The install choice: one primary button for every Bot, a secondary one that opens the checklist (empty pick = install for none). */
export function EnableChoicePicker({ bots, busy, onChoose, onCancel }: { bots: BotRef[]; busy: boolean; onChoose: (choice: EnableChoice) => void; onCancel?: () => void }) {
  const { t } = useTranslation("extensions");
  const [choosing, setChoosing] = useState(false);
  const [picked, setPicked] = useState<string[]>([]);
  if (!choosing) return <div className="extension-actions skill-enable" data-step="default">
    <button type="button" className="primary-button" disabled={busy} onClick={() => onChoose({ mode: "all" })}>{busy ? t("skills.installing") : t("skills.library.enable_all")}</button>
    {bots.length > 0 && <button type="button" className="secondary-button" disabled={busy} onClick={() => setChoosing(true)}>{t("skills.library.choose_bots")}</button>}
    {onCancel && <button type="button" className="secondary-button" disabled={busy} onClick={onCancel}>{t("action.cancel")}</button>}
  </div>;
  return <div className="skill-enable" data-step="choose">
    <fieldset className="skill-enable-bots" disabled={busy}>
      <legend>{t("skills.library.choose_title")}</legend>
      {bots.map(bot => <label key={bot.id}><input type="checkbox" checked={picked.includes(bot.id)} onChange={event => setPicked(current => event.target.checked ? [...current, bot.id] : current.filter(id => id !== bot.id))} /><span>{bot.name}</span></label>)}
    </fieldset>
    <div className="extension-actions">
      <button type="button" className="primary-button" disabled={busy} onClick={() => onChoose({ mode: "bots", ids: picked })}>{picked.length ? t("skills.library.add_enable", { count: picked.length }) : t("skills.library.add_without")}</button>
      <button type="button" className="secondary-button" disabled={busy} onClick={() => setChoosing(false)}>{t("skills.library.back")}</button>
    </div>
  </div>;
}

function skillTitle(skill: { name: string }): string {
  return skillCatalog.find(item => item.name === skill.name)?.title ?? skill.name;
}
function skillText(skill: { name: string; description: string }): string {
  return skillCatalog.find(item => item.name === skill.name)?.description ?? skill.description;
}

function VersionList({ skill, busy, onRollback }: { skill: LibrarySkill; busy: boolean; onRollback: (skill: string, version: number) => void }) {
  const { t } = useTranslation("extensions");
  const versions = skill.versions ?? [];
  const current = skill.version ?? 1;
  return <Disclosure title={t("skills.library.versions", { count: versions.length })}>
    <ul className="skill-version-list">
      {versions.map((version: SkillVersion) => <li key={version.version} data-current={version.current ? "true" : undefined}>
        <span className="skill-version-copy">
          <strong>{t("skills.library.version", { version: version.version })}{version.current && <span className="skill-tag">{t("skills.library.current")}</span>}</strong>
          <span>{installedLine(version.created_by, version.created_at)}</span>
          {version.note && <span className="skill-version-note">{version.note}</span>}
        </span>
        {!version.current && <button type="button" className="text-button" disabled={busy} onClick={() => onRollback(skill.name, version.version)}>
          {version.version < current ? t("skills.library.roll_back") : t("skills.library.roll_forward")}
        </button>}
      </li>)}
    </ul>
  </Disclosure>;
}

function SkillMeta({ skill }: { skill: LibrarySkill }) {
  const { t } = useTranslation("extensions");
  const parts: string[] = [];
  if (skill.builtin) parts.push(t("skills.library.official_note"));
  else {
    parts.push(t("skills.library.version", { version: skill.version ?? 1 }));
    parts.push(installedLine(skill.installed_by, skill.installed_at));
  }
  parts.push(t("skills.library.on_for", { on: skill.enabled_bots, total: skill.bots_total, count: skill.bots_total }));
  return <p className="skill-meta">{parts.join(" · ")}</p>;
}

type RowHandlers = { busy: boolean; onRemove: (name: string) => Promise<void>; onRollback: (name: string, version: number) => void };

function SkillRow({ skill, member, handlers }: { skill: LibrarySkill; member?: boolean; handlers: RowHandlers }) {
  const { t } = useTranslation("extensions");
  return <article className="extension-card skill-row" data-builtin={skill.builtin ? "true" : undefined} data-member={member ? "true" : undefined} data-skill={skill.name}>
    <strong>{skillTitle(skill)}
      {skill.builtin && <span className="skill-tag">{t("skills.builtin.tag")}</span>}
      {skill.updated && <span className="skill-tag" data-tone="updated" title={skill.updated_at ? formatDateTime(skill.updated_at) : undefined}>{t("skills.library.updated")}</span>}
    </strong>
    <p>{skillText(skill)}</p>
    <SkillMeta skill={skill} />
    {skill.versions && skill.versions.length > 1 && <VersionList skill={skill} busy={handlers.busy} onRollback={handlers.onRollback} />}
    {!skill.builtin && !member && <div className="extension-actions">
      <ConfirmAction label={t("skills.remove")} question={t("skills.remove_confirm")} disabled={handlers.busy} onConfirm={() => handlers.onRemove(skill.name)} />
    </div>}
  </article>;
}

function BundleRow({ bundle, members, handlers, onRemoveBundle, defaultOpen }: { bundle: LibraryBundle; members: LibrarySkill[]; handlers: RowHandlers; onRemoveBundle: (id: string) => Promise<void>; defaultOpen?: boolean }) {
  const { t } = useTranslation("extensions");
  const [open, setOpen] = useState(Boolean(defaultOpen));
  const enabled = members.length ? Math.max(...members.map(member => member.enabled_bots)) : 0;
  const total = members[0]?.bots_total ?? 0;
  return <article className="extension-card skill-row skill-bundle" data-bundle={bundle.id}>
    <strong>{bundle.id}<span className="skill-tag">{t("skills.library.bundle_tag", { count: bundle.skills.length })}</span></strong>
    {bundle.description && <p>{bundle.description}</p>}
    <p className="skill-meta">{[bundle.version ? t("skills.library.version_label", { version: bundle.version }) : "", installedLine(bundle.installed_by, bundle.installed_at), t("skills.library.on_for", { on: enabled, total, count: total })].filter(Boolean).join(" · ")}</p>
    <div className="extension-actions">
      <button type="button" className="text-button skill-expand" aria-expanded={open} onClick={() => setOpen(value => !value)}>
        {open ? t("skills.library.hide_members") : t("skills.library.show_members", { count: bundle.skills.length })}
        <TofiIcon name="chevron-down" size={14} />
      </button>
      <ConfirmAction label={t("skills.remove")} question={t("skills.library.remove_bundle_confirm", { count: bundle.skills.length })} disabled={handlers.busy} onConfirm={() => onRemoveBundle(bundle.id)} />
    </div>
    {open && <div className="skill-members">{members.map(member => <SkillRow key={member.name} skill={member} member handlers={handlers} />)}</div>}
  </article>;
}

export type SkillLibraryViewProps = {
  listing: LibraryListing; loaded: boolean; loading: boolean; busy: boolean; error: string; notice: string;
  form: boolean; formState: AddFormState;
  onToggleForm: () => void; onFormChange: (next: Partial<AddFormState>) => void; onSubmitForm: (choice: EnableChoice) => void;
  onPickFolder: (files: File[]) => void; onStarter: (name: string) => void;
  onRemove: (name: string) => Promise<void>; onRemoveBundle: (id: string) => Promise<void>; onRollback: (name: string, version: number) => void;
  onReload: () => void; showReload: boolean; openBundles?: string[];
  bots: BotRef[]; pending: string | null; onCancelPending: () => void; onConfirmPending: (choice: EnableChoice) => void;
};
export type AddFormState = { name: string; text: string };

/** Settings › Skills: add in one click, then one row per skill or bundle. Presentational; SkillLibrary feeds it. */
export function SkillLibraryView(props: SkillLibraryViewProps) {
  const { t } = useTranslation("extensions");
  const { listing } = props;
  const handlers: RowHandlers = { busy: props.busy, onRemove: props.onRemove, onRollback: props.onRollback };
  const folderInput = useRef<HTMLInputElement>(null);
  const bundled = new Set(listing.bundles.flatMap(bundle => bundle.skills));
  const loose = listing.skills.filter(skill => !bundled.has(skill.name));
  const official = loose.filter(skill => skill.builtin);
  const installed = loose.filter(skill => !skill.builtin);
  const have = new Set(listing.skills.map(skill => skill.name));
  const starters = skillCatalog.filter(item => !have.has(item.name));
  const empty = props.loaded && !props.error && installed.length === 0 && listing.bundles.length === 0;
  return <div className="extension-panel skill-library">
    {props.error && !props.form && props.pending === null && <Banner tone="error" title={props.error} />}
    {props.notice && <p className="extension-notice" role="status">{props.notice}</p>}
    <div className="extension-toolbar">
      <span className="extension-global">{t("skills.library.intro")}</span>
      <span className="skill-add-actions">
        <input aria-label={t("skills.folder_label")} hidden type="file" multiple ref={el => { folderInput.current = el; el?.setAttribute("webkitdirectory", ""); }}
          onChange={event => { const files = Array.from(event.target.files ?? []); event.target.value = ""; if (files.length) props.onPickFolder(files); }} />
        <button type="button" className="secondary-button" disabled={props.busy} onClick={() => folderInput.current?.click()}><TofiIcon name="folder-open" size={16} />{t("skills.library.add_folder")}</button>
        <button type="button" className="secondary-button" disabled={props.busy} aria-expanded={props.form} onClick={props.onToggleForm}><TofiIcon name="plus" size={16} />{t("skills.library.add_paste")}</button>
      </span>
    </div>
    {props.pending !== null && <section className="extension-form skill-pending" aria-label={t("skills.library.pending_title", { name: props.pending })}>
      <strong>{t("skills.library.pending_title", { name: props.pending })}</strong>
      {props.error && <p className="error-text" role="alert">{props.error}</p>}
      <EnableChoicePicker bots={props.bots} busy={props.busy} onChoose={props.onConfirmPending} onCancel={props.onCancelPending} />
    </section>}
    {props.form && <form className="extension-form" aria-busy={props.busy} onSubmit={event => event.preventDefault()}>
      <fieldset className="form-fields" disabled={props.busy}>
        <label>{t("skills.name")}<input value={props.formState.name} onChange={event => props.onFormChange({ name: event.target.value })} placeholder="research-assistant" /></label>
        <label>SKILL.md<textarea rows={9} value={props.formState.text} onChange={event => props.onFormChange({ text: event.target.value })} placeholder={"---\nname: research-assistant\ndescription: Research and verify sources\n---\nWorkflow instructions…"} /></label>
        <p className="field-note">{t("skills.support_note")}</p>
        {props.error && <p className="error-text" role="alert">{props.error}</p>}
        <EnableChoicePicker bots={props.bots} busy={props.busy} onChoose={props.onSubmitForm} onCancel={props.onToggleForm} />
      </fieldset>
    </form>}
    {props.loading && !props.loaded && <p className="field-note" role="status">{t("skills.loading")}</p>}
    {!props.form && props.pending === null && props.loaded && starters.length > 0 && <section className="integration-section skill-starters">
      <h3>{t("skills.starters")}</h3>
      <div className="integration-grid">{starters.map(skill => <button className="integration-tile" disabled={props.busy} key={skill.name} onClick={() => props.onStarter(skill.name)}>
        <span className="service-mark"><TofiIcon name="skill" size={20} /></span><span><strong>{skill.title}</strong><small>{skill.description}</small></span><TofiIcon name="plus" size={16} />
      </button>)}</div>
    </section>}
    {empty && !props.form && <EmptyState name="skills-empty" className="extension-empty" look={CATS.taro} pose="curious" size={72} title={t("skills.empty")}><p>{t("skills.library.empty_hint")}</p></EmptyState>}
    <div className="extension-list">
      {listing.bundles.map(bundle => <BundleRow key={bundle.id} bundle={bundle} members={listing.skills.filter(skill => skill.bundle_id === bundle.id)} handlers={handlers} onRemoveBundle={props.onRemoveBundle} defaultOpen={props.openBundles?.includes(bundle.id)} />)}
      {installed.map(skill => <SkillRow key={skill.name} skill={skill} handlers={handlers} />)}
      {official.length > 0 && <h3 className="skill-group-title">{t("skills.library.official_title")}</h3>}
      {official.map(skill => <SkillRow key={skill.name} skill={skill} handlers={handlers} />)}
    </div>
    {props.showReload && <button className="extension-refresh text-button" disabled={props.busy || props.loading} onClick={props.onReload}>{t("skills.reload")}</button>}
  </div>;
}

const message = (error: unknown): string => {
  const value = error instanceof Error ? error.message : String(error);
  if (value === "skill already exists") return i18n.t("extensions:skills.error.name_taken");
  return value;
};

/** Settings › Skills (decision 0058): the one workspace library. A Bot's own set lives in its settings panel. */
export function SkillLibrary({ refreshToken = 0, debug = false }: { refreshToken?: number; debug?: boolean }) {
  const { t } = useTranslation("extensions");
  const [listing, setListing] = useState<LibraryListing>({ skills: [], bundles: [] });
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState(false);
  const [formState, setFormState] = useState<AddFormState>({ name: "", text: "" });
  const [bots, setBots] = useState<BotRef[]>([]);
  const [pending, setPending] = useState<{ title: string; run: (choice: EnableChoice) => Promise<void> } | null>(null);
  const version = useRef(0);
  const refresh = useCallback(async () => {
    const mine = ++version.current;
    try {
      const value = await request<LibraryListing>("/api/extensions/skills");
      if (mine !== version.current) return;
      setListing({ skills: value.skills ?? [], bundles: value.bundles ?? [], diagnostics: value.diagnostics });
      setLoaded(true);
      setError(value.diagnostics?.map(item => item.message).join(i18n.t("extensions:diagnostics_separator")) ?? "");
    } catch (cause) {
      if (mine === version.current) setError(message(cause));
    } finally {
      if (mine === version.current) setLoading(false);
    }
  }, []);
  useEffect(() => {
    let alive = true;
    api.bots().then(result => { if (alive) setBots(result.bots.filter(bot => !bot.archived).map(bot => ({ id: bot.id, name: bot.name }))); }).catch(() => {});
    return () => { alive = false; };
  }, [refreshToken]);
  useEffect(() => { void refresh(); return () => { version.current++; }; }, [refresh, refreshToken]);
  useEffect(() => {
    const focus = () => { if (!document.hidden) void refresh(); };
    window.addEventListener("focus", focus);
    document.addEventListener("visibilitychange", focus);
    return () => { window.removeEventListener("focus", focus); document.removeEventListener("visibilitychange", focus); };
  }, [refresh]);

  async function act(run: () => Promise<unknown>, success?: string): Promise<boolean> {
    if (busy) return false;
    setBusy(true); setError(""); setNotice("");
    try { await run(); setNotice(success ?? t("skills.saved")); await refresh(); return true; }
    catch (cause) { setError(message(cause)); return false; }
    finally { setBusy(false); }
  }
  const addedNotice = (choice: EnableChoice) => choice.mode === "all" ? t("skills.library.added_all")
    : choice.ids.length ? t("skills.library.added_some", { count: choice.ids.length }) : t("skills.library.added_notice");
  /** One install: files go to the route with the owner's choice of Bots; the form or pending panel closes only on success. */
  const install = (url: string, body: Record<string, unknown>, choice: EnableChoice) =>
    act(() => request(url, post({ ...body, enable: enableField(choice) })), addedNotice(choice));
  async function pickFolder(files: File[]) {
    if (busy) return;
    try {
      const picked = await readSkillFolder(files);
      setError(""); setNotice(""); setForm(false);
      setPending({ title: picked.name, run: async choice => {
        if (await (picked.kind === "bundle" ? install("/api/extensions/skill-bundles", { files: picked.files }, choice) : install("/api/extensions/skills", { name: picked.name, files: picked.files }, choice))) setPending(null);
      } });
    } catch (cause) {
      const key = cause instanceof Error ? cause.message : "";
      setError(["no_skill_md", "too_large", "too_many_files", "bad_plugin", "no_plugin_manifest"].includes(key) ? t(`skills.error.${key}` as "skills.error.too_large", { max: 256 }) : message(cause));
    }
  }
  function submitForm(choice: EnableChoice) {
    if (busy) return;
    if (!/^[a-zA-Z0-9_-]+$/.test(formState.name) || !formState.text.trim()) { setError(t("skills.error.missing_content")); return; }
    void install("/api/extensions/skills", { name: formState.name, files: { "SKILL.md": formState.text } }, choice).then(ok => { if (ok) { setForm(false); setFormState({ name: "", text: "" }); } });
  }
  return <SkillLibraryView listing={listing} loaded={loaded} loading={loading} busy={busy} error={error} notice={notice}
    form={form} formState={formState} showReload={Boolean(error || debug)}
    bots={bots} pending={pending?.title ?? null} onCancelPending={() => { setError(""); setPending(null); }} onConfirmPending={choice => void pending?.run(choice)}
    onToggleForm={() => { setError(""); setPending(null); setForm(value => !value); }}
    onFormChange={next => setFormState(current => ({ ...current, ...next }))} onSubmitForm={submitForm}
    onPickFolder={files => void pickFolder(files)}
    onStarter={name => { const item = skillCatalog.find(entry => entry.name === name); if (item) { setError(""); setNotice(""); setForm(false); setPending({ title: item.title, run: async choice => { if (await install("/api/extensions/skills", { name: item.name, files: { "SKILL.md": item.body } }, choice)) setPending(null); } }); } }}
    onRemove={async name => { await act(() => request(`/api/extensions/skills/${encodeURIComponent(name)}`, { method: "DELETE" }), t("skills.removed")); }}
    onRemoveBundle={async id => { await act(() => request(`/api/extensions/skill-bundles/${encodeURIComponent(id)}`, { method: "DELETE" }), t("skills.removed")); }}
    onRollback={(name, versionNumber) => void act(() => request(`/api/extensions/skills/${encodeURIComponent(name)}/rollback`, post({ version: versionNumber })), t("skills.library.rolled_back"))}
    onReload={() => { setLoading(true); void refresh(); }} />;
}
