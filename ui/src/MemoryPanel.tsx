import { useState } from "react";
import { TofiIcon as Icon } from "./icons";
import { ConfirmAction } from "./InteractionSystem";
import { useDebugMode } from "./debugMode";
import { isDesktop } from "./desktop";
import { WakeableCat } from "./motion-lab/lib/WakeableCat";
import { memoryDisplay } from "./displayMetadata";
import type { Memory, MemoryInput } from "./types";
import "./memory-metadata.css";

const empty: MemoryInput = { title: "", description: "", content: "" };
const ready = (input: MemoryInput) => !!(input.title.trim() && input.description.trim() && input.content.trim());

export function MemoryPanel({ memories, onClose, onCreate, onUpdate, onDelete }: { memories: Memory[]; conversationId: string; onClose: () => void; onCreate: (input: MemoryInput) => Promise<void>; onUpdate: (id: string, input: MemoryInput) => Promise<void>; onDelete: (id: string) => Promise<void> }) {
  const debug = useDebugMode();
  const [draft, setDraft] = useState<MemoryInput>(empty);
  const [editing, setEditing] = useState<string | null>(null);
  const [edit, setEdit] = useState<MemoryInput>(empty);
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function save(action: () => Promise<void>, success: () => void) {
    if (busy) return;
    setBusy(true); setError("");
    try { await action(); success(); } catch (cause) { setError(cause instanceof Error ? cause.message : "暂时无法保存记忆"); }
    finally { setBusy(false); }
  }
  function fields(input: MemoryInput, change: (input: MemoryInput) => void, prefix: string) {
    return <>
      <label>标题<input aria-label={`${prefix}标题`} maxLength={120} required value={input.title} disabled={busy} onChange={event => change({ ...input, title: event.target.value })} /></label>
      <label>说明<textarea aria-label={`${prefix}说明`} maxLength={280} required rows={2} value={input.description} disabled={busy} onChange={event => change({ ...input, description: event.target.value })} /></label>
      <label>完整记忆内容<textarea aria-label={prefix} required rows={4} value={input.content} disabled={busy} onChange={event => change({ ...input, content: event.target.value })} placeholder="记录一个需要长期记住的事实…" /></label>
    </>;
  }
  const visible = memories.filter(item => [item.title, item.description, item.content].some(value => value?.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())));
  return <div className="detail-content">
    <div className="detail-heading"><h2>记忆</h2><button className="close-button" aria-label="关闭记忆" onClick={onClose}><Icon name="close" size={18} /></button></div>
    {error && <p className="error-text" role="alert">{error}</p>}
    <form className="memory-create" onSubmit={event => { event.preventDefault(); if (ready(draft)) void save(() => onCreate(draft), () => setDraft(empty)); }}>
      {fields(draft, setDraft, "新增记忆")}
      <button className="secondary-button" disabled={!ready(draft) || busy}>保存记忆</button>
    </form>
    {!!memories.length && <label className="memory-search">搜索记忆<input type="search" aria-label="搜索记忆" value={query} onChange={event => setQuery(event.target.value)} /></label>}
    <div className="memory-list">{visible.map(memory => {
      const display = memoryDisplay(memory);
      return <div className="memory-card" key={memory.id}>{editing === memory.id ? <form className="memory-edit" onSubmit={event => { event.preventDefault(); if (ready(edit)) void save(() => onUpdate(memory.id, edit), () => setEditing(null)); }}>
        {fields(edit, setEdit, "编辑记忆")}
        <div className="card-actions"><button disabled={!ready(edit) || busy}>保存</button><button type="button" disabled={busy} onClick={() => setEditing(null)}>取消</button></div>
      </form> : <>
        <strong className="memory-title">{display.title}</strong><p className="memory-description">{display.description}</p>
        <details className="memory-details"><summary>查看完整记忆</summary><p>{memory.content}</p></details>
        <div className="memory-footer">{debug && <span>修订 {memory.revision}</span>}<span><button disabled={busy} onClick={() => { setEditing(memory.id); setEdit({ title: memory.title || "", description: memory.description || "", content: memory.content }); }}>编辑</button><ConfirmAction label="删除" question="删除这条记忆？" disabled={busy} onConfirm={() => save(() => onDelete(memory.id), () => {})} /></span></div>
      </>}</div>;
    })}{!visible.length && !!memories.length && <div className="panel-empty">没有匹配的记忆。</div>}{!memories.length && (isDesktop ? <div className="panel-empty">还没有记忆。</div> : <div className="panel-empty web-memory-empty"><WakeableCat config={{ shape: "loaf", pattern: "solid", palette: "ivory" }} name="糯米" size={84} /><strong>还没有记忆</strong><p>Bot 记住的事会出现在这里。</p></div>)}</div>
  </div>;
}
