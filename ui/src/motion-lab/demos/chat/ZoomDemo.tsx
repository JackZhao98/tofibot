import { useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal, flushSync } from "react-dom";
import { TofiIcon } from "../../../icons";
import { SPRINGS, springEasing } from "../../lib/spring";
import { prefersReducedMotion, settled } from "../../lib/hooks";
import { BoardArt, ChartArt, TermArt } from "./shotArt";
import "./chat-cards.css";

const SHOTS: readonly { id: string; name: string; art: ReactNode }[] = [
  { id: "chart", name: "本周数据.png", art: <ChartArt /> },
  { id: "term", name: "测试结果.png", art: <TermArt /> },
  { id: "board", name: "看板.png", art: <BoardArt /> },
];
const OPEN = springEasing(SPRINGS.morph);

/** Shared-element zoom: the thumbnail itself grows to the lightbox and shrinks back to its slot. */
export function ZoomDemo() {
  const [open, setOpen] = useState<number | null>(null);
  const thumbs = useRef<(HTMLButtonElement | null)[]>([]);
  const figureRef = useRef<HTMLDivElement>(null);
  const backdropRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const closing = useRef(false);

  // From the thumbnail's box to the figure's box, as one transform.
  const delta = (index: number) => {
    const from = thumbs.current[index]?.getBoundingClientRect();
    const to = figureRef.current?.getBoundingClientRect();
    if (!from || !to) return null;
    return `translate(${from.left - to.left}px, ${from.top - to.top}px) scale(${from.width / to.width}, ${from.height / to.height})`;
  };

  const show = (index: number) => {
    flushSync(() => setOpen(index));
    closeRef.current?.focus({ preventScroll: true });
    const start = delta(index);
    if (!start || prefersReducedMotion()) return;
    figureRef.current?.animate({ transform: [start, "none"] }, { duration: OPEN.durationMs, easing: OPEN.easing });
    backdropRef.current?.animate({ opacity: [0, 1] }, { duration: 260, easing: "ease-out" });
  };

  const hide = async () => {
    if (open === null || closing.current) return;
    closing.current = true;
    const index = open;
    const end = delta(index);
    if (end && !prefersReducedMotion()) {
      backdropRef.current?.animate({ opacity: [1, 0] }, { duration: 300, easing: "ease-in", fill: "forwards" });
      const figure = figureRef.current;
      if (figure) await settled(figure.animate({ transform: ["none", end] }, { duration: 340, easing: "cubic-bezier(.4,0,.2,1)", fill: "forwards" }));
    }
    setOpen(null);
    closing.current = false;
    thumbs.current[index]?.focus({ preventScroll: true });
  };

  useEffect(() => {
    if (open === null) return;
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") void hide(); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  });

  const shot = open === null ? null : SHOTS[open];
  return (
    <div className="zoom-demo">
      <div className="zoom-grid">
        {SHOTS.map((item, index) => (
          <button
            key={item.id}
            type="button"
            ref={(element) => { thumbs.current[index] = element; }}
            className={`zoom-thumb${open === index ? " is-away" : ""}`}
            aria-label={`放大 ${item.name}`}
            onClick={() => show(index)}
          >
            {item.art}
          </button>
        ))}
      </div>
      {shot && createPortal(
        <div className="zoom-layer" role="dialog" aria-modal="true" aria-label={shot.name}>
          <div className="zoom-backdrop" ref={backdropRef} onClick={() => void hide()} />
          <figure className="zoom-figure">
            <div className="zoom-art" ref={figureRef}>{shot.art}</div>
            <figcaption>{shot.name}</figcaption>
          </figure>
          <button type="button" className="zoom-close" ref={closeRef} aria-label="关闭图片" onClick={() => void hide()}><TofiIcon name="close" size={20} /></button>
        </div>,
        document.body,
      )}
    </div>
  );
}
