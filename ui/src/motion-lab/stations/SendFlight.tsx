import { useEffect, useRef, useState, type CSSProperties, type FormEvent, type KeyboardEvent } from "react";
import { flushSync } from "react-dom";
import { ComposerGlyph } from "../../ComposerGlyph";
import { TofiIcon, type TofiIconName } from "../../icons";
import { CatStage, type CatHandle } from "../lib/CatStage";
import { BOTS } from "../lib/crew";
import { arcPoints } from "../lib/choreo";
import { flipList } from "../lib/flip";
import { SPRINGS, springEasing } from "../lib/spring";
import { prefersReducedMotion, settled, useSeenOnce, wait } from "../lib/hooks";
import { VoiceBar } from "./VoiceBar";
import "./send-flight.css";

type Message = Readonly<{ id: number; from: "me" | "bot"; text: string; hidden?: boolean }>;

const SETTLE = springEasing(SPRINGS.morph);
const REPLIES = [
  ["收到，我先把要点列出来。", "整理好后再发你确认。"],
  ["好，十分钟内给你初稿。"],
  ["记下了，明早提醒你。"],
  ["没问题，做完告诉你。"],
];
const SAMPLE = "帮我把这周的周报整理一下，发给团队。";
const MENU: readonly { icon: TofiIconName | "screenshot"; label: string }[] = [
  { icon: "image", label: "文件或图片" },
  { icon: "screenshot", label: "截屏" },
  { icon: "clock", label: "定个时" },
  { icon: "checklist", label: "问个选择题" },
];

/** A message lifts off the composer, arcs into the thread, and the perched cat answers. */
export function SendFlight() {
  const [rootRef, seen] = useSeenOnce<HTMLDivElement>();
  const stageRef = useRef<HTMLDivElement>(null);
  const threadRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const cat = useRef<CatHandle>(null);
  const nextId = useRef(2);
  const replyIndex = useRef(0);
  const busy = useRef(false);
  const lifetime = useRef<AbortController | null>(null);
  const voiceRun = useRef<AbortController | null>(null);
  const formRef = useRef<HTMLFormElement>(null);
  const [messages, setMessages] = useState<readonly Message[]>([{ id: 1, from: "bot", text: "早上好。今天要我先处理什么？" }]);
  const [draft, setDraftState] = useState("");
  // Sequences await across renders, so they read the live draft from a ref.
  const draftRef = useRef("");
  const setDraft = (value: string) => { draftRef.current = value; setDraftState(value); };
  const [voice, setVoice] = useState(false);
  const [menu, setMenu] = useState(false);
  // The flight is the currently selected production send gesture.
  const [showy, setShowy] = useState(true);
  const showyRef = useRef(true);
  const [thinking, setThinking] = useState<number | null>(null);
  const [thinkSeconds, setThinkSeconds] = useState(0);

  // Created in an effect (not at ref init) so StrictMode's remount gets a live signal.
  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    return () => controller.abort();
  }, []);
  const signal = () => lifetime.current?.signal ?? AbortSignal.abort();

  useEffect(() => {
    if (!menu) return;
    const close = (event: PointerEvent) => {
      if (!formRef.current?.contains(event.target as Node)) setMenu(false);
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [menu]);

  useEffect(() => {
    if (thinking === null) return;
    const timer = window.setInterval(() => setThinkSeconds(Math.round((performance.now() - thinking) / 1000)), 250);
    return () => window.clearInterval(timer);
  }, [thinking]);

  const append = (message: Message) => {
    flipList(threadRef.current, () => setMessages((current) => [...current.slice(-5), message]), { duration: SETTLE.durationMs, easing: SETTLE.easing });
  };

  const fly = async (id: number, text: string) => {
    const stage = stageRef.current;
    const input = inputRef.current;
    const target = stage?.querySelector<HTMLElement>(`[data-flip="${id}"]`);
    if (!stage || !input || !target || prefersReducedMotion()) return;
    const base = stage.getBoundingClientRect();
    const from = input.getBoundingClientRect();
    const to = target.getBoundingClientRect();
    const ghost = document.createElement("p");
    ghost.className = "bubble me flight";
    ghost.textContent = text;
    Object.assign(ghost.style, { left: `${to.left - base.left}px`, top: `${to.top - base.top}px`, width: `${to.width}px` });
    stage.append(ghost);
    const path = arcPoints({ x: from.left - to.left, y: from.top - to.top }, { x: 0, y: 0 }, 70, 18);
    const tokens = getComputedStyle(stage);
    const bubble = tokens.getPropertyValue("--surface-2").trim();
    const edge = tokens.getPropertyValue("--line-soft").trim();
    const flight = ghost.animate(path.map((point, index) => {
      const t = index / (path.length - 1);
      return {
        transform: `translate(${point.x.toFixed(1)}px, ${point.y.toFixed(1)}px) rotate(${(-4 * Math.sin(t * Math.PI)).toFixed(2)}deg) scale(${(0.94 + 0.1 * Math.sin(t * Math.PI)).toFixed(3)})`,
        backgroundColor: t < 0.25 ? "transparent" : bubble,
        borderColor: t < 0.25 ? "transparent" : edge,
      };
    }), { duration: 640, easing: "cubic-bezier(.4,0,.15,1)" });
    await settled(flight);
    ghost.remove();
  };

  const reply = async (signal: AbortSignal) => {
    if (!await wait(420, signal)) return;
    setThinking(performance.now());
    setThinkSeconds(0);
    void cat.current?.play("work");
    if (!await wait(1900, signal)) return;
    setThinking(null);
    const beats = REPLIES[replyIndex.current++ % REPLIES.length];
    for (const [index, text] of beats.entries()) {
      if (index > 0 && !await wait(420, signal)) return;
      const id = nextId.current++;
      append({ id, from: "bot", text: "" });
      for (let count = 1; count <= text.length; count += 1) {
        setMessages((current) => current.map((message) => (message.id === id ? { ...message, text: text.slice(0, count) } : message)));
        if (!await wait(34, signal)) return;
      }
    }
    void cat.current?.play("happy");
  };

  const send = async () => {
    const text = draftRef.current.trim();
    if (!text || busy.current) return;
    busy.current = true;
    const id = nextId.current++;
    void cat.current?.play("look");
    append({ id, from: "me", text, hidden: true });
    flushSync(() => setDraft(""));
    if (showyRef.current) await fly(id, text);
    flushSync(() => setMessages((current) => current.map((message) => (message.id === id ? { ...message, hidden: false } : message))));
    if (!showyRef.current && !prefersReducedMotion()) {
      // Quiet version: the bubble rises a short, straight distance and fades in.
      stageRef.current?.querySelector<HTMLElement>(`[data-flip="${id}"]`)?.animate(
        { transform: ["translateY(14px)", "none"], opacity: [0, 1] },
        { duration: 220, easing: "cubic-bezier(.22,1,.36,1)" },
      );
    }
    await reply(signal());
    busy.current = false;
  };

  const typeOut = async (text: string, signal: AbortSignal, pace = 55) => {
    for (let count = 1; count <= text.length; count += 1) {
      setDraft(text.slice(0, count));
      if (!await wait(pace, signal)) return false;
    }
    return true;
  };

  const demo = async () => {
    if (busy.current || voice) return;
    inputRef.current?.focus({ preventScroll: true });
    if (!await typeOut(SAMPLE, signal())) return;
    if (!await wait(380, signal())) return;
    await send();
  };

  useEffect(() => {
    if (seen && !prefersReducedMotion()) void demo();
    // Run once when the station first scrolls into view.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seen]);

  const onSubmit = (event: FormEvent) => { event.preventDefault(); void send(); };
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void send(); }
    if (event.key === "Escape") setMenu(false);
  };
  // Simulated live transcription: words land in the box while recording.
  const startVoice = () => {
    setMenu(false);
    setVoice(true);
    voiceRun.current?.abort();
    const run = new AbortController();
    voiceRun.current = run;
    lifetime.current?.signal.addEventListener("abort", () => run.abort(), { once: true });
    void typeOut("帮我查一下明天下午有没有空", run.signal, 150);
  };
  const endVoice = (keep: boolean) => {
    voiceRun.current?.abort();
    setVoice(false);
    if (!keep) setDraft("");
    else inputRef.current?.focus();
  };

  const ready = draft.trim().length > 0;
  const style = { "--spring": SETTLE.easing, "--spring-ms": `${SETTLE.durationMs}ms` } as CSSProperties;
  return (
    <div className="send-flight" ref={rootRef} style={style}>
      <div className="stage send-stage" ref={stageRef}>
        <div className="thread" ref={threadRef} aria-live="polite">
          {messages.map((message) => (
            <p key={message.id} data-flip={message.id} className={`bubble ${message.from} ${message.hidden ? "is-hidden" : ""}`}>
              {message.text || " "}
            </p>
          ))}
        </div>
        <div className="composer-dock">
          <p className={`think-line ${thinking !== null ? "is-on" : ""}`} aria-live="polite">{thinking !== null ? `正在想 · ${thinkSeconds}s` : ""}</p>
          <form className={`lab-composer ${voice ? "is-voice" : ""}`} onSubmit={onSubmit} ref={formRef}>
            <span className="perch"><CatStage config={BOTS.writer.cat} ref={cat} autoplay /></span>
            {menu && (
              <div className="plus-menu" role="menu">
                {MENU.map((item, index) => (
                  <button key={item.label} type="button" role="menuitem" style={{ "--i": index } as CSSProperties} onClick={() => setMenu(false)}>
                    {item.icon === "screenshot" ? <ComposerGlyph name="screenshot" size={18} /> : <TofiIcon name={item.icon} size={18} />}
                    {item.label}
                  </button>
                ))}
              </div>
            )}
            {voice ? (
              <VoiceBar onCancel={() => endVoice(false)} onDone={() => endVoice(true)} />
            ) : (
              <>
                <button type="button" className={`composer-icon plus ${menu ? "is-open" : ""}`} aria-label="添加" aria-expanded={menu} onClick={() => setMenu((value) => !value)}>
                  <TofiIcon name="plus" size={22} />
                </button>
                <button type="button" className="composer-icon" aria-label="语音输入" onClick={startVoice}><ComposerGlyph name="mic" size={22} /></button>
              </>
            )}
            <textarea
              ref={inputRef}
              className="composer-input"
              rows={1}
              value={draft}
              placeholder="给写作搭子发消息"
              aria-label="消息"
              readOnly={voice}
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={onKeyDown}
            />
            {!voice && (
              <button type="submit" className={`send-button ${ready ? "is-ready" : ""}`} aria-label="发送" disabled={!ready}>
                <TofiIcon name="arrow-up" size={24} variant="filled" />
              </button>
            )}
          </form>
        </div>
      </div>
      <div className="stage-controls">
        <button type="button" className="lab-btn" onClick={() => void demo()}><TofiIcon name="play" size={16} animated />自动演示</button>
        <div className="send-style" role="radiogroup" aria-label="发送动画">
          {([[false, "克制版"], [true, "表演版"]] as const).map(([value, label]) => (
            <button key={label} type="button" role="radio" aria-checked={showy === value} onClick={() => { showyRef.current = value; setShowy(value); }}>{label}</button>
          ))}
        </div>
        <span className="stage-note">也可以自己打字，按回车发送；点麦克风看录音条。</span>
      </div>
    </div>
  );
}
