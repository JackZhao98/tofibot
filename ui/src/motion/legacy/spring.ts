// A small damped-spring integrator shared by the physics toys (per-frame) and
// the CSS transitions (pre-baked into linear() easings).

export type SpringConfig = Readonly<{ stiffness: number; damping: number; mass: number }>;
export type SpringState = Readonly<{ position: number; velocity: number }>;

export const SPRINGS = {
  /** Toy blocks: visible overshoot, lands with a wobble. */
  toy: { stiffness: 240, damping: 13, mass: 1 },
  /** Status morphs: one small overshoot, then still. */
  morph: { stiffness: 320, damping: 22, mass: 1 },
  /** Drag follow and reduced-energy settling: no overshoot. */
  settle: { stiffness: 380, damping: 39, mass: 1 },
} as const satisfies Record<string, SpringConfig>;

const SUBSTEP = 1 / 240;
// A backgrounded tab can hand us seconds at once; simulate at most 64ms.
const MAX_FRAME = 0.064;

function assertConfig(config: SpringConfig) {
  const { stiffness, damping, mass } = config;
  if (!(stiffness > 0) || !(damping >= 0) || !(mass > 0) || ![stiffness, damping, mass].every(Number.isFinite)) {
    throw new Error(`Invalid spring config: ${JSON.stringify(config)}`);
  }
}

/** Advance a spring by dt seconds. Returns a new state; the input is untouched. */
export function stepSpring(state: SpringState, target: number, config: SpringConfig, dt: number): SpringState {
  assertConfig(config);
  let position = state.position;
  let velocity = state.velocity;
  let remaining = Math.min(Math.max(dt, 0), MAX_FRAME);
  while (remaining > 1e-9) {
    const h = Math.min(SUBSTEP, remaining);
    const force = -config.stiffness * (position - target) - config.damping * velocity;
    velocity += (force / config.mass) * h;
    position += velocity * h;
    remaining -= h;
  }
  return { position, velocity };
}

export function isSpringSettled(state: SpringState, target: number, tolerance = 0.01): boolean {
  return Math.abs(state.position - target) < tolerance && Math.abs(state.velocity) < tolerance * 10;
}

/**
 * Bake a spring into a CSS `linear()` easing so plain transitions and WAAPI
 * animations get real spring overshoot without a JS animation loop.
 */
export function springEasing(config: SpringConfig, maxSeconds = 3): { easing: string; durationMs: number } {
  assertConfig(config);
  const frame = 1 / 60;
  const stops: number[] = [0];
  let state: SpringState = { position: 0, velocity: 0 };
  for (let elapsed = frame; elapsed <= maxSeconds; elapsed += frame) {
    state = stepSpring(state, 1, config, frame);
    stops.push(state.position);
    if (isSpringSettled(state, 1, 0.001)) break;
  }
  stops[stops.length - 1] = 1;
  const body = stops.map((value, index) => (index === 0 ? "0" : index === stops.length - 1 ? "1" : value.toFixed(4))).join(", ");
  return { easing: `linear(${body})`, durationMs: Math.round((stops.length - 1) * frame * 1000) };
}
