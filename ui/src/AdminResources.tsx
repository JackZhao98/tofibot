import {useCallback, useEffect, useId, useMemo, useState, type FormEvent, type ReactNode} from "react";
import {ApiError} from "./api";
import {useGroups} from "./AdminGroupBits";
import {adminOrgApi, createOrgWebToolsAdapter, orgError, type OrgMissing, type OrgResource, type OrgResourceId, type OrgResourceInput} from "./adminOrgApi";
import {useHostHasComputer} from "./computerCapability";
import {useTranslation} from "./i18n";
import {formatList} from "./i18n/format";
import {ConfirmAction} from "./InteractionSystem";
import {useOwnerSession} from "./OwnerSession";
import {CardHead, formatVerified, maskedKey} from "./ProviderSettings";
import {Banner, SettingsCard, SettingsRow, SettingsSection, type BadgeState} from "./settings/components";
import type {SettingsTab} from "./SettingsShell";
import {useUserTimezone} from "./UserTimezone";
import {WebToolsSettings} from "./WebToolsSettings";
import "./admin-accounts.css";
import "./admin-org.css";

/** Settings > Administration > Organization resources: the keys that belong to this server, for accounts whose group provides them. */
export function AdminResources({openTab}: {openTab: (tab: SettingsTab) => void}) {
  const {t} = useTranslation("settings");
  const session = useOwnerSession();
  const allowed = Boolean(session?.multi_account && session.owner?.role === "admin");
  const hostHasVm = useHostHasComputer();
  const {groups} = useGroups(allowed);
  const [resources, setResources] = useState<OrgResource[] | null>(null);
  const [missing, setMissing] = useState<OrgMissing[]>([]);
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);
  const webAdapter = useMemo(createOrgWebToolsAdapter, []);
  useEffect(() => {
    if (!allowed) return;
    let alive = true;
    adminOrgApi.resources().then(value => { if (alive) { setResources(value.resources); setMissing(value.missing); setError(""); } }).catch(cause => { if (alive) setError(orgError(cause)); });
    return () => { alive = false; };
  }, [allowed, version]);
  /** A write answered with the resource: put it in the list and read the missing list again. */
  const adopt = useCallback((resource: OrgResource) => { setResources(list => list ? [...list.filter(item => item.id !== resource.id), resource] : [resource]); setVersion(value => value + 1); }, []);
  const dropped = useCallback((id: OrgResourceId, rest: OrgMissing[]) => { setResources(list => list && list.filter(item => item.id !== id)); setMissing(rest); }, []);
  if (!allowed) return null;
  const find = (id: OrgResourceId) => resources?.find(item => item.id === id);
  const loading = !resources && !error;
  return <section className="admin-resources" data-testid="admin-resources">
    <p className="admin-intro">{t("admin.org.intro")}</p>
    {error && <p role="alert" className="admin-error">{error} <button type="button" className="text-button" onClick={() => setVersion(value => value + 1)}>{t("action.retry")}</button></p>}
    {missing.length > 0 && <MissingBanner missing={missing} groupName={id => groups?.find(group => group.id === id)?.name ?? id} openGroups={() => openTab("admin-groups")}/>}

    <SettingsSection id="models" title={t("admin.org.models.title")} description={t("admin.org.models.help")}>
      <SettingsCard className="provider-list org-card"><ModelKeyCard id="provider:anthropic" title="Claude" resource={find("provider:anthropic")} loading={loading} onChange={adopt} onDropped={dropped}/></SettingsCard>
      <SettingsCard className="provider-list org-card"><ModelKeyCard id="provider:openai" title="OpenAI" resource={find("provider:openai")} loading={loading} onChange={adopt} onDropped={dropped}/></SettingsCard>
      <SettingsCard className="provider-list org-card"><CompatCard resource={find("provider:compatible")} loading={loading} onChange={adopt} onDropped={dropped}/></SettingsCard>
    </SettingsSection>

    <SettingsSection id="computers" title={t("admin.org.computers.title")} description={t("admin.org.computers.help")}>
      <SettingsCard className="provider-list org-card"><E2BCard resource={find("computer:e2b")} loading={loading} onChange={adopt} onDropped={dropped}/></SettingsCard>
      <SettingsCard className="org-vm"><SettingsRow label={t("admin.org.computers.vm_title")} description={hostHasVm ? t("admin.org.computers.vm_ready") : t("admin.org.computers.vm_none")}
        control={<button type="button" className="secondary-button" onClick={() => openTab("admin")}>{t("admin.org.computers.vm_open")}</button>}/></SettingsCard>
    </SettingsSection>

    <SettingsSection id="web" title={t("admin.org.web.title")} description={t("admin.org.web.help")}>
      <WebToolsSettings adapter={webAdapter} bare/>
    </SettingsSection>
  </section>;
}

/** A group (or an account's override) provides a capability the organization holds no resource for: runs fail with org_resource_missing until it is added. */
function MissingBanner({missing, groupName, openGroups}: {missing: OrgMissing[]; groupName: (id: string) => string; openGroups: () => void}) {
  const {t} = useTranslation("settings");
  const items = missing.map(item => {
    const who = [...item.group_ids.map(groupName), ...(item.account_ids.length ? [t("admin.org.missing_accounts", {count: item.account_ids.length})] : [])];
    return t("admin.org.missing_item", {capability: t(`policy.capability.${item.capability}.name`), who: formatList(who)});
  });
  return <div data-testid="org-missing"><Banner tone="warn" title={t("admin.org.missing_title")} action={{label: t("admin.org.missing_action"), onClick: openGroups}}>{items.join(" · ")}</Banner></div>;
}

type CardProps = {resource?: OrgResource; loading: boolean; onChange: (resource: OrgResource) => void; onDropped: (id: OrgResourceId, missing: OrgMissing[]) => void};

/** What a stored resource is: verified, stored without a provider check, rejected (with the named reason), or not set. */
function resourceState(resource: OrgResource | undefined, loading: boolean): BadgeState {
  if (loading) return "testing";
  if (!resource) return "need";
  return resource.status === "rejected" ? "bad" : resource.status === "verified" ? "ok" : "need";
}

/** The badge word for a stored resource; absent (the default "Needs setup") while nothing is stored. */
function useBadgeWord() {
  const {t} = useTranslation("settings");
  return (resource?: OrgResource) => resource ? t(`admin.org.badge.${resource.status}`) : undefined;
}

function StatusLine({resource}: {resource: OrgResource}) {
  const {t} = useTranslation("settings");
  const {timezone} = useUserTimezone();
  const at = resource.verified_at ? formatVerified(resource.verified_at, timezone) : "";
  const text = resource.status === "rejected" ? t("admin.org.status.rejected", {reason: t(`admin.org.error.${resource.error_code || "invalid_key"}` as "admin.org.error.invalid_key", {defaultValue: t("admin.org.error.generic")})})
    : resource.status === "verified" ? (at ? t("providers.verified_at", {time: at}) : t("admin.org.status.verified")) : t("admin.org.status.stored");
  return <span className={`provider-key-meta${resource.status === "rejected" ? " is-failed" : ""}`} data-status={resource.status}>{text}</span>;
}

/** Test and Remove for a stored resource; shared by every card. */
function useResourceActions(id: OrgResourceId, onChange: CardProps["onChange"], onDropped: CardProps["onDropped"]) {
  const {t} = useTranslation("settings");
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  async function test() {
    setTesting(true); setError(""); setNotice("");
    try { onChange(await adminOrgApi.testResource(id)); setNotice(t("admin.org.test_ok")); }
    catch (cause) {
      setError(orgError(cause));
      // The provider's refusal is recorded on the resource: read it back so the badge matches.
      if (cause instanceof ApiError && cause.status < 500) void adminOrgApi.resources().then(value => { const row = value.resources.find(item => item.id === id); if (row) onChange(row); }).catch(() => {});
    } finally { setTesting(false); }
  }
  async function remove() {
    try { const result = await adminOrgApi.deleteResource(id); setNotice(t("admin.org.removed")); onDropped(id, result.missing); }
    catch (cause) { throw new Error(orgError(cause)); }
  }
  return {testing, error, notice, setError, setNotice, test, remove};
}

function ResourceActions({resource, label, actions, replacing, onReplace}: {resource: OrgResource; label: string; actions: ReturnType<typeof useResourceActions>; replacing: boolean; onReplace: () => void}) {
  const {t} = useTranslation("settings");
  return <div className="provider-actions">
    <button type="button" className="text-button" aria-expanded={replacing} onClick={onReplace}>{t(resource.id === "provider:compatible" ? "admin.org.edit" : "providers.replace_key")}</button>
    <button type="button" className="text-button" data-action="test" disabled={actions.testing} onClick={() => void actions.test()}>{actions.testing ? t("webtools.testing") : t("admin.org.test")}</button>
    <ConfirmAction label={t("providers.remove")} question={t("admin.org.remove_question", {name: label})} onConfirm={actions.remove}/>
  </div>;
}

/** Claude and OpenAI: one key (and the Claude Console workspace ID for Claude), masked when stored, never shown again. */
function ModelKeyCard({id, title, resource, loading, onChange, onDropped}: CardProps & {id: "provider:anthropic" | "provider:openai"; title: string}) {
  const {t} = useTranslation("settings");
  const badge = useBadgeWord();
  const titleId = useId(), inputId = useId();
  const claude = id === "provider:anthropic";
  const [replacing, setReplacing] = useState(false);
  const [key, setKey] = useState("");
  const [workspace, setWorkspace] = useState("");
  const [saving, setSaving] = useState(false);
  const actions = useResourceActions(id, onChange, onDropped);
  const showForm = !resource || replacing;
  async function save(event: FormEvent) {
    event.preventDefault();
    if (saving || key.trim().length === 0) return;
    setSaving(true); actions.setError(""); actions.setNotice("");
    try {
      const body: OrgResourceInput = {key: key.trim(), ...(claude ? {workspace_id: workspace.trim()} : {})};
      onChange(await adminOrgApi.putResource(id, body));
      setKey(""); setWorkspace(""); setReplacing(false); actions.setNotice(t("admin.org.saved", {name: title}));
    } catch (cause) { actions.setError(orgError(cause)); }
    finally { setSaving(false); }
  }
  return <article className="detail-content provider-card org-resource" data-resource={id} aria-labelledby={titleId} aria-busy={saving || loading}>
    <CardHead id={titleId} title={title} state={resourceState(resource, loading)} status={badge(resource)} tag={t("providers.api_key_tag")}/>
    {resource && <p className={`provider-key-line${resource.status === "rejected" ? " is-failed" : ""}`} role="status"><code className="provider-key-mask">{maskedKey(resource) || t("admin.org.no_hint")}</code><StatusLine resource={resource}/></p>}
    {resource && <ResourceActions resource={resource} label={title} actions={actions} replacing={replacing} onReplace={() => { setReplacing(value => !value); actions.setNotice(""); }}/>}
    {showForm && !loading && <form className="provider-key-form" onSubmit={event => void save(event)}>
      <label htmlFor={inputId}>{t("providers.key_label", {provider: title})}</label>
      <div className="provider-key-row">
        <input id={inputId} name={`${id}-key`} type="password" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder={claude ? "sk-ant-…" : "sk-…"} value={key} disabled={saving} onChange={event => { setKey(event.target.value); actions.setError(""); }}/>
        {!claude && <button type="submit" className="primary-button" disabled={saving || !key.trim()}>{saving ? t("providers.verifying") : t("providers.save")}</button>}
        {resource && !claude && <button type="button" className="secondary-button" disabled={saving} onClick={() => setReplacing(false)}>{t("action.cancel")}</button>}
      </div>
      {claude && <>
        <label htmlFor={`${inputId}-workspace`}>{t("providers.workspace_label")}</label>
        <input id={`${inputId}-workspace`} name="org-anthropic-workspace-id" type="text" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder="wrkspc_…" value={workspace} disabled={saving} onChange={event => setWorkspace(event.target.value)}/>
        <div className="provider-key-row"><button type="submit" className="primary-button" disabled={saving || !key.trim() || !workspace.trim()}>{saving ? t("providers.verifying") : t("providers.save")}</button>{resource && <button type="button" className="secondary-button" disabled={saving} onClick={() => setReplacing(false)}>{t("action.cancel")}</button>}</div>
      </>}
      <p className="field-note">{t("admin.org.key_note")}</p>
    </form>}
    <Feedback error={actions.error} notice={actions.notice}/>
  </article>;
}

function Feedback({error, notice}: {error: string; notice: string}): ReactNode {
  return <>{error && <p className="error-text" role="alert">{error}</p>}{notice && !error && <p className="field-note" role="status">{notice}</p>}</>;
}

/** The OpenAI-compatible endpoint: address, an optional key and the model IDs. A blank key keeps the stored one for the same address. */
function CompatCard({resource, loading, onChange, onDropped}: CardProps) {
  const {t} = useTranslation("settings");
  const badge = useBadgeWord();
  const titleId = useId(), baseId = useId();
  const [editing, setEditing] = useState(false);
  const [base, setBase] = useState("");
  const [key, setKey] = useState("");
  const [models, setModels] = useState("");
  const [saving, setSaving] = useState(false);
  const actions = useResourceActions("provider:compatible", onChange, onDropped);
  const showForm = !resource || editing;
  useEffect(() => { if (editing) { setBase(resource?.config.base_url ?? ""); setModels((resource?.config.models ?? []).join(", ")); } }, [editing, resource]);
  async function save(event: FormEvent) {
    event.preventDefault();
    if (saving || !base.trim()) return;
    setSaving(true); actions.setError(""); actions.setNotice("");
    try {
      onChange(await adminOrgApi.putResource("provider:compatible", {base_url: base.trim(), models: models.split(/[\s,]+/).filter(Boolean), ...(key.trim() ? {key: key.trim()} : {})}));
      setKey(""); setEditing(false); actions.setNotice(t("admin.org.saved", {name: t("admin.org.compat_name")}));
    } catch (cause) { actions.setError(orgError(cause)); }
    finally { setSaving(false); }
  }
  return <article className="detail-content provider-card org-resource" data-resource="provider:compatible" aria-labelledby={titleId} aria-busy={saving || loading}>
    <CardHead id={titleId} title={t("admin.org.compat_name")} state={resourceState(resource, loading)} status={badge(resource)} tag={t("admin.org.endpoint_tag")}/>
    {resource && <p className={`provider-key-line${resource.status === "rejected" ? " is-failed" : ""}`} role="status"><code className="provider-key-mask">{resource.config.base_url}</code>{resource.key_hint && <code className="provider-key-mask">{maskedKey(resource)}</code>}<StatusLine resource={resource}/></p>}
    {resource && (resource.config.models ?? []).length > 0 && <p className="field-note">{t("admin.org.compat_models", {models: (resource.config.models ?? []).join(", ")})}</p>}
    {resource && <ResourceActions resource={resource} label={t("admin.org.compat_name")} actions={actions} replacing={editing} onReplace={() => { setEditing(value => !value); actions.setNotice(""); }}/>}
    {showForm && !loading && <form className="provider-key-form" onSubmit={event => void save(event)}>
      <label htmlFor={baseId}>{t("admin.org.compat_address")}</label>
      <input id={baseId} name="org-compat-address" type="url" autoComplete="off" spellCheck={false} placeholder="https://models.example.com/v1" value={base} disabled={saving} onChange={event => setBase(event.target.value)}/>
      <label htmlFor={`${baseId}-key`}>{t("admin.org.compat_key")}</label>
      <input id={`${baseId}-key`} name="org-compat-key" type="password" autoComplete="off" spellCheck={false} value={key} disabled={saving} onChange={event => setKey(event.target.value)}/>
      <label htmlFor={`${baseId}-models`}>{t("admin.org.compat_models_label")}</label>
      <input id={`${baseId}-models`} name="org-compat-models" type="text" autoComplete="off" spellCheck={false} placeholder="llama-3.3-70b, qwen3-32b" value={models} disabled={saving} onChange={event => setModels(event.target.value)}/>
      <div className="provider-key-row"><button type="submit" className="primary-button" disabled={saving || !base.trim()}>{saving ? t("providers.verifying") : t("providers.save")}</button>{resource && <button type="button" className="secondary-button" disabled={saving} onClick={() => setEditing(false)}>{t("action.cancel")}</button>}</div>
      <p className="field-note">{t("admin.org.compat_note")}</p>
    </form>}
    <Feedback error={actions.error} notice={actions.notice}/>
  </article>;
}

/** The E2B key for shared Bot computers. The template build status comes from the shared-computer work (slice 3): until then the card says so. */
function E2BCard({resource, loading, onChange, onDropped}: CardProps) {
  const {t} = useTranslation("settings");
  const badge = useBadgeWord();
  const titleId = useId(), inputId = useId();
  const [replacing, setReplacing] = useState(false);
  const [key, setKey] = useState("");
  const [template, setTemplate] = useState("");
  const [saving, setSaving] = useState(false);
  const actions = useResourceActions("computer:e2b", onChange, onDropped);
  const showForm = !resource || replacing;
  async function save(event: FormEvent) {
    event.preventDefault();
    if (saving || !key.trim()) return;
    setSaving(true); actions.setError(""); actions.setNotice("");
    try {
      onChange(await adminOrgApi.putResource("computer:e2b", {key: key.trim(), ...(template.trim() ? {template_id: template.trim()} : {})}));
      setKey(""); setTemplate(""); setReplacing(false); actions.setNotice(t("admin.org.saved", {name: "E2B"}));
    } catch (cause) { actions.setError(orgError(cause)); }
    finally { setSaving(false); }
  }
  return <article className="detail-content provider-card org-resource" data-resource="computer:e2b" aria-labelledby={titleId} aria-busy={saving || loading}>
    <CardHead id={titleId} title={t("admin.org.e2b_name")} state={resourceState(resource, loading)} status={badge(resource)} tag={t("providers.api_key_tag")}/>
    {resource && <p className={`provider-key-line${resource.status === "rejected" ? " is-failed" : ""}`} role="status"><code className="provider-key-mask">{maskedKey(resource) || t("admin.org.no_hint")}</code><StatusLine resource={resource}/></p>}
    {resource && <p className="field-note org-template" data-state="pending" data-slice="shared-computers">{resource.config.template_id ? t("admin.org.e2b_template_id", {id: resource.config.template_id}) : t("admin.org.e2b_template_pending")}</p>}
    {resource && <ResourceActions resource={resource} label={t("admin.org.e2b_name")} actions={actions} replacing={replacing} onReplace={() => { setReplacing(value => !value); actions.setNotice(""); }}/>}
    {showForm && !loading && <form className="provider-key-form" onSubmit={event => void save(event)}>
      <label htmlFor={inputId}>{t("admin.org.e2b_key")}</label>
      <input id={inputId} name="org-e2b-key" type="password" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder="e2b_…" value={key} disabled={saving} onChange={event => { setKey(event.target.value); actions.setError(""); }}/>
      <label htmlFor={`${inputId}-template`}>{t("admin.org.e2b_template_label")}</label>
      <input id={`${inputId}-template`} name="org-e2b-template" type="text" autoComplete="off" spellCheck={false} value={template} disabled={saving} onChange={event => setTemplate(event.target.value)}/>
      <div className="provider-key-row"><button type="submit" className="primary-button" disabled={saving || !key.trim()}>{saving ? t("providers.verifying") : t("providers.save")}</button>{resource && <button type="button" className="secondary-button" disabled={saving} onClick={() => setReplacing(false)}>{t("action.cancel")}</button>}</div>
      <p className="field-note">{t("admin.org.key_note")}</p>
    </form>}
    <Feedback error={actions.error} notice={actions.notice}/>
  </article>;
}
