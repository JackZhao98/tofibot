import { useRef, useState } from "react";
import { TofiIcon } from "../../../icons";
import { CatStage, type CatHandle } from "../../lib/CatStage";
import { CopyFeedbackIcon } from "../../lib/CopyFeedbackIcon";
import { wait } from "../../lib/hooks";
import "./settings.css";

type Phase = "waiting" | "linked";
const CODE = "WXYZ-4821";
const BRAND_CAT = { shape: "curl", pattern: "calico", palette: "calico" } as const;

/** Device-flow login: a dashed line flows while you authorise in the browser, then draws solid and the cat wakes. */
export function OAuthDemo() {
  const [phase, setPhase] = useState<Phase>("waiting");
  const [copied, setCopied] = useState(false);
  const cat = useRef<CatHandle>(null);

  const copy = async () => {
    try { await navigator.clipboard.writeText(CODE); setCopied(true); } catch { setCopied(false); }
    window.setTimeout(() => setCopied(false), 1400);
  };

  const authorise = async () => {
    setPhase("linked");
    const controller = new AbortController();
    if (!await wait(620, controller.signal)) return;
    if (await cat.current?.play("wake")) void cat.current?.play("happy");
  };

  const reset = async () => {
    setPhase("waiting");
    await cat.current?.play("sleep");
  };

  return (
    <div className={`oauth-demo is-${phase}`}>
      <div className="oauth-link">
        <span className="oauth-node"><CatStage config={BRAND_CAT} initialState="asleep" ref={cat} /></span>
        <svg className="oauth-line" viewBox="0 0 200 60" aria-hidden="true">
          <path className="oauth-wait" d="M6 44 Q100 -6 194 44" />
          <path className="oauth-done" d="M6 44 Q100 -6 194 44" pathLength={1} />
        </svg>
        <span className="oauth-node is-provider"><TofiIcon name="key" size={22} /><small>Codex</small><i className="oauth-check"><TofiIcon name="check" size={12} variant="filled" /></i></span>
      </div>
      {phase === "waiting" ? (
        <div className="oauth-code">
          <span className="oauth-code-value">{CODE}</span>
          <button type="button" className="icon-quiet" aria-label="复制验证码" onClick={() => void copy()}><CopyFeedbackIcon copied={copied} /></button>
          <small>在浏览器里打开授权页，输入这组码。</small>
        </div>
      ) : (
        <p className="oauth-linked" role="status"><i />已连接 · 用你的 Codex 账户回复</p>
      )}
      <div className="demo-actions">
        {phase === "waiting"
          ? <button type="button" className="lab-btn primary" onClick={() => void authorise()}>我已在浏览器授权</button>
          : <button type="button" className="lab-btn quiet" onClick={() => void reset()}>断开，重来</button>}
      </div>
    </div>
  );
}
