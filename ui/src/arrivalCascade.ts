import { CASCADE, MOTION, EASE } from "./motion";

/*
 * Arrival cascade. When a whole message arrives under a reader who is following the bottom, the scroll position lands
 * exactly where it belongs in that same frame; what the reader sees is a visual offset (the individual `translate`
 * property, composited, nothing in React state) on the messages in view that settles to 0:
 *   - the new message comes first and rises in from below (it starts CASCADE.enterExtraPx lower than the content it pushes);
 *   - every message further up starts CASCADE.staggerMs later than the one below it, so a wave travels up the pane, the
 *     impulse handed from link to link like a Newton's cradle;
 *   - each is a slightly under-damped spring: it passes its true place by 2-4 px, springs back and is home at 0.
 * Layout, scrollTop and scrollHeight never change during it, so nothing shifts and the pane's position is always exact.
 */

type Config = { -readonly [K in keyof typeof CASCADE]: number };
const config = (): Config => ({ ...CASCADE, ...((window as unknown as { __TOFI_CASCADE?: Partial<Config> }).__TOFI_CASCADE ?? {}) });

const TAG = "tofi-cascade";
/** A safety ceiling on how many blocks can ever be animated at once (a very tall window of very short messages). */
const HARD_LINKS = 40;

/** Normalised damped-spring step response from 1 toward 0 that first crosses 0 at tc and rings out; s(0)=1, s'(0)=0. */
function spring(t: number, zeta: number, crossAt: number) {
  const phi = Math.asin(zeta);
  const wd = (Math.PI / 2 + phi) / crossAt;
  const w = wd / Math.sqrt(1 - zeta * zeta);
  return Math.exp(-zeta * w * t) * Math.cos(wd * t - phi) / Math.cos(phi);
}

/** The damping that makes a spring starting `start` px away pass its resting place by `overshoot` px. */
function dampingFor(start: number, overshoot: number) {
  const ratio = Math.min(0.4, Math.max(0.01, overshoot / start));
  const ln = -Math.log(ratio);
  return ln / Math.sqrt(Math.PI * Math.PI + ln * ln);
}

/** Keyframes of one message's offset: start px below its place -> past it by `overshoot` -> 0. */
export function cascadeKeyframes(start: number, overshoot: number, crossShare: number, samples: number): Keyframe[] {
  const zeta = dampingFor(start, overshoot);
  const frames: Keyframe[] = [];
  for (let i = 0; i <= samples; i++) {
    const tau = i / samples;
    // The ring-out is faded to exactly 0 over the last 14% so the motion ends on the true position, not near it.
    const fade = tau < 0.86 ? 1 : 0.5 + 0.5 * Math.cos(Math.PI * (tau - 0.86) / 0.14);
    const y = i === samples ? 0 : start * spring(tau, zeta, crossShare) * fade;
    frames.push({ offset: tau, translate: `0 ${y.toFixed(2)}px` });
  }
  return frames;
}

function currentOffset(el: HTMLElement) {
  const value = getComputedStyle(el).translate;
  if (!value || value === "none") return 0;
  return parseFloat(value.split(" ")[1] ?? "0") || 0;
}

/** Messages that take part: the newest block, then every block in view (at least maxLinks of them are taken when they exist). */
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
/** How far the cascade may reach: the pane's bottom padding absorbs the offset, so scrollHeight never grows while it plays. */
export function cascadeRoom(scroller: HTMLElement) {
  const cfg = config();
  const room = (parseFloat(getComputedStyle(scroller).paddingBottom) || 0) - 8 - cfg.enterExtraPx;
  return Math.min(cfg.maxShiftPx, room);
}

/**
 * Starts the cascade. `shift` is how far the scroll just moved the content (the offset every message starts at).
 * Call right after the scroll position was set, before the browser paints. Returns false if nothing was animated.
 */
export function playArrivalCascade(scroller: HTMLElement, shift: number) {
  if (typeof Element.prototype.animate !== "function" || shift < 4) return false;
  const cfg = config();
  const chain = links(scroller, cfg.maxLinks);
  if (!chain.length) return false;
  const carried = chain.map(el => { const y = currentOffset(el); for (const a of el.getAnimations()) if (a.id === TAG) a.cancel(); return y; });
  chain.forEach((el, k) => {
    const lead = k === 0;
    const start = shift + carried[k] + (lead ? cfg.enterExtraPx : 0);
    const overshoot = Math.max(cfg.overshootMinPx, cfg.overshootPx - k * cfg.overshootTaperPx);
    const cross = lead ? cfg.crossShare * cfg.leadSpeedup : cfg.crossShare;
    el.animate(cascadeKeyframes(start, overshoot, cross, cfg.samples), { id: TAG, duration: cfg.durationMs, delay: Math.min(k, cfg.maxLinks - 1) * cfg.staggerMs, easing: "linear", fill: "backwards" });
  });
  return true;
}

/** Reduced motion: no travel, a plain fade of the arriving message. */
export function fadeArrival(block: HTMLElement | null) {
  if (!block || typeof block.animate !== "function") return;
  block.animate([{ opacity: 0 }, { opacity: 1 }], { id: TAG, duration: MOTION.quick, easing: EASE.out });
}
