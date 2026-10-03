import { useState } from "react";
import { api } from "./api";
import type { Schedule } from "./types";

export function ScheduleEditor({ schedule, disabled, onSave }: { schedule: Schedule; disabled: boolean; onSave: (action: () => Promise<unknown>) => Promise<void> }) {
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(schedule.title || "");
  const [description, setDescription] = useState(schedule.description || "");
  const [content, setContent] = useState(schedule.content);
  const [error, setError] = useState("");
  if (!editing) return <button type="button" disabled={disabled} onClick={() => { setTitle(schedule.title || ""); setDescription(schedule.description || ""); setContent(schedule.content); setError(""); setEditing(true); }}>编辑</button>;
  return <form className="work-create schedule-edit" onSubmit={event => {
    event.preventDefault(); setError("");
    void onSave(async () => {
      try {
        const result = await api.updateSchedule(schedule.id, { title, description, ...(content !== schedule.content ? { content } : {}) });
        setEditing(false); return result;
      } catch (cause) { setError(cause instanceof Error ? cause.message : "暂时无法保存日程"); throw cause; }
    });
  }}>
    <label>标题<input aria-label="编辑日程标题" value={title} maxLength={120} required disabled={disabled} onChange={event => setTitle(event.target.value)} /></label>
    <label>说明<textarea aria-label="编辑日程说明" value={description} maxLength={280} required disabled={disabled} rows={2} onChange={event => setDescription(event.target.value)} /></label>
    <label>完整执行指令<textarea aria-label="编辑日程执行指令" value={content} maxLength={32768} required disabled={disabled} rows={6} onChange={event => setContent(event.target.value)} /></label>
    {error && <p className="error-text" role="alert">{error}</p>}
    <div className="work-create-actions"><button type="button" disabled={disabled} onClick={() => setEditing(false)}>取消</button><button disabled={disabled || !title.trim() || !description.trim() || !content.trim()}>保存</button></div>
  </form>;
}
