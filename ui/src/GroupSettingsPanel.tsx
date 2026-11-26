import { Button } from "./ds/Button/Button";
import { useEffect, useRef, useState } from "react";
import { api, ApiError } from "./api";
import { Banner } from "./ds/Banner/Banner";
import { SettingsSection } from "./ds/SettingsSection/SettingsSection";
import { SettingsShell as Frame } from "./ds/SettingsShell/SettingsShell";
import { TextInput } from "./ds/TextInput/TextInput";
import { MemberPicker } from "./MemberPicker";
import { SettingsCard, SettingsRow } from "./settings/components";
import { DirtyBar, LeaveConfirm } from "./CoverForm";
import { useCover, useCoverGuard } from "./coverGuard";
import { errorText } from "./errorText";
import { useTranslation } from "./i18n";
import { takeSettingsSection } from "./settings/pendingSection";
import { jumpWhenReady } from "./settings/sectionJump";
import type { Bot, Conversation, TaskImpact } from "./types";
import "./group-settings.css";

/** What removing members ends: open tasks they gave or hold, and waits they hold. Shown before saving; a failed read shows nothing. */
function RemovalImpact({ groupId, removed }: { groupId: string; removed: { id: string; name: string }[] }) {
  const { t } = useTranslation("chat");
  const key = removed.map(bot => bot.id).join("\u0000");
  const [impacts, setImpacts] = useState<{ id: string; name: string; impact: TaskImpact }[]>([]);
  useEffect(() => {
    if (!key) { setImpacts([]); return; }
    const controller = new AbortController();
    const names = new Map(removed.map(bot => [bot.id, bot.name]));
    Promise.all(key.split("\u0000").map(id => api.botTaskImpact(id, groupId, controller.signal).then(impact => ({ id, name: names.get(id) ?? "", impact })).catch(() => null)))
      .then(rows => { if (!controller.signal.aborted) setImpacts(rows.filter((row): row is { id: string; name: string; impact: TaskImpact } => !!row && row.impact.given_tasks + row.impact.held_tasks + row.impact.open_waits > 0)); });
    return () => controller.abort();
    // removed is derived from key; names only label the result.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [groupId, key]);
  if (!impacts.length) return null;
  return <Banner tone="warning" className="gs-note">{impacts.map(({ id, name, impact }) => <span key={id} className="gs-note-line">
    {impact.given_tasks > 0 && <>{t("members.impact_given", { name, count: impact.given_tasks })} </>}
    {impact.held_tasks > 0 && <>{t("members.impact_held", { name, count: impact.held_tasks })} </>}
    {impact.open_waits > 0 && t("members.impact_waits", { name, count: impact.open_waits })}
  </span>)}</Banner>;
}

const sameSet = (a: readonly string[], b: readonly string[]) => [...a].sort().join("\u0000") === [...b].sort().join("\u0000");

/**
 * Group settings, the cover (docs/design/widgets-and-sidebar.md section 5.1, the second half of the old Members panel): the group's
 * name and members, a link to the group's memory, and Delete group. Name and members are one form with the same dirty bar as Bot
 * settings; an edit made by someone else meanwhile is kept apart until Reload. The roster to look at is the Members widget.
 * Lazy chunk `groupSettings`.
 */
export function GroupSettingsPanel({ conversation, bots, onSaved, onReload, onOpenMemory, onDelete }: {
  conversation: Conversation;
  bots: Bot[];
  onSaved: (id: string, input: { name?: string; bot_ids?: string[]; expected_bot_ids?: string[]; expected_name?: string }) => Promise<Conversation>;
  onReload: (id: string) => Promise<Conversation>;
  onOpenMemory: () => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation("chat");
  const cover = useCover();
  const [name, setName] = useState(conversation.name);
  const [selectedIds, setSelectedIds] = useState<string[]>(conversation.bot_ids);
  const [saving, setSaving] = useState(false);
  const [reloading, setReloading] = useState(false);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);
  const [justSaved, setJustSaved] = useState(false);
  const baselineIds = useRef<string[]>(conversation.bot_ids);
  const baselineName = useRef(conversation.name);
  const panelConversationId = useRef(conversation.id);

  // Workspace SSE refreshes replace the Conversation prop. Keep an in-progress edit intact; a new conversation is the only event that
  // resets this form, and a pristine form follows the server.
  useEffect(() => {
    if (panelConversationId.current !== conversation.id) {
      panelConversationId.current = conversation.id;
      setName(conversation.name);
      setSelectedIds(conversation.bot_ids);
      baselineIds.current = conversation.bot_ids;
      baselineName.current = conversation.name;
      setError("");
      setConflict(false);
      return;
    }
    const localDirty = name.trim() !== baselineName.current || !sameSet(selectedIds, baselineIds.current);
    if (!localDirty && !saving) {
      setName(conversation.name);
      setSelectedIds(conversation.bot_ids);
      baselineIds.current = conversation.bot_ids;
      baselineName.current = conversation.name;
      setError("");
      setConflict(false);
    }
  }, [conversation.id, conversation.name, conversation.bot_ids, name, selectedIds, saving]);
  useEffect(() => {
    if (!justSaved) return;
    const timer = window.setTimeout(() => setJustSaved(false), 2400);
    return () => window.clearTimeout(timer);
  }, [justSaved]);

  const changed = name.trim() !== baselineName.current || !sameSet(selectedIds, baselineIds.current);
  const valid = name.trim().length > 0 && selectedIds.length >= 2 && selectedIds.length <= 8;
  const [asking, setAsking] = useState(false);
  useCoverGuard(changed, () => setAsking(true));

  function toggleMember(id: string) {
    setError("");
    setSelectedIds(current => current.includes(id) ? current.filter(memberId => memberId !== id) : current.length >= 8 ? current : [...current, id]);
  }

  async function save() {
    if (saving || !valid || !changed) return;
    setSaving(true);
    setError("");
    try {
      const next = await onSaved(conversation.id, { name: name.trim(), bot_ids: selectedIds, expected_bot_ids: baselineIds.current, expected_name: baselineName.current });
      setName(next.name);
      setSelectedIds(next.bot_ids);
      baselineName.current = next.name;
      baselineIds.current = next.bot_ids;
      setConflict(false);
      setJustSaved(true);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409 && cause.code !== "group_busy" && cause.code !== "conversation_busy") {
        setError(t("members.conflict"));
        setConflict(true);
      } else if (cause instanceof ApiError && (cause.status === 423 || cause.code === "group_busy" || cause.code === "conversation_busy")) {
        setError(t("members.busy"));
      } else {
        setError(errorText(cause));
      }
    } finally {
      setSaving(false);
    }
  }

  async function reload() {
    if (saving || reloading) return;
    setReloading(true);
    setError("");
    try {
      const fresh = await onReload(conversation.id);
      setName(fresh.name);
      setSelectedIds(fresh.bot_ids);
      baselineName.current = fresh.name;
      baselineIds.current = fresh.bot_ids;
      setConflict(false);
    } catch (cause) {
      setError(errorText(cause));
    } finally {
      setReloading(false);
    }
  }

  // A link to one section (`/g/<id>/settings#members`) scrolls to it and flashes it.
  useEffect(() => {
    const target = takeSettingsSection("group-settings");
    return target ? jumpWhenReady(() => document.querySelector(".group-settings"), target) : undefined;
  }, []);

  const discard = () => { setName(baselineName.current); setSelectedIds(baselineIds.current); setError(""); setConflict(false); };
  const busy = saving || reloading;
  const candidates = bots.filter(bot => !bot.archived || baselineIds.current.includes(bot.id) || selectedIds.includes(bot.id));
  return <div className="group-settings" aria-busy={busy}>
    <Frame flush groups={[]} active="" onNavigate={() => {}} navLabel={t("members.members")} title={name.trim() || conversation.name}
      status={justSaved ? t("cover.saved") : undefined} hooks={{ root: "gs-frame", content: "gs-content" }}>
      <div className="gs-scroll">
        <div className="gs-column">
          <SettingsCard>
            <div data-section="general"><SettingsRow label={t("members.name")} labelFor="gs-name" control={
              <TextInput id="gs-name" value={name} maxLength={200} disabled={busy} onChange={event => setName(event.target.value)} />} /></div>
          </SettingsCard>
          <SettingsSection title={t("members.members")} actions={<span className="gs-count">{selectedIds.length}/8</span>} description={t("members.pick_hint")}>
            <div data-section="members">
              <MemberPicker bots={candidates} selected={selectedIds} disabled={busy} label={t("members.pick")} onToggle={toggleMember} />
            </div>
          </SettingsSection>
          <RemovalImpact groupId={conversation.id} removed={bots.filter(bot => baselineIds.current.includes(bot.id) && !selectedIds.includes(bot.id)).map(bot => ({ id: bot.id, name: bot.name }))} />
          <SettingsCard>
            <SettingsRow label={t("groupSettings.memory_title")} description={t("groupSettings.memory_hint")} link={{ onOpen: onOpenMemory }} />
            <SettingsRow label={t("header.delete_group")} description={t("groupSettings.delete_hint")} inline control={<Button variant="danger" size="sm" onClick={onDelete}>{t("header.delete_group")}</Button>} />
          </SettingsCard>
        </div>
      </div>
    </Frame>
    <DirtyBar open={changed} busy={busy} error={error} canSave={valid} onDiscard={discard} onSave={() => void save()}>
      {conflict && <Button variant="ghost" size="sm" className="cover-dirty-discard" disabled={reloading} onClick={() => void reload()}>{reloading ? t("members.reloading") : t("members.reload")}</Button>}
    </DirtyBar>
    {asking && <LeaveConfirm busy={busy} onKeep={() => setAsking(false)} onLeave={() => { setAsking(false); cover.close(); }} />}
  </div>;
}
