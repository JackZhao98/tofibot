export type FloatingPoint = { left: number; top: number };
export type FloatingSize = { width: number; height: number };

const EDGE = 20;
const COMPOSER_GAP = 16;

export function clampFloatingPosition(point: FloatingPoint, size: FloatingSize, viewport: FloatingSize): FloatingPoint {
  return {
    left: Math.min(Math.max(EDGE, point.left), Math.max(EDGE, viewport.width - size.width - EDGE)),
    top: Math.min(Math.max(EDGE, point.top), Math.max(EDGE, viewport.height - size.height - EDGE)),
  };
}

/** A preview only docks at the right edge, above the composer or below the header. */
export function dockFloatingPosition(point: FloatingPoint, size: FloatingSize, viewport: FloatingSize, composerTop: number): FloatingPoint {
  const right = Math.max(EDGE, viewport.width - size.width - EDGE);
  const bottom = Math.max(EDGE, Math.min(viewport.height - size.height - EDGE, composerTop - size.height - COMPOSER_GAP));
  const top = Math.min(EDGE, bottom);
  return { left: right, top: Math.abs(point.top - top) <= Math.abs(point.top - bottom) ? top : bottom };
}
