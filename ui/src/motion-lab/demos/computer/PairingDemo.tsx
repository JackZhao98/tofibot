import { useEffect, useRef, useState } from "react";
import { TofiIcon } from "../../../icons";
import { prefersReducedMotion, useSeenOnce, wait } from "../../lib/hooks";
import "./computer.css";

type Phase = "idle" | "code" | "waiting" | "paired";
const CODE = "482915";
const BLANK = "------";

/** Device pairing: digits flap into place, the link waits as a moving dashed line, then draws solid. */
export function PairingDemo() {
  const [rootRef, seen] = useSeenOnce<HTMLDivElement>();
  const [phase, setPhase] = useState<Phase>("idle");
  const [digits, setDigits] = useState(BLANK);
  const [run, setRun] = useState(0);
  const lifetime = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!seen) return;
    const controller = new AbortController();
    lifetime.current = controller;
    const { signal } = controller;
    const play = async () => {
      setPhase("code");
      setDigits(BLANK);
      if (prefersReducedMotion()) { setDigits(CODE); setPhase("waiting"); return; }
      // Split-flap: each place shuffles a few random digits before landing.
      for (let place = 0; place < CODE.length; place += 1) {
        for (let flap = 0; flap < 4; flap += 1) {
          const shuffled = String(Math.floor(Math.random() * 10));
          setDigits((current) => current.slice(0, place) + shuffled + current.slice(place + 1));
          if (!await wait(45, signal)) return;
        }
        setDigits((current) => current.slice(0, place) + CODE[place] + current.slice(place + 1));
      }
      if (!await wait(300, signal)) return;
      setPhase("waiting");
    };
    void play();
    return () => controller.abort();
  }, [seen, run]);

  const confirm = () => { if (phase === "waiting") setPhase("paired"); };

  return (
    <div className={`pair-demo is-${phase}`} ref={rootRef}>
      <div className="pair-row">
        <span className="pair-device"><b><TofiIcon name="laptop" size={24} /></b><small>这台 Mac</small></span>
        <span className="pair-link" aria-hidden="true"><i className="pair-wait" /><i className="pair-done" /></span>
        <span className="pair-device"><b><TofiIcon name="phone" size={24} /></b><small>手机</small></span>
        <span className="pair-check" aria-hidden="true"><TofiIcon name="check" size={16} variant="filled" /></span>
      </div>
      <p className="pair-code" aria-label={`配对码 ${digits}`}>
        {digits.split("").map((digit, index) => <span key={`${index}-${digit}`} className="pair-digit">{digit === "-" ? "" : digit}</span>)}
      </p>
      <p className="pair-status" role="status">
        {phase === "paired" ? "已配对 · 这台 Mac ↔ 手机" : phase === "waiting" ? "在手机上输入这组数字" : "生成配对码…"}
      </p>
      <div className="demo-actions">
        <button type="button" className="lab-btn primary" disabled={phase !== "waiting"} onClick={confirm}>在手机上确认</button>
        <button type="button" className="lab-btn quiet" onClick={() => setRun((value) => value + 1)}>重来</button>
      </div>
    </div>
  );
}
