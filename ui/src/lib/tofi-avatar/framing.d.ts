import type { Shape } from './index.js';
/** Placement of a cat host inside a square frame so the head fills it, as multiples of the frame edge. */
export function catFraming(shape: Shape, headY?: number): {size: number; left: number; top: number};
