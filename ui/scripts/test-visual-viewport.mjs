import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const out = await mkdtemp(join(tmpdir(), "tofi-visual-viewport-"));

try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), [
    "src/visualViewport.ts", "--ignoreConfig", "--target", "ES2022",
    "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", out,
    "--skipLibCheck", "--declaration", "false", "--pretty", "false",
  ], { cwd: root });
  const { observeVisualViewport, visualViewportMetrics } = await import(pathToFileURL(join(out, "visualViewport.js")));
  const sample = (visualHeight, offsetTop, wasKeyboardOpen = false, scale = 1, layoutHeight = 1024) => visualViewportMetrics({ layoutHeight, visualHeight, offsetTop, wasKeyboardOpen, scale });

  assert.deepEqual(sample(620, 0), { ignored: false, keyboardOpen: true, height: 620 }, "portrait keyboard at offset 0");
  assert.deepEqual(sample(620, 100), { ignored: false, keyboardOpen: true, height: 720 }, "keyboard height must include the visual viewport offset");
  assert.equal(sample(1024, 200).keyboardOpen, false, "ordinary page scroll must not look like a keyboard");
  const panned = sample(620, 404, true);
  assert.deepEqual(panned, { ignored: false, keyboardOpen: true, height: 1024 }, "panning to the layout bottom must retain keyboard state");
  assert.deepEqual(sample(1024, 0, true), { ignored: false, keyboardOpen: false, height: undefined }, "keyboard close restores CSS dvh handling");
  assert.deepEqual(sample(768, 0, false, 1, 768), { ignored: false, keyboardOpen: false, height: undefined }, "orientation change while closed");
  assert.deepEqual(sample(540, 0, false, 1.2), { ignored: true, keyboardOpen: false }, "pinch zoom does not rewrite app height");
  assert.deepEqual(sample(540, 80, true, 1.2), { ignored: true, keyboardOpen: true }, "pinch zoom preserves the current keyboard state");

  class Target {
    listeners = new Map();
    addEventListener(type, listener) { this.listeners.set(type, listener); }
    removeEventListener(type, listener) { if (this.listeners.get(type) === listener) this.listeners.delete(type); }
    dispatch(type) { this.listeners.get(type)?.(new Event(type)); }
  }
  const visual = new Target();
  const windowTarget = new Target();
  let events = 0;
  const stop = observeVisualViewport(visual, windowTarget, () => { events += 1; });
  visual.dispatch("resize");
  visual.dispatch("scroll");
  windowTarget.dispatch("resize");
  assert.equal(events, 3, "resize and visual scroll events must resync the viewport");
  stop();
  visual.dispatch("resize");
  visual.dispatch("scroll");
  windowTarget.dispatch("resize");
  assert.equal(events, 3, "cleanup must remove every viewport listener");
  console.log("visual viewport checks: PASS (offset anchoring, panning hysteresis, close/orientation, pinch guard, events/cleanup)");
} finally {
  await rm(out, { recursive: true, force: true });
}
