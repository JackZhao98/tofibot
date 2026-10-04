import { useCallback, useEffect, useId, useRef, useState } from "react";
import { ApiError, request } from "./api";
import { GazeAvatar } from "./GazeAvatar";
import { isDesktop } from "./desktop";
import { TofiIcon } from "./icons";
import { MessageMarkdown } from "./MessageMarkdown";
import { ApprovalCard } from "./ApprovalCard";
import { useUserTimezone } from "./UserTimezone";
import type { Bot } from "./types";
import { autoReviewPresentation, reconcileQuestion, type Question } from "./questionTimeline";
import { userFormAnswerRows, userFormIdentity, userFormSchemaError, validateUserForm } from "./userForm";
import "./question-card.css";

/** A separate read failure must not prevent ordinary conversation history loading. */
export function useQuestions(conversationId: string | null) {
  const [snapshot, setSnapshot] = useState<{ id: string; items: Question[] }>({ id: "", items: [] });
  const [failure, setFailure] = useState<{ id: string; text: string } | null>(null);
  const [loading, setLoading] = useState(false);
  const generation = useRef(0);
  const requestSequence = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const refresh = useCallback(async (resolved?: Question) => {
    if (!conversationId) return;
    if (resolved?.conversation_id === conversationId) setSnapshot(current => ({ id: conversationId, items: [...(current.id === conversationId ? current.items : []).filter(q => q.question_id !== resolved.question_id), resolved] }));
    const version = generation.current;
    const sequence = ++requestSequence.current;
    controller.current?.abort();
    const pending = new AbortController(); controller.current = pending;
    setLoading(true);
    try {
      const result = await request<{ questions: Question[] }>(`/api/questions?conversation_id=${encodeURIComponent(conversationId)}`, { signal: pending.signal });
      if (version !== generation.current || sequence !== requestSequence.current) return;
      setSnapshot({ id: conversationId, items: (result.questions ?? []).filter(q => q.conversation_id === conversationId) });
      setFailure(null);
    } catch (cause) {
      if (!pending.signal.aborted && version === generation.current && sequence === requestSequence.current) setFailure({ id: conversationId, text: cause instanceof Error ? cause.message : "读取问题失败" });
    } finally {
      if (version === generation.current && sequence === requestSequence.current) setLoading(false);
    }
  }, [conversationId]);
  useEffect(() => {
    generation.current++;
    setFailure(null);
    let stopped = false;
    let timer: number | undefined;
    async function poll() {
      if (!document.hidden) await refresh();
      if (!stopped) timer = window.setTimeout(poll, 3000);
    }
    void poll();
    const focused = () => { if (!document.hidden) void refresh(); };
    window.addEventListener("focus", focused); document.addEventListener("visibilitychange", focused);
    return () => { stopped = true; generation.current++; clearTimeout(timer); controller.current?.abort(); window.removeEventListener("focus", focused); document.removeEventListener("visibilitychange", focused); };
  }, [refresh]);
  return { items: snapshot.id === conversationId ? snapshot.items : [], error: failure?.id === conversationId ? failure.text : "", loading, refresh };
}

function answerLabel(item: Question) {
  if (typeof item.answer === "boolean") return item.answer ? "是" : "否";
  const label = (id: string) => item.options?.find(option => option.id === id)?.label ?? id;
  if (Array.isArray(item.answer)) return item.answer.map(label).join("、");
  if (item.answer && typeof item.answer === "object" && "other_text" in item.answer && typeof item.answer.other_text === "string" && Array.isArray(item.answer.values)) return [...item.answer.values.map(label), `其他：${item.answer.other_text}`].join("、");
  return item.question_type === "single_choice" && typeof item.answer === "string" ? label(item.answer) : String(item.answer ?? "");
}

type QuestionCardProps = { item: Question; bot?: Bot; group?: boolean; archived?: boolean; onChanged: (resolved?: Question) => Promise<void> };

export function QuestionCard(props: QuestionCardProps) {
  if (props.item.question_type === "approval" || (!isDesktop && props.item.question_type === "yes_no" && !props.item.allow_other)) return <BinaryQuestionCard key={props.item.question_id} {...props} />;
  return props.item.question_type === "form"
    ? <UserFormCard key={userFormIdentity(props.item)} {...props} />
    : <LegacyQuestionCard key={props.item.question_id} {...props} />;
}

/** Binary answers use the approval layout without claiming authorization. */
function BinaryQuestionCard({ item, bot, group, archived, onChanged }: QuestionCardProps) {
  const { timezone } = useUserTimezone();
  const [busy, setBusy] = useState<"accept" | "decline" | "cancel" | undefined>();
  const [error, setError] = useState("");
  const [resolved, setResolved] = useState<Question | null>(null);
  const sending = useRef(false);
  const controller = useRef<AbortController | null>(null);
  const current = reconcileQuestion(item, resolved);
  const approval = current.question_type === "approval" ? current.approval : undefined;
  const review = approval?.review;
  const reviewPresentation = autoReviewPresentation(current);
  const reviewBlocked = ["setup_required", "context_required", "unavailable", "policy_denied", "terminal"].includes(review?.status ?? "");
  const autoApproved = current.answered_by === "auto-review" && current.status === "answered" && current.answer === true;
  useEffect(() => () => controller.current?.abort(), []);
  async function answer(value: boolean | null) {
    if (sending.current || current.status !== "pending" || archived || reviewBlocked) return;
    sending.current = true; setBusy(value === null ? "cancel" : value ? "accept" : "decline"); setError("");
    const pending = new AbortController(); controller.current = pending;
    try {
      const result = await request<{ question: Question }>(`/api/questions/${encodeURIComponent(item.question_id)}${value === null ? "" : "/answer"}`, { method:value === null ? "DELETE" : "POST", body:value === null ? undefined : JSON.stringify({ value }), signal:pending.signal });
      setResolved(result.question);
      try { await onChanged(result.question); } catch { if (!pending.signal.aborted) setError("回答已接收，列表刷新失败，请刷新页面查看。"); }
    } catch (cause) {
      if (pending.signal.aborted) return;
      setError(cause instanceof Error ? cause.message : "提交未完成，请重试。");
      if (cause instanceof ApiError && (cause.status === 404 || cause.status === 409)) {
        try { await onChanged(); } catch { if (!pending.signal.aborted) setError("问题状态已变化，请刷新页面后再操作。"); }
      }
    } finally { sending.current = false; if (!pending.signal.aborted) setBusy(undefined); }
  }
  const validTime = Number.isFinite(Date.parse(item.created_at));
  return <article className={`binary-question-message${group ? " is-group" : " is-dm"}`} data-question-id={item.question_id} tabIndex={-1} aria-label={`${bot?.name ?? "Bot"} 的问题`}>
    {group && <div className="message-meta"><strong>{bot?.name ?? "Bot"}</strong></div>}
    <ApprovalCard title={<MessageMarkdown content={item.question} />} avatar={group ? <GazeAvatar id={item.bot_id} mini /> : undefined} badge={reviewPresentation?.badge ?? (autoApproved ? "AutoReview 自动批准" : approval ? "需要你批准" : "需要你回答")}
      time={validTime ? new Intl.DateTimeFormat("zh-CN", { timeZone:timezone, hour:"2-digit", minute:"2-digit", hourCycle:"h23" }).format(new Date(item.created_at)) : undefined}
      exactTime={item.created_at} facts={approval ? [{ label:"动作", value:approval.action }, { label:"对象", value:approval.target }, { label:"影响", value:approval.impact }] : undefined} payload={approval?.payload}
      acceptLabel={approval ? approval.approve_label || "批准" : "是"} declineLabel={approval ? approval.deny_label || "暂不批准" : "否"} onAnswer={value => void answer(value)} busy={busy} disabled={archived}
      secondaryAction={!archived && !reviewBlocked && <><button type="button" disabled={Boolean(busy)} onClick={() => void answer(null)}>{busy === "cancel" ? "取消中…" : approval ? "取消审批" : "取消问题"}</button>{approval?.draft_id && <button type="button" onClick={() => { const node = [...document.querySelectorAll<HTMLElement>("[data-draft-id]")].find(element => element.dataset.draftId === approval.draft_id); node?.scrollIntoView({ behavior:"smooth", block:"center" }); }} >查看草稿</button>}</>}
      note={archived ? "恢复会话后可回答。" : reviewBlocked ? "此提案不可执行；批准不能修复配置或上下文缺口。" : approval ? "批准后会继续任务，具体操作仍需由 Bot 执行。" : undefined} error={error}
      resolution={current.status === "pending" && reviewBlocked ? {label:reviewPresentation?.label ?? "不可执行", accepted:false} : current.status === "pending" ? undefined : { label:current.status === "answered" ? autoApproved ? "AutoReview 自动批准" : approval ? "人工已决定" : "已回答" : current.status === "expired" ? "已过期" : current.status === "run_done" ? "任务已结束" : "已取消", answer:current.status === "answered" ? approval ? current.answer === true ? approval.approve_label || "已批准" : approval.deny_label || "未批准" : answerLabel(current) : undefined, accepted:current.status === "answered" && current.answer === true }} />
    {review && <p className="approval-note" role="status">AutoReview · {reviewPresentation?.label ?? "状态待核实"}：{review.reason}</p>}

  </article>;
}

function UserFormCard({ item, bot, group, archived, onChanged }: QuestionCardProps) {
  const title = useId();
  const privacy = `${title}-privacy`;
  const form = useRef<HTMLFormElement | null>(null);
  const mounted = useRef(false);
  const inFlight = useRef(false);
  const controller = useRef<AbortController | null>(null);
  const [busy, setBusy] = useState<"submit" | "cancel" | null>(null);
  const [error, setError] = useState("");
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [resolved, setResolved] = useState<Question | null>(null);
  const current = reconcileQuestion(item, resolved);
  const active = !resolved && item.status === "pending";
  const fields = item.fields ?? [];
  const schemaError = userFormSchemaError(item.fields);

  // Uncontrolled inputs keep private values out of React state and rendered value
  // attributes. Clear detached nodes too, before an identity/status change commits.
  const attachForm = useCallback((node: HTMLFormElement | null) => {
    if (form.current && form.current !== node) clearUserForm(form.current);
    form.current = node;
  }, []);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      controller.current?.abort();
      if (form.current) clearUserForm(form.current);
    };
  }, []);

  async function finish(cancel = false) {
    if (inFlight.current || !active || archived || !form.current) return;
    let body: string | undefined;
    if (cancel) {
      clearUserForm(form.current);
      setFieldErrors({});
    } else {
      if (schemaError) return;
      const data = new FormData(form.current);
      const values = Object.fromEntries(fields.map(field => [field.id, String(data.get(field.id) ?? "")]));
      const errors = validateUserForm(fields, values);
      setFieldErrors(errors);
      if (Object.keys(errors).length) {
        setError("请检查标出的字段后再提交。");
        const first = fields.find(field => Object.hasOwn(errors, field.id));
        const control = first && form.current.elements.namedItem(first.id);
        if (control instanceof HTMLElement) control.focus();
        return;
      }
      body = JSON.stringify({ fields: values });
    }
    inFlight.current = true;
    setBusy(cancel ? "cancel" : "submit"); setError("");
    const pending = new AbortController(); controller.current = pending;
    let result: { question: Question };
    try {
      const sending = request<{ question: Question }>(`/api/questions/${encodeURIComponent(item.question_id)}${cancel ? "" : "/answer"}`,
        { method: cancel ? "DELETE" : "POST", body, signal: pending.signal });
      body = undefined;
      result = await sending;
    } catch (cause) {
      if (!mounted.current || pending.signal.aborted) return;
      // Server/network messages may echo an input. Only fixed UI copy is safe here.
      if (cause instanceof ApiError && (cause.status === 409 || cause.status === 404)) {
        clearUserForm(form.current);
        setError("表单状态已变化，请确认刷新后的状态再操作。");
        try { await onChanged(); } catch { if (mounted.current) setError("无法刷新表单状态，请刷新页面后再操作。"); }
      } else {
        setError(cancel ? "取消未完成，输入已清空，请重试取消。" : "提交未完成，输入已保留，请检查后重试。");
      }
      return;
    } finally {
      body = undefined;
      if (mounted.current) { inFlight.current = false; setBusy(null); }
    }
    if (!mounted.current || pending.signal.aborted) return;
    clearUserForm(form.current);
    setResolved(result.question);
    // Acceptance clears the draft even if the following list refresh fails.
    try { await onChanged(result.question); }
    catch { if (mounted.current) setError(cancel ? "已取消，状态刷新失败，请刷新页面查看。" : "已接收回答，状态刷新失败，请刷新页面查看。"); }
  }

  return <article className={`question-message${group ? " is-group" : " is-dm"}`} data-question-id={item.question_id} tabIndex={-1}>
    {group && <GazeAvatar id={item.bot_id} mini />}
    <div className="question-body">{group && <div className="message-meta"><strong>{bot?.name ?? "Bot"}</strong></div>}
      <form ref={attachForm} className={`question-card question-form ${active ? "is-pending" : "is-resolved"}`}
        aria-labelledby={title} aria-describedby={active ? privacy : undefined} aria-busy={Boolean(busy)}
        autoComplete="off" noValidate onSubmit={event => { event.preventDefault(); void finish(); }}>
        {active && <span className="question-state">需要你填写</span>}
        <div id={title} className="question-prompt"><MessageMarkdown content={item.question} /></div>
        <p className="question-form-target"><span>目标网站</span> <span>{item.source_url || "未指定"}</span></p>
        {active ? <>
          <div id={privacy} className="question-form-privacy">
            <p>普通字段会以明文提供给 Bot；密码通过私密输入提供，不会作为明文回答显示给 Bot。密码中的空格会保留。</p>
            <p>提交后 Bot 将继续任务，不代表已向网站提交。</p>
          </div>
          {schemaError ? <p className="error-text" role="alert">{schemaError}</p> : <fieldset className="question-form-fields" disabled={Boolean(busy) || archived} aria-labelledby={title}>
            {fields.map((field, index) => {
              const id = `${title}-field-${index}`;
              const fieldError = Object.hasOwn(fieldErrors, field.id) ? fieldErrors[field.id] : "";
              const attributes = {
                id, name: field.id, required: field.required, placeholder: field.placeholder,
                autoComplete: field.type === "password" ? "new-password" : "off",
                "aria-invalid": Boolean(fieldError), "aria-describedby": fieldError ? `${id}-error ${privacy}` : privacy,
              };
              return <div className="question-form-field" key={field.id}>
                <label htmlFor={id}><span>{field.label}{field.type === "password" && <span className="question-form-private"> · 私密输入</span>}</span><span className="question-form-optional">{field.required ? "必填" : "选填"}</span></label>
                {field.type === "textarea" ? <textarea {...attributes} rows={3} />
                  : <input {...attributes} type={field.type} autoCapitalize="none" autoCorrect="off" spellCheck={false} />}
                {fieldError && <p id={`${id}-error`} className="error-text">{fieldError}</p>}
              </div>;
            })}
          </fieldset>}
          {archived ? <p className="field-note">恢复会话后可填写。</p> : <div className="question-actions">
            <button className="text-button" type="button" disabled={Boolean(busy)} onClick={() => void finish(true)}>{busy === "cancel" ? "取消中…" : "取消"}</button>
            <button className="primary-button" type="submit" disabled={Boolean(busy) || Boolean(schemaError)}>{busy === "submit" ? "提交中…" : "提交并继续"}</button>
          </div>}
        </> : <div className="question-result" role="status">
          <span>{current.status === "answered" ? "已提交回答" : current.status === "expired" ? "已过期" : current.status === "run_done" ? "任务已结束" : "已取消"}</span>
          {current.status === "answered" && <dl className="question-form-summary">{userFormAnswerRows(fields, current.answer).map(row => <div key={row.id}><dt>{row.label}</dt><dd>{row.value}</dd></div>)}</dl>}
        </div>}
        {error && <p className="error-text" role="alert">{error}</p>}
      </form>
    </div>
  </article>;
}

function clearUserForm(form: HTMLFormElement | null) {
  form?.querySelectorAll("input, textarea").forEach(control => { (control as HTMLInputElement | HTMLTextAreaElement).value = ""; });
}

function LegacyQuestionCard({ item, bot, group, archived, onChanged }: QuestionCardProps) {
  const title = useId();
  const [text, setText] = useState("");
  const [selection, setSelection] = useState<string[]>([]);
  const [otherSelected, setOtherSelected] = useState(false);
  const [otherText, setOtherText] = useState("");
  const [resolved, setResolved] = useState<Question | null>(null);
  const submitting = useRef(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const current = reconcileQuestion(item, resolved);
  const active = current.status === "pending";
  const allowOther = Boolean(item.allow_other) && ["yes_no", "single_choice", "multi_choice"].includes(item.question_type);
  const selectedCount = selection.length + (otherSelected ? 1 : 0);
  const previousActive = useRef(active);
  const closing = useRef(false);
  const [settled, setSettled] = useState(false);
  const foldAnswer = !isDesktop && !active && current.status === "answered" && selectedCount > 0 && !settled && (previousActive.current || closing.current);
  useEffect(() => {
    const changedToAnswered = previousActive.current && !active && current.status === "answered" && selectedCount > 0;
    previousActive.current = active;
    if (!changedToAnswered || isDesktop) return;
    closing.current = true;
    const timer = window.setTimeout(() => { closing.current = false; setSettled(true); }, window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 440);
    return () => window.clearTimeout(timer);
  }, [active, current.status, selectedCount]);
  const options = item.question_type === "yes_no" ? [{ id: "yes", label: "是" }, { id: "no", label: "否" }] : item.options ?? [];
  const minimum = Math.max(1, item.min_selections ?? 1);
  const maximum = item.question_type === "multi_choice" ? item.max_selections || options.length + (allowOther ? 1 : 0) : 1;
  const valid = item.question_type === "text" ? Boolean(text.trim()) : selectedCount >= minimum && selectedCount <= maximum && (!otherSelected || (allowOther && Boolean(otherText.trim()) && [...otherText.trim()].length <= 4000));
  async function finish(cancel = false) {
    if (submitting.current || busy || !active || archived || (!cancel && !valid)) return;
    const answer = otherSelected && allowOther ? { values:selection, other_text:otherText.trim() } : item.question_type === "text" ? { text: text.trim() } : item.question_type === "yes_no" ? { value: selection[0] === "yes" } : { values: selection };
    submitting.current = true;
    setBusy(true); setError("");
    try {
      const result = await request<{ question: Question }>(`/api/questions/${encodeURIComponent(item.question_id)}${cancel ? "" : "/answer"}`, cancel ? { method: "DELETE" } : { method: "POST", body: JSON.stringify(answer) });
      setResolved(result.question);
      setOtherText("");
      try { await onChanged(result.question); } catch { setError("回答已接收，列表刷新失败，请刷新页面查看。"); }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "提交未完成，请重试。");
      if (cause instanceof ApiError && (cause.status === 404 || cause.status === 409)) { try { await onChanged(); } catch { setError("问题状态已变化，请刷新页面后再操作。"); } }
    } finally { submitting.current = false; setBusy(false); }
  }
  return <article className={`question-message${group ? " is-group" : " is-dm"}`} data-question-id={item.question_id} tabIndex={-1}>
    {group && <GazeAvatar id={item.bot_id} mini />}
    <div className="question-body">{group && <div className="message-meta"><strong>{bot?.name ?? "Bot"}</strong></div>}
      <form className={`question-card ${active ? "is-pending" : foldAnswer ? "is-folding" : "is-resolved"}${current.status === "expired" ? " is-expired" : ""}${current.status === "answered" ? " is-answered" : ""}`} aria-labelledby={title} onSubmit={event => { event.preventDefault(); void finish(); }}>
        {(active || foldAnswer) && <span className="question-state">需要你决定</span>}
        <div id={title} className="question-prompt"><MessageMarkdown content={item.question} /></div>
        {active || foldAnswer ? <>
          {item.question_type === "text" ? <textarea aria-labelledby={title} value={text} onChange={event => setText(event.target.value)} rows={3} disabled={busy || archived || !active} placeholder="输入回答…" /> : <fieldset className="question-options" disabled={busy || archived || !active} aria-labelledby={title}>
            {options.map(option => <label key={option.id} className={`${selection.includes(option.id) ? "selected" : ""}${foldAnswer && !selection.includes(option.id) ? " is-fold-away" : ""}`}>
              <input type={item.question_type === "multi_choice" ? "checkbox" : "radio"} name={item.question_id} value={option.id} checked={selection.includes(option.id)} disabled={item.question_type === "multi_choice" && !selection.includes(option.id) && selectedCount >= maximum} onChange={() => { if (item.question_type !== "multi_choice") setOtherSelected(false); setSelection(current => item.question_type === "multi_choice" ? current.includes(option.id) ? current.filter(id => id !== option.id) : [...current, option.id] : [option.id]); }} /><span>{option.label}</span>
            </label>)}
            {allowOther && <label className={`${otherSelected ? "selected" : ""}${foldAnswer && !otherSelected ? " is-fold-away" : ""}`}>
              <input type={item.question_type === "multi_choice" ? "checkbox" : "radio"} name={item.question_id} checked={otherSelected} disabled={item.question_type === "multi_choice" && !otherSelected && selectedCount >= maximum} onChange={() => { if (item.question_type !== "multi_choice") setSelection([]); setOtherSelected(value => item.question_type === "multi_choice" ? !value : true); }} /><span>其他回答</span>
            </label>}
          </fieldset>}
          {allowOther && otherSelected && !foldAnswer && <div className="question-other-answer">
            <label htmlFor={`${title}-other`}>你的回答</label>
            <textarea id={`${title}-other`} value={otherText} onChange={event => setOtherText(event.target.value)} rows={3} disabled={busy || archived || !active} placeholder="说说你的想法…" aria-describedby={`${title}-other-note`} />
            <p id={`${title}-other-note`} className={[...otherText.trim()].length > 4000 ? "error-text" : "field-note"}>最多 4,000 字；不要填写密码或密钥。</p>
          </div>}
          {item.question_type === "multi_choice" && <p className="field-note">{minimum === maximum ? `选择 ${minimum} 项` : `选择 ${minimum}–${maximum} 项`}</p>}
          {foldAnswer ? null : archived ? <p className="field-note">恢复会话后可回答。</p> : <div className="question-actions"><button className="text-button" type="button" disabled={busy} onClick={() => void finish(true)}>取消</button><button className="primary-button" disabled={busy || !valid}>{busy ? "提交中…" : error ? "重试提交" : "提交回答"}</button></div>}
        </> : <div className="question-result" role="status">{!isDesktop && current.status === "answered" && <TofiIcon name="check" size={15} variant="filled" aria-hidden="true" />}<span>{current.status === "answered" ? "已回答" : current.status === "expired" ? "已过期" : current.status === "run_done" ? "任务已结束" : "已取消"}</span>{current.status === "answered" && <p>{answerLabel(current)}</p>}</div>}
        {error && <p className="error-text" role="alert">{error}</p>}
      </form>
    </div>
  </article>;
}
