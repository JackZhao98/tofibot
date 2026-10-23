import { useEffect, useRef, useState } from "react";
import { ApiError, request } from "./api";
import { BotAvatar } from "./BotAvatar";
import { BotAvatarPicker } from "./BotAvatarPicker";
import { BotInspector } from "./BotInspector";
import { BotSecrets } from "./BotSecrets";
import { BotSkills } from "./BotSkills";
import { buildBotPackage, downloadBotPackage } from "./botPackage";
import { getBotAvatarConfig, saveBotAvatarConfig } from "./avatarStore";
import { memoryDisplay } from "./displayMetadata";
import { errorText } from "./errorText";
import { useTranslation } from "./i18n";
import { Disclosure } from "./InteractionSystem";
import { TofiIcon as Icon } from "./icons";
import { followsGlobal } from "./modelCatalog";
import { ModelFields } from "./ModelSettings";
import { PrimaryBadge } from "./PrimaryAssistant";
import type { Bot, Conversation, Memory } from "./types";
import { WorkPreview } from "./workAgenda";
import { LazyGroupAvatar as GroupAvatar } from "./LazyGroupAvatar";
import type { BotSkillListing } from "./skillLibrary";

/** The share file names the library Skills the Bot has on, and nothing else about them. */
async function shareBotPackage(bot: Bot) {
  let names: string[] = [];
  try {
    const listing = await request<BotSkillListing>(`/api/bots/${encodeURIComponent(bot.id)}/skills`);
    names = (listing.skills ?? []).filter(skill => skill.enabled && !skill.builtin).map(skill => skill.name);
  } catch { /* a share file without skill names is still a valid share file */ }
  downloadBotPackage(buildBotPackage(bot, names));
}

export function MembersPanel({ conversation, bots, onClose, onOpen, onSaved, onReload, onOpenWork, refreshToken }: {
  onOpenWork: () => void;
  refreshToken: number;
  conversation: Conversation;
  bots: Bot[];
  onClose: () => void;
  onOpen: (id: string) => void;
  onSaved: (id: string, input: { name?: string; bot_ids?: string[]; expected_bot_ids?: string[]; expected_name?: string }) => Promise<Conversation>;
  onReload: (id: string) => Promise<Conversation>;
}) {
  const { t } = useTranslation("chat");
  const [name, setName] = useState(conversation.name);
  const [selectedIds, setSelectedIds] = useState<string[]>(conversation.bot_ids);
  const [saving, setSaving] = useState(false);
  const [reloading, setReloading] = useState(false);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);
  const baselineIds = useRef<string[]>(conversation.bot_ids);
  const baselineName = useRef(conversation.name);
  const panelConversationId = useRef(conversation.id);

  // Workspace SSE refreshes replace the Conversation prop. Keep an in-progress
  // edit intact; a new conversation is the only event that resets this form.
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
    // A workspace event should update a pristine panel. Once the user starts
    // typing/selecting, retain the local draft until save or explicit reload.
    const localDirty = name.trim() !== baselineName.current || [...selectedIds].sort().join("\u0000") !== [...baselineIds.current].sort().join("\u0000");
    if (!localDirty && !saving) {
      setName(conversation.name);
      setSelectedIds(conversation.bot_ids);
      baselineIds.current = conversation.bot_ids;
      baselineName.current = conversation.name;
      setError("");
      setConflict(false);
    }
  }, [conversation.id, conversation.name, conversation.bot_ids, name, selectedIds, saving]);

  const changed = name.trim() !== baselineName.current || [...selectedIds].sort().join("\u0000") !== [...baselineIds.current].sort().join("\u0000");
  const valid = name.trim().length > 0 && selectedIds.length >= 2 && selectedIds.length <= 8;

  function toggleMember(id: string) {
    setError("");
    setSelectedIds((current) => current.includes(id) ? current.filter((memberId) => memberId !== id) : current.length >= 8 ? current : [...current, id]);
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
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

  return <div className="detail-content member-details">
    <div className="detail-heading"><div className="members-heading"><span className="members-heading-avatar" aria-hidden="true"><GroupAvatar ids={conversation.bot_ids} iconSize={20} /></span><h2>{t("members.title")}</h2></div><button className="close-button" aria-label={t("members.close")} disabled={saving || reloading} onClick={onClose}><Icon name="close" size={18} /></button></div>
    <nav className="bot-v2-rows members-v2-rows" aria-label={t("members.title")}><WorkPreview disabled={saving || reloading} scope={{type:"conversations",id:conversation.id}} refreshToken={refreshToken} onOpen={onOpenWork} leading={<span className="bot-v2-row-icon" aria-hidden="true"><Icon name="checklist" size={18}/></span>}/></nav>
    <form className="members-manage-form" aria-busy={saving || reloading} onSubmit={(event) => void submit(event)}>
      <label>{t("members.name")}<input aria-label={t("members.name")} value={name} maxLength={200} disabled={saving || reloading} onChange={(event) => setName(event.target.value)} /></label>
      <div className="members-manage-label"><span>{t("members.members")}</span><span className="muted small-text">{selectedIds.length}/8</span></div>
      <div className="member-picker members-manage-picker" aria-label={t("members.pick")}>
        {bots.filter(bot => !bot.archived || baselineIds.current.includes(bot.id) || selectedIds.includes(bot.id)).map((bot) => <div key={bot.id} className={`member-option member-manage-row ${selectedIds.includes(bot.id) ? "selected" : ""}`}>
          <label><input type="checkbox" checked={selectedIds.includes(bot.id)} disabled={saving || reloading || (!selectedIds.includes(bot.id) && selectedIds.length >= 8)} onChange={() => toggleMember(bot.id)} />
          <BotAvatar id={bot.id} mini /><span>{bot.name}</span>{bot.is_primary && <PrimaryBadge />}</label>
          <button type="button" className="text-button" disabled={saving || reloading} aria-label={t("members.open_dm", { name: bot.name })} onClick={() => onOpen(bot.dm_conversation_id)}>{t("members.dm")}</button>
        </div>)}
      </div>
      <p className="field-note">{t("members.pick_hint")}</p>
      {error && <div className="members-manage-error" role="alert"><p className="error-text">{error}</p>{conflict && <button type="button" className="secondary-button" disabled={reloading} onClick={() => void reload()}>{reloading ? t("members.reloading") : t("members.reload")}</button>}</div>}
      <button className="primary-button" disabled={saving || reloading || !valid || !changed}>{saving ? t("action.saving") : t("members.save")}</button>
    </form>
  </div>;
}

export function BotPanel({ bots, activeBot, onExportData, onClose, onUpdate, onCreateGroup, onOpenWork, onOpenMemory, onMakePrimary, memories = [], refreshToken = 0 }: { onMakePrimary?: (id: string) => Promise<void>; onExportData?: (id: string) => void; onOpenWork?: () => void; onOpenMemory?: () => void; memories?: Memory[]; refreshToken?: number; bots: Bot[]; activeBot?: Bot; onClose: () => void; onUpdate: (id: string, input: Partial<Pick<Bot, "name" | "instructions" | "model" | "reasoning_effort">>) => Promise<void>; onCreateGroup: (name: string, botIds: string[]) => Promise<void> }) {
  const { t } = useTranslation("chat");
  const mode = activeBot ? "bot" : "group";
  const [view, setView] = useState<"home" | "appearance" | "config">("home");
  const [name, setName] = useState(activeBot?.name ?? "");
  const [instructions, setInstructions] = useState(activeBot?.instructions ?? "");
  const [model, setModel] = useState(activeBot?.model ?? "");
  const [effort, setEffort] = useState(activeBot?.reasoning_effort ?? "");
  const [avatarConfig, setAvatarConfig] = useState(() => activeBot ? getBotAvatarConfig(activeBot.id) : undefined);
  const [members, setMembers] = useState<string[]>([]);
  useEffect(() => {
    const available = new Set(bots.map(bot => bot.id));
    setMembers(current => current.every(id => available.has(id)) ? current : current.filter(id => available.has(id)));
  }, [bots]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const valid = Boolean(name.trim()) && (mode === "bot" || members.length >= 2 && members.length <= 8);
  async function submit(event: React.FormEvent) {
    event.preventDefault(); if (!valid || saving) return;
    setSaving(true); setError("");
    try {
      if (mode === "group") await onCreateGroup(name.trim(), members);
      else if (activeBot) await onUpdate(activeBot.id, { name: name.trim(), instructions, model, reasoning_effort: effort });
    } catch (cause) { setError(errorText(cause)); }
    finally { setSaving(false); }
  }
  if (activeBot) {
    const role = instructions.trim().split(/[\n\u3002\uFF01\uFF1F.!?]/)[0]?.trim();
    // "default" is a stored sentinel; show what the Bot runs with now.
    const follows = followsGlobal(activeBot.model);
    const modelLine = follows ? (activeBot.effective_model ? `${t("botPanel.follows_global")} · ${activeBot.effective_model}` : t("botPanel.follows_global")) : activeBot.model;
    const shownEffort = activeBot.effective_reasoning_effort || (activeBot.reasoning_effort !== "default" ? activeBot.reasoning_effort : "");
    const effortLabel = shownEffort ? ` · ${shownEffort}` : "";
    const latestMemoryDisplay = memories.length ? memoryDisplay(memories.at(-1)) : undefined;
    const latestMemory = latestMemoryDisplay ? `${latestMemoryDisplay.title} · ${latestMemoryDisplay.description}` : undefined;
    return <div className="detail-content bot-v2-panel">
      <div className="detail-heading bot-v2-heading">
        {view === "home" ? <h2>{t("botPanel.title")}</h2> : <button type="button" className="bot-v2-back" onClick={() => setView("home")}><Icon name="arrow-left" size={18}/> {t("botPanel.back")}</button>}
        <button className="close-button" aria-label={t("botPanel.close")} data-hint={t("botPanel.close_hint")} onClick={onClose}><Icon name="close" size={18} /></button>
      </div>
      {view === "home" ? <>
        {/* One entry per destination: the cat opens its style, each row opens one page. */}
        <div className="bot-v2-profile">
          <button type="button" className="bot-v2-hero" onClick={() => setView("appearance")} aria-label={t("botPanel.change_style_label", { name: activeBot.name })} data-hint={t("botPanel.change_style")}>
            <span className="bot-v2-cat"><BotAvatar id={activeBot.id} config={avatarConfig} motion="awake" /></span>
            <span className="bot-v2-change" aria-hidden="true"><Icon name="edit" size={13}/></span>
          </button>
          <div className="bot-v2-identity"><h2>{activeBot.name}</h2>{role && <p>{role}</p>}<span className="bot-v2-model">{modelLine}{effortLabel}</span></div>
        </div>
        <nav className="bot-v2-rows" aria-label={t("botPanel.title")}>
          {onOpenWork && <WorkPreview disabled={saving} scope={{type:"bots",id:activeBot.id}} refreshToken={refreshToken} onOpen={onOpenWork} leading={<span className="bot-v2-row-icon" aria-hidden="true"><Icon name="checklist" size={18}/></span>}/>}
          {onOpenMemory && <button type="button" className="bot-v2-row" onClick={onOpenMemory}><span className="bot-v2-row-icon" aria-hidden="true"><Icon name="memory" size={18}/></span><span className="bot-v2-row-copy"><strong>{t("botPanel.memory")} <span>{memories.length}</span></strong><small>{latestMemory ?? t("botPanel.no_memory")}</small></span><Icon name="chevron-right" size={16}/></button>}
          <button type="button" className="bot-v2-row" onClick={() => setView("config")}><span className="bot-v2-row-icon" aria-hidden="true"><Icon name="bot-config" size={18}/></span><span className="bot-v2-row-copy"><strong>{t("botPanel.config")}</strong><small>{role || t("botPanel.no_role")}</small></span><Icon name="chevron-right" size={16}/></button>
        </nav>
        {(activeBot.is_primary || (onMakePrimary && !activeBot.archived)) && <div className="bot-v2-primary">{activeBot.is_primary ? <><PrimaryBadge /><span className="muted small-text">{t("primary.bot_panel_note")}</span></> : <button type="button" className="text-button" onClick={() => void onMakePrimary?.(activeBot.id)}>{t("primary.make")}</button>}</div>}
        <div className="bot-v2-footer"><button type="button" className="text-button" onClick={() => void shareBotPackage(activeBot)}><Icon name="share" size={15}/> {t("botPanel.export_share")}</button><button type="button" className="text-button" onClick={() => onExportData?.(activeBot.id)}>{t("botPanel.export_data")}</button></div>
      </> : view === "appearance" ? <><div className="bot-v2-subtitle"><h2>{t("botPanel.style")}</h2><p>{t("botPanel.style_hint")}</p></div><BotAvatarPicker botId={activeBot.id} config={avatarConfig!} onChange={(next) => setAvatarConfig(saveBotAvatarConfig(activeBot.id, next))}/><button type="button" className="primary-button bot-v2-done" onClick={() => setView("home")}>{t("botPanel.done")}</button></> : <><div className="bot-v2-subtitle"><h2>{t("botPanel.config")}</h2><p>{t("botPanel.config_hint")}</p></div><form className="detail-form" aria-busy={saving} onSubmit={(event) => void submit(event)}><fieldset className="form-fields" disabled={saving}><label>{t("botPanel.name")}<input value={name} maxLength={200} onChange={(event) => setName(event.target.value)}/></label><label>{t("botPanel.instructions")}<textarea value={instructions} onChange={(event) => setInstructions(event.target.value)} placeholder={t("botPanel.instructions_placeholder")} rows={6}/></label><Disclosure title={t("botPanel.model_section")}><ModelFields disabled={saving} model={model} effort={effort} onChange={(next,level)=>{setModel(next);setEffort(level)}}/></Disclosure>{error && <p className="error-text" role="alert">{error}</p>}<button className="primary-button" disabled={!valid || saving}>{saving ? t("action.saving") : t("botPanel.save")}</button></fieldset></form><BotSkills botId={activeBot.id} refreshToken={refreshToken}/><BotSecrets botId={activeBot.id}/><BotInspector botId={activeBot.id}/></>}
    </div>;
  }
  return <div className="detail-content">
    <div className="detail-heading"><h2>{t("create.new_group")}</h2><button className="close-button" aria-label={t("groupCreate.close")} disabled={saving} onClick={onClose}><Icon name="close" size={18} /></button></div>
    <form className="detail-form" aria-busy={saving} onSubmit={(event) => void submit(event)}>
      <fieldset className="form-fields" disabled={saving}>
        <label>{t("botPanel.name")}<input value={name} maxLength={200} onChange={(event) => setName(event.target.value)} placeholder={t("members.name")} /></label>
          <div className="members-manage-label"><span>{t("members.members")}</span><span className="muted small-text">{members.length}/8</span></div>
          <div className="member-picker create-member-picker">{bots.map((bot) => <label key={bot.id} className={`member-option ${members.includes(bot.id) ? "selected" : ""}`}>
            <BotAvatar id={bot.id} mini /><span>{bot.name}</span><input type="checkbox" checked={members.includes(bot.id)} disabled={!members.includes(bot.id) && members.length >= 8} onChange={() => setMembers((current) => current.includes(bot.id) ? current.filter((id) => id !== bot.id) : [...current, bot.id])} />
          </label>)}</div><p className="field-note">{t("members.pick_hint")}</p>
        {error && <p className="error-text" role="alert">{error}</p>}
        <button className="primary-button" disabled={!valid || saving}>{saving ? t("action.saving") : t("groupCreate.create")}</button>
      </fieldset>
    </form>
  </div>;
}


