import { useEffect, useRef, useState, type CSSProperties } from "react";
import { flushSync } from "react-dom";
import { GazeAvatar } from "../../GazeAvatar";
import { TofiIcon, type TofiIconName } from "../../icons";
import { normalizeConfig } from "../../lib/tofi-avatar/index.js";
import { BOTS } from "../lib/crew";
import { SPRINGS, springEasing } from "../lib/spring";
import { prefersReducedMotion, settled, useSeenOnce, wait } from "../lib/hooks";
import "./tool-steps.css";

type Step = Readonly<{ icon: TofiIconName; label: string; ms: number }>;
type Phase = "idle" | "running" | "streaming" | "done";

const STEPS: readonly Step[] = [
  { icon: "search", label: "搜索「Codex device flow」", ms: 1100 },
  { icon: "globe", label: "打开 developers.openai.com/codex/auth", ms: 1400 },
  { icon: "file-text", label: "读 API 文档", ms: 1900 },
  { icon: "edit", label: "整理要点", ms: 900 },
];
const ANSWER = [
  "三步就能接上：",
  "1. 先换一组 device code，把链接和验证码展示给用户。",
  "2. 按 interval 轮询 token 接口，遇到 slow_down 就把间隔加长。",
  "3. 拿到 token 存进凭据库，日志里只记结果，不记内容。",
].join("\n");
const SETTLE = springEasing(SPRINGS.morph);
const writerCat = normalizeConfig(BOTS.research.cat);

function seconds(ms: number): string {
  return `${(ms / 1000).toFixed(1)}s`;
}

function StepList({ running, times, now }: { running: number; times: readonly number[]; now: number }) {
  return (
    <ol className="steps">
      {STEPS.map((step, index) => {
        const done = index < times.length;
        const active = index === running && !done;
        if (!done && !active) return null;
        return (
          <li key={step.label} className={`step ${active ? "is-active" : "is-done"}`}>
            <TofiIcon name={step.icon} size={16} />
            <span className="step-label">{step.label}</span>
            <span className="step-meta">
              {active ? <><span className="breath-dot" aria-hidden="true" />{seconds(now)}</> : <><TofiIcon name="check" size={14} variant="filled" />{seconds(times[index])}</>}
            </span>
          </li>
        );
      })}
    </ol>
  );
}

/** Tool calls open up while the Bot works, then fold into one quiet line under the answer. */
export function ToolSteps() {
  const [stageRef, seen] = useSeenOnce<HTMLDivElement>();
  const [phase, setPhase] = useState<Phase>("idle");
  const [running, setRunning] = useState(0);
  const [times, setTimes] = useState<readonly number[]>([]);
  const [elapsed, setElapsed] = useState(0);
  const [shown, setShown] = useState(0);
  const [open, setOpen] = useState(false);
  const [run, setRun] = useState(0);
  const messageRef = useRef<HTMLDivElement>(null);
  const stepsRef = useRef<HTMLDivElement>(null);
  const answerRef = useRef<HTMLDivElement>(null);
  const summaryRef = useRef<HTMLDivElement>(null);

  // FLIP: freeze the open step list as a ghost, swap layout, then fly the ghost
  // into the summary line while the answer glides up into the vacated space.
  const collapse = async (signal: AbortSignal) => {
    const message = messageRef.current;
    const steps = stepsRef.current;
    const answer = answerRef.current;
    if (!message || !steps || !answer || prefersReducedMotion()) { setPhase("done"); return; }
    const base = message.getBoundingClientRect();
    const firstSteps = steps.getBoundingClientRect();
    const firstAnswer = answer.getBoundingClientRect();
    const ghost = steps.cloneNode(true) as HTMLDivElement;
    ghost.classList.add("steps-ghost");
    Object.assign(ghost.style, { left: `${firstSteps.left - base.left}px`, top: `${firstSteps.top - base.top}px`, width: `${firstSteps.width}px` });
    message.append(ghost);
    flushSync(() => setPhase("done"));
    const lastAnswer = answer.getBoundingClientRect();
    const summary = summaryRef.current?.getBoundingClientRect();
    const glide = answer.animate(
      { transform: [`translateY(${firstAnswer.top - lastAnswer.top}px)`, "translateY(0)"] },
      { duration: SETTLE.durationMs, easing: SETTLE.easing },
    );
    const fold = summary
      ? ghost.animate({
        transform: ["none", `translate(${summary.left - firstSteps.left}px, ${summary.top - firstSteps.top}px) scale(${Math.min(1, summary.width / firstSteps.width)}, ${summary.height / firstSteps.height})`],
        opacity: [1, 0.6, 0],
      }, { duration: 520, easing: "cubic-bezier(.5,0,.2,1)" })
      : null;
    summaryRef.current?.animate({ opacity: [0, 1], transform: ["translateY(-8px)", "none"] }, { duration: 360, delay: 260, easing: "cubic-bezier(.22,1,.36,1)", fill: "backwards" });
    signal.addEventListener("abort", () => { glide.cancel(); fold?.cancel(); ghost.remove(); }, { once: true });
    await Promise.all([settled(glide), fold ? settled(fold) : Promise.resolve()]);
    ghost.remove();
  };

  useEffect(() => {
    if (!seen) return;
    const controller = new AbortController();
    const { signal } = controller;
    const play = async () => {
      setTimes([]);
      setShown(0);
      setOpen(false);
      if (prefersReducedMotion()) {
        setTimes(STEPS.map((step) => step.ms));
        setShown(ANSWER.length);
        setPhase("done");
        return;
      }
      setPhase("running");
      for (let index = 0; index < STEPS.length; index += 1) {
        setRunning(index);
        const started = performance.now();
        const ticker = window.setInterval(() => setElapsed(performance.now() - started), 100);
        const finished = await wait(STEPS[index].ms, signal);
        window.clearInterval(ticker);
        if (!finished) return;
        setTimes((current) => [...current, performance.now() - started]);
      }
      setPhase("streaming");
      for (let count = 0; count <= ANSWER.length; count += 2) {
        setShown(count);
        if (!await wait(ANSWER[count] === "\n" ? 180 : 26, signal)) return;
      }
      setShown(ANSWER.length);
      if (!await wait(700, signal)) return;
      await collapse(signal);
    };
    void play();
    return () => controller.abort();
    // collapse only reads refs and stable setters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seen, run]);

  const total = times.reduce((sum, time) => sum + time, 0);
  const style = { "--spring": SETTLE.easing, "--spring-ms": `${SETTLE.durationMs}ms` } as CSSProperties;
  const working = phase === "running" || phase === "streaming";
  return (
    <div className="tool-steps" ref={stageRef} style={style}>
      <div className="stage convo">
        <p className="user-bubble">帮我查一下 Codex device flow 怎么接，列个要点。</p>
        <div className="bot-message" ref={messageRef}>
          <div className="bot-head">
            <GazeAvatar id="motion-lab-steps" config={writerCat} motion={working ? "working" : "awake"} mini />
            <span className="bot-name">{BOTS.research.name}</span>
          </div>
          {(phase === "running" || phase === "streaming") && (
            <div className="steps-wrap" ref={stepsRef}>
              <StepList running={running} times={times} now={elapsed} />
            </div>
          )}
          <div className="answer" ref={answerRef} aria-live="polite">
            {ANSWER.slice(0, shown).split("\n").map((line, index, lines) => (
              <p key={index}>{line}{phase === "streaming" && index === lines.length - 1 && <span className="caret" aria-hidden="true" />}</p>
            ))}
          </div>
          {phase === "done" && (
            <div className="work-footer" ref={summaryRef}>
              <span className="done-dot" aria-hidden="true" />
              <span className="footer-time">14:32</span>
              <button type="button" className="tools-toggle" aria-expanded={open} onClick={() => setOpen((value) => !value)}>
                <span className="chevron" aria-hidden="true"><TofiIcon name="chevron-right" size={14} /></span>
                {STEPS.length} 个工具 · {seconds(total)}
              </button>
              <span className="footer-actions">
                <button type="button" className="icon-quiet" aria-label="复制回答"><TofiIcon name="copy" size={16} animated /></button>
                <button type="button" className="icon-quiet" aria-label="重新生成" onClick={() => setRun((value) => value + 1)}><TofiIcon name="retry" size={16} animated /></button>
              </span>
              <div className={`tools-drawer ${open ? "is-open" : ""}`}>
                <div className="tools-drawer-inner"><StepList running={-1} times={times} now={0} /></div>
              </div>
            </div>
          )}
        </div>
      </div>
      <div className="stage-controls">
        <button type="button" className="lab-btn" onClick={() => setRun((value) => value + 1)} disabled={working}>
          <TofiIcon name="retry" size={16} animated />重播
        </button>
      </div>
    </div>
  );
}
