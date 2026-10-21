import { animate } from "motion";
import { SPRING } from "../../motion";
import { mountPhoneSwipe, paint, type Settle, type SwipeLive } from "../../phoneSwipeGesture";

/*
 * Migration step 4, experiment 2, the Motion variant of the swipe-back (decision D35 kept the Web Animations one,
 * src/phoneSwipeEngine.ts). Same gesture core and the same spring (SPRING.push, the finger's velocity handed over); only
 * the travel after release differs: Motion's spring drives one number and every frame paints the panes from it
 * (Motion 14.4.0 hands only `transform`, `opacity`, `filter`, `clipPath` and `backgroundColor` to Web Animations, and
 * never when `onUpdate` is set, so this runs on the main thread each frame; a `transform` string animation would take the
 * compositor path but cannot carry the finger's velocity).
 *
 * Not part of any build: nothing in the app imports it. `motionExperimentPlugin` swaps it in for ./phoneSwipeEngine in
 * `npm run test:motion-experiment` and in the dev server with TOFI_MOTION_EXPERIMENT=motion.
 */
const settleWithMotion: Settle = ({ panes, from, to, velocity, done }) => {
  let x = from;
  const ahead = to > from;
  const controls = animate(from, to, {
    type: "spring", ...SPRING.push, velocity: velocity * 1000, restDelta: 0.5, restSpeed: 20,
    onUpdate: value => { x = ahead ? Math.min(value, to) : Math.max(value, to); paint(panes, x); },
    onComplete: done,
  });
  return { stop() { controls.stop(); return x; } };
};

export const mount = (back: SwipeLive) => mountPhoneSwipe(back, settleWithMotion);
