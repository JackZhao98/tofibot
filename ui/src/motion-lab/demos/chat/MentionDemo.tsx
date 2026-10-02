import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent } from "react";
import { flushSync } from "react-dom";
import { TofiIcon } from "../../../icons";
import type { AvatarConfig } from "../../../lib/tofi-avatar/index.js";
import { CatStage } from "../../lib/CatStage";
import { BOTS } from "../../lib/crew";
import { prefersReducedMotion, settled, useSeenOnce, wait } from "../../lib/hooks";
import "./mention.css";

type Status = "working" | "idle" | "asleep";
type Member = Readonly<{ id: string; name: string; role: string; status: Status; cat: Partial<AvatarConfig> }>;
type Token = Readonly<{ kind: "text"; text: string; key: number }> | Readonly<{ kind: "chip"; name: string; key: number }>;

const MEMBERS: readonly Member[] = [
  { id: "research", name: BOTS.research.name, role: "查资料、读文档", status: "working", cat: BOTS.research.cat },
  { id: "writer", name: BOTS.writer.name, role: "起草和润色", status: "idle", cat: BOTS.writer.cat },
  { id: "ops", name: BOTS.ops.name, role: "盯服务和告警", status: "working", cat: BOTS.ops.cat },
  { id: "archivist", name: BOTS.archivist.name, role: "整理归档", status: "asleep", cat: BOTS.archivist.cat },
];
const STATUS_TEXT: Readonly<Record<Status, string>> = { working: "工作中", idle: "空闲", asleep: "睡着" };

/** The trailing "@query" of the draft and the members it matches. */
function mentionState(text: string) {
  const match = /@([^\s@]*)$/.exec(text);
  const query = match ? match[1] : null;
  const options = query === null ? [] : MEMBERS.filter((member) => member.name.toLowerCase().includes(query.toLowerCase()));
  return { match, query, options };
}

function MemberName({ name, query }: { name: string; query: string }) {
  const at = query ? name.toLowerCase().indexOf(query.toLowerCase()) : -1;
  const lineRef = useRef<HTMLSpanElement>(null);
  const markRef = useRef<HTMLSpanElement>(null);
  // The iris underline is one element that grows/slides under the typed part.
  useLayoutEffect(() => {
    const line = lineRef.current;
    const mark = markRef.current;
    if (!line) return;
    line.style.width = mark ? `${mark.offsetWidth}px` : "0px";
    line.style.transform = `translateX(${mark ? mark.offsetLeft : 0}px)`;
  }, [at, query]);
  return (
    <span className="mention-name">
      {at >= 0 ? <>{name.slice(0, at)}<span ref={markRef}>{name.slice(at, at + query.length)}</span>{name.slice(at + query.length)}</> : name}
      <span className="mention-line" ref={lineRef} aria-hidden="true" />
    </span>
  );
}

/** @ menu: grows from the caret, highlight springs between rows, the pick flies in as a chip. */
export function MentionDemo() {
  const [rootRef, seen] = useSeenOnce<HTMLDivElement>();
  const stageRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const mirrorRef = useRef<HTMLSpanElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const pillRef = useRef<HTMLSpanElement>(null);
  const nextKey = useRef(1);
  const valueRef = useRef("");
  const [tokens, setTokens] = useState<readonly Token[]>([]);
  const [value, setValueState] = useState("");
  const [active, setActive] = useState(0);
  const [dismissed, setDismissed] = useState(false);
  const [flying, setFlying] = useState<number | null>(null);
  const [anchor, setAnchor] = useState(0);
  const [run, setRun] = useState(0);

  const setValue = (next: string) => { valueRef.current = next; setValueState(next); setDismissed(false); setActive(0); };
  const { match, query, options } = mentionState(value);
  const open = options.length > 0 && !dismissed;

  // Anchor the menu under the "@" using a hidden mirror of the text before it.
  useLayoutEffect(() => {
    const input = inputRef.current;
    const mirror = mirrorRef.current;
    const stage = stageRef.current;
    if (!open || !input || !mirror || !stage) return;
    mirror.textContent = value.slice(0, match?.index ?? 0);
    const left = input.getBoundingClientRect().left - stage.getBoundingClientRect().left + mirror.offsetWidth;
    setAnchor(Math.max(12, Math.min(left - 14, stage.clientWidth - 300)));
  }, [open, value, match?.index]);

  useLayoutEffect(() => {
    const item = listRef.current?.querySelectorAll<HTMLElement>(".mention-item")[active];
    const pill = pillRef.current;
    if (!item || !pill) return;
    pill.style.height = `${item.offsetHeight}px`;
    pill.style.transform = `translateY(${item.offsetTop}px)`;
  }, [active, open, options.length]);

  // Reads the live draft from the ref: the scripted demo calls this from an older render.
  const choose = async (index: number) => {
    const { match, options } = mentionState(valueRef.current);
    const member = options[index];
    const stage = stageRef.current;
    const from = listRef.current?.querySelectorAll<HTMLElement>(".mention-item .mention-name")[index]?.getBoundingClientRect();
    if (!member || !match || !stage) return;
    const before = valueRef.current.slice(0, match.index);
    const key = nextKey.current++;
    flushSync(() => {
      setTokens((current) => [...current, ...(before ? [{ kind: "text" as const, text: before, key: nextKey.current++ }] : []), { kind: "chip", name: member.name, key }]);
      setValue("");
      setFlying(key);
    });
    const chip = stage.querySelector<HTMLElement>(`[data-chip="${key}"]`);
    if (from && chip && !prefersReducedMotion()) {
      const base = stage.getBoundingClientRect();
      const to = chip.getBoundingClientRect();
      const ghost = document.createElement("span");
      ghost.className = "mention-chip mention-ghost";
      ghost.textContent = `@${member.name}`;
      Object.assign(ghost.style, { left: `${from.left - base.left - 8}px`, top: `${from.top - base.top - 3}px` });
      stage.append(ghost);
      const lift = Math.min(from.top, to.top) - base.top - 40;
      await settled(ghost.animate([
        { transform: "translate(0, 0) scale(.9)", backgroundColor: "transparent" },
        { transform: `translate(${(to.left - from.left) * 0.5 + 8}px, ${lift - (from.top - base.top) + 3}px) scale(1.08)`, offset: 0.45 },
        { transform: `translate(${to.left - from.left + 8}px, ${to.top - from.top + 3}px) scale(1)` },
      ], { duration: 520, easing: "cubic-bezier(.3,.7,.3,1)" }));
      ghost.remove();
    }
    setFlying(null);
    inputRef.current?.focus({ preventScroll: true });
  };

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.nativeEvent.isComposing) return;
    if (open && event.key === "ArrowDown") { event.preventDefault(); setActive((index) => (index + 1) % options.length); }
    else if (open && event.key === "ArrowUp") { event.preventDefault(); setActive((index) => (index - 1 + options.length) % options.length); }
    else if (open && (event.key === "Enter" || event.key === "Tab")) { event.preventDefault(); void choose(active); }
    else if (open && event.key === "Escape") setDismissed(true);
    else if (event.key === "Backspace" && !value && tokens.length) setTokens((current) => current.slice(0, -1));
  };

  // Scripted first look: type @, walk the list, narrow it, pick, keep typing.
  useEffect(() => {
    if (!seen) return;
    const controller = new AbortController();
    const { signal } = controller;
    const type = async (text: string, pace: number) => {
      for (const character of text) {
        setValue(valueRef.current + character);
        if (!await wait(pace, signal)) return false;
      }
      return true;
    };
    const play = async () => {
      setTokens([]);
      setValue("");
      if (prefersReducedMotion() || !await wait(500, signal)) return;
      if (!await type("@", 200) || !await wait(520, signal)) return;
      for (let step = 0; step < 2; step += 1) { setActive((index) => index + 1); if (!await wait(420, signal)) return; }
      if (!await type("研", 200) || !await wait(700, signal)) return;
      await choose(0);
      if (!await wait(260, signal)) return;
      await type("帮我查一下 device flow", 70);
    };
    void play();
    return () => controller.abort();
    // choose/type read refs; only a replay restarts the script.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seen, run]);

  return (
    <div className="mention-demo" ref={rootRef}>
      <div className="stage convo mention-stage" ref={stageRef}>
        <p className="mention-hint">群聊 · 4 位成员</p>
        {open && (
          <div className="mention-menu" style={{ left: anchor, transformOrigin: `18px 100%` }} role="listbox" aria-label="提及成员" ref={listRef}>
            <span className="mention-pill" ref={pillRef} aria-hidden="true" />
            {options.map((member, index) => (
              <button
                key={member.id}
                type="button"
                role="option"
                aria-selected={index === active}
                className="mention-item"
                onMouseEnter={() => setActive(index)}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => void choose(index)}
              >
                <span className="mention-cat"><CatStage config={member.cat} /></span>
                <span className="mention-copy"><MemberName name={member.name} query={query ?? ""} /><small>{member.role}</small></span>
                <span className={`mention-status is-${member.status}`}><i aria-hidden="true" />{STATUS_TEXT[member.status]}</span>
              </button>
            ))}
          </div>
        )}
        <div className="lab-composer mention-composer" onClick={() => inputRef.current?.focus()}>
          <div className="mention-field">
            {tokens.map((token) => (token.kind === "chip"
              ? <span key={token.key} data-chip={token.key} className={`mention-chip${flying === token.key ? " is-flying" : " is-landed"}`}>@{token.name}</span>
              : <span key={token.key} className="mention-text">{token.text}</span>))}
            <span className="mention-input-wrap">
              <span className="mention-mirror" ref={mirrorRef} aria-hidden="true" />
              <input
                ref={inputRef}
                className="mention-input"
                value={value}
                placeholder={tokens.length ? "" : "输入 @ 提及成员"}
                aria-label="消息"
                aria-expanded={open}
                aria-autocomplete="list"
                onChange={(event) => setValue(event.target.value)}
                onKeyDown={onKeyDown}
              />
            </span>
          </div>
        </div>
      </div>
      <div className="stage-controls">
        <button type="button" className="lab-btn" onClick={() => setRun((count) => count + 1)}><TofiIcon name="retry" size={16} animated />重播</button>
        <span className="stage-note">自己输入 @ 试试：方向键移动，回车选中，Esc 关闭。</span>
      </div>
    </div>
  );
}
