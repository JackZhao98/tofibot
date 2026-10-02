import { useRef, useState } from "react";
import { TofiIcon } from "../../../icons";
import { prefersReducedMotion, settled } from "../../lib/hooks";
import "./settings.css";

type Phase = "idle" | "testing" | "ok" | "fail";

/** Connection test: the plug edges toward the socket; success clicks in with sparks, failure bounces back. */
export function McpDemo() {
  const [phase, setPhase] = useState<Phase>("idle");
  const [failNext, setFailNext] = useState(false);
  const plugRef = useRef<SVGGElement>(null);

  const test = async () => {
    const plug = plugRef.current;
    if (!plug || phase === "testing") return;
    const fails = failNext;
    setPhase("testing");
    plug.getAnimations().forEach((animation) => animation.cancel());
    if (!prefersReducedMotion()) {
      // A hesitant approach, like a real handshake taking a moment.
      await settled(plug.animate({ transform: ["translateX(0)", "translateX(10px)", "translateX(8px)", "translateX(15px)"], offset: [0, 0.5, 0.62, 1] }, { duration: 1000, easing: "ease-in-out", fill: "forwards" }));
      if (fails) {
        await settled(plug.animate({ transform: ["translateX(15px)", "translateX(19px)", "translateX(-5px)", "translateX(2px)", "translateX(0)"], offset: [0, 0.15, 0.55, 0.8, 1] }, { duration: 620, easing: "ease-out", fill: "forwards" }));
      } else {
        await settled(plug.animate({ transform: ["translateX(15px)", "translateX(22px)"] }, { duration: 120, easing: "cubic-bezier(.6,0,1,1)", fill: "forwards" }));
      }
    }
    setPhase(fails ? "fail" : "ok");
  };

  return (
    <div className={`mcp-demo is-${phase}`}>
      <div className="mcp-row">
        <span className="mcp-logo"><TofiIcon name="mcp" size={20} /></span>
        <span className="mcp-copy">
          <strong>GitHub MCP</strong>
          <small role="status">
            {phase === "idle" && "还没测试过"}
            {phase === "testing" && "正在连接…"}
            {phase === "ok" && <><i className="mcp-dot is-ok" aria-hidden="true" />已连接 · 12 个工具</>}
            {phase === "fail" && <><i className="mcp-dot is-fail" aria-hidden="true">!</i>401：token 已过期，重新授权后再试</>}
          </small>
        </span>
      </div>
      <svg className="mcp-plug" viewBox="0 0 170 56" aria-hidden="true">
        <g ref={plugRef}>
          <path className="mcp-cable" d="M-20 28 C 10 28, 22 28, 46 28" />
          <rect className="mcp-body" x="46" y="13" width="36" height="30" rx="8" />
          <rect className="mcp-prong" x="82" y="19" width="16" height="5" rx="1.5" />
          <rect className="mcp-prong" x="82" y="32" width="16" height="5" rx="1.5" />
        </g>
        <rect className="mcp-socket" x="112" y="8" width="44" height="40" rx="10" />
        <rect className="mcp-hole" x="112" y="19" width="9" height="5" />
        <rect className="mcp-hole" x="112" y="32" width="9" height="5" />
        <g className="mcp-sparks">
          <path d="M108 8 l-6 -6" /><path d="M104 28 h-9" /><path d="M108 48 l-6 6" />
        </g>
      </svg>
      <div className="demo-actions">
        <button type="button" className="lab-btn primary" disabled={phase === "testing"} onClick={() => void test()}><TofiIcon name="plug" size={16} animated />测试连接</button>
        <label className="mcp-fail">
          <input type="checkbox" checked={failNext} onChange={(event) => setFailNext(event.target.checked)} />下次测试失败
        </label>
      </div>
    </div>
  );
}
