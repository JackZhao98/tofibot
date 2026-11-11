import { SPRING } from "./motion";
import {
  PIP_DRAG_PX, PIP_HOLD_MS, nearestPipCorner, pipPlaceFromPoint, projectPipPoint, resolvePipPlace, rubberBandPipPoint,
  type FloatingPoint, type FloatingSize, type PipPlace,
} from "./floatingDesktopPosition";

/*
 * The Bot-computer mini window's drag engine (docs/design/computer-pip.md section 5.2). One principle, the sidebar engine's:
 * measure once at the press, then write only `transform` once per animation frame. Nothing in here touches React, layout or
 * the style cache while the window is dragged or settling; the owner learns of the result in one `onCommit` call.
 */

export type PipMeasure = { size: FloatingSize; viewport: FloatingSize; composerTop?: number; point: FloatingPoint };
type Tune = { stiffness?: number; damping?: number; mass?: number };

export const translate = (point: FloatingPoint) => `translate3d(${point.left}px,${point.top}px,0)`;

const reduced = () => typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;
const spring = () => ({ ...SPRING.pipSnap, ...((window as unknown as { __TOFI_PIP?: { snap?: Tune } }).__TOFI_PIP?.snap ?? {}) });

/** Semi-implicit Euler at a fixed 1/120 s, one frame at most 32 ms (a tab coming back to the front does not blow up). */
export function stepSpring(state: { x: number; v: number }, target: number, dt: number, tune: { stiffness: number; damping: number; mass: number }) {
  let left = Math.min(dt, 0.032);
  while (left > 1e-9) {
    const h = Math.min(left, 1 / 120);
    state.v += ((-tune.stiffness * (state.x - target) - tune.damping * state.v) / tune.mass) * h;
    state.x += state.v * h;
    left -= h;
  }
}

export type PipEngine = {
  /** Wire to the shell's `pointerdown`. `controls` selects what a press must leave alone (the buttons). */
  press: (event: PointerEvent) => void;
  /** True for a moment after a drag ended, so the click that follows the release does not also enlarge. */
  wasDragged: () => boolean;
  /** Stops any frame in flight (unmount). */
  dispose: () => void;
  /** Whether a drag or a settle is running: the owner must not re-measure then. */
  busy: () => boolean;
};

export function createPipEngine({ element, measure, controls, commit }: {
  element: () => HTMLElement | null;
  /** The one measurement per press. */
  measure: () => PipMeasure | null;
  controls: string;
  /** The single call after the drag ends and the spring settles: the owner stores it and renders once. */
  commit: (place: PipPlace) => void;
}): PipEngine {
  let frame = 0;
  let dragging = false;
  let settling = false;
  let draggedAt = -Infinity;

  const end = (el: HTMLElement) => { el.style.willChange = ""; delete el.dataset.pip; };
  const stop = () => { if (frame) cancelAnimationFrame(frame); frame = 0; };

  function settle(el: HTMLElement, from: FloatingPoint, velocity: { x: number; y: number }, place: PipPlace, target: FloatingPoint) {
    const finish = () => { settling = false; el.style.transform = translate(target); end(el); commit(place); };
    if (reduced() || (Math.abs(from.left - target.left) < 0.5 && Math.abs(from.top - target.top) < 0.5 && Math.hypot(velocity.x, velocity.y) * 1000 < 5)) { finish(); return; }
    settling = true;
    el.dataset.pip = "snap";
    const tune = spring();
    const x = { x: from.left, v: velocity.x * 1000 };
    const y = { x: from.top, v: velocity.y * 1000 };
    let last = performance.now();
    const tick = (now: number) => {
      const dt = (now - last) / 1000;
      last = now;
      stepSpring(x, target.left, dt, tune);
      stepSpring(y, target.top, dt, tune);
      const done = Math.abs(x.x - target.left) < 0.5 && Math.abs(y.x - target.top) < 0.5 && Math.abs(x.v) < 5 && Math.abs(y.v) < 5;
      if (done) { frame = 0; finish(); return; }
      el.style.transform = translate({ left: x.x, top: y.x });
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
  }

  function press(event: PointerEvent) {
    const el = element();
    if (!el || settling || event.button !== 0 || !event.isPrimary) return;
    if ((event.target as Element | null)?.closest(controls)) return;
    const start = measure();
    if (!start) return;
    const pointerId = event.pointerId;
    const origin = { x: event.clientX, y: event.clientY, at: performance.now() };
    const latest = { x: origin.x, y: origin.y, alt: event.altKey };
    const samples: { t: number; x: number; y: number }[] = [{ t: origin.at, x: origin.x, y: origin.y }];
    let current = start.point;

    const paint = () => {
      frame = 0;
      current = rubberBandPipPoint({ left: start.point.left + latest.x - origin.x, top: start.point.top + latest.y - origin.y }, start.size, start.viewport, !reduced() && !latest.alt);
      el.style.transform = translate(current);
    };
    const move = (e: PointerEvent) => {
      if (e.pointerId !== pointerId) return;
      latest.x = e.clientX; latest.y = e.clientY; latest.alt = e.altKey;
      if (!dragging) {
        const far = Math.hypot(latest.x - origin.x, latest.y - origin.y);
        // Moving past 6 px drags; a press held 350 ms drags at the first step.
        if (far < PIP_DRAG_PX && !(far > 1 && performance.now() - origin.at >= PIP_HOLD_MS)) return;
        dragging = true;
        el.dataset.pip = "drag";
        el.style.willChange = "transform";
        try { el.setPointerCapture(pointerId); } catch { /* the press already ended */ }
      }
      samples.push({ t: e.timeStamp || performance.now(), x: e.clientX, y: e.clientY });
      while (samples.length > 2 && samples[0].t < samples[samples.length - 1].t - 100) samples.shift();
      if (!frame) frame = requestAnimationFrame(paint);
    };
    const finish = (e: PointerEvent) => {
      if (e.pointerId !== pointerId) return;
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", finish);
      el.removeEventListener("pointercancel", finish);
      try { el.releasePointerCapture(pointerId); } catch { /* not captured */ }
      if (!dragging) return;
      dragging = false;
      draggedAt = performance.now();
      stop();
      latest.alt = e.altKey || latest.alt;
      paint();
      const first = samples[0], last = samples[samples.length - 1];
      const span = Math.max(1, last.t - first.t);
      const velocity = e.type === "pointercancel" ? { x: 0, y: 0 } : { x: (last.x - first.x) / span, y: (last.y - first.y) / span };
      let place: PipPlace;
      if (latest.alt) place = pipPlaceFromPoint(current, start.size, start.viewport, start.composerTop);
      else place = { corner: nearestPipCorner(projectPipPoint(current, velocity), start.size, start.viewport, start.composerTop), dx: 0, dy: 0 };
      settle(el, current, velocity, place, resolvePipPlace(place, start.size, start.viewport, start.composerTop));
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", finish);
    el.addEventListener("pointercancel", finish);
  }

  return {
    press,
    wasDragged: () => performance.now() - draggedAt < 350,
    dispose: () => { stop(); dragging = false; settling = false; const el = element(); if (el) end(el); },
    busy: () => dragging || settling,
  };
}
