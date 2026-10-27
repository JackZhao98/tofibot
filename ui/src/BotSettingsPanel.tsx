import { useEffect, useRef, useState, type ReactNode } from "react";
import { request } from "./api";
import { BotAvatar } from "./BotAvatar";
import { BotAvatarPicker } from "./BotAvatarPicker";
import { BotInspector } from "./BotInspector";
import { BotSecrets } from "./BotSecrets";
import { BotSkills } from "./BotSkills";
import { buildBotPackage, downloadBotPackage } from "./botPackage";
import { getBotAvatarConfig, saveBotAvatarConfig } from "./avatarStore";
import { DirtyBar, LeaveConfirm } from "./CoverForm";
import { coverContainer, useCover, useCoverGuard } from "./coverGuard";
import { errorText } from "./errorText";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon, type TofiIconName } from "./icons";
import { followsGlobal } from "./modelCatalog";
import { ModelFields } from "./ModelSettings";
import { OverlayPopover } from "./overlays/OverlayHost";
import { PrimaryBadge } from "./PrimaryAssistant";
import type { BotSkillListing } from "./skillLibrary";
import { usePreviousWhileFading } from "./surfacePresence";
import type { Bot } from "./types";
import "./bot-settings.css";

/**
 * Bot settings, the cover (docs/design/widgets-and-sidebar.md section 5): one page with a section list on the left (a list that
 * pushes the page on phones). Identity, Role and Model are one form with one dirty bar; Skills and Secrets save on their own.
 * Lazy chunk `botSettings`.
 */
const SECTIONS: readonly { id: SectionId; icon: TofiIconName }[] = [
  { id: "identity", icon: "id-card" }, { id: "role", icon: "edit" }, { id: "model", icon: "sliders" },
  { id: "skills", icon: "skill" }, { id: "secrets", icon: "key" }, { id: "advanced", icon: "settings" },
];
type SectionId = "identity" | "role" | "model" | "skills" | "secrets" | "advanced";
type Draft = { name: string; instructions: string; model: string; effort: string };
const FADE_MS = 140;
const narrowQuery = "(max-width: 899px)";

function useNarrow() {
  const [narrow, setNarrow] = useState(() => typeof matchMedia === "function" && matchMedia(narrowQuery).matches);
  useEffect(() => {
    const query = matchMedia(narrowQuery);
    const update = () => setNarrow(query.matches);
    update();
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);
  return narrow;
}

/** The share file names the library Skills the Bot has on, and nothing else about them. */
async function shareBotPackage(bot: Bot) {
  let names: string[] = [];
  try {
    const listing = await request<BotSkillListing>(`/api/bots/${encodeURIComponent(bot.id)}/skills`);
    names = (listing.skills ?? []).filter(skill => skill.enabled && !skill.builtin).map(skill => skill.name);
  } catch { /* a share file without skill names is still a valid share file */ }
  downloadBotPackage(buildBotPackage(bot, names));
}

function Row({ title, hint, children, danger }: { title: string; hint?: string; children?: ReactNode; danger?: boolean }) {
  return <div className="bs-row" data-danger={danger || undefined}>
    <div className="bs-row-copy"><strong>{title}</strong>{hint && <small>{hint}</small>}</div>
    {children}
  </div>;
}

export function BotSettingsPanel({ bot, refreshToken = 0, onUpdate, onMakePrimary, onExportData, onDelete }: {
  bot: Bot; refreshToken?: number;
  onUpdate: (id: string, input: Partial<Pick<Bot, "name" | "instructions" | "model" | "reasoning_effort">>) => Promise<void>;
  onMakePrimary: (id: string) => Promise<void>;
  onExportData: (id: string) => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation("chat");
  const cover = useCover();
  const narrow = useNarrow();
  const [section, setSection] = useState<SectionId>("identity");
  const [view, setView] = useState<"list" | "page">("list");
  const [visited, setVisited] = useState<readonly SectionId[]>(["identity"]);
  const fading = usePreviousWhileFading(section, FADE_MS);

  // Identity, Role and Model: one form. The saved values are the Bot itself; a refresh that arrives while nothing is edited is adopted.
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
  useCoverGuard(dirty, () => setAsking(true));

  // Phones: the section list, then a page. Wide: both, and the list marks the page.
  const heading = useRef<HTMLHeadingElement>(null);
  const items = useRef<Record<string, HTMLButtonElement | null>>({});
  const entered = useRef(false);
  useEffect(() => {
    if (!narrow) return;
    if (view === "page") heading.current?.focus({ preventScroll: true });
    else if (entered.current) items.current[section]?.focus({ preventScroll: true });
    entered.current = true;
    // Focus follows the view, not the section.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, narrow]);
  const open = (id: SectionId) => {
    setSection(id);
    setVisited(current => current.includes(id) ? current : [...current, id]);
    setView("page");
  };

  // Avatar style: the picker opens in place, on the avatar's own button. It saves on every pick (the avatar is stored on this device).
  const [avatar, setAvatar] = useState(() => getBotAvatarConfig(bot.id));
  const [styleOpen, setStyleOpen] = useState(false);
  const styleButton = useRef<HTMLButtonElement>(null);
  const [makingPrimary, setMakingPrimary] = useState(false);

  const follows = followsGlobal(draft.model);
  const effective = bot.effective_model;
  const modelLine = follows
    ? (effective ? `${t("botPanel.follows_global")} · ${effective}` : t("botPanel.follows_global"))
    : t("botSettings.model_pinned", { model: draft.model });
  const role = draft.instructions.trim().split("\n")[0]?.trim();
  const listHint: Partial<Record<SectionId, string>> = { identity: draft.name.trim() || bot.name, role: role || t("botPanel.no_role"), model: modelLine };

  const body: Record<SectionId, ReactNode> = {
    identity: <div className="bs-stack">
      <div className="bs-row bs-avatar-row">
        <span className="bs-avatar" aria-hidden="true"><BotAvatar id={bot.id} config={avatar} motion="awake" /></span>
        <div className="bs-row-copy"><strong>{t("botSettings.avatar_title")}</strong><small>{t("botSettings.avatar_hint")}</small></div>
        <button ref={styleButton} type="button" className="bs-button" aria-haspopup="dialog" aria-expanded={styleOpen} aria-label={t("botPanel.change_style_label", { name: draft.name.trim() || bot.name })} onClick={() => setStyleOpen(value => !value)}>{t("botPanel.change_style")}</button>
      </div>
      <label className="bs-field"><span>{t("botPanel.name")}</span>
        <input value={draft.name} maxLength={200} disabled={busy} aria-invalid={!canSave || undefined} onChange={event => setDraft(current => ({ ...current, name: event.target.value }))} />
        {!canSave && <small className="bs-field-error" role="alert">{t("botSettings.name_required")}</small>}
      </label>
      {(bot.is_primary || !bot.archived) && <Row title={t("botSettings.primary_title")} hint={bot.is_primary ? t("primary.bot_panel_note") : t("botSettings.primary_off")}>
        {bot.is_primary ? <PrimaryBadge /> : <button type="button" className="bs-button" disabled={makingPrimary} onClick={() => { setMakingPrimary(true); void onMakePrimary(bot.id).finally(() => setMakingPrimary(false)); }}>{t("primary.make")}</button>}
      </Row>}
      {bot.archived && <Row title={t("botSettings.archived_title")} hint={t("botSettings.archived_body")} />}
    </div>,
    role: <div className="bs-stack">
      <label className="bs-field"><span className="sr-only">{t("botPanel.instructions")}</span>
        <textarea rows={10} value={draft.instructions} disabled={busy} placeholder={t("botPanel.instructions_placeholder")} onChange={event => setDraft(current => ({ ...current, instructions: event.target.value }))} />
        <small className="bs-count">{t("botSettings.role_count", { count: draft.instructions.length })}</small>
      </label>
    </div>,
    model: <div className="bs-stack">
      <ModelFields disabled={busy} model={draft.model} effort={draft.effort} onChange={(model, effort) => setDraft(current => ({ ...current, model, effort }))} />
      <p className="bs-model-line">{modelLine}{bot.effective_reasoning_effort && follows ? ` · ${bot.effective_reasoning_effort}` : ""}</p>
    </div>,
    skills: <div className="bs-stack"><BotSkills botId={bot.id} refreshToken={refreshToken} /></div>,
    secrets: <div className="bs-stack"><BotSecrets botId={bot.id} /></div>,
    advanced: <div className="bs-stack">
      <BotInspector botId={bot.id} />
      <Row title={t("botPanel.export_share")} hint={t("botSettings.share_hint")}><button type="button" className="bs-button" onClick={() => void shareBotPackage(bot)}>{t("botSettings.export_action")}</button></Row>
      <Row title={t("botPanel.export_data")} hint={t("botSettings.data_hint")}><button type="button" className="bs-button" onClick={() => onExportData(bot.id)}>{t("botSettings.export_action")}</button></Row>
      <Row title={t("header.delete_bot")} hint={t("botSettings.delete_hint")} danger><button type="button" className="bs-button is-danger" onClick={() => { cover.close(); onDelete(); }}>{t("header.delete_bot")}</button></Row>
    </div>,
  };

  const title = (id: SectionId) => t(`botSettings.section.${id}`);
  const describe = (id: SectionId) => id === "identity" || id === "role" || id === "model" || id === "advanced" ? t(`botSettings.desc.${id}`) : "";
  return <div className="bot-settings" data-view={view} data-narrow={narrow || undefined}>
    <nav className="bs-nav" aria-label={t("botSettings.nav")} inert={narrow && view === "page" || undefined}>
      <div className="bs-who">
        <span className="bs-who-avatar" aria-hidden="true"><BotAvatar id={bot.id} config={avatar} motion="awake" /></span>
        <span className="bs-who-copy"><strong>{draft.name.trim() || bot.name}</strong><small>{modelLine}</small></span>
      </div>
      <div className="bs-nav-list">
        {SECTIONS.map(item => <button key={item.id} ref={node => { items.current[item.id] = node; }} type="button" className="bs-nav-item" data-section={item.id} aria-current={section === item.id ? "page" : undefined} onClick={() => open(item.id)}>
          <Icon name={item.icon} size={19} /><span className="bs-nav-label">{title(item.id)}</span>
          {listHint[item.id] && <small className="bs-nav-hint">{listHint[item.id]}</small>}
          <Icon className="bs-nav-chevron" name="chevron-right" size={15} />
        </button>)}
      </div>
    </nav>
    <div className="bs-page" inert={narrow && view === "list" || undefined}>
      <header className="bs-head">
        <button type="button" className="bs-back" aria-label={t("botSettings.back")} data-hint={t("botSettings.back")} onClick={() => setView("list")}><Icon name="chevron-left" size={20} /></button>
        <div className="bs-head-copy">
          <h3 ref={heading} tabIndex={-1}>{title(section)}</h3>
          {describe(section) && <p>{describe(section)}</p>}
        </div>
        <span className="bs-saved" data-on={justSaved || undefined} role="status">{justSaved ? t("cover.saved") : ""}</span>
      </header>
      <div className="bs-scroll">
        <div className="bs-sections">
          {SECTIONS.map(({ id }) => visited.includes(id) && <section key={id} className="bs-section" data-section={id} data-state={id === section ? "in" : id === fading ? "out" : "off"} hidden={id !== section && id !== fading} inert={id !== section || undefined} aria-label={title(id)}>
            {body[id]}
          </section>)}
        </div>
      </div>
      <DirtyBar open={dirty} busy={busy} error={error} canSave={canSave} onDiscard={discard} onSave={() => void save()} />
    </div>
    <OverlayPopover open={styleOpen} onOpenChange={setStyleOpen} className="bot-style-pop" anchor={styleButton} container={coverContainer()} side="bottom" align="start" sideOffset={8} aria-label={t("botPanel.style")}>
      <BotAvatarPicker botId={bot.id} config={avatar} onChange={next => setAvatar(saveBotAvatarConfig(bot.id, next))} />
    </OverlayPopover>
    {asking && <LeaveConfirm busy={busy} onKeep={() => setAsking(false)} onLeave={() => { setAsking(false); cover.close(); }} />}
  </div>;
}
