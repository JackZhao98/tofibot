import { useRef, useState, type KeyboardEvent } from "react";
import { CatStage } from "../../lib/CatStage";
import { BOTS } from "../../lib/crew";
import { prefersReducedMotion } from "../../lib/hooks";
import "./bot.css";

type Phase = "idle" | "holding" | "deleted";
const HOLD_MS = 1200;

/** Press and hold to delete: the fill must reach the end; letting go drains it back. */
export function HoldDeleteDemo() {
  const [phase, setPhase] = useState<Phase>("idle");
  const fillRef = useRef<HTMLSpanElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const hold = useRef<Animation | null>(null);

  const start = () => {
    const fill = fillRef.current;
    if (phase === "deleted" || !fill) return;
    if (prefersReducedMotion()) { setPhase("deleted"); return; }
    setPhase("holding");
    const running = hold.current;
    if (running && running.playState === "running") { running.playbackRate = 1; return; }
    const animation = fill.animate({ transform: ["scaleX(0)", "scaleX(1)"] }, { duration: HOLD_MS, easing: "linear", fill: "forwards" });
    hold.current = animation;
    animation.finished.then(() => {
      if (animation.playbackRate < 0) { animation.cancel(); return; }
      buttonRef.current?.animate({ transform: ["scale(1)", "scale(1.08)", "scale(1)"] }, { duration: 260, easing: "ease-out" });
      window.setTimeout(() => setPhase("deleted"), 200);
    }).catch(() => undefined);
  };

  // Released early: run the fill backwards, faster, to zero.
  const release = () => {
    const animation = hold.current;
    if (!animation || phase !== "holding") return;
    if (animation.playState === "finished" && animation.playbackRate > 0) return;
    animation.playbackRate = -2.6;
    if (animation.playState !== "running") animation.play();
    setPhase("idle");
  };

  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if ((event.key === " " || event.key === "Enter") && !event.repeat) { event.preventDefault(); start(); }
  };
  const onKeyUp = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.key === " " || event.key === "Enter") { event.preventDefault(); release(); }
  };

  const undo = () => {
    hold.current?.cancel();
    hold.current = null;
    setPhase("idle");
  };

  return (
    <div className="hold-demo">
      <div className={`hold-slot${phase === "deleted" ? " is-gone" : ""}`}>
        <div className="hold-row">
          <span className="hold-cat"><CatStage config={BOTS.archivist.cat} autoplay /></span>
          <span className="hold-copy"><strong>{BOTS.archivist.name}</strong><small>它的记忆和 2 个定时任务会一起删除</small></span>
          <button
            type="button"
            ref={buttonRef}
            className="hold-button"
            aria-label={`按住删除 ${BOTS.archivist.name}`}
            onPointerDown={(event) => {
              start();
              // Keep receiving the release even if the pointer drifts off the button.
              try { event.currentTarget.setPointerCapture(event.pointerId); } catch { /* capture is optional */ }
            }}
            onPointerUp={release}
            onPointerCancel={release}
            onKeyDown={onKeyDown}
            onKeyUp={onKeyUp}
            onBlur={release}
          >
            <span className="hold-fill" ref={fillRef} aria-hidden="true" />
            <span className="hold-label">{phase === "holding" ? "继续按住…" : "按住删除"}</span>
          </button>
        </div>
      </div>
      {phase === "deleted" && (
        <p className="hold-undo" role="status">已删除「{BOTS.archivist.name}」<button type="button" className="lab-btn quiet" onClick={undo}>撤销</button></p>
      )}
    </div>
  );
}
