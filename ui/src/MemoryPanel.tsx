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
import "./memory-metadata.css";

const empty: MemoryInput = { title: "", description: "", content: "" };
const ready = (input: MemoryInput) => !!(input.title.trim() && input.description.trim() && input.content.trim());

export function MemoryPanel({ memories, scope = "bot", onClose, onCreate, onUpdate, onDelete }: { memories: Memory[]; scope?: "bot" | "group"; conversationId: string; onClose: () => void; onCreate: (input: MemoryInput) => Promise<void>; onUpdate: (id: string, input: ContentPatch) => Promise<void>; onDelete: (id: string) => Promise<void> }) {
  const debug = useDebugMode();
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
    try { await action(); success(); } catch (cause) { setError(cause instanceof ApiError && cause.code === "edit_conflict" ? "这条记忆在编辑期间已更改。草稿已保留，请取消后重新打开核对。" : cause instanceof Error ? cause.message : "暂时无法保存记忆"); }
    finally { setBusy(false); }
  }
  function fields(input: MemoryInput, change: (input: MemoryInput) => void, prefix: string, isEdit = false) {
    return <>
      <label>标题<input aria-label={`${prefix}标题`} maxLength={120} required={!isEdit} value={input.title} disabled={busy} onChange={event => change({ ...input, title: event.target.value })} /></label>
      <label>说明<textarea aria-label={`${prefix}说明`} maxLength={280} required={!isEdit} rows={2} value={input.description} disabled={busy} onChange={event => change({ ...input, description: event.target.value })} /></label>
      <label>完整记忆内容<textarea aria-label={prefix} required rows={4} value={input.content} disabled={busy} onChange={event => change({ ...input, content: event.target.value })} placeholder="记录一个需要长期记住的事实…" /></label>
    </>;
  }
  const visible = memories.filter(item => [item.title, item.description, item.content].some(value => value?.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())));
  return <div className="detail-content">
    <div className="detail-heading"><h2>{scope === "group" ? "本群共享记忆" : "这个 Bot 的记忆"}</h2><button className="close-button" aria-label="关闭记忆" onClick={onClose}><Icon name="close" size={18} /></button></div>
    <p className="field-note">{scope === "group" ? "这里的记忆由本群成员共享。" : "这里保存这个 Bot 的记忆。"}切换模型会保留已保存的记忆和聊天记录；不同模型使用这些内容的方式可能不同。</p>
    {error && <p className="error-text" role="alert">{error}</p>}
    <form className="memory-create" onSubmit={event => { event.preventDefault(); if (ready(draft)) void save(() => onCreate(draft), () => setDraft(empty)); }}>
      {fields(draft, setDraft, "新增记忆")}
      <button className="secondary-button" disabled={!ready(draft) || busy}>保存记忆</button>
    </form>
    {!!memories.length && <label className="memory-search">搜索记忆<input type="search" aria-label="搜索记忆" value={query} onChange={event => setQuery(event.target.value)} /></label>}
    <div className="memory-list">{visible.map(memory => {
      const display = memoryDisplay(memory);
      return <div className="memory-card" key={memory.id}>{editing === memory.id ? <form className="memory-edit" onSubmit={event => { event.preventDefault(); if (!edit.content.trim()) return; const patch = contentEditPatch(baseline, edit); if (!patch) { setEditing(null); return; } void save(() => onUpdate(memory.id, patch), () => setEditing(null)); }}>
        {fields(edit, setEdit, "编辑记忆", true)}
        <div className="card-actions"><button disabled={!edit.content.trim() || busy}>保存</button><button type="button" disabled={busy} onClick={() => setEditing(null)}>取消</button></div>
      </form> : <>
        <strong className="memory-title">{display.title}</strong><p className="memory-description">{display.description}</p>
        <details className="memory-details"><summary>查看完整记忆</summary><p>{memory.content}</p></details>
        <div className="memory-footer">{debug && <span>修订 {memory.revision}</span>}<span><button disabled={busy} onClick={() => { const initial = { title: memory.title || "", description: memory.description || "", content: memory.content }; setEditing(memory.id); setBaseline(initial); setEdit(initial); setError(""); }}>编辑</button><ConfirmAction label="删除" question="删除这条记忆？" disabled={busy} onConfirm={() => save(() => onDelete(memory.id), () => {})} /></span></div>
      </>}</div>;
    })}{!visible.length && !!memories.length && <div className="panel-empty">没有匹配的记忆。</div>}{!memories.length && (isDesktop ? <div className="panel-empty">还没有记忆。</div> : <div className="panel-empty web-memory-empty"><WakeableCat config={{ shape: "loaf", pattern: "solid", palette: "ivory" }} name="糯米" size={84} /><strong>还没有记忆</strong><p>{scope === "group" ? "本群保存的共享记忆会出现在这里。" : "这个 Bot 保存的记忆会出现在这里。"}</p></div>)}</div>
  </div>;
}
