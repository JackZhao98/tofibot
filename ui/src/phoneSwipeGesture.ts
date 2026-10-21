import { SPRING } from "./motion";
import { holdAvatarLife } from "./lib/tofi-avatar/index.js";

/*
 * Phone swipe-back (D33, docs/design/migration.md step 4). On the phone layout a finger that drags the open chat to the
 * right pulls it off the list underneath: the tap push of D20 (styles.css, max-width:767px) played by hand.
 *   - The chat follows the finger 1:1; the list follows the chat (it sits 22% to the left and dimmed when covered, at rest
 *     when uncovered), so every frame shows the same picture as the tap push at that point.
 *   - Released past half the width, or flicked right faster than SWIPE.flickPxPerMs, it completes; flicked left that fast,
 *     or released short of half, it returns. The pane leaves the finger with the finger's velocity (SPRING.push).
 *   - Dragged left of where it started, the chat resists (rubber band) and gives at most SWIPE.rubberShare of the width.
 *   - A finger that lands on the pane while it travels catches it where it is.
 *   - Reduced motion: the chat still follows the finger (direct manipulation); a release lands at once.
 * It only starts on touch or pen, on a horizontal-first move, never in a text field or in something scrolled sideways.
 * Nothing goes through React until a completed swipe commits; the panes move by transform only.
 *
 * The travel after release is the part the two experiment variants differ in (`Settle`): phoneSwipeEngine.ts bakes the
 * spring into Web Animations keyframes (compositor); src/motion-lab/experiments/phoneSwipeMotion.ts drives it with Motion.
 */

export const SWIPE = {
  slopPx: 10,          // finger travel before the gesture decides between a horizontal drag and anything else
  completeShare: 0.5,  // released past this share of the width it completes
  flickPxPerMs: 0.5,   // faster than this (500 px/s) the direction of the flick decides, whatever the distance
  velocityWindowMs: 80, // the release velocity is the finger's mean velocity over its last 80 ms
  rubberShare: 0.1,    // dragged left of its start, the chat gives at most 10% of the width
  rubberStiffness: 0.55, // the resistance constant of the rubber band (the classic 0.55)
  listShiftShare: 0.22, // the covered list sits 22% to the left (styles.css, the tap push)
} as const;

export type SwipeLive = { current: () => void };
export type Panes = { grid: HTMLElement; chat: HTMLElement; list: HTMLElement; width: number; dim: Animation | null };
export type SettleRun = { panes: Panes; from: number; to: number; velocity: number; done: () => void };
/** Moves the released chat from `from` to `to` (px), leaving with `velocity` (px/ms); `stop` halts it and says where it is. */
export type Settle = (run: SettleRun) => { stop: () => number };

const clamp01 = (value: number) => Math.min(1, Math.max(0, value));
/** The list's offset and its dim when the chat stands at x: the same mapping as the tap push. */
export const listOffset = (x: number, width: number) => -SWIPE.listShiftShare * width * (1 - clamp01(x / width));
export const dimAt = (x: number, width: number) => 1 - clamp01(x / width);
/** How far the chat goes when the finger is `over` px left of its start: always less, never past rubberShare x width. */
export function rubberBand(over: number, width: number) {
  const room = SWIPE.rubberShare * width;
  return (1 - 1 / (over * SWIPE.rubberStiffness / room + 1)) * room;
}
/** Whether a release at x with velocity v (px/ms, + is rightward) completes the swipe. */
export function completes(x: number, velocity: number, width: number) {
  if (velocity >= SWIPE.flickPxPerMs) return x > 0;
  if (velocity <= -SWIPE.flickPxPerMs) return false;
  return x >= SWIPE.completeShare * width;
}

/** One frame of the gesture: the chat at x, the list and its dim where the tap push would have them. */
export function paint(panes: Panes, x: number) {
  panes.chat.style.transform = `translate3d(${x}px,0,0)`;
  panes.list.style.transform = `translate3d(${listOffset(x, panes.width)}px,0,0)`;
  if (panes.dim) panes.dim.currentTime = DIM_SPAN * (1 - dimAt(x, panes.width));
}
const DIM_SPAN = 1000;
/** The dim is the list's ::after; a paused animation scrubbed by the gesture is the only way to drive a pseudo-element. */
export function scrubDim(panes: Panes) {
  panes.dim?.cancel();
  panes.dim = typeof panes.list.animate === "function" ? panes.list.animate([{ opacity: 1 }, { opacity: 0 }], { duration: DIM_SPAN, fill: "both", pseudoElement: "::after" }) : null;
  panes.dim?.pause();
}
export const readX = (el: HTMLElement) => new DOMMatrixReadOnly(getComputedStyle(el).transform).m41;

/**
 * The spring path from `from` to `to`, leaving with `velocity` px/ms, sampled every `step` ms until it is within 0.5 px
 * and nearly still. Critically damped (SPRING.push); it is clamped at `to` so a flick never carries the pane past the edge.
 */
export function springPath(from: number, to: number, velocity: number, step: number) {
  const { stiffness, damping, mass } = SPRING.push;
  const side = Math.sign(from - to) || -Math.sign(velocity) || 1;
  let x = from - to, v = velocity * 1000; // relative to `to`, px and px/s
  const out = [from];
  for (let ms = 1; ms <= 1500; ms++) {
    v += ((-stiffness * x - damping * v) / mass) / 1000;
    x += v / 1000;
    if (x * side < 0) { x = 0; v = 0; }
    if (ms % step === 0) out.push(to + x);
    if (Math.abs(x) < 0.5 && Math.abs(v) < 20) break;
  }
  out.push(to);
  return out;
}

/** Watches the page for the gesture; returns the function that stops watching. */
export function mountPhoneSwipe(back: SwipeLive, settle: Settle): () => void {
  const phone = window.matchMedia("(max-width: 767px)");
  const reduce = window.matchMedia("(prefers-reduced-motion: reduce)");
  type Track = { id: number; x0: number; y0: number; panes: Panes | null; origin: number; x: number; samples: [number, number][]; chat: HTMLElement };
  let track: Track | null = null;
  let flight: { panes: Panes; stop: () => number } | null = null;
  let quietClicksUntil = 0;

  const begin = (panes: Panes, at: number) => {
    panes.chat.dataset.swipe = panes.list.dataset.swipe = "drag";
    scrubDim(panes);
    paint(panes, at);
    holdAvatarLife(600);
  };
  const end = (panes: Panes, completed: boolean) => {
    flight = null;
    // transition:none while the classes and inline values change together, so no CSS transition replays the move.
    panes.chat.dataset.swipe = panes.list.dataset.swipe = "done";
    if (completed) back.current();
    panes.dim?.cancel();
    panes.dim = null;
    for (const el of [panes.chat, panes.list]) { el.style.transform = ""; for (const a of el.getAnimations()) if (a.id === TAG) a.cancel(); }
    void getComputedStyle(panes.chat).transform;
    void getComputedStyle(panes.list).visibility;
    requestAnimationFrame(() => { delete panes.chat.dataset.swipe; delete panes.list.dataset.swipe; });
  };
  const release = (panes: Panes, x: number, velocity: number) => {
    const done = completes(x, velocity, panes.width);
    quietClicksUntil = performance.now() + 400;
    if (reduce.matches) { end(panes, done); return; }
    const run = settle({ panes, from: x, to: done ? panes.width : 0, velocity, done: () => end(panes, done) });
    flight = { panes, stop: run.stop };
  };
  const velocityOf = (t: Track, now: number) => {
    const recent = t.samples.filter(([at]) => at >= now - SWIPE.velocityWindowMs);
    if (recent.length < 2) return 0;
    const [t0, x0] = recent[0], [t1, x1] = recent[recent.length - 1];
    return t1 > t0 ? (x1 - x0) / (t1 - t0) : 0;
  };

  const down = (event: PointerEvent) => {
    if (!event.isPrimary || (event.pointerType !== "touch" && event.pointerType !== "pen") || !phone.matches) return;
    const target = event.target instanceof Element ? event.target : null;
    quietClicksUntil = 0;
    if (flight) {
      // Caught in flight: the pane stops where it is and follows this finger from there.
      if (!target || !flight.panes.grid.contains(target)) return;
      const panes = flight.panes;
      const x = flight.stop();
      flight = null;
      begin(panes, x);
      quietClicksUntil = Infinity; // a touch that catches the pane is part of the swipe, never a tap on what is under it
      track = { id: event.pointerId, x0: event.clientX, y0: event.clientY, panes, origin: x, x, samples: [[event.timeStamp, x]], chat: panes.chat };
      return;
    }
    const chat = target?.closest<HTMLElement>(".chat-pane.mobile-chat");
    if (!target || !chat || target.closest("input,textarea,select,[contenteditable]:not([contenteditable=false]),[data-no-swipe]")) return;
    for (let el: Element | null = target; el && el !== chat; el = el.parentElement) if (el.scrollLeft > 0) return;
    track = { id: event.pointerId, x0: event.clientX, y0: event.clientY, panes: null, origin: 0, x: 0, samples: [], chat };
  };
  const move = (event: PointerEvent) => {
    const t = track;
    if (!t || event.pointerId !== t.id) return;
    const dx = event.clientX - t.x0, dy = event.clientY - t.y0;
    if (!t.panes) {
      if (Math.abs(dx) < SWIPE.slopPx && Math.abs(dy) < SWIPE.slopPx) return;
      // Only a rightward, horizontal-first move is a swipe; anything else belongs to the page.
      const grid = t.chat.parentElement, list = grid?.querySelector<HTMLElement>(":scope > .contact-pane");
      if (dx <= Math.abs(dy) || !grid || !list) { track = null; return; }
      t.panes = { grid, chat: t.chat, list, width: t.chat.getBoundingClientRect().width || window.innerWidth, dim: null };
      t.x0 = event.clientX; // the slop is not travel: the pane starts from where the finger is now
      begin(t.panes, 0);
      t.samples.push([event.timeStamp, 0]);
      return;
    }
    const raw = t.origin + dx;
    t.x = raw >= 0 ? Math.min(raw, t.panes.width) : -rubberBand(-raw, t.panes.width);
    t.samples.push([event.timeStamp, t.x]);
    if (t.samples.length > 24) t.samples.splice(0, t.samples.length - 24);
    paint(t.panes, t.x);
    holdAvatarLife(320);
  };
  const up = (event: PointerEvent) => {
    const t = track;
    if (!t || event.pointerId !== t.id) return;
    track = null;
    if (!t.panes) return;
    // A cancelled pointer (the browser took the touch) returns the pane; a lifted one decides by distance and speed.
    release(t.panes, t.x, event.type === "pointercancel" ? 0 : velocityOf(t, event.timeStamp));
  };
  const click = (event: MouseEvent) => {
    if (performance.now() < quietClicksUntil) { event.preventDefault(); event.stopPropagation(); quietClicksUntil = 0; }
  };
  const options = { capture: true, passive: true } as const;
  window.addEventListener("pointerdown", down, options);
  window.addEventListener("pointermove", move, options);
  window.addEventListener("pointerup", up, options);
  window.addEventListener("pointercancel", up, options);
  window.addEventListener("click", click, true);
  return () => {
    window.removeEventListener("pointerdown", down, options);
    window.removeEventListener("pointermove", move, options);
    window.removeEventListener("pointerup", up, options);
    window.removeEventListener("pointercancel", up, options);
    window.removeEventListener("click", click, true);
    if (flight) { const panes = flight.panes; flight.stop(); end(panes, false); }
    if (track?.panes) end(track.panes, false);
    track = null;
  };
}
/** The id every settle animation carries, so a finished or caught swipe can clear exactly its own. */
export const TAG = "tofi-swipe";
