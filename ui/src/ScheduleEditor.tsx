import { useState } from "react";
import { api, ApiError } from "./api";
import { contentEditPatch } from "./contentEditPatch";
import { useTranslation } from "./i18n";
import type { MemoryInput, Schedule } from "./types";

export function ScheduleEditor({ schedule, disabled, onSave }: { schedule: Schedule; disabled: boolean; onSave: (action: () => Promise<unknown>) => Promise<void> }) {
  const { t } = useTranslation("schedules");
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(schedule.title || "");
  const [description, setDescription] = useState(schedule.description || "");
  const [content, setContent] = useState(schedule.content);
  const [baseline, setBaseline] = useState<MemoryInput>({ title: schedule.title || "", description: schedule.description || "", content: schedule.content });
  const [error, setError] = useState("");
  if (!editing) return <button type="button" disabled={disabled} onClick={() => { setBaseline({ title: schedule.title || "", description: schedule.description || "", content: schedule.content }); setTitle(schedule.title || ""); setDescription(schedule.description || ""); setContent(schedule.content); setError(""); setEditing(true); }}>{t("editor.edit")}</button>;
  return <form className="work-create schedule-edit" onSubmit={event => {
    event.preventDefault(); setError("");
    const patch = contentEditPatch(baseline, { title, description, content });
    if (!patch) { setEditing(false); return; }
    void onSave(async () => {
      try {
        const result = await api.updateSchedule(schedule.id, patch);
        setEditing(false); return result;
      } catch (cause) { setError(cause instanceof ApiError && cause.code === "edit_conflict" ? t("editor.conflict") : cause instanceof Error ? cause.message : t("editor.save_failed")); throw cause; }
    });
  }}>
    <label>{t("editor.title")}<input aria-label={t("editor.title_aria")} value={title} maxLength={120} disabled={disabled} onChange={event => setTitle(event.target.value)} /></label>
    <label>{t("editor.description")}<textarea aria-label={t("editor.description_aria")} value={description} maxLength={280} disabled={disabled} rows={2} onChange={event => setDescription(event.target.value)} /></label>
    <label>{t("editor.instructions")}<textarea aria-label={t("editor.instructions_aria")} value={content} maxLength={32768} required disabled={disabled} rows={6} onChange={event => setContent(event.target.value)} /></label>
    {error && <p className="error-text" role="alert">{error}</p>}
    <div className="work-create-actions"><button type="button" disabled={disabled} onClick={() => setEditing(false)}>{t("editor.cancel")}</button><button disabled={disabled || !content.trim()}>{t("editor.save")}</button></div>
  </form>;
}
