import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "./api";
import { TofiIcon } from "./icons";
import { skillCatalog } from "./skillCatalog";
import { useTranslation } from "./i18n";
import { setByLine, type BotSkill, type BotSkillListing, type LibraryBundle } from "./skillLibrary";
import "./bot-secrets.css";
import "./skill-library.css";

const titleOf = (skill: { name: string }) => skillCatalog.find(item => item.name === skill.name)?.title ?? skill.name;
const textOf = (skill: { name: string; description: string }) => skillCatalog.find(item => item.name === skill.name)?.description ?? skill.description;

function Check({ label, checked, indeterminate, disabled, onChange }: { label: string; checked: boolean; indeterminate?: boolean; disabled?: boolean; onChange: (next: boolean) => void }) {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => { if (ref.current) ref.current.indeterminate = Boolean(indeterminate) && !checked; }, [indeterminate, checked]);
  return <input ref={ref} type="checkbox" aria-label={label} checked={checked} disabled={disabled} onChange={event => onChange(event.target.checked)} />;
}

function SkillCheckRow({ skill, busy, onToggle, nested }: { skill: BotSkill; busy: boolean; onToggle: (name: string, next: boolean) => void; nested?: boolean }) {
  const set = setByLine(skill);
  return <li className="bot-skill-row" data-nested={nested ? "true" : undefined} data-skill={skill.name}>
    <label>
      <Check label={titleOf(skill)} checked={skill.enabled} disabled={busy} onChange={next => onToggle(skill.name, next)} />
      <span className="bot-skill-copy"><strong>{titleOf(skill)}</strong><small>{textOf(skill)}</small>{set && <em>{set}</em>}</span>
    </label>
  </li>;
}

function BundleCheckRow({ bundle, members, busy, onToggle, onToggleBundle, defaultOpen }: { bundle: LibraryBundle; members: BotSkill[]; busy: boolean; onToggle: (name: string, next: boolean) => void; onToggleBundle: (id: string, next: boolean) => void; defaultOpen?: boolean }) {
  const { t } = useTranslation("bots");
  const [open, setOpen] = useState(Boolean(defaultOpen));
  const on = members.filter(member => member.enabled).length;
  return <li className="bot-skill-row bot-skill-bundle" data-bundle={bundle.id}>
    <div className="bot-skill-bundle-head">
      <label>
        <Check label={t("skills.bundle_label", { name: bundle.id })} checked={members.length > 0 && on === members.length} indeterminate={on > 0 && on < members.length} disabled={busy} onChange={next => onToggleBundle(bundle.id, next)} />
        <span className="bot-skill-copy"><strong>{bundle.id}</strong><small>{[bundle.description, t("skills.bundle_count", { count: members.length })].filter(Boolean).join(" · ")}</small></span>
      </label>
      <button type="button" className="text-button skill-expand" aria-expanded={open} aria-label={open ? t("skills.hide_members", { name: bundle.id }) : t("skills.show_members", { name: bundle.id })} onClick={() => setOpen(value => !value)}>
        {t("skills.on_of", { on, total: members.length })}<TofiIcon name="chevron-down" size={14} />
      </button>
    </div>
    {open && <ul className="bot-skill-members">{members.map(member => <SkillCheckRow key={member.name} skill={member} busy={busy} onToggle={onToggle} nested />)}</ul>}
  </li>;
}

export type BotSkillsViewProps = { botId: string; listing: BotSkillListing; loading: boolean; busy: boolean; error: string; onToggle: (name: string, next: boolean) => void; onToggleBundle: (id: string, next: boolean) => void; openBundles?: string[] };

/** One Bot's enabled set: the whole library as checkboxes, bundles expandable (decision 0058). Presentational. */
export function BotSkillsView({ botId, listing, loading, busy, error, onToggle, onToggleBundle, openBundles }: BotSkillsViewProps) {
  const { t } = useTranslation("bots");
  const bundled = new Set(listing.bundles.flatMap(bundle => bundle.skills));
  const loose = listing.skills.filter(skill => !bundled.has(skill.name));
  const own = loose.filter(skill => !skill.builtin);
  const official = loose.filter(skill => skill.builtin);
  const libraryEmpty = !loading && !error && own.length === 0 && listing.bundles.length === 0;
  return <section className="bot-skills" aria-labelledby={`bot-skills-${botId}`}>
    <div className="bot-v2-subtitle"><h2 id={`bot-skills-${botId}`}>{t("skills.title")}</h2><p>{t("skills.hint")}</p></div>
    {loading ? <p className="field-note" role="status">{t("skills.loading")}</p> : <>
      {libraryEmpty && <p className="field-note">{t("skills.empty")}</p>}
      {(own.length > 0 || listing.bundles.length > 0) && <ul className="bot-skill-list">
        {listing.bundles.map(bundle => <BundleCheckRow key={bundle.id} bundle={bundle} members={listing.skills.filter(skill => skill.bundle_id === bundle.id)} busy={busy} onToggle={onToggle} onToggleBundle={onToggleBundle} defaultOpen={openBundles?.includes(bundle.id)} />)}
        {own.map(skill => <SkillCheckRow key={skill.name} skill={skill} busy={busy} onToggle={onToggle} />)}
      </ul>}
      {official.length > 0 && <><h3 className="bot-skill-group">{t("skills.official_title")}</h3><ul className="bot-skill-list">{official.map(skill => <SkillCheckRow key={skill.name} skill={skill} busy={busy} onToggle={onToggle} />)}</ul></>}
    </>}
    {error && <p className="error-text" role="alert">{error}</p>}
  </section>;
}

/** The Skills section of a Bot's settings. Turning a box on or off is the owner's choice for this Bot only. */
export function BotSkills({ botId, refreshToken = 0 }: { botId: string; refreshToken?: number }) {
  const { t } = useTranslation("bots");
  const [listing, setListing] = useState<BotSkillListing>({ skills: [], bundles: [] });
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const alive = useRef(false);
  const path = `/api/bots/${encodeURIComponent(botId)}/skills`;
  const load = useCallback(async () => {
    try {
      const value = await request<BotSkillListing>(path);
      if (alive.current) { setListing({ skills: value.skills ?? [], bundles: value.bundles ?? [] }); setError(""); }
    } catch { if (alive.current) setError(t("skills.error.load")); }
    finally { if (alive.current) setLoading(false); }
  }, [path, t]);
  useEffect(() => {
    alive.current = true; setLoading(true); setListing({ skills: [], bundles: [] }); void load();
    const focus = () => { if (!document.hidden) void load(); };
    window.addEventListener("focus", focus);
    return () => { alive.current = false; window.removeEventListener("focus", focus); };
  }, [load, refreshToken]);
  async function put(url: string, enabled: boolean) {
    if (busy) return;
    setBusy(true); setError("");
    try { await request(url, { method: "PUT", body: JSON.stringify({ enabled }) }); await load(); }
    catch { if (alive.current) setError(t("skills.error.save")); }
    finally { if (alive.current) setBusy(false); }
  }
  return <BotSkillsView botId={botId} listing={listing} loading={loading} busy={busy} error={error}
    onToggle={(name, next) => void put(`${path}/${encodeURIComponent(name)}`, next)}
    onToggleBundle={(id, next) => void put(`/api/bots/${encodeURIComponent(botId)}/skill-bundles/${encodeURIComponent(id)}`, next)} />;
}
