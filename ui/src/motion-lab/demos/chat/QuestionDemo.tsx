import { useEffect, useRef, useState } from "react";
import { TofiIcon } from "../../../icons";
import { GazeAvatar } from "../../../GazeAvatar";
import { normalizeConfig } from "../../../lib/tofi-avatar/index.js";
import { BOTS } from "../../lib/crew";
import { prefersReducedMotion, useSeenOnce } from "../../lib/hooks";
import "./question.css";

type Phase = "hidden" | "pending" | "chosen" | "answered" | "expired";

const OPTIONS = [
  { key: "A", label: "精简版", hint: "三段话，一分钟读完" },
  { key: "B", label: "完整版", hint: "带数据表和下周计划" },
  { key: "C", label: "两个都发", hint: "让团队自己挑" },
] as const;
const writer = normalizeConfig(BOTS.writer.cat);

/** QuestionCard: rises above the composer; an answer folds it to one line; expiry quiets it. */
export function QuestionDemo() {
  const [rootRef, seen] = useSeenOnce<HTMLDivElement>();
  const [phase, setPhase] = useState<Phase>("hidden");
  const [chosen, setChosen] = useState(-1);
  const [run, setRun] = useState(0);
  const [other, setOther] = useState(false);
  const [otherText, setOtherText] = useState("");
  const [allowOther, setAllowOther] = useState(true);
  const answerTimer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(answerTimer.current), []);

  useEffect(() => {
    if (!seen) return;
    setChosen(-1);
    setOther(false); setOtherText(""); window.clearTimeout(answerTimer.current);
    setPhase("hidden");
    const timer = window.setTimeout(() => setPhase("pending"), 420);
    return () => window.clearTimeout(timer);
  }, [seen, run]);

  // Press first, then fold: the chosen option sinks, the rest collapse around it.
  const answer = (index: number) => {
    if (phase !== "pending") return;
    setChosen(index);
    if (prefersReducedMotion()) { setPhase("answered"); return; }
    setPhase("chosen");
    answerTimer.current = window.setTimeout(() => setPhase("answered"), 260);
  };

  const folded = phase === "answered";
  return (
    <div className="question-demo" ref={rootRef}>
      <div className="stage convo question-stage">
        <div className="bot-head">
          <GazeAvatar id="motion-lab-question" config={writer} motion="awake" mini />
          <span className="bot-name">{BOTS.writer.name}</span>
        </div>
        <p className="question-lead">周报拟好了，有两个版本。</p>
        <div className="question-dock">
          {phase !== "hidden" && (
            <div className={`q-card is-${phase}`} role="group" aria-label="待回答的问题">
              <div className="q-fold q-headwrap">
                <div className="q-inner">
                  <div className="q-head">
                    <span className="q-tag">{phase === "expired" ? "已过期" : "要你决定"}</span>
                    <span className="q-meta">{phase === "expired" ? "没有在 30 分钟内回答" : "30 分钟内有效"}</span>
                  </div>
                  <p className="q-title">周报先发哪个版本？</p>
                </div>
              </div>
              {[...OPTIONS, ...(allowOther ? [{ key:"…", label:folded && chosen === 3 ? otherText : "其他回答", hint:"用自己的话回答" }] : [])].map((option, index) => (
                <div key={option.key} className={`q-fold q-optwrap${folded && index !== chosen ? " is-gone" : ""}`}>
                  <div className="q-inner">
                    <button
                      type="button"
                      className={`q-opt${index === chosen ? " is-chosen" : ""}`}
                      disabled={phase !== "pending"}
                      aria-pressed={index === chosen}
                      onClick={() => index === 3 ? setOther(true) : answer(index)}
                    >
                      <span className="q-key">{folded && index === chosen ? <TofiIcon name="check" size={14} variant="filled" /> : option.key}</span>
                      <span className="q-text">
                        {folded && index === chosen && <span className="q-answered">已回答</span>}
                        <strong>{option.label}</strong>
                        <small>{option.hint}</small>
                      </span>
                    </button>
                  </div>
                </div>
              ))}
              {allowOther && other && phase === "pending" && <div className="q-other-entry">
                <label htmlFor="lab-other-answer">你的回答</label>
                <textarea id="lab-other-answer" rows={3} value={otherText} onChange={event => setOtherText(event.target.value)} placeholder="说说你的想法…" />
                <small>最多 4,000 字；不要填写密码或密钥。</small>
                <button type="button" className="lab-btn" disabled={!otherText.trim() || [...otherText.trim()].length > 4000} onClick={() => answer(3)}>提交回答</button>
              </div>}
            </div>
          )}
          <div className="q-composer" aria-hidden="true">回答上面的问题，或直接输入</div>
        </div>
      </div>
      <div className="stage-controls">
        <button type="button" className="lab-btn" onClick={() => setRun((value) => value + 1)}><TofiIcon name="retry" size={16} animated />重播</button>
        <button type="button" className="lab-btn quiet" disabled={phase !== "pending"} onClick={() => setPhase("expired")}>让它过期</button>
        <label className="q-other-toggle"><input type="checkbox" checked={allowOther} onChange={event => { setAllowOther(event.target.checked); setRun(value => value + 1); }} />允许其他回答</label>
      </div>
    </div>
  );
}
