import { TextArea } from "./ds/TextArea/TextArea";
import { TextInput } from "./ds/TextInput/TextInput";
import { Button } from "./ds/Button/Button";
import { Banner } from "./ds/Banner/Banner";
import { Card } from "./ds/Card/Card";
import { Field } from "./ds/Field/Field";
import { IconButton } from "./ds/IconButton/IconButton";
import { Tag } from "./ds/Tag/Tag";
import { Disclosure } from "./InteractionPrimitives";
import { useState } from "react";
import { CATS, EmptyState } from "./EmptyCat";
import { ConfirmAction } from "./ConfirmAction";
import { useDebugMode } from "./debugMode";
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
  const [creating, setCreating] = useState(false);
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
      <Field label={t("memory.field.title")}><TextInput aria-label={aria.title} maxLength={120} required={!isEdit} value={input.title} disabled={busy} onChange={event => change({ ...input, title: event.target.value })} /></Field>
      <Field label={t("memory.field.description")}><TextArea aria-label={aria.description} maxLength={280} required={!isEdit} rows={2} value={input.description} disabled={busy} onChange={event => change({ ...input, description: event.target.value })} /></Field>
      <Field label={t("memory.field.content")}><TextArea aria-label={aria.content} required rows={4} value={input.content} disabled={busy} onChange={event => change({ ...input, content: event.target.value })} placeholder={t("memory.field.content_placeholder")} /></Field>
    </>;
  }
  const visible = memories.filter(item => [item.title, item.description, item.content].some(value => value?.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())));
  return <div className="detail-content">
    <div className="detail-heading"><h2>{scope === "group" ? t("memory.heading.group") : t("memory.heading.bot")}</h2><IconButton className="close-button" icon="close" label={t("memory.close_aria")} onClick={onClose} /></div>
    {error && <Banner tone="error">{error}</Banner>}
    <Button size="sm" className="memory-add" aria-expanded={creating} onClick={() => setCreating(value => !value)}>{t("memory.add")}</Button>
    {creating && <form className="memory-create" onSubmit={event => { event.preventDefault(); if (ready(draft)) void save(() => onCreate(draft), () => { setDraft(empty); setCreating(false); }); }}>
      {fields(draft, setDraft)}
      <Button type="submit" variant="primary" size="sm" disabled={!ready(draft) || busy}>{t("memory.save_memory")}</Button>
    </form>}
    {!!memories.length && <Field className="memory-search" label={t("memory.search")}><TextInput type="search" aria-label={t("memory.search")} value={query} onChange={event => setQuery(event.target.value)} /></Field>}
    <div className="memory-list">{visible.map(memory => {
      const display = memoryDisplay(memory);
      return <Card variant="quiet" className="memory-card" key={memory.id}>{editing === memory.id ? <form className="memory-edit" onSubmit={event => { event.preventDefault(); if (!edit.content.trim()) return; const patch = contentEditPatch(baseline, edit); if (!patch) { setEditing(null); return; } void save(() => onUpdate(memory.id, patch), () => setEditing(null)); }}>
        {fields(edit, setEdit, true)}
        <div className="card-actions"><Button type="submit" variant="primary" size="sm" disabled={!edit.content.trim() || busy}>{t("memory.save")}</Button><Button variant="ghost" size="sm" disabled={busy} onClick={() => setEditing(null)}>{t("memory.cancel")}</Button></div>
      </form> : <>
        <strong className="memory-title">{display.title}</strong><p className="memory-description">{display.description}</p>
        <Disclosure title={t("memory.view_full")}><div className="memory-details"><p>{memory.content}</p></div></Disclosure>
        <div className="memory-footer">{debug && <Tag>{t("memory.revision", { revision: memory.revision })}</Tag>}<span><Button variant="ghost" size="sm" disabled={busy} onClick={() => { const initial = { title: memory.title || "", description: memory.description || "", content: memory.content }; setEditing(memory.id); setBaseline(initial); setEdit(initial); setError(""); }}>{t("memory.edit_action")}</Button><ConfirmAction label={t("memory.delete")} question={t("memory.delete_question")} disabled={busy} onConfirm={() => save(() => onDelete(memory.id), () => {})} /></span></div>
      </>}</Card>;
    })}{!visible.length && !!memories.length && <EmptyState name="memory-no-match" className="panel-empty is-compact" look={CATS.azuki} pose="curious"><p>{t("memory.no_match")}</p></EmptyState>}{!memories.length && <EmptyState name="memory-empty" className="panel-empty web-memory-empty is-card" look={CATS.mochi} pose="asleep" size={72} title={t("memory.empty_title")}><p>{scope === "group" ? t("memory.empty.group") : t("memory.empty.bot")}</p></EmptyState>}</div>
  </div>;
}
