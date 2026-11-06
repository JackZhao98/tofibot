import { animate } from "motion";
import { CASCADE } from "../../motion";
export { cascadeRoom, fadeArrival } from "../../arrivalCascade";

/*
 * Migration step 4, experiment 1: the arrival cascade (D19) with Motion's spring instead of keyframes baked by
 * arrivalCascade.ts. Same links, delays and spring physics: the stiffness and damping are derived from the same CASCADE
 * numbers, so every link crosses its place at the same time and overshoots by the same 2-4 px. Two ways to use Motion:
 *   - "waapi" (default): animate `transform`. Motion 14.4.0 bakes the spring into a linear() easing and hands it to Web
 *     Animations (compositor), but a string value carries no velocity, so an interrupting arrival restarts each link at rest.
 *   - "js" (window.__TOFI_CASCADE_MOTION = "js"): animate `y`. Motion carries the velocity into an interrupting arrival,
 *     and in exchange runs every link on the main thread each frame.
 * Not part of any build; motionExperimentPlugin swaps it in for ./arrivalCascade (test:motion-experiment, or the dev
 * server with TOFI_MOTION_EXPERIMENT=motion).
 */
type Config = { -readonly [K in keyof typeof CASCADE]: number };
const config = (): Config => ({ ...CASCADE, ...((window as unknown as { __TOFI_CASCADE?: Partial<Config> }).__TOFI_CASCADE ?? {}) });
const mode = () => (window as unknown as { __TOFI_CASCADE_MOTION?: string }).__TOFI_CASCADE_MOTION === "js" ? "js" : "waapi";
const HARD_LINKS = 40;
type Controls = { stop: () => void; pause: () => void; play: () => void };
const running = new WeakMap<HTMLElement, Controls>();
// The links in motion, for holdArrivalCascade (a WeakMap cannot be walked).
const active = new Map<Controls, HTMLElement>();

function dampingFor(start: number, overshoot: number) {
  const ratio = Math.min(0.4, Math.max(0.01, overshoot / start));
  const ln = -Math.log(ratio);
  return ln / Math.sqrt(Math.PI * Math.PI + ln * ln);
}
/** The spring that starts at rest, first crosses 0 after crossMs and overshoots by `overshoot` px (as arrivalCascade.ts). */
function springFor(start: number, overshoot: number, crossMs: number) {
  const zeta = dampingFor(start, overshoot);
  const wd = (Math.PI / 2 + Math.asin(zeta)) / (crossMs / 1000);
  const w = wd / Math.sqrt(1 - zeta * zeta);
  return { stiffness: w * w, damping: 2 * zeta * w, mass: 1 };
}
function currentOffset(el: HTMLElement) {
  const value = getComputedStyle(el).transform;
  return !value || value === "none" ? 0 : new DOMMatrixReadOnly(value).m42;
}
function links(scroller: HTMLElement, max: number) {
  const top = scroller.getBoundingClientRect().top;
  const blocks = scroller.querySelectorAll<HTMLElement>(".message-block");
  const out: HTMLElement[] = [];
  for (let i = blocks.length - 1; i >= 0 && out.length < HARD_LINKS; i--) {
    const block = blocks[i];
    if (out.length >= max && block.getBoundingClientRect().bottom < top - 16) break;
    out.push(block);
  }
  return out;
}

export function playArrivalCascade(scroller: HTMLElement, shift: number) {
  if (shift < 4) return false;
  const cfg = config();
  const chain = links(scroller, cfg.maxLinks);
  if (!chain.length) return false;
  const carried = chain.map(el => { const y = currentOffset(el); const prior = running.get(el); prior?.stop(); if (prior) active.delete(prior); return y; });
  const js = mode() === "js";
  chain.forEach((el, k) => {
    const lead = k === 0;
    const start = shift + carried[k] + (lead ? cfg.enterExtraPx : 0);
    const overshoot = Math.max(cfg.overshootMinPx, cfg.overshootPx - k * cfg.overshootTaperPx);
    const cross = (lead ? cfg.crossShare * cfg.leadSpeedup : cfg.crossShare) * cfg.durationMs;
    const options = { type: "spring" as const, ...springFor(start, overshoot, cross), delay: Math.min(k, cfg.maxLinks - 1) * cfg.staggerMs / 1000, restDelta: 0.05 };
    // Motion resolves keyframes in its next frame; the offset has to be on the page in this one, or the frame painted
    // right after the scroll jump would show every link at home.
    el.style.transform = `translateY(${start}px)`;
    const controls = js
      ? animate(el, { y: [start, 0] }, options)
      : animate(el, { transform: [`translateY(${start}px)`, "translateY(0px)"] }, options);
    running.set(el, controls);
    active.set(controls, el);
    void controls.finished.then(() => { active.delete(controls); if (running.get(el) === controls) { running.delete(el); el.style.transform = ""; } });
  });
  return true;
}

/** A press on the content holds the cascade where it is; the release lets it finish (as arrivalCascade.ts holdArrivalCascade). */
export function holdArrivalCascade(scroller: HTMLElement, hold: boolean) {
  for (const [controls, el] of active) if (scroller.contains(el)) { if (hold) controls.pause(); else controls.play(); }
}
