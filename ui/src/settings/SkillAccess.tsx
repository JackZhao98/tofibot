import {useEffect, useId, useRef, useState} from "react";
import {createPortal} from "react-dom";
import {BotAvatar} from "../BotAvatar";
import {request} from "../api";
import {useTranslation} from "../i18n";
import type {Bot} from "../types";
import {Banner, SettingsRow} from "./components";

export type SkillAccess = {mode: "all"} | {mode: "selected"; bot_ids?: string[]};

const MAX_CHIPS = 4;

/** Selected Bot ids that still exist and are not archived; archived Bots keep their rows but never run. */
export function accessBots(access: SkillAccess, bots: Bot[]): Bot[] {
  if (access.mode === "all") return bots;
  const ids = new Set(access.bot_ids ?? []);
  return bots.filter(bot => ids.has(bot.id));
}

/** The "Who can use it" row on a skill card: avatar chips, a summary and a Change button. */
export function SkillAccessRow({skill, access, bots, disabled, onChange}: {skill: string; access: SkillAccess; bots: Bot[]; disabled?: boolean; onChange: () => void}) {
  const {t} = useTranslation("extensions");
  const active = bots.filter(bot => !bot.archived);
  const shown = accessBots(access, active);
  const summary = access.mode === "all" ? t("skills.access.all") : t("skills.access.some", {selected: shown.length, total: active.length});
  const chips = shown.slice(0, MAX_CHIPS);
  const copy = <>
    <span className="skill-access-chips" data-testid={`skill-access-chips-${skill}`}>
      {chips.map(bot => <span className="skill-access-chip" key={bot.id} title={bot.name}><BotAvatar id={bot.id} mini/></span>)}
      {shown.length > chips.length && <span className="skill-access-more">+{shown.length - chips.length}</span>}
    </span>
    <span className="skill-access-summary">{summary}</span>
    {access.mode === "selected" && shown.length > 0 && <span className="skill-access-names">{shown.map(bot => bot.name).join(" · ")}</span>}
  </>;
  return <SettingsRow label={t("skills.access.label")} description={copy} control={<button type="button" className="secondary-button" disabled={disabled} onClick={onChange} aria-label={t("skills.access.change_for", {name: skill})}>{t("skills.access.change")}</button>}/>;
}

/** Small modal sheet: All Bots / Only selected Bots, with a checklist of non-archived Bots. Saves through the PUT. */
export function SkillAccessSheet({skill, title, access, bots, onClose, onSaved}: {skill: string; title: string; access: SkillAccess; bots: Bot[]; onClose: () => void; onSaved: () => void}) {
  const {t} = useTranslation("extensions");
  const active = bots.filter(bot => !bot.archived);
  const [mode, setMode] = useState<"all" | "selected">(access.mode);
  const [picked, setPicked] = useState<string[]>(() => access.mode === "selected" ? (access.bot_ids ?? []).filter(id => active.some(bot => bot.id === id)) : []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pane = useRef<HTMLDivElement>(null);
  const headingId = useId();
  const cannotSave = busy || (mode === "selected" && picked.length === 0);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    pane.current?.focus();
    const keys = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.defaultPrevented) { event.preventDefault(); event.stopPropagation(); onClose(); return; }
      if (event.key !== "Tab") return;
      const targets = Array.from(pane.current?.querySelectorAll<HTMLElement>("button:not([disabled]), input:not([disabled])") ?? []).filter(el => el.getClientRects().length);
      const i = targets.indexOf(document.activeElement as HTMLElement);
      if (i < 0 || (!event.shiftKey && i === targets.length - 1) || (event.shiftKey && i === 0)) {
        event.preventDefault(); (event.shiftKey ? targets.at(-1) : targets[0])?.focus();
      }
    };
    document.addEventListener("keydown", keys, true);
    return () => { document.removeEventListener("keydown", keys, true); previous?.focus(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function save() {
    if (cannotSave) return;
    setBusy(true); setError("");
    try {
      await request(`/api/extensions/skills/${encodeURIComponent(skill)}/access`, {method: "PUT", body: JSON.stringify(mode === "all" ? {mode} : {mode, bot_ids: picked})});
      onSaved();
    } catch (cause) {
      const code = (cause as {code?: string}).code;
      setError(code === "unknown_bot" ? t("skills.access.error.unknown_bot") : t("skills.access.error.save_failed"));
      setBusy(false);
    }
  }

  const toggle = (id: string) => setPicked(current => current.includes(id) ? current.filter(x => x !== id) : [...current, id]);
  return createPortal(<div className="sheet-overlay" onMouseDown={event => { if (event.target === event.currentTarget && !busy) onClose(); }}>
    <div ref={pane} className="sheet skill-access-sheet" role="dialog" aria-modal="true" aria-labelledby={headingId} tabIndex={-1}>
      <h3 id={headingId}>{t("skills.access.sheet_title", {name: title})}</h3>
      <fieldset className="sheet-fieldset" disabled={busy}>
        <legend className="sr-only">{t("skills.access.label")}</legend>
        <label className="sheet-radio"><input type="radio" name="skill-access-mode" checked={mode === "all"} onChange={() => setMode("all")}/><span><strong>{t("skills.access.mode_all")}</strong><small>{t("skills.access.mode_all_hint")}</small></span></label>
        <label className="sheet-radio"><input type="radio" name="skill-access-mode" checked={mode === "selected"} onChange={() => setMode("selected")}/><span><strong>{t("skills.access.mode_selected")}</strong><small>{t("skills.access.mode_selected_hint")}</small></span></label>
      </fieldset>
      <fieldset className="sheet-fieldset sheet-checklist" disabled={busy || mode === "all"} aria-describedby={mode === "selected" && picked.length === 0 ? `${headingId}-pick` : undefined}>
        <legend className="sr-only">{t("skills.access.bots_label")}</legend>
        {active.length === 0 && <p className="field-note">{t("skills.access.no_bots")}</p>}
        {active.map(bot => <label className="sheet-check" key={bot.id}><input type="checkbox" checked={picked.includes(bot.id)} onChange={() => toggle(bot.id)}/><span className="skill-access-chip"><BotAvatar id={bot.id} mini/></span><span>{bot.name}</span></label>)}
      </fieldset>
      {mode === "selected" && picked.length === 0 && active.length > 0 && <p className="field-note" id={`${headingId}-pick`}>{t("skills.access.pick_required")}</p>}
      {error && <Banner tone="error" title={error}/>}
      <div className="sheet-actions">
        <button type="button" className="secondary-button" disabled={busy} onClick={onClose}>{t("action.cancel")}</button>
        <button type="button" className="primary-button" disabled={cannotSave} onClick={() => void save()}>{busy ? t("skills.access.saving") : t("skills.access.save")}</button>
      </div>
    </div>
  </div>, document.body);
}
