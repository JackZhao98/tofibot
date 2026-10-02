import { useEffect, useRef, useState } from "react";
import { CopyFeedbackIcon } from "../../lib/CopyFeedbackIcon";
import "./chat-cards.css";

type Target = "code" | "answer";
type Result = Readonly<{ target: Target; ok: boolean }> | null;

const COMMAND = "curl -X POST localhost:8321/api/runs";
const ANSWER = "三步就能接上：先换 device code，再按间隔轮询 token，最后存进凭据库。";

/** Copy: the glyph spins into a filled check, holds, then turns back. */
export function CopyDemo() {
  const [result, setResult] = useState<Result>(null);
  const timer = useRef(0);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const copy = async (target: Target, text: string) => {
    let ok = true;
    try { await navigator.clipboard.writeText(text); } catch { ok = false; }
    setResult({ target, ok });
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setResult(null), 1400);
  };
  const copied = (target: Target) => result?.target === target && result.ok;
  const failed = (target: Target) => result?.target === target && !result.ok;

  return (
    <div className="copy-demo">
      <div className="copy-code">
        <header>
          <span>shell</span>
          <button type="button" className="icon-quiet" aria-label="复制命令" onClick={() => void copy("code", COMMAND)}>
            <CopyFeedbackIcon copied={copied("code")} />
          </button>
        </header>
        <pre>{COMMAND}</pre>
      </div>
      <div className="copy-answer">
        <p>{ANSWER}</p>
        <button type="button" className="lab-btn quiet copy-text-button" onClick={() => void copy("answer", ANSWER)}>
          <CopyFeedbackIcon copied={copied("answer")} />
          {copied("answer") ? "已复制" : failed("answer") ? "浏览器没允许复制" : "复制回答"}
        </button>
      </div>
      <span className="sr-only" role="status">{result ? (result.ok ? "已复制" : "复制失败") : ""}</span>
    </div>
  );
}
