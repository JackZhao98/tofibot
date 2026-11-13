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

/*
 * The Bot-computer mini window (docs/design/computer-pip.md section 5). Pure geometry, no DOM: FloatingDesktop feeds it
 * measurements. A position is stored as a corner plus an offset from it, so a resized window or a toggled sidebar leaves the mini
 * window hugging the same corner.
 */
export type PipCorner = "tl" | "tr" | "bl" | "br";
/** `corner` is where it snaps; `dx`/`dy` is the free (Alt) placement, measured from that corner's own point. */
export type PipPlace = { corner: PipCorner; dx: number; dy: number };
export type PipStored = PipPlace & { size: number };

/** Small, medium, large: all 8:5 (the frame's own ratio keeps them so). */
export const PIP_SIZES = [280, 392, 520] as const;
export const PIP_DEFAULT_SIZE = 392;
/** Below the chat header; the top corners must not sit under the title bar. */
const TOP_INSET = 64;
/** The perched cat needs room on the right above the composer. */
const BOTTOM_RIGHT_GAP = 64;

export const pipCorners: readonly PipCorner[] = ["tl", "tr", "bl", "br"];
export const defaultPipStored: PipStored = { corner: "br", dx: 0, dy: 0, size: PIP_DEFAULT_SIZE };

/** Where the window's top-left sits for a corner; the bottom corners end above the composer's top edge. */
export function pipCornerPoint(corner: PipCorner, size: FloatingSize, viewport: FloatingSize, composerTop?: number): FloatingPoint {
  const left = corner === "tl" || corner === "bl" ? EDGE : Math.max(EDGE, viewport.width - size.width - EDGE);
  if (corner === "tl" || corner === "tr") return { left, top: Math.min(TOP_INSET, Math.max(EDGE, viewport.height - size.height - EDGE)) };
  const floor = viewport.height - size.height - EDGE;
  const above = composerTop === undefined ? floor : composerTop - size.height - (corner === "br" ? BOTTOM_RIGHT_GAP : COMPOSER_GAP);
  return { left, top: Math.max(EDGE, Math.min(floor, above)) };
}

/** Keep the whole mini window above the composer, including a remembered free position. */
export function clampPipPoint(point: FloatingPoint, size: FloatingSize, viewport: FloatingSize, composerTop?: number): FloatingPoint {
  const inside = clampFloatingPosition(point, size, viewport);
  const bottom = pipCornerPoint("bl", size, viewport, composerTop).top;
  return { left: inside.left, top: Math.min(inside.top, bottom) };
}

/** The window's visible position for a stored place, kept inside the viewport. */
export function resolvePipPlace(place: PipPlace, size: FloatingSize, viewport: FloatingSize, composerTop?: number): FloatingPoint {
  const origin = pipCornerPoint(place.corner, size, viewport, composerTop);
  return clampPipPoint({ left: origin.left + place.dx, top: origin.top + place.dy }, size, viewport, composerTop);
}

/** The corner whose point is nearest to `point` (the window's top-left, usually the projected landing point). */
export function nearestPipCorner(point: FloatingPoint, size: FloatingSize, viewport: FloatingSize, composerTop?: number): PipCorner {
  let best: PipCorner = "br";
  let distance = Infinity;
  for (const corner of pipCorners) {
    const origin = pipCornerPoint(corner, size, viewport, composerTop);
    const d = (origin.left - point.left) ** 2 + (origin.top - point.top) ** 2;
    if (d < distance) { distance = d; best = corner; }
  }
  return best;
}

/** A free spot (the Alt placement) as corner + offset, measured from the nearest corner. */
export function pipPlaceFromPoint(point: FloatingPoint, size: FloatingSize, viewport: FloatingSize, composerTop?: number): PipPlace {
  const clamped = clampPipPoint(point, size, viewport, composerTop);
  const corner = nearestPipCorner(clamped, size, viewport, composerTop);
  const origin = pipCornerPoint(corner, size, viewport, composerTop);
  return { corner, dx: Math.round(clamped.left - origin.left), dy: Math.round(clamped.top - origin.top) };
}

/** Where a release lands: the corner nearest to the point 120 ms of speed ahead. */
export function projectPipPoint(point: FloatingPoint, velocity: { x: number; y: number }, ahead = 0.12): FloatingPoint {
  return { left: point.left + velocity.x * ahead, top: point.top + velocity.y * ahead };
}

/** One arrow press from a window position: 16 px, Shift 64 px. Alt moves to the corner on that side instead (`snapPipSide`). */
export function nudgePipPoint(point: FloatingPoint, key: string, big: boolean): FloatingPoint {
  const step = big ? 64 : 16;
  return { left: point.left + (key === "ArrowLeft" ? -step : key === "ArrowRight" ? step : 0), top: point.top + (key === "ArrowUp" ? -step : key === "ArrowDown" ? step : 0) };
}

/** Alt + arrow: the corner on that side, keeping the other axis where it was. */
export function snapPipSide(corner: PipCorner, key: string): PipCorner {
  const top = corner === "tl" || corner === "tr";
  const left = corner === "tl" || corner === "bl";
  const nextTop = key === "ArrowUp" ? true : key === "ArrowDown" ? false : top;
  const nextLeft = key === "ArrowLeft" ? true : key === "ArrowRight" ? false : left;
  return `${nextTop ? "t" : "b"}${nextLeft ? "l" : "r"}` as PipCorner;
}

/** What the engine reads back from localStorage: anything that is not a well-formed place falls back to the default. */
export function parsePipStored(raw: string | null): PipStored {
  try {
    const value = raw ? JSON.parse(raw) as Partial<PipStored> : null;
    if (!value || !pipCorners.includes(value.corner as PipCorner)) return defaultPipStored;
    const num = (n: unknown) => typeof n === "number" && Number.isFinite(n) ? Math.round(n) : 0;
    return { corner: value.corner as PipCorner, dx: num(value.dx), dy: num(value.dy), size: PIP_SIZES.includes(value.size as 280) ? value.size as number : PIP_DEFAULT_SIZE };
  } catch { return defaultPipStored; }
}
