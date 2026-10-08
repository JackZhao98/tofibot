import { useState } from "react";
import { TofiIcon as Icon } from "./icons";
import { ConfirmAction } from "./InteractionSystem";
import { useDebugMode } from "./debugMode";
import { isDesktop } from "./desktop";
import { WakeableCat } from "./motion-lab/lib/WakeableCat";
import { memoryDisplay } from "./displayMetadata";
import { ApiError } from "./api";
import { contentEditPatch } from "./contentEditPatch";
import type { ContentPatch, Memory, MemoryInput } from "./types";
import { useTranslation } from "./i18n";
import "./memory-metadata.css";

const empty: MemoryInput = { title: "", description: "", content: "" };
const ready = (input: MemoryInput) => !!(input.title.trim() && input.description.trim() && input.content.trim());

export function MemoryPanel({ memories, scope = "bot", onClose, onCreate, onUpdate, onDelete }: { memories: Memory[]; scope?: "bot" | "group"; conversationId: string; onClose: () => void; onCreate: (input: MemoryInput) => Promise<void>; onUpdate: (id: string, input: ContentPatch) => Promise<void>; onDelete: (id: string) => Promise<void> }) {
  const debug = useDebugMode();
  const { t } = useTranslation("bots");
  const [draft, setDraft] = useState<MemoryInput>(empty);
  const [editing, setEditing] = useState<string | null>(null);
  const [edit, setEdit] = useState<MemoryInput>(empty);
  const [baseline, setBaseline] = useState<MemoryInput>(empty);
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function save(action: () => Promise<void>, success: () => void) {
    if (busy) return;
    setBusy(true); setError("");
    try { await action(); success(); } catch (cause) { setError(cause instanceof ApiError && cause.code === "edit_conflict" ? t("memory.error.edit_conflict") : cause instanceof Error ? cause.message : t("memory.error.save")); }
    finally { setBusy(false); }
  }
  function fields(input: MemoryInput, change: (input: MemoryInput) => void, isEdit = false) {
    const aria = isEdit
      ? { title: t("memory.edit.title_aria"), description: t("memory.edit.description_aria"), content: t("memory.edit.content_aria") }
      : { title: t("memory.create.title_aria"), description: t("memory.create.description_aria"), content: t("memory.create.content_aria") };
    return <>
      <label>{t("memory.field.title")}<input aria-label={aria.title} maxLength={120} required={!isEdit} value={input.title} disabled={busy} onChange={event => change({ ...input, title: event.target.value })} /></label>
      <label>{t("memory.field.description")}<textarea aria-label={aria.description} maxLength={280} required={!isEdit} rows={2} value={input.description} disabled={busy} onChange={event => change({ ...input, description: event.target.value })} /></label>
      <label>{t("memory.field.content")}<textarea aria-label={aria.content} required rows={4} value={input.content} disabled={busy} onChange={event => change({ ...input, content: event.target.value })} placeholder={t("memory.field.content_placeholder")} /></label>
    </>;
  }
  const visible = memories.filter(item => [item.title, item.description, item.content].some(value => value?.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())));
  return <div className="detail-content">
    <div className="detail-heading"><h2>{scope === "group" ? t("memory.heading.group") : t("memory.heading.bot")}</h2><button className="close-button" aria-label={t("memory.close_aria")} onClick={onClose}><Icon name="close" size={18} /></button></div>
    <p className="field-note">{scope === "group" ? t("memory.note.group") : t("memory.note.bot")}</p>
    {error && <p className="error-text" role="alert">{error}</p>}
    <form className="memory-create" onSubmit={event => { event.preventDefault(); if (ready(draft)) void save(() => onCreate(draft), () => setDraft(empty)); }}>
      {fields(draft, setDraft)}
      <button className="secondary-button" disabled={!ready(draft) || busy}>{t("memory.save_memory")}</button>
    </form>
    {!!memories.length && <label className="memory-search">{t("memory.search")}<input type="search" aria-label={t("memory.search")} value={query} onChange={event => setQuery(event.target.value)} /></label>}
    <div className="memory-list">{visible.map(memory => {
      const display = memoryDisplay(memory);
      return <div className="memory-card" key={memory.id}>{editing === memory.id ? <form className="memory-edit" onSubmit={event => { event.preventDefault(); if (!edit.content.trim()) return; const patch = contentEditPatch(baseline, edit); if (!patch) { setEditing(null); return; } void save(() => onUpdate(memory.id, patch), () => setEditing(null)); }}>
        {fields(edit, setEdit, true)}
        <div className="card-actions"><button disabled={!edit.content.trim() || busy}>{t("memory.save")}</button><button type="button" disabled={busy} onClick={() => setEditing(null)}>{t("memory.cancel")}</button></div>
      </form> : <>
        <strong className="memory-title">{display.title}</strong><p className="memory-description">{display.description}</p>
        <details className="memory-details"><summary>{t("memory.view_full")}</summary><p>{memory.content}</p></details>
        <div className="memory-footer">{debug && <span>{t("memory.revision", { revision: memory.revision })}</span>}<span><button disabled={busy} onClick={() => { const initial = { title: memory.title || "", description: memory.description || "", content: memory.content }; setEditing(memory.id); setBaseline(initial); setEdit(initial); setError(""); }}>{t("memory.edit_action")}</button><ConfirmAction label={t("memory.delete")} question={t("memory.delete_question")} disabled={busy} onConfirm={() => save(() => onDelete(memory.id), () => {})} /></span></div>
      </>}</div>;
    })}{!visible.length && !!memories.length && <div className="panel-empty">{t("memory.no_match")}</div>}{!memories.length && (isDesktop ? <div className="panel-empty">{t("memory.empty_short")}</div> : <div className="panel-empty web-memory-empty"><WakeableCat config={{ shape: "loaf", pattern: "solid", palette: "ivory" }} name={t("memory.cat_name")} size={84} /><strong>{t("memory.empty_title")}</strong><p>{scope === "group" ? t("memory.empty.group") : t("memory.empty.bot")}</p></div>)}</div>
  </div>;
}
