import { arcPoints } from "./motion-lab/lib/choreo";

/** Fly the confirmed user bubble along the same arc and timing as Motion Lab. */
export function flySentMessage(origin: DOMRect, target: HTMLElement): void {
  if (!target.isConnected || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
  const to = target.getBoundingClientRect();
  if (!to.width || !to.height) return;
  const style = getComputedStyle(target);
  const ghost = target.cloneNode(true) as HTMLElement;
  ghost.className = "message-content v2-send-flight-ghost";
  ghost.setAttribute("aria-hidden", "true");
  Object.assign(ghost.style, {
    position: "fixed", left: `${to.left}px`, top: `${to.top}px`, width: `${to.width}px`,
    minHeight: `${to.height}px`, margin: "0", padding: style.padding,
    font: style.font, lineHeight: style.lineHeight, color: style.color,
    backgroundColor: style.backgroundColor, border: style.border,
    borderRadius: style.borderRadius, boxSizing: "border-box", overflowWrap: "anywhere",
    zIndex: "2147483000", pointerEvents: "none", transformOrigin: "100% 100%",
  });
  const previousVisibility = target.style.visibility;
  target.style.visibility = "hidden";
  document.body.append(ghost);
  const path = arcPoints({ x: origin.left - to.left, y: origin.top - to.top }, { x: 0, y: 0 }, 70, 18);
  const animation = ghost.animate(path.map((point, index) => {
    const t = index / (path.length - 1);
    return {
      transform: `translate(${point.x.toFixed(1)}px, ${point.y.toFixed(1)}px) rotate(${(-4 * Math.sin(t * Math.PI)).toFixed(2)}deg) scale(${(0.94 + 0.06 * t + 0.1 * Math.sin(t * Math.PI)).toFixed(3)})`,
      backgroundColor: t < 0.25 ? "transparent" : style.backgroundColor,
      borderColor: t < 0.25 ? "transparent" : style.borderColor,
    };
  }), { duration: 640, easing: "cubic-bezier(.4,0,.15,1)" });
  let frame = 0;
  const cleanup = () => {
    cancelAnimationFrame(frame);
    ghost.remove();
    target.style.visibility = previousVisibility;
  };
  // Composer clearing, streaming and manual scrolling can move the real bubble
  // after its first measurement. Keep the fixed overlay's resting position
  // aligned with that bubble so revealing it never changes the landing point.
  const followTarget = () => {
    if (!target.isConnected || window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      animation.cancel();
      cleanup();
      return;
    }
    const current = target.getBoundingClientRect();
    ghost.style.left = `${current.left}px`;
    ghost.style.top = `${current.top}px`;
    // Keyboard/orientation reflow can change line wrapping during the flight.
    // Position alone leaves the clone wider/taller than the revealed bubble.
    ghost.style.width = `${current.width}px`;
    ghost.style.minHeight = `${current.height}px`;
    frame = requestAnimationFrame(followTarget);
  };
  frame = requestAnimationFrame(followTarget);
  void animation.finished.then(cleanup, cleanup);
}
