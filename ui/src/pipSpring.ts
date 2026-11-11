// The mini window's spring integrator (pipDrag.ts), alone so test:floating-desktop can hold the pipSnap numbers without a browser.

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
