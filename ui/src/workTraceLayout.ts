/**
 * The grid of a work-trace strip. The server draws the strip (internal/app/montage.go) with these cell sizes; the page
 * needs them to cut a thumbnail out of the one picture and to tell which step a click on the big picture landed on.
 * Keep the numbers equal to montageTileW/H, montageLabelH and montageGap. No other module is imported, so a
 * test can run this file alone.
 */
export const TRACE_TILE_W = 424;
export const TRACE_TILE_H = 265;
export const TRACE_LABEL_H = 18;
export const TRACE_GAP = 2;
export const TRACE_MAX_COLS = 3;

/** The cells of a strip: the steps shown, plus the summary cell when earlier steps were left out. */
export function traceCells(tiles: number, steps: number): number {
  return tiles + (steps > tiles ? 1 : 0);
}

function columns(cells: number): number {
  return Math.max(1, Math.min(TRACE_MAX_COLS, cells));
}

export function traceStripSize(cells: number): { width: number; height: number } {
  const cols = columns(cells), rows = Math.ceil(cells / cols);
  return {
    width: cols * TRACE_TILE_W + (cols + 1) * TRACE_GAP,
    height: rows * (TRACE_TILE_H + TRACE_LABEL_H) + (rows + 1) * TRACE_GAP,
  };
}

/** The picture area of a cell (without its label line), in strip pixels. */
export function traceCellRect(index: number, cells: number): { x: number; y: number; width: number; height: number } {
  const cols = columns(cells);
  return {
    x: TRACE_GAP + (index % cols) * (TRACE_TILE_W + TRACE_GAP),
    y: TRACE_GAP + Math.floor(index / cols) * (TRACE_TILE_H + TRACE_LABEL_H + TRACE_GAP),
    width: TRACE_TILE_W,
    height: TRACE_TILE_H,
  };
}

/** The cell under a point of the strip, given as fractions of its width and height; the label line belongs to its cell. */
export function traceCellAt(fx: number, fy: number, cells: number): number | null {
  if (!(fx >= 0 && fx <= 1 && fy >= 0 && fy <= 1)) return null;
  const { width, height } = traceStripSize(cells);
  const x = fx * width, y = fy * height, cols = columns(cells);
  const col = Math.floor((x - TRACE_GAP / 2) / (TRACE_TILE_W + TRACE_GAP));
  const row = Math.floor((y - TRACE_GAP / 2) / (TRACE_TILE_H + TRACE_LABEL_H + TRACE_GAP));
  if (col < 0 || col >= cols || row < 0) return null;
  const index = row * cols + col;
  return index < cells ? index : null;
}

/** Background styles that cut one cell out of the strip at a thumbnail width; no second file is stored. */
export function traceThumbStyle(url: string, index: number, cells: number, thumbWidth: number): { width: number; height: number; backgroundImage: string; backgroundSize: string; backgroundPosition: string } {
  const scale = thumbWidth / TRACE_TILE_W;
  const size = traceStripSize(cells), rect = traceCellRect(index, cells);
  return {
    width: thumbWidth,
    height: Math.round(TRACE_TILE_H * scale),
    backgroundImage: `url("${url}")`,
    backgroundSize: `${(size.width * scale).toFixed(2)}px ${(size.height * scale).toFixed(2)}px`,
    backgroundPosition: `${(-rect.x * scale).toFixed(2)}px ${(-rect.y * scale).toFixed(2)}px`,
  };
}

/**
 * Where a run's work trace line goes. It ends the run's Activity list when that list is drawn (Activity on, or opened for the
 * run, and a task block exists); otherwise it sits under the run's final reply. Never both.
 */
export function traceUnderReply(input: { hasTrace: boolean; isFinalReply: boolean; hasTaskBlock: boolean; activityShown: boolean }): boolean {
  return input.hasTrace && input.isFinalReply && !(input.hasTaskBlock && input.activityShown);
}
