import {useCallback, useEffect, useRef, useState} from "react";
import {AdminSheet} from "./AdminSheets";
import {adminApi, initialOf, type AdminAccount} from "./adminApi";
import {GroupMenu, PolicyControl, useAccountGroups, useGroups} from "./AdminGroupBits";
import {adminOrgApi, CAPABILITIES, orgError, SELF_SERVE, type AdminGroup, type OrgMissing, type PolicyMap, type PolicyValue} from "./adminOrgApi";
import {useHostHasComputer} from "./computerCapability";
import {useTranslation} from "./i18n";
import {TofiIcon} from "./icons";
import {useOwnerSession} from "./OwnerSession";
import {DangerZone, SettingsCard, SettingsRow, SettingsSection} from "./settings/components";
import type {SettingsTab} from "./SettingsShell";
import type {CapabilityId} from "./types";
import "./admin-accounts.css";
import "./admin-org.css";

const SELF_SERVE_ID = "self_serve";

/** Settings > Administration > Groups: the groups, one editor with a row per capability, and who is in the group. */
export function AdminGroups({openTab}: {openTab: (tab: SettingsTab) => void}) {
  const {t} = useTranslation("settings");
  const session = useOwnerSession();
  const allowed = Boolean(session?.multi_account && session.owner?.role === "admin");
  const hostHasVm = useHostHasComputer();
  const {groups, error: loadError, reload, replace, setGroups} = useGroups(allowed);
  const [accounts, setAccounts] = useState<AdminAccount[] | null>(null);
  const [missing, setMissing] = useState<OrgMissing[]>([]);
  const {byAccount, assign, refresh: refreshMembers} = useAccountGroups(accounts, allowed);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [view, setView] = useState<"list" | "editor">("list");
  const [creating, setCreating] = useState(false);
  const [adding, setAdding] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState("");
  const busyRef = useRef(false);

  useEffect(() => {
    if (!allowed) return;
    let alive = true;
    adminApi.accounts().then(list => { if (alive) setAccounts(list); }).catch(cause => { if (alive) setError(orgError(cause)); });
    adminOrgApi.resources().then(value => { if (alive) setMissing(value.missing); }).catch(() => { /* the editor still works without the missing list */ });
    return () => { alive = false; };
  }, [allowed]);

  const selected = groups?.find(group => group.id === selectedId) ?? groups?.[0];
  useEffect(() => { if (groups && !selectedId && groups[0]) setSelectedId(groups[0].id); }, [groups, selectedId]);

  /** One change at a time; the failure text is shown on the page. */
  const run = useCallback(async (work: () => Promise<void>) => {
    if (busyRef.current) return false;
    busyRef.current = true; setBusy(true); setError(""); setSaved("");
    let ok = false;
    try { await work(); ok = true; } catch (cause) { setError(orgError(cause)); }
    busyRef.current = false; setBusy(false);
    return ok;
  }, []);

  if (!allowed) return null;
  const members = selected ? (accounts ?? []).filter(account => (byAccount[account.id]?.id ?? SELF_SERVE_ID) === selected.id && account.id in byAccount) : [];
  const readonly = selected?.id === SELF_SERVE_ID;
  const missingFor = (capability: CapabilityId) => selected ? missing.find(item => item.capability === capability && item.group_ids.includes(selected.id)) : undefined;

  const savePolicy = (capability: CapabilityId, value: PolicyValue) => {
    if (!selected) return;
    const policies = Object.fromEntries(CAPABILITIES.map(id => [id, id === capability ? value : selected.policies[id] ?? SELF_SERVE[id]])) as PolicyMap;
    void run(async () => {
      replace(await adminOrgApi.patchGroup(selected.id, {policies}));
      setSaved(t("admin.groups.saved"));
      void adminOrgApi.resources().then(value => setMissing(value.missing)).catch(() => {});
    });
  };
  const move = (ids: string[], groupId: string) => {
    const target = groups?.find(group => group.id === groupId);
    if (!target) return Promise.resolve(false);
    return run(async () => {
      await adminOrgApi.moveMembers(groupId, ids);
      assign(ids, {id: target.id, name: target.name});
      reload(); refreshMembers();
    });
  };

  const list = <div className="ag-list" role="list" aria-label={t("admin.groups.title")}>
    {groups?.map(group => <button key={group.id} type="button" role="listitem" className="ag-item" data-group={group.id} aria-current={group.id === selected?.id ? "true" : undefined}
      onClick={() => { setSelectedId(group.id); setView("editor"); setConfirmDelete(false); setSaved(""); setError(""); }}>
      <span className="ag-item-name"><strong>{group.name}</strong>{group.builtin && <span className="settings-tag" data-tag="builtin">{t("admin.groups.builtin")}</span>}</span>
      <span className="ag-item-count">{t("admin.groups.members_count", {count: group.member_count})}</span>
    </button>)}
    {!groups && !loadError && <p className="field-note" role="status">{t("admin.groups.loading")}</p>}
  </div>;

  return <section className="admin-groups" data-view={view} aria-busy={busy}>
    <p className="admin-intro">{t("admin.groups.intro")}</p>
    {(loadError || error) && <p role="alert" className="admin-error">{error || loadError}</p>}
    <div className="ag-layout">
      <div className="ag-side">
        <div className="ag-side-head"><h4>{t("admin.groups.title")}</h4><button type="button" className="secondary-button ag-new" onClick={() => setCreating(true)} disabled={busy}><TofiIcon name="plus" size={16} aria-hidden="true"/>{t("admin.groups.new")}</button></div>
        {list}
      </div>
      {selected && <div className="ag-editor" data-group={selected.id}>
        <button type="button" className="settings-ghost-button admin-back ag-back" onClick={() => setView("list")}><TofiIcon name="chevron-left" size={18} aria-hidden="true"/>{t("admin.groups.back")}</button>
        <GroupHeader group={selected} busy={busy} onRename={name => void run(async () => { replace(await adminOrgApi.patchGroup(selected.id, {name})); reload(); })}/>
        <SettingsSection id="policy" title={t("admin.groups.policy_title")} description={readonly ? t("admin.groups.policy_readonly") : t("admin.groups.policy_help")}>
          <SettingsCard className="ag-policy">
            {CAPABILITIES.map(capability => {
              const gap = missingFor(capability);
              return <div key={capability} className="ag-policy-row" data-capability={capability}>
                <SettingsRow label={t(`policy.capability.${capability}.name`)} description={t(`policy.capability.${capability}.help`)} control={
                  <PolicyControl capability={capability} value={selected.policies[capability]} hostHasVm={hostHasVm} disabled={busy || readonly} label={t(`policy.capability.${capability}.name`)} onChange={value => savePolicy(capability, value)}/>}/>
                {gap && <p className="ag-missing" role="status" data-missing={capability}><TofiIcon name="alert" size={14} aria-hidden="true"/>{t("admin.groups.resource_missing")} <button type="button" className="text-button" onClick={() => openTab("admin-resources")}>{t("admin.groups.open_resources")}</button></p>}
              </div>;
            })}
          </SettingsCard>
          <p className="field-note ag-saved" role="status">{saved}</p>
        </SettingsSection>

        <SettingsSection id="members" title={t("admin.groups.members_title", {count: members.length})} description={selected.id === SELF_SERVE_ID ? t("admin.groups.members_self_serve") : undefined}>
          <div className="admin-list-actions ag-member-actions"><button type="button" className="secondary-button" onClick={() => setAdding(true)} disabled={busy || !accounts}><TofiIcon name="user-add" size={16} aria-hidden="true"/>{t("admin.groups.add_members")}</button></div>
          <SettingsCard className="ag-members">
            {members.length === 0 && <p className="settings-card-note">{t("admin.groups.members_empty")}</p>}
            {members.map(account => <div key={account.id} className="ag-member" data-account={account.id}>
              <span className="admin-avatar" aria-hidden="true">{initialOf(account.username)}</span>
              <span className="admin-row-main"><strong><span className="admin-name" title={account.username}>{account.username}</span></strong><span className="admin-row-email">{account.email.endsWith("@account.invalid") ? t("admin.no_email") : account.email}</span></span>
              <GroupMenu groups={groups ?? []} value={selected.id} disabled={busy} label={t("admin.accounts.group_of", {name: account.username})} onPick={id => void move([account.id], id)}/>
            </div>)}
          </SettingsCard>
        </SettingsSection>

        {!selected.builtin && <DangerZone title={t("admin.groups.delete_title")}>
          <SettingsRow label={t("admin.groups.delete_label")} description={selected.member_count > 0 ? t("admin.groups.delete_in_use", {count: selected.member_count}) : t("admin.groups.delete_help")}
            control={<button type="button" className="admin-danger-button" disabled={busy || selected.member_count > 0} onClick={() => setConfirmDelete(true)}>{t("admin.groups.delete_action")}</button>}/>
        </DangerZone>}
      </div>}
    </div>
    {creating && <CreateGroupSheet onClose={() => setCreating(false)} onCreated={group => { setCreating(false); setGroups(list => list ? [...list, group] : [group]); setSelectedId(group.id); setView("editor"); }}/>}
    {adding && selected && <AddMembersSheet group={selected} accounts={accounts ?? []} byAccount={byAccount} busy={busy} onClose={() => setAdding(false)} onAdd={ids => void move(ids, selected.id).then(ok => { if (ok) setAdding(false); })}/>}
    {confirmDelete && selected && <AdminSheet title={t("admin.groups.delete_confirm_title", {name: selected.name})} onClose={() => setConfirmDelete(false)} locked={busy}>
      <p className="admin-sheet-lead">{t("admin.groups.delete_confirm_body")}</p>
      <div className="sheet-actions">
        <button type="button" className="secondary-button" disabled={busy} onClick={() => setConfirmDelete(false)}>{t("action.cancel")}</button>
        <button type="button" className="admin-danger-button" disabled={busy} onClick={() => void run(async () => {
          await adminOrgApi.deleteGroup(selected.id);
          setGroups(list => list && list.filter(item => item.id !== selected.id)); setSelectedId(null); setView("list"); setConfirmDelete(false);
        }).then(ok => { if (!ok) setConfirmDelete(false); })}>{t("admin.groups.delete_action")}</button>
      </div>
    </AdminSheet>}
  </section>;
}

function GroupHeader({group, busy, onRename}: {group: AdminGroup; busy: boolean; onRename: (name: string) => void}) {
  const {t} = useTranslation("settings");
  const [name, setName] = useState(group.name);
  useEffect(() => setName(group.name), [group.id, group.name]);
  const changed = name.trim() !== group.name && name.trim() !== "";
  return <div className="ag-head">
    <div className="ag-head-title"><h3>{group.name}</h3>{group.builtin && <span className="settings-tag" data-tag="builtin">{t("admin.groups.builtin")}</span>}</div>
    {!group.builtin && <form className="ag-rename" onSubmit={event => { event.preventDefault(); if (changed) onRename(name.trim()); }}>
      <label htmlFor={`ag-name-${group.id}`}>{t("admin.groups.name_label")}</label>
      <div className="provider-key-row">
        <input id={`ag-name-${group.id}`} value={name} maxLength={64} autoComplete="off" disabled={busy} onChange={event => setName(event.target.value)}/>
        <button type="submit" className="secondary-button" disabled={busy || !changed}>{t("admin.groups.rename")}</button>
      </div>
    </form>}
  </div>;
}

function CreateGroupSheet({onClose, onCreated}: {onClose: () => void; onCreated: (group: AdminGroup) => void}) {
  const {t} = useTranslation("settings");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit() {
    if (!name.trim() || busy) return;
    setBusy(true); setError("");
    try { onCreated(await adminOrgApi.createGroup({name: name.trim()})); }
    catch (cause) { setError(orgError(cause)); setBusy(false); }
  }
  return <AdminSheet title={t("admin.groups.create_title")} onClose={onClose} locked={busy}>
    <form className="admin-create" onSubmit={event => { event.preventDefault(); void submit(); }}>
      <label>{t("admin.groups.create_name")}<input name="group-name" value={name} maxLength={64} autoComplete="off" autoFocus disabled={busy} onChange={event => setName(event.target.value)}/></label>
      <p className="admin-sheet-note">{t("admin.groups.create_note")}</p>
      {error && <p role="alert" className="admin-error">{error}</p>}
      <div className="sheet-actions">
        <button type="button" className="secondary-button" disabled={busy} onClick={onClose}>{t("action.cancel")}</button>
        <button type="submit" className="primary-button" disabled={busy || !name.trim()}>{t("admin.groups.create_submit")}</button>
      </div>
    </form>
  </AdminSheet>;
}

/** Pick accounts that are in another group and move them here in one call. */
function AddMembersSheet({group, accounts, byAccount, busy, onClose, onAdd}: {group: AdminGroup; accounts: AdminAccount[]; byAccount: Record<string, {id: string; name: string}>; busy: boolean; onClose: () => void; onAdd: (ids: string[]) => void}) {
  const {t} = useTranslation("settings");
  const [picked, setPicked] = useState<string[]>([]);
  const candidates = accounts.filter(account => account.id in byAccount && byAccount[account.id].id !== group.id && !account.deleting);
  const toggle = (id: string) => setPicked(current => current.includes(id) ? current.filter(item => item !== id) : [...current, id]);
  return <AdminSheet title={t("admin.groups.add_title", {name: group.name})} onClose={onClose} locked={busy}>
    {candidates.length === 0 ? <p className="admin-sheet-lead">{t("admin.groups.add_none")}</p> : <fieldset className="ag-pick">
      <legend className="sr-only">{t("admin.groups.add_members")}</legend>
      {candidates.map(account => <label key={account.id} className="ag-pick-row" data-account={account.id}>
        <input type="checkbox" checked={picked.includes(account.id)} disabled={busy} onChange={() => toggle(account.id)}/>
        <span className="admin-avatar" aria-hidden="true">{initialOf(account.username)}</span>
        <span className="admin-row-main"><strong>{account.username}</strong><span className="admin-row-email">{t("admin.groups.add_current", {group: byAccount[account.id].name})}</span></span>
      </label>)}
    </fieldset>}
    <div className="sheet-actions">
      <button type="button" className="secondary-button" disabled={busy} onClick={onClose}>{t("action.cancel")}</button>
      <button type="button" className="primary-button" disabled={busy || picked.length === 0} onClick={() => onAdd(picked)}>{t("admin.groups.add_submit", {count: picked.length})}</button>
    </div>
  </AdminSheet>;
}

