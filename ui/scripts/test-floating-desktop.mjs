import assert from "node:assert/strict";
import { spring } from "motion";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const run = promisify(execFile);
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-floating-desktop-"));
try {
  await run(join(root, "node_modules/.bin/tsc"), ["src/floatingDesktopPosition.ts", "src/motion.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", output, "--skipLibCheck", "--pretty", "false"], { cwd: root });
  const pip = await import(pathToFileURL(join(output, "floatingDesktopPosition.js")));
  const { clampFloatingPosition, dockFloatingPosition } = await import(pathToFileURL(join(output, "floatingDesktopPosition.js")));
  const size = { width: 360, height: 210 };
  const viewport = { width: 1440, height: 900 };
  const top = dockFloatingPosition({ left: 400, top: 70 }, size, viewport, 790);
  const bottom = dockFloatingPosition({ left: 300, top: 650 }, size, viewport, 790);
  if (top.left !== 1060 || top.top !== 20) throw new Error(`top docking failed: ${JSON.stringify(top)}`);
  if (bottom.left !== 1060 || bottom.top !== 564) throw new Error(`composer docking failed: ${JSON.stringify(bottom)}`);
  const clamped = clampFloatingPosition({ left: -200, top: 1200 }, size, viewport);
  if (clamped.left !== 20 || clamped.top !== 670) throw new Error(`clamping failed: ${JSON.stringify(clamped)}`);
  // The mini window: four snap targets, nearest corner, corner + offset surviving a viewport change, keys, storage.
  const win = { width: 392, height: 245 };
  const eq = (actual, expected, label) => { if (JSON.stringify(actual) !== JSON.stringify(expected)) throw new Error(`${label}: ${JSON.stringify(actual)} != ${JSON.stringify(expected)}`); };
  eq(pip.pipCornerPoint("tl", win, viewport, 790), { left: 20, top: 64 }, "top-left");
  eq(pip.pipCornerPoint("tr", win, viewport, 790), { left: 1028, top: 64 }, "top-right");
  eq(pip.pipCornerPoint("bl", win, viewport, 790), { left: 20, top: 529 }, "bottom-left ends 16px above the composer");
  eq(pip.pipCornerPoint("br", win, viewport, 790), { left: 1028, top: 481 }, "bottom-right leaves room for the cat");
  eq(pip.nearestPipCorner({ left: 900, top: 500 }, win, viewport, 790), "br", "nearest corner");
  eq(pip.nearestPipCorner({ left: 100, top: 120 }, win, viewport, 790), "tl", "nearest corner, top-left");
  eq(pip.nearestPipCorner(pip.projectPipPoint({ left: 500, top: 150 }, { x: -2000, y: 0 }), win, viewport, 790), "tl", "a flick to the left lands on the left");
  const free = pip.pipPlaceFromPoint({ left: 880, top: 400 }, win, viewport, 790);
  eq(free.corner, "br", "free place hangs from the nearest corner");
  eq(pip.resolvePipPlace(free, win, viewport, 790), { left: 880, top: 400 }, "free place resolves to where it was let go");
  eq(pip.resolvePipPlace({ corner: "br", dx: -100, dy: -50 }, win, { width: 1000, height: 700 }, 600), { left: 488, top: 241 }, "corner + offset after the window shrank");
  eq(pip.resolvePipPlace({ corner: "br", dx: 900, dy: 900 }, win, { width: 500, height: 400 }, 300), { left: 88, top: 39 }, "an offset stays above the composer and inside the viewport");
  eq(pip.snapPipSide("br", "ArrowLeft"), "bl", "Alt+Left from the bottom-right");
  eq(pip.snapPipSide("tl", "ArrowDown"), "bl", "Alt+Down from the top-left");
  eq(pip.nudgePipPoint({ left: 100, top: 100 }, "ArrowRight", false), { left: 116, top: 100 }, "an arrow moves 16");
  eq(pip.nudgePipPoint({ left: 100, top: 100 }, "ArrowUp", true), { left: 100, top: 36 }, "Shift moves 64");
  eq(pip.parsePipStored(null), pip.defaultPipStored, "nothing stored");
  eq(pip.parsePipStored("{broken"), pip.defaultPipStored, "unreadable storage");
  eq(pip.parsePipStored(JSON.stringify({ corner: "tl", dx: 12.4, dy: -3, size: 280 })), { corner: "tl", dx: 12, dy: -3, size: 280 }, "stored place");
  eq(pip.parsePipStored(JSON.stringify({ corner: "x", dx: 1, dy: 1, size: 99 })), pip.defaultPipStored, "unknown corner");
  // Hold the shared token and measure Motion's own spring, rather than a second integrator.
  const { SPRING } = await import(pathToFileURL(join(output, "motion.js")));
  assert.deepEqual(SPRING.pipSnap, { stiffness: 420, damping: 34, mass: 1, restDelta: 1, restSpeed: 10 });
  const travel = 300;
  const generator = spring({ ...SPRING.pipSnap, keyframes: [travel, 0] });
  let peakPast = 0, nearAt = null;
  for (let t = 0; t <= 2000; t += 1000 / 60) {
    const { value } = generator.next(t);
    peakPast = Math.max(peakPast, -value);
    if (nearAt === null && Math.abs(value) < travel * 0.01) nearAt = t;
  }
  if (nearAt === null || nearAt > 300) throw new Error(`pipSnap needs ${nearAt} ms to come within 1% of home, over 300`);
  if (peakPast > travel * 0.01) throw new Error(`pipSnap overshoots by ${peakPast.toFixed(2)} px of ${travel} (over 1%)`);
  console.log(`pipSnap: within 1% in ${Math.round(nearAt)} ms, overshoot ${peakPast.toFixed(2)} px of ${travel}`);
  console.log("floating desktop position checks: PASS");
} finally {
  await rm(output, { recursive: true, force: true });
}
