import { Button } from "./ds/Button/Button";
import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { request } from "./api";
import { BotAvatar } from "./BotAvatar";
import { catCentre } from "./lib/tofi-avatar/framing.js";
import { BotAvatarPicker } from "./BotAvatarPicker";
import { BotInspector } from "./BotInspector";
import { BotSecrets } from "./BotSecrets";
import { BotTools } from "./BotTools";
import { buildBotPackage, downloadBotPackage } from "./botPackage";
import type { AvatarConfig } from "./lib/tofi-avatar/index.js";
import { getBotAvatarConfig, getBotGround, saveBotAvatarConfig, saveBotGround, type Ground } from "./avatarStore";
import { SettingsSection } from "./ds/SettingsSection/SettingsSection";
import { SettingsShell as Frame, type SettingsShellGroup } from "./ds/SettingsShell/SettingsShell";
import { SwatchPicker } from "./ds/SwatchPicker/SwatchPicker";
import { TextArea } from "./ds/TextArea/TextArea";
import { TextInput } from "./ds/TextInput/TextInput";
import { pushBotAvatar } from "./avatarSync";
import { DirtyBar, LeaveConfirm } from "./CoverForm";
import { coverContainer, requestCoverClose, useCover, useCoverGuard } from "./coverGuard";
import { errorText } from "./errorText";
import { useTranslation } from "./i18n";
import type { TofiIconName } from "./icons";
import { ModelFields } from "./ModelSettings";
import { OverlayPopover, pressToggle } from "./overlays/OverlayHost";
import { PrimaryBadge } from "./PrimaryAssistant";
import { takeSettingsSection } from "./settings/pendingSection";
import { LEGACY_BOT_SETTINGS_SECTIONS } from "./settings/sections";
import { jumpWhenReady } from "./settings/sectionJump";
import { SettingsCard, SettingsRow } from "./settings/components";
import type { BotSkillListing } from "./skillLibrary";
import { usePreviousWhileFading } from "./surfacePresence";
import type { SettingsTab } from "./SettingsShell";
import type { Bot } from "./types";
import "./bot-settings.css";

/**
 * Bot settings, the cover: the kit settings frame with six sections. Profile, Role and model are one form with one dirty bar; Tools
 * (connections, tool tiers and Skills for this Bot) and Secrets save on their own. Lazy chunk `botSettings`.
 */
const SECTIONS: readonly { id: SectionId; icon: TofiIconName }[] = [
  { id: "identity", icon: "id-card" }, { id: "role", icon: "sliders" }, { id: "tools", icon: "plug" },
  { id: "secrets", icon: "key" }, { id: "export", icon: "download" }, { id: "danger", icon: "alert" },
];
type SectionId = "identity" | "role" | "tools" | "secrets" | "export" | "danger";
type Draft = { name: string; instructions: string; model: string; effort: string };
const FADE_MS = 140;
const GROUND_NAMES = ["tomato", "mustard", "teal", "grape", "lime"] as const;

/** The share file names the library Skills the Bot has on, and nothing else about them. */
async function shareBotPackage(bot: Bot) {
  let names: string[] = [];
  try {
    const listing = await request<BotSkillListing>(`/api/bots/${encodeURIComponent(bot.id)}/skills`);
    names = (listing.skills ?? []).filter(skill => skill.enabled && !skill.builtin).map(skill => skill.name);
  } catch { /* a share file without skill names is still a valid share file */ }
  downloadBotPackage(buildBotPackage(bot, names));
}

/** The cat is not centred in its drawing box (a loaf sits low, a tall cat leans): the tile shifts it by the measured centre. */
const centred = (shape: AvatarConfig["shape"]) => { const c = catCentre(shape); return { "--cat-dx": `${c.x * 100}%`, "--cat-dy": `${c.y * 100}%` } as CSSProperties; };
export function BotSettingsPanel({ bot, refreshToken = 0, onUpdate, onMakePrimary, onExportData, onOpenSettings, onDelete }: {
  bot: Bot; refreshToken?: number;
  onUpdate: (id: string, input: Partial<Pick<Bot, "name" | "instructions" | "model" | "reasoning_effort">>) => Promise<void>;
  onMakePrimary: (id: string) => Promise<void>;
  onExportData: (id: string) => void;
  /** Opens Settings on a tab; the cover is replaced (after the unsaved-changes question, when there is something unsaved). */
  onOpenSettings: (tab: SettingsTab) => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation("chat");
  const cover = useCover();
  const [section, setSection] = useState<SectionId>("identity");
  const [visited, setVisited] = useState<readonly SectionId[]>(["identity"]);
  const fading = usePreviousWhileFading(section, FADE_MS);

  // Profile and Role and model: one form. The saved values are the Bot itself; a refresh that arrives while nothing is edited is adopted.
  const saved: Draft = { name: bot.name, instructions: bot.instructions, model: bot.model, effort: bot.reasoning_effort ?? "" };
  const [draft, setDraft] = useState<Draft>(saved);
  const baseline = useRef<Draft>(saved);
  useEffect(() => {
    const previous = baseline.current;
    baseline.current = saved;
    setDraft(current => current.name === previous.name && current.instructions === previous.instructions && current.model === previous.model && current.effort === previous.effort ? saved : current);
    // `saved` is rebuilt every render; its four fields are the dependencies.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [saved.name, saved.instructions, saved.model, saved.effort]);
  const dirty = draft.name.trim() !== saved.name || draft.instructions !== saved.instructions || draft.model !== saved.model || draft.effort !== saved.effort;
  const canSave = draft.name.trim().length > 0;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [justSaved, setJustSaved] = useState(false);
  useEffect(() => {
    if (!justSaved) return;
    const timer = window.setTimeout(() => setJustSaved(false), 2400);
    return () => window.clearTimeout(timer);
  }, [justSaved]);
  async function save() {
    if (busy || !dirty || !canSave) return;
    setBusy(true); setError("");
    try {
      await onUpdate(bot.id, { name: draft.name.trim(), instructions: draft.instructions, model: draft.model, reasoning_effort: draft.effort });
      setDraft(current => ({ ...current, name: current.name.trim() }));
      setJustSaved(true);
    } catch (cause) { setError(errorText(cause)); }
    finally { setBusy(false); }
  }
  const discard = () => { setDraft(saved); setError(""); };

  // Closing with unsaved changes asks first (every way out goes through CoverShell, which asks the guard).
  const [asking, setAsking] = useState(false);
  // Where the person goes once they have said "discard": the close button, Escape and the scrim leave; the Settings link goes on to Settings.
  const afterLeave = useRef<(() => void) | null>(null);
  useCoverGuard(dirty, () => setAsking(true));

  /** A link out of the cover: it closes first (asking about unsaved changes), then `go` runs. */
  const leaveTo = (go: () => void) => { afterLeave.current = go; requestCoverClose(go); };
  const open = (id: SectionId) => {
    setSection(id);
    setVisited(current => current.includes(id) ? current : [...current, id]);
  };

  // A link to one section (`/b/<id>/settings#role`) opens the cover on it.
  useEffect(() => {
    const asked = takeSettingsSection("bot-settings");
    const target = asked && (LEGACY_BOT_SETTINGS_SECTIONS[asked] ?? asked);
    if (!target || !SECTIONS.some(item => item.id === target)) return;
    open(target as SectionId);
    return jumpWhenReady(() => document.querySelector(".bot-settings .bs-sections"), target);
    // Once, on mount: the section is taken from the pending link.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Avatar style: the picker opens in place, on the avatar's own button. It saves on every pick (the avatar is stored on this device).
  const [avatar, setAvatar] = useState(() => getBotAvatarConfig(bot.id));
  const [ground, setGround] = useState<Ground>(() => getBotGround(bot.id));
  const pickGround = (next: Ground) => { saveBotGround(bot.id, next); setGround(next); pushBotAvatar(request, bot.id, { ...avatar, schemaVersion: 2, ground: next }); };
  const [styleOpen, setStyleOpen] = useState(false);
  const styleButton = useRef<HTMLButtonElement>(null);
  const [makingPrimary, setMakingPrimary] = useState(false);
  const name = draft.name.trim() || bot.name;

  const body: Record<SectionId, ReactNode> = {
    identity: <div className="bs-stack">
      <SettingsCard>
        <SettingsRow label={t("botSettings.avatar_title")} description={t("botSettings.avatar_hint")} inline control={<>
          <span className="bs-avatar" style={centred(avatar.shape)} aria-hidden="true"><BotAvatar id={bot.id} config={avatar} motion="awake" /></span>
          <Button ref={styleButton} size="sm" aria-haspopup="dialog" aria-expanded={styleOpen} aria-label={t("botPanel.change_style_label", { name })} {...pressToggle(() => setStyleOpen(value => !value))}>{t("botPanel.change_style")}</Button>
        </>} />
        <SettingsRow label={t("botSettings.ground_label")} inline control={<SwatchPicker label={t("botSettings.ground_label")} value={ground} onChange={pickGround} options={GROUND_NAMES.map(id => ({ value: id, label: t(`botSettings.ground.${id}`) }))} />} />
        <SettingsRow label={t("botPanel.name")} labelFor="bs-name" description={canSave ? undefined : t("botSettings.name_required")} control={
          <TextInput id="bs-name" value={draft.name} maxLength={200} disabled={busy} aria-invalid={!canSave || undefined} onChange={event => setDraft(current => ({ ...current, name: event.target.value }))} />} />
        {(bot.is_primary || !bot.archived) && <SettingsRow label={t("botSettings.primary_title")} description={bot.is_primary ? t("primary.bot_panel_note") : t("botSettings.primary_off")} inline control={
          bot.is_primary ? <PrimaryBadge /> : <Button size="sm" disabled={makingPrimary} onClick={() => { setMakingPrimary(true); void onMakePrimary(bot.id).finally(() => setMakingPrimary(false)); }}>{t("primary.make")}</Button>} />}
        {bot.archived && <SettingsRow label={t("botSettings.archived_title")} description={t("botSettings.archived_body")} />}
      </SettingsCard>
    </div>,
    role: <div className="bs-stack">
      <SettingsSection title={t("botPanel.instructions")}>
        <div className="bs-field">
          <TextArea rows={10} aria-label={t("botPanel.instructions")} value={draft.instructions} disabled={busy} placeholder={t("botPanel.instructions_placeholder")} onChange={event => setDraft(current => ({ ...current, instructions: event.target.value }))} />
          <small className="bs-count">{t("botSettings.role_count", { count: draft.instructions.length })}</small>
        </div>
      </SettingsSection>
      <ModelFields layout="rows" disabled={busy} model={draft.model} effort={draft.effort} onChange={(model, effort) => setDraft(current => ({ ...current, model, effort }))} />
    </div>,
    tools: <BotTools bot={bot} refreshToken={refreshToken} leaveTo={leaveTo} onOpenSettings={onOpenSettings} />,
    secrets: <div className="bs-stack"><BotSecrets botId={bot.id} /></div>,
    export: <div className="bs-stack">
      <SettingsCard>
        <SettingsRow label={t("botPanel.export_share")} description={t("botSettings.share_hint")} inline control={<Button size="sm" onClick={() => void shareBotPackage(bot)}>{t("botSettings.export_action")}</Button>} />
        <SettingsRow label={t("botPanel.export_data")} description={t("botSettings.data_hint")} inline control={<Button size="sm" onClick={() => onExportData(bot.id)}>{t("botSettings.export_action")}</Button>} />
      </SettingsCard>
      <BotInspector botId={bot.id} />
    </div>,
    danger: <div className="bs-stack">
      <SettingsCard>
        <SettingsRow label={t("header.delete_bot")} description={t("botSettings.delete_hint")} inline control={<Button variant="danger" size="sm" onClick={() => { cover.close(); onDelete(); }}>{t("header.delete_bot")}</Button>} />
      </SettingsCard>
    </div>,
  };

  const title = (id: SectionId) => t(`botSettings.section.${id}`);
  const groups: SettingsShellGroup[] = [{ id: "bot", items: SECTIONS.map(item => ({ value: item.id, label: title(item.id), icon: item.icon })) }];
  return <div className="bot-settings">
    <Frame flush groups={groups} active={section} onNavigate={value => open(value as SectionId)} navLabel={t("botSettings.nav")} crumb={name} title={title(section)}
      status={justSaved ? t("cover.saved") : undefined} hooks={{ root: "bs-frame", nav: "bs-nav", link: "bs-nav-item", content: "bs-content" }}>
      <div className="bs-scroll">
        <div className="bs-sections">
          {SECTIONS.map(({ id }) => visited.includes(id) && <section key={id} className="bs-section" data-section={id} data-state={id === section ? "in" : id === fading ? "out" : "off"} hidden={id !== section && id !== fading} inert={id !== section || undefined} aria-label={title(id)}>
            {body[id]}
          </section>)}
        </div>
      </div>
    </Frame>
    <DirtyBar open={dirty} busy={busy} error={error} canSave={canSave} onDiscard={discard} onSave={() => void save()} />
    <OverlayPopover open={styleOpen} onOpenChange={setStyleOpen} className="bot-style-pop" anchor={styleButton} container={coverContainer()} side="bottom" align="start" sideOffset={8} aria-label={t("botPanel.style")}>
      <BotAvatarPicker botId={bot.id} config={avatar} onChange={next => { const saved = saveBotAvatarConfig(bot.id, next); pushBotAvatar(request, bot.id, saved); setAvatar(saved); }} />
    </OverlayPopover>
    {asking && <LeaveConfirm busy={busy} onKeep={() => { afterLeave.current = null; setAsking(false); }} onLeave={() => { const go = afterLeave.current ?? cover.close; afterLeave.current = null; setAsking(false); go(); }} />}
  </div>;
}
