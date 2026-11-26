import { CASCADE, MOTION, EASE } from "../motion";

/*
 * Arrival glide (V3, PLAN 8.2). When a whole message arrives under a reader who is following the bottom, the scroll
 * position lands exactly where it belongs in that same frame; what the reader sees is a visual offset (the individual
 * `translate` property, composited, nothing in React state) on the rows in view that glides to 0: 420ms on the out
 * curve, nearest row first, 16ms later for every row above, capped at 8. No overshoot, no extra rise.
 * Layout, scrollTop and scrollHeight never change during it, so nothing shifts and the pane's position is always exact.
 */

type Config = { -readonly [K in keyof typeof CASCADE]: number };
const config = (): Config => ({ ...CASCADE, ...((window as unknown as { __TOFI_CASCADE?: Partial<Config> }).__TOFI_CASCADE ?? {}) });

const TAG = "tofi-cascade";
/** A safety ceiling on how many blocks can ever be animated at once (a very tall window of very short messages). */
const HARD_LINKS = 40;

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
  // A press holds the glide; one that starts under the held press waits paused too, so nothing moves under the pointer.
  const held = typeof document.getAnimations === "function" && document.getAnimations().some(x => x.id === TAG && x.playState === "paused" && x.effect instanceof KeyframeEffect && x.effect.target instanceof Node && scroller.contains(x.effect.target));
  const carried = chain.map(el => { const y = currentOffset(el); for (const a of el.getAnimations()) if (a.id === TAG) a.cancel(); return y; });
  chain.forEach((el, k) => {
    // V3: one shared shift, a straight glide home on the out curve; every row from the 8th up shares the last delay.
    const start = shift + carried[k] + (k === 0 ? cfg.enterExtraPx : 0);
    const glide = el.animate([{ translate: `0 ${start.toFixed(2)}px` }, { translate: "0 0" }], { id: TAG, duration: cfg.durationMs, delay: Math.min(k, cfg.maxLinks - 1) * cfg.staggerMs, easing: EASE.out, fill: "backwards" });
    if (held) glide.pause();
  });
  return true;
}

/** Reduced motion: no travel, a plain fade of the arriving message. */
export function fadeArrival(block: HTMLElement | null) {
  if (!block || typeof block.animate !== "function") return;
  block.animate([{ opacity: 0 }, { opacity: 1 }], { id: TAG, duration: MOTION.quick, easing: EASE.out });
}

/** A press on the content holds the cascade where it is, so nothing moves under the pointer; the release lets it finish. */
export function holdArrivalCascade(scroller: HTMLElement, hold: boolean) {
  if (typeof document.getAnimations !== "function") return;
  for (const a of document.getAnimations()) {
    if (a.id !== TAG || !(a.effect instanceof KeyframeEffect) || !(a.effect.target instanceof Node) || !scroller.contains(a.effect.target)) continue;
    // Pause every glide that has not finished, including one still waiting on its stagger delay or not yet started.
    // pause() settles on the next frame, so a busy frame would let the glide travel on under the pointer; setting the time at once completes it where it is.
    if (hold && a.playState !== "finished" && a.playState !== "paused") { const at = a.currentTime; a.pause(); if (at !== null) a.currentTime = at; }
    else if (!hold && a.playState === "paused") a.play();
  }
}
