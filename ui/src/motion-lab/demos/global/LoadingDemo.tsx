import { useEffect, useRef, useState } from "react";
import { LoadingCat } from "../../lib/LoadingCat";
import "./global.css";

type Phase = "ready" | "quiet" | "waiting";

// Matches the app's DelayedFeedback: nothing shows for waits shorter than this.
const SHOW_AFTER = 300;
const LINES = ["研究助理：三篇文章都读完了", "写作搭子：周报初稿在这里", "Ops Watcher：告警已经恢复"];

/** Loading: short waits flash nothing; long ones show a sleeping cat that wakes while you wait. */
export function LoadingDemo() {
  const [phase, setPhase] = useState<Phase>("ready");
  const [round, setRound] = useState(0);
  const timers = useRef<number[]>([]);
  useEffect(() => () => timers.current.forEach((timer) => window.clearTimeout(timer)), []);

  const load = (ms: number) => {
    timers.current.forEach((timer) => window.clearTimeout(timer));
    setPhase("quiet");
    timers.current = [
      window.setTimeout(() => setPhase((current) => (current === "quiet" ? "waiting" : current)), SHOW_AFTER),
      window.setTimeout(() => { setPhase("ready"); setRound((value) => value + 1); }, ms),
    ];
  };

  return (
    <div className="loading-demo">
      <div className="loading-box" aria-busy={phase !== "ready"}>
        {phase === "waiting" && <p className="loading-state" role="status"><LoadingCat size={34} />读取消息…</p>}
        {phase === "ready" && (
          <ul className="loading-list" key={round}>
            {LINES.map((line) => <li key={line}>{line}</li>)}
          </ul>
        )}
      </div>
      <div className="demo-actions">
        <button type="button" className="lab-btn" disabled={phase !== "ready"} onClick={() => load(2600)}>慢加载 2.6 秒</button>
        <button type="button" className="lab-btn quiet" disabled={phase !== "ready"} onClick={() => load(200)}>快加载 0.2 秒</button>
      </div>
    </div>
  );
}
