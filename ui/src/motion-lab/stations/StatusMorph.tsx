import { useEffect, useRef, useState, type CSSProperties } from "react";
import { GazeAvatar } from "../../GazeAvatar";
import { normalizeConfig } from "../../lib/tofi-avatar/index.js";
import { BOTS } from "../lib/crew";
import { SPRINGS, springEasing } from "../lib/spring";
import { prefersReducedMotion, useSeenOnce } from "../lib/hooks";
import "./status-morph.css";

type Status = "working" | "unread" | "need" | "error" | "done";

type StatusInfo = Readonly<{ label: string; hint: string; hold: number }>;

const STATUSES: Readonly<Record<Status, StatusInfo>> = {
  working: { label: "工作中", hint: "呼吸灯", hold: 3600 },
  unread: { label: "有新消息", hint: "实心数字", hold: 3000 },
  need: { label: "要你决定", hint: "问号", hold: 2400 },
  error: { label: "出错", hint: "感叹号", hold: 2600 },
  done: { label: "完成", hint: "一颗绿点", hold: 2400 },
};
const ORDER: readonly Status[] = ["working", "unread", "need", "error", "done"];
const MORPH = springEasing(SPRINGS.morph);
const researchCat = normalizeConfig(BOTS.research.cat);

function Odometer({ value }: { value: number }) {
  const digits = String(Math.min(value, 99)).split("");
  return (
    <span className="odometer" aria-hidden="true">
      {digits.map((digit, index) => (
        <span className="odo-col" key={digits.length - index}>
          <span className="odo-reel" style={{ "--digit": Number(digit) } as CSSProperties}>
            {"0123456789".split("").map((face) => <span key={face}>{face}</span>)}
          </span>
        </span>
      ))}
    </span>
  );
}

function StatusSlot({ status, count }: { status: Status; count: number }) {
  const shake = useRef<HTMLSpanElement>(null);
  const ring = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (prefersReducedMotion()) return;
    if (status === "error") {
      shake.current?.animate(
        { transform: ["translateX(0)", "translateX(-14%)", "translateX(11%)", "translateX(-7%)", "translateX(4%)", "translateX(0)"] },
        { duration: 460, easing: "ease-out", delay: 120 },
      );
    }
    if (status === "done") {
      ring.current?.animate(
        { transform: ["scale(.6)", "scale(2.6)"], opacity: [0.9, 0] },
        { duration: 700, easing: "cubic-bezier(.22,1,.36,1)", delay: 140 },
      );
    }
  }, [status]);
  return (
    <span className="slot-shake" ref={shake}>
      <span className="slot-ring" ref={ring} data-status={status} />
      <span className="slot" data-status={status}>
        <span className="slot-glyph glyph-count"><Odometer value={count} /></span>
        <span className="slot-glyph glyph-need">?</span>
        <span className="slot-glyph glyph-error">!</span>
      </span>
    </span>
  );
}

const PREVIEW: Readonly<Record<Status, (seconds: number, count: number) => string>> = {
  working: (seconds) => `正在读 API 文档 · ${seconds}s`,
  unread: (_, count) => `整理好了，${count > 1 ? `${count} 条` : "一条"}新结果等你看`,
  need: () => "要你决定：先发哪个版本？",
  error: () => "会话已归档，结果没发出去 · 重试",
  done: () => "周报已发给团队",
};

/** One status position, five states, each with its own shape; springs between them. */
export function StatusMorph() {
  const [stageRef, seen] = useSeenOnce<HTMLDivElement>();
  const [status, setStatus] = useState<Status>("working");
  const [auto, setAuto] = useState(true);
  const [count, setCount] = useState(1);
  const [seconds, setSeconds] = useState(8);

  // Autoplay walks the story; unread ticks the counter up while it holds.
  useEffect(() => {
    if (!seen || !auto) return;
    const timers: number[] = [];
    if (status === "unread") {
      timers.push(window.setTimeout(() => setCount(2), 900), window.setTimeout(() => setCount(3), 1700), window.setTimeout(() => setCount(12), 2400));
    }
    timers.push(window.setTimeout(() => {
      const next = ORDER[(ORDER.indexOf(status) + 1) % ORDER.length];
      if (next === "unread") setCount(1);
      if (next === "working") setSeconds(8);
      setStatus(next);
    }, STATUSES[status].hold));
    return () => timers.forEach((timer) => window.clearTimeout(timer));
  }, [seen, auto, status]);

  useEffect(() => {
    if (status !== "working") return;
    const timer = window.setInterval(() => setSeconds((value) => value + 1), 1000);
    return () => window.clearInterval(timer);
  }, [status]);

  const choose = (next: Status) => {
    setAuto(false);
    if (next === "unread") setCount((value) => (status === "unread" ? Math.min(99, value + 1) : 1));
    setStatus(next);
  };

  const style = { "--spring": MORPH.easing, "--spring-ms": `${MORPH.durationMs}ms` } as CSSProperties;
  return (
    <div className="status-morph" style={style} ref={stageRef}>
      <div className="stage dotted status-specimen" aria-hidden="true">
        <span className="specimen-unit"><StatusSlot status={status} count={count} /></span>
        <span className="specimen-label">{STATUSES[status].hint}</span>
      </div>
      <div className="status-row" aria-live="polite">
        <GazeAvatar id="motion-lab-research" config={researchCat} motion={status === "working" ? "working" : "awake"} />
        <span className="row-text">
          <span className="row-top">
            <span className="row-name">{BOTS.research.name}</span>
            <span className="row-time">{status === "done" ? "14:32" : status === "working" ? "" : "14:30"}</span>
          </span>
          <span className="row-preview" key={`${status}-${status === "unread" ? count : ""}`} data-status={status}>
            {PREVIEW[status](seconds, count)}
          </span>
        </span>
        <span className="row-unit"><StatusSlot status={status} count={count} /></span>
      </div>
      <div className="stage-controls" role="group" aria-label="切换状态">
        {ORDER.map((key) => (
          <button key={key} type="button" className="lab-btn status-pick" aria-pressed={status === key} onClick={() => choose(key)}>
            <span className={`pick-dot pick-${key}`} aria-hidden="true" />
            {STATUSES[key].label}
          </button>
        ))}
        <button type="button" className="lab-btn quiet" aria-pressed={auto} onClick={() => setAuto((value) => !value)}>
          {auto ? "暂停轮播" : "继续轮播"}
        </button>
      </div>
    </div>
  );
}
