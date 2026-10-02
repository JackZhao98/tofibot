import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const run = promisify(execFile);
const uiRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const outputDir = await mkdtemp(join(tmpdir(), "tofi-motion-lab-"));

try {
  await run(join(uiRoot, "node_modules/.bin/tsc"), [
    "src/motion-lab/lib/spring.ts", "src/motion-lab/lib/choreo.ts", "--ignoreConfig",
    "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler",
    "--outDir", outputDir, "--skipLibCheck", "--declaration", "false", "--pretty", "false",
  ], { cwd: uiRoot });
  const spring = await import(pathToFileURL(join(outputDir, "spring.js")));
  const choreo = await import(pathToFileURL(join(outputDir, "choreo.js")));
  const assert = (condition, reason) => { if (!condition) throw new Error(reason); };
  const near = (a, b, eps = 1e-6) => Math.abs(a - b) <= eps;

  // stepSpring is pure: it returns a new state and leaves its input untouched.
  const bouncy = { stiffness: 260, damping: 12, mass: 1 };
  const start = Object.freeze({ position: 0, velocity: 0 });
  const next = spring.stepSpring(start, 1, bouncy, 1 / 60);
  assert(next !== start && start.position === 0 && start.velocity === 0, "stepSpring must not mutate its input");
  assert(next.position > 0 && next.velocity > 0, "a spring must start moving toward its target");

  // It converges, and an underdamped spring overshoots before settling.
  let state = start;
  let peak = 0;
  for (let frame = 0; frame < 600; frame += 1) {
    state = spring.stepSpring(state, 1, bouncy, 1 / 60);
    peak = Math.max(peak, state.position);
  }
  assert(spring.isSpringSettled(state, 1), "an underdamped spring must settle within 10s");
  assert(peak > 1.05, `an underdamped spring must overshoot, peak was ${peak}`);

  // A critically damped spring reaches the target without a visible overshoot.
  const critical = { stiffness: 300, damping: 2 * Math.sqrt(300), mass: 1 };
  state = start;
  peak = 0;
  for (let frame = 0; frame < 600; frame += 1) {
    state = spring.stepSpring(state, 1, critical, 1 / 60);
    peak = Math.max(peak, state.position);
  }
  assert(peak <= 1.002, `a critically damped spring must not overshoot, peak was ${peak}`);

  // A long frame (backgrounded tab) is clamped and stays numerically stable.
  const stiff = { stiffness: 900, damping: 10, mass: 1 };
  const afterStall = spring.stepSpring({ position: 0, velocity: 0 }, 1, stiff, 5);
  assert(Number.isFinite(afterStall.position) && Math.abs(afterStall.position) < 3, "a stalled frame must be clamped");

  // Invalid configuration fails fast instead of producing NaN motion.
  for (const invalid of [{ stiffness: 0, damping: 10, mass: 1 }, { stiffness: 100, damping: -1, mass: 1 }, { stiffness: 100, damping: 10, mass: Number.NaN }]) {
    let threw = false;
    try { spring.stepSpring(start, 1, invalid, 1 / 60); } catch { threw = true; }
    assert(threw, `invalid spring ${JSON.stringify(invalid)} must throw`);
  }

  // springEasing produces a CSS linear() easing that starts at 0 and ends exactly at 1.
  const easing = spring.springEasing(bouncy);
  assert(easing.easing.startsWith("linear(0, ") && easing.easing.endsWith(", 1)"), `unexpected easing ${easing.easing.slice(0, 40)}`);
  assert(easing.durationMs > 200 && easing.durationMs <= 3000, `unexpected spring duration ${easing.durationMs}`);
  const stops = easing.easing.slice(7, -1).split(", ").map(Number);
  assert(stops.every(Number.isFinite) && stops.length >= 8 && stops.length <= 181, "easing stops must be finite and bounded");
  assert(Math.max(...stops) > 1, "a bouncy easing must keep its overshoot");

  // rippleDelays grows with distance from the origin.
  const delays = choreo.rippleDelays({ x: 0, y: 0 }, [{ x: 0, y: 0 }, { x: 30, y: 40 }, { x: 300, y: 400 }], 0.5);
  assert(delays[0] === 0 && near(delays[1], 100) && near(delays[2], 1000), `unexpected ripple delays ${delays}`);

  // cumulativeStops maps segment lengths to 0..1 progress stops.
  const cumulative = choreo.cumulativeStops([10, 30, 60]);
  assert(cumulative.length === 4 && cumulative[0] === 0 && near(cumulative[1], 0.1) && near(cumulative[2], 0.4) && cumulative[3] === 1, `unexpected stops ${cumulative}`);
  assert(choreo.cumulativeStops([]).length === 1, "an empty path has a single stop");

  // arcPoints follows a quadratic arc: exact ends, raised middle.
  const arc = choreo.arcPoints({ x: 0, y: 100 }, { x: 200, y: 0 }, 80, 9);
  assert(arc.length === 9 && near(arc[0].x, 0) && near(arc[0].y, 100) && near(arc[8].x, 200) && near(arc[8].y, 0), "arc must start and end at its endpoints");
  assert(arc[4].y < 50, "arc midpoint must be lifted above the straight line");

  // benchSlots lays out blocks inside the bench without overlap.
  const slots = choreo.benchSlots(900, 7, 120);
  assert(slots.length === 7, "one slot per block");
  assert(slots.every((slot) => slot.x >= 0 && slot.x + 120 <= 900), "slots must stay inside the bench width");
  for (let index = 1; index < slots.length; index += 1) {
    const sameRow = slots[index].row === slots[index - 1].row;
    if (sameRow) assert(slots[index].x - slots[index - 1].x >= 120, "slots in one row must not overlap");
  }
  const narrow = choreo.benchSlots(360, 7, 96);
  assert(new Set(narrow.map((slot) => slot.row)).size > 1, "a narrow bench must wrap into several rows");
  assert(narrow.every((slot) => slot.x >= 0 && slot.x + 96 <= 360), "wrapped slots must stay inside the bench width");

  // closedSpline passes through every node and closes the loop.
  const loop = choreo.closedSpline([{ x: 0, y: 0 }, { x: 100, y: 0 }, { x: 100, y: 100 }, { x: 0, y: 100 }]);
  assert(loop.segments.length === 4, "one cubic segment per node in a closed loop");
  assert(loop.path.startsWith("M0 0 C") && loop.path.endsWith(" 0 0"), `closed path must start and end at the first node: ${loop.path}`);
  assert(loop.segments[1].startsWith("M100 0 C") && loop.segments[1].endsWith(" 100 100"), `segment 1 must run node 1 -> node 2: ${loop.segments[1]}`);
  let threwSpline = false;
  try { choreo.closedSpline([{ x: 0, y: 0 }, { x: 1, y: 1 }]); } catch { threwSpline = true; }
  assert(threwSpline, "a closed spline needs at least three nodes");

  console.log("motion lab: spring + choreography checks passed");
} finally {
  await rm(outputDir, { recursive: true, force: true });
}
