import { useState } from "react";
import { api, ApiError } from "./api";
import { contentEditPatch } from "./contentEditPatch";
import type { MemoryInput, Schedule } from "./types";

export function ScheduleEditor({ schedule, disabled, onSave }: { schedule: Schedule; disabled: boolean; onSave: (action: () => Promise<unknown>) => Promise<void> }) {
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(schedule.title || "");
  const [description, setDescription] = useState(schedule.description || "");
  const [content, setContent] = useState(schedule.content);
  const [baseline, setBaseline] = useState<MemoryInput>({ title: schedule.title || "", description: schedule.description || "", content: schedule.content });
  const [error, setError] = useState("");
  if (!editing) return <button type="button" disabled={disabled} onClick={() => { setBaseline({ title: schedule.title || "", description: schedule.description || "", content: schedule.content }); setTitle(schedule.title || ""); setDescription(schedule.description || ""); setContent(schedule.content); setError(""); setEditing(true); }}>编辑</button>;
  return <form className="work-create schedule-edit" onSubmit={event => {
    event.preventDefault(); setError("");
    const patch = contentEditPatch(baseline, { title, description, content });
    if (!patch) { setEditing(false); return; }
    void onSave(async () => {
      try {
        const result = await api.updateSchedule(schedule.id, patch);
        setEditing(false); return result;
      } catch (cause) { setError(cause instanceof ApiError && cause.code === "edit_conflict" ? "这项日程在编辑期间已更改。草稿已保留，请取消后重新打开核对。" : cause instanceof Error ? cause.message : "暂时无法保存日程"); throw cause; }
    });
  }}>
    <label>标题<input aria-label="编辑日程标题" value={title} maxLength={120} disabled={disabled} onChange={event => setTitle(event.target.value)} /></label>
    <label>说明<textarea aria-label="编辑日程说明" value={description} maxLength={280} disabled={disabled} rows={2} onChange={event => setDescription(event.target.value)} /></label>
    <label>完整执行指令<textarea aria-label="编辑日程执行指令" value={content} maxLength={32768} required disabled={disabled} rows={6} onChange={event => setContent(event.target.value)} /></label>
    {error && <p className="error-text" role="alert">{error}</p>}
    <div className="work-create-actions"><button type="button" disabled={disabled} onClick={() => setEditing(false)}>取消</button><button disabled={disabled || !content.trim()}>保存</button></div>
  </form>;
}
