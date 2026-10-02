// Pure geometry and timing helpers for the lab's choreographed sequences.

export type Point = Readonly<{ x: number; y: number }>;
export type BenchSlot = Readonly<{ x: number; y: number; row: number; tilt: number }>;

/** Start delay per point so a wave travels outward from the origin at pxPerMs. */
export function rippleDelays(origin: Point, points: readonly Point[], pxPerMs: number): number[] {
  if (!(pxPerMs > 0)) throw new Error("pxPerMs must be positive");
  return points.map((point) => Math.hypot(point.x - origin.x, point.y - origin.y) / pxPerMs);
}

/** Segment lengths -> cumulative 0..1 progress at each joint (first 0, last 1). */
export function cumulativeStops(lengths: readonly number[]): number[] {
  const total = lengths.reduce((sum, length) => sum + length, 0);
  if (total <= 0) return [0];
  return lengths.reduce<number[]>((stops, length) => [...stops, stops[stops.length - 1] + length / total], [0])
    .map((stop, index, all) => (index === all.length - 1 ? 1 : stop));
}

/** Samples along a quadratic arc whose control point is lifted above the midpoint. */
export function arcPoints(from: Point, to: Point, lift: number, samples: number): Point[] {
  const control = { x: (from.x + to.x) / 2, y: Math.min(from.y, to.y) - lift };
  const count = Math.max(2, samples);
  return Array.from({ length: count }, (_, index) => {
    const t = index / (count - 1);
    const u = 1 - t;
    return {
      x: u * u * from.x + 2 * u * t * control.x + t * t * to.x,
      y: u * u * from.y + 2 * u * t * control.y + t * t * to.y,
    };
  });
}

// Resting tilts repeat so the row reads as hand-placed toys, not a grid.
const TILTS = [-5, 3, -2, 6, -4, 2, -6];

/** Home positions for the toy blocks: one playful row, wrapping when narrow. */
export function benchSlots(width: number, count: number, size: number): BenchSlot[] {
  const minGap = Math.round(size * 0.14);
  const perRow = Math.max(1, Math.min(count, Math.floor((width + minGap) / (size + minGap))));
  const rows = Math.ceil(count / perRow);
  return Array.from({ length: count }, (_, index) => {
    const row = Math.floor(index / perRow);
    const column = index % perRow;
    const inRow = row === rows - 1 ? count - row * perRow : perRow;
    const gap = inRow > 1 ? Math.min(size * 0.5, (width - inRow * size) / (inRow - 1)) : 0;
    const rowWidth = inRow * size + (inRow - 1) * gap;
    const left = (width - rowWidth) / 2;
    const bob = column % 2 === 0 ? 0 : size * 0.16;
    return {
      x: left + column * (size + gap),
      y: row * size * 1.18 + bob,
      row,
      tilt: TILTS[index % TILTS.length],
    };
  });
}

const fmt = (value: number) => String(Math.round(value * 10) / 10);

/**
 * Closed Catmull-Rom loop through every node, as cubic Béziers. Returns the
 * whole path and each node-to-node segment (for measuring progress stops).
 */
export function closedSpline(nodes: readonly Point[]): { path: string; segments: string[] } {
  if (nodes.length < 3) throw new Error("A closed spline needs at least three nodes");
  const at = (index: number) => nodes[(index + nodes.length) % nodes.length];
  const curves = nodes.map((start, index) => {
    const before = at(index - 1);
    const end = at(index + 1);
    const after = at(index + 2);
    const c1 = { x: start.x + (end.x - before.x) / 6, y: start.y + (end.y - before.y) / 6 };
    const c2 = { x: end.x - (after.x - start.x) / 6, y: end.y - (after.y - start.y) / 6 };
    return { start, curve: `C${fmt(c1.x)} ${fmt(c1.y)} ${fmt(c2.x)} ${fmt(c2.y)} ${fmt(end.x)} ${fmt(end.y)}` };
  });
  const move = (point: Point) => `M${fmt(point.x)} ${fmt(point.y)}`;
  return {
    path: `${move(nodes[0])} ${curves.map(({ curve }) => curve).join(" ")}`,
    segments: curves.map(({ start, curve }) => `${move(start)} ${curve}`),
  };
}
