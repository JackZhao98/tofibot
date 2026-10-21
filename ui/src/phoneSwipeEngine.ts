import { dimAt, listOffset, mountPhoneSwipe, readX, springPath, TAG, type Settle, type SwipeLive } from "./phoneSwipeGesture";

/*
 * The shipped swipe-back variant (decision D35): the released pane travels on Web Animations. The spring (SPRING.push,
 * leaving with the finger's velocity) is baked into keyframes once, at release, and the chat, the list and the list's dim
 * play them on the compositor; the main thread does nothing until the end. This chunk is loaded in idle time on the phone
 * layout only (usePhoneSwipeBack.ts).
 */
const STEP_MS = 8;

const settleWithWaapi: Settle = ({ panes, from, to, velocity, done }) => {
  const path = springPath(from, to, velocity, STEP_MS);
  const timing: KeyframeAnimationOptions = { id: TAG, duration: (path.length - 1) * STEP_MS, easing: "linear", fill: "forwards" };
  panes.dim?.cancel();
  panes.dim = null;
  const chat = panes.chat.animate(path.map(x => ({ transform: `translate3d(${x}px,0,0)` })), timing);
  panes.list.animate(path.map(x => ({ transform: `translate3d(${listOffset(x, panes.width)}px,0,0)` })), timing);
  const dim = panes.list.animate(path.map(x => ({ opacity: dimAt(x, panes.width) })), { ...timing, pseudoElement: "::after" });
  let stopped = false;
  // The dim lives on a pseudo-element, which getAnimations() on the list does not return: it is cleared here, after the
  // commit, in the same frame (transition:none still holds), so it never lingers over a later tap push.
  chat.finished.then(() => { if (!stopped) { done(); dim.cancel(); } }, () => {});
  return {
    stop() {
      stopped = true;
      const x = readX(panes.chat);
      for (const el of [panes.chat, panes.list]) for (const a of el.getAnimations()) if (a.id === TAG) a.cancel();
      dim.cancel();
      return x;
    },
  };
};

export const mount = (back: SwipeLive) => mountPhoneSwipe(back, settleWithWaapi);
