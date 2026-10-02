import { SPRINGS, isSpringSettled, stepSpring, type SpringConfig, type SpringState } from "../lib/spring";
import type { BenchSlot } from "../lib/choreo";

// Per-block rigid-ish body: springs toward its home slot, bounces on its own floor.
export type Body = Readonly<{
  x: SpringState;
  y: SpringState;
  tilt: SpringState;
  lift: SpringState;
  squash: SpringState;
  home: BenchSlot;
  /** Free fall with gravity (intro drop) instead of the home spring. */
  falling: boolean;
  /** Not yet released into the intro drop. */
  waitingUntil: number;
  dragging: boolean;
  dragTilt: number;
}>;

export type Impact = Readonly<{ index: number; speed: number }>;

const GRAVITY = 4200; // px/s²
const RESTITUTION = 0.4;
const SQUASH: SpringConfig = { stiffness: 560, damping: 15, mass: 1 };
const REST: SpringState = { position: 0, velocity: 0 };

export function restingBody(home: BenchSlot): Body {
  return {
    x: { position: home.x, velocity: 0 },
    y: { position: home.y, velocity: 0 },
    tilt: { position: home.tilt, velocity: 0 },
    lift: REST,
    squash: REST,
    home,
    falling: false,
    waitingUntil: 0,
    dragging: false,
    dragTilt: 0,
  };
}

/** Parked above the bench, released at `releaseAt` into a gravity drop. */
export function droppingBody(home: BenchSlot, dropHeight: number, releaseAt: number, spin: number): Body {
  return {
    ...restingBody(home),
    y: { position: home.y - dropHeight, velocity: 0 },
    tilt: { position: home.tilt + spin, velocity: 0 },
    falling: true,
    waitingUntil: releaseAt,
  };
}

export function kickSquash(body: Body, speed: number): Body {
  const kick = Math.min(3.4, speed / 850);
  return { ...body, squash: { position: body.squash.position, velocity: body.squash.velocity + kick } };
}

function fall(body: Body, dt: number): { body: Body; impact: number } {
  const velocity = body.y.velocity + GRAVITY * dt;
  const position = body.y.position + velocity * dt;
  if (position < body.home.y) return { body: { ...body, y: { position, velocity } }, impact: 0 };
  const bounce = -velocity * RESTITUTION;
  const landed = Math.abs(bounce) < 140;
  return {
    body: { ...body, y: { position: body.home.y, velocity: landed ? 0 : bounce }, falling: !landed },
    impact: velocity,
  };
}

function spring(body: Body, dt: number): { body: Body; impact: number } {
  const x = stepSpring(body.x, body.home.x, SPRINGS.toy, dt);
  const y = stepSpring(body.y, body.home.y, SPRINGS.toy, dt);
  // The bench is a floor: a block springing home lands on it rather than sinking through.
  if (y.position > body.home.y && y.velocity > 0) {
    return { body: { ...body, x, y: { position: body.home.y, velocity: -y.velocity * RESTITUTION } }, impact: y.velocity };
  }
  return { body: { ...body, x, y }, impact: 0 };
}

/** Advance one body. Dragged bodies only animate tilt/lift; the pointer owns x/y. */
export function stepBody(body: Body, dt: number, now: number): { body: Body; impact: number } {
  if (body.waitingUntil > now) return { body, impact: 0 };
  const tiltTarget = body.dragging ? body.dragTilt : body.home.tilt;
  const common: Body = {
    ...body,
    tilt: stepSpring(body.tilt, tiltTarget, body.dragging ? SPRINGS.settle : SPRINGS.toy, dt),
    lift: stepSpring(body.lift, body.dragging ? 1 : 0, SPRINGS.morph, dt),
    squash: stepSpring(body.squash, 0, SQUASH, dt),
  };
  if (body.dragging) return { body: common, impact: 0 };
  return body.falling ? fall(common, dt) : spring(common, dt);
}

export function isBodyAtRest(body: Body, now: number): boolean {
  if (body.dragging || body.falling || body.waitingUntil > now) return false;
  return isSpringSettled(body.x, body.home.x, 0.3)
    && isSpringSettled(body.y, body.home.y, 0.3)
    && isSpringSettled(body.tilt, body.home.tilt, 0.05)
    && isSpringSettled(body.lift, 0, 0.002)
    && isSpringSettled(body.squash, 0, 0.002);
}

/** CSS transform for a body; lifting raises it up-left so the hard shadow reads as height. */
export function bodyTransform(body: Body): string {
  const lift = body.lift.position;
  const squash = Math.max(-0.22, Math.min(0.22, body.squash.position));
  const grow = 1 + lift * 0.06;
  const x = body.x.position - lift * 5;
  const y = body.y.position - lift * 8;
  return `translate3d(${x.toFixed(2)}px, ${y.toFixed(2)}px, 0) rotate(${body.tilt.position.toFixed(2)}deg) scale(${(grow * (1 + squash)).toFixed(4)}, ${(grow * (1 - squash)).toFixed(4)})`;
}
