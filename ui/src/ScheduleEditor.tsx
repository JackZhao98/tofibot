import { useState, type ReactNode } from "react";
import { api, ApiError } from "./api";
import { contentEditPatch } from "./contentEditPatch";
import { useTranslation } from "./i18n";
import { ScheduleIconButton } from "./ScheduleActions";
import { ScheduleTimingFields, timingDraftOf, timingEditPatch, type TimingDraft } from "./ScheduleTimingFields";
import type { MemoryInput, Schedule } from "./types";

export function ScheduleEditor({ schedule, disabled, onSave, actions }: { schedule: Schedule; disabled: boolean; /** The other icon controls of the card, drawn after Edit in one row. */ actions?: ReactNode; onSave: (action: () => Promise<unknown>) => Promise<void> }) {
  const { t } = useTranslation("schedules");
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(schedule.title || "");
  const [description, setDescription] = useState(schedule.description || "");
  const [content, setContent] = useState(schedule.content);
  const [baseline, setBaseline] = useState<MemoryInput>({ title: schedule.title || "", description: schedule.description || "", content: schedule.content });
  const [timing, setTiming] = useState<TimingDraft>(() => timingDraftOf(schedule));
  const [timingBaseline, setTimingBaseline] = useState<TimingDraft>(() => timingDraftOf(schedule));
  const [error, setError] = useState("");
  // A one-time schedule that has been claimed or has finished cannot move; the server refuses it too.
  const timingLocked = schedule.status === "completed" || (schedule.kind === "once" && !!schedule.last_run_id);
  if (!editing) return <div className="work-actions work-icon-actions"><ScheduleIconButton icon="edit" label={t("editor.edit")} disabled={disabled} onClick={() => {
    const opened = timingDraftOf(schedule);
    setBaseline({ title: schedule.title || "", description: schedule.description || "", content: schedule.content });
    setTitle(schedule.title || ""); setDescription(schedule.description || ""); setContent(schedule.content);
    setTiming(opened); setTimingBaseline(opened); setError(""); setEditing(true);
  }} />{actions}</div>;
  const timezone = timing.zone || schedule.timezone;
  return <form className="work-create schedule-edit" onSubmit={event => {
    event.preventDefault(); setError("");
    const contentPatch = contentEditPatch(baseline, { title, description, content });
    const timingPatch = timingLocked ? undefined : timingEditPatch(schedule, timingBaseline, timing);
    if (!contentPatch && !timingPatch) { setEditing(false); return; }
    void onSave(async () => {
      try {
        const result = await api.updateSchedule(schedule.id, { ...contentPatch, ...timingPatch });
        setEditing(false); return result;
      } catch (cause) { setError(cause instanceof ApiError && cause.code === "edit_conflict" ? t("editor.conflict") : cause instanceof Error ? cause.message : t("editor.save_failed")); throw cause; }
    });
  }}>
    <label>{t("editor.title")}<input aria-label={t("editor.title_aria")} value={title} maxLength={120} disabled={disabled} onChange={event => setTitle(event.target.value)} /></label>
    <label>{t("editor.description")}<textarea aria-label={t("editor.description_aria")} value={description} maxLength={280} disabled={disabled} rows={2} onChange={event => setDescription(event.target.value)} /></label>
    <label>{t("editor.instructions")}<textarea aria-label={t("editor.instructions_aria")} value={content} maxLength={32768} required disabled={disabled} rows={6} onChange={event => setContent(event.target.value)} /></label>
    <fieldset className="schedule-edit-timing" style={{ display: "contents", border: 0, padding: 0, margin: 0, minWidth: 0 }} aria-label={t("editor.timing")}>
      {timingLocked ? <p className="field-note">{t("editor.timing_locked")}</p> : <ScheduleTimingFields draft={timing} timezone={timezone} disabled={disabled} onChange={change => setTiming(current => ({ ...current, ...change }))} />}
    </fieldset>
    {error && <p className="error-text" role="alert">{error}</p>}
    <div className="work-create-actions"><button type="button" disabled={disabled} onClick={() => setEditing(false)}>{t("editor.cancel")}</button><button disabled={disabled || !content.trim() || (timing.kind === "weekly" && !timing.weekdays.length)}>{t("editor.save")}</button></div>
  </form>;
}
