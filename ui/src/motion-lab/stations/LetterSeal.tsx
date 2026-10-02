import { useEffect, useRef, useState } from "react";
import { TofiIcon } from "../../icons";
import { SPRINGS, springEasing } from "../lib/spring";
import { prefersReducedMotion, settled, wait } from "../lib/hooks";
import { Envelope, LETTER, LetterPaper } from "./letterParts";
import "./letter-seal.css";

type Phase = "draft" | "sending" | "done";

const SPRING = springEasing(SPRINGS.morph);
const TOY = springEasing(SPRINGS.toy);
const FOLD_EASE = "cubic-bezier(.62,0,.24,1)";

/** Three stacked copies of the paper, each clipped to one third, hinged on its fold line. */
function buildFold(paper: HTMLElement, stage: DOMRect): { root: HTMLDivElement; top: HTMLDivElement; bottom: HTMLDivElement } {
  const rect = paper.getBoundingClientRect();
  const third = rect.height / 3;
  const root = document.createElement("div");
  root.className = "fold";
  Object.assign(root.style, { left: `${rect.left - stage.left}px`, top: `${rect.top - stage.top}px`, width: `${rect.width}px`, height: `${rect.height}px` });
  const panels = [0, 1, 2].map((index) => {
    const panel = document.createElement("div");
    panel.className = `fold-panel fold-panel-${index}`;
    Object.assign(panel.style, { top: `${index * third}px`, height: `${third}px` });
    const front = document.createElement("div");
    front.className = "fold-face fold-front";
    const copy = paper.cloneNode(true) as HTMLElement;
    Object.assign(copy.style, { position: "absolute", left: "0", top: `${-index * third}px`, width: `${rect.width}px`, height: `${rect.height}px` });
    front.append(copy);
    const back = document.createElement("div");
    back.className = "fold-face fold-back";
    panel.append(front, back);
    root.append(panel);
    return panel;
  });
  return { root, top: panels[0], bottom: panels[2] };
}

/** The mail draft: fold in thirds, into the envelope, flap, stamp, postmark, away. */
export function LetterSeal() {
  const [phase, setPhase] = useState<Phase>("draft");
  const [run, setRun] = useState(0);
  const stageRef = useRef<HTMLDivElement>(null);
  const cardRef = useRef<HTMLDivElement>(null);
  const paperRef = useRef<HTMLDivElement>(null);
  const envelopeRef = useRef<HTMLDivElement>(null);
  const innerRef = useRef<HTMLDivElement>(null);
  const flapRef = useRef<HTMLDivElement>(null);
  const stampRef = useRef<HTMLSpanElement>(null);
  const markRef = useRef<SVGSVGElement>(null);
  const lifetime = useRef<AbortController | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    return () => controller.abort();
  }, [run]);

  const thunk = (element: HTMLElement, amount: number) => element.animate(
    { transform: ["scale(1,1)", `scale(${1 + amount},${1 - amount})`, "scale(1,1)"] },
    { duration: 260, easing: "ease-out", composite: "add" },
  );

  const send = async () => {
    const signal = lifetime.current?.signal;
    const [stage, card, paper, envelope, inner, flap, stamp, mark] = [stageRef.current, cardRef.current, paperRef.current, envelopeRef.current, innerRef.current, flapRef.current, stampRef.current, markRef.current];
    if (phase !== "draft" || !signal || !stage || !card || !paper || !envelope || !inner || !flap || !stamp || !mark) return;
    if (prefersReducedMotion()) { setPhase("done"); return; }
    setPhase("sending");
    if (!await wait(260, signal)) return;

    // 1. Fold: bottom third up, then top third down over it.
    const base = stage.getBoundingClientRect();
    const paperRect = paper.getBoundingClientRect();
    const fold = buildFold(paper, base);
    stage.append(fold.root);
    signal.addEventListener("abort", () => fold.root.remove(), { once: true });
    card.style.visibility = "hidden";
    await settled(fold.bottom.animate({ transform: ["rotateX(0deg)", "translateZ(1px) rotateX(180deg)"] }, { duration: 560, easing: FOLD_EASE, fill: "forwards" }));
    await settled(fold.top.animate({ transform: ["rotateX(0deg)", "translateZ(2px) rotateX(-180deg)"] }, { duration: 560, easing: FOLD_EASE, fill: "forwards" }));
    if (signal.aborted) return;

    // 2. The envelope rises while the packet lifts and shrinks to its opening.
    envelope.classList.add("is-in");
    const rise = envelope.animate({ transform: ["translateY(170%) rotate(-8deg)", "none"] }, { duration: TOY.durationMs, easing: TOY.easing });
    const packetW = inner.offsetWidth;
    const scale = packetW / paperRect.width;
    inner.style.height = `${(paperRect.height / 3) * scale}px`;
    const targetX = envelope.offsetLeft + inner.offsetLeft + packetW / 2;
    const targetY = envelope.offsetTop + inner.offsetTop + inner.offsetHeight / 2;
    const fromX = paperRect.left - base.left + paperRect.width / 2;
    const fromY = paperRect.top - base.top + paperRect.height / 2;
    const lift = fold.root.animate({
      transform: ["none", `translate(${(targetX - fromX) * 0.5}px, ${Math.min(-40, (targetY - fromY) * 0.5 - 60)}px) scale(${(1 + scale) / 2}) rotate(-3deg)`, `translate(${targetX - fromX}px, ${targetY - fromY}px) scale(${scale})`],
    }, { duration: 760, easing: "cubic-bezier(.3,.6,.25,1)", fill: "forwards" });
    await Promise.all([settled(rise), settled(lift)]);
    if (signal.aborted) return;

    // 3. Hand over to the in-envelope copy and slide it into the pocket.
    fold.root.remove();
    inner.classList.add("is-in");
    await settled(inner.animate({ transform: ["none", `translateY(${envelope.offsetHeight * 0.52}px)`] }, { duration: 380, easing: "cubic-bezier(.55,0,.8,.3)", fill: "forwards" }));
    thunk(envelope, 0.04);

    // 4. Flap closes, stamp lands, postmark hits.
    flap.classList.add("is-closing");
    await settled(flap.animate({ transform: ["rotateX(180deg)", "rotateX(0deg)"] }, { duration: SPRING.durationMs, easing: SPRING.easing, fill: "forwards" }));
    await settled(stamp.animate({ opacity: [0, 1], transform: ["translate(-40px,-70px) scale(2.4) rotate(-28deg)", "translate(0,0) scale(1) rotate(6deg)"] }, { duration: TOY.durationMs * 0.6, easing: TOY.easing, fill: "forwards" }));
    thunk(envelope, 0.05);
    await settled(mark.animate({ opacity: [0, 0.92], transform: ["scale(1.7) rotate(-34deg)", "scale(1) rotate(-10deg)"] }, { duration: 240, easing: "cubic-bezier(.2,.9,.3,1.2)", fill: "forwards" }));
    envelope.animate({ transform: ["translateX(0)", "translateX(-5px)", "translateX(4px)", "translateX(-2px)", "translateX(0)"] }, { duration: 300, composite: "add" });
    if (!await wait(560, signal)) return;

    // 5. Off it goes.
    envelope.classList.add("is-leaving");
    await settled(envelope.animate({ transform: ["none", "translate(-3%, 4%) rotate(-3deg)", "translate(160%, -70%) rotate(16deg) scale(.7)"], opacity: [1, 1, 0] }, { duration: 760, easing: "cubic-bezier(.5,0,.75,.1)", fill: "forwards" }));
    if (!signal.aborted) setPhase("done");
  };

  const replay = () => { setPhase("draft"); setRun((value) => value + 1); };

  return (
    <div className="letter-seal">
      <div className="stage dotted letter-stage" ref={stageRef} key={run}>
        {phase !== "done" && (
          <>
            <div className={`letter ${phase === "sending" ? "is-sending" : ""}`} ref={cardRef}>
              <LetterPaper ref={paperRef} />
              <div className="letter-foot">
                <button type="button" className="lab-btn quiet"><TofiIcon name="edit" size={16} />编辑</button>
                <button type="button" className="lab-btn quiet"><TofiIcon name="copy" size={16} />复制</button>
                <span className="letter-dest">发到 team@tofi.dev</span>
                <button type="button" className="lab-btn primary" onClick={() => void send()} disabled={phase !== "draft"}>
                  <TofiIcon name="send" size={16} />发送邮件
                </button>
              </div>
            </div>
            <Envelope refs={{ envelope: envelopeRef, inner: innerRef, flap: flapRef, stamp: stampRef, mark: markRef }} />
            <span className="speed-lines" aria-hidden="true"><i /><i /><i /></span>
          </>
        )}
        {phase === "done" && (
          <div className="draft-done" role="status">
            <span className="done-dot" aria-hidden="true" />
            <span className="draft-done-text">已发送 · {LETTER.subject}</span>
            <span className="draft-done-meta">团队 · 14:32</span>
            <button type="button" className="lab-btn" onClick={replay}><TofiIcon name="retry" size={16} animated />再寄一封</button>
          </div>
        )}
      </div>
    </div>
  );
}
