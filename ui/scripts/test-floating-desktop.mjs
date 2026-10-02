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
  await run(join(root, "node_modules/.bin/tsc"), ["src/floatingDesktopPosition.ts", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--moduleResolution", "Bundler", "--outDir", output, "--skipLibCheck", "--pretty", "false"], { cwd: root });
  const { clampFloatingPosition, dockFloatingPosition } = await import(pathToFileURL(join(output, "floatingDesktopPosition.js")));
  const size = { width: 360, height: 210 };
  const viewport = { width: 1440, height: 900 };
  const top = dockFloatingPosition({ left: 400, top: 70 }, size, viewport, 790);
  const bottom = dockFloatingPosition({ left: 300, top: 650 }, size, viewport, 790);
  if (top.left !== 1060 || top.top !== 20) throw new Error(`top docking failed: ${JSON.stringify(top)}`);
  if (bottom.left !== 1060 || bottom.top !== 564) throw new Error(`composer docking failed: ${JSON.stringify(bottom)}`);
  const clamped = clampFloatingPosition({ left: -200, top: 1200 }, size, viewport);
  if (clamped.left !== 20 || clamped.top !== 670) throw new Error(`clamping failed: ${JSON.stringify(clamped)}`);
  console.log("floating desktop position checks: PASS");
} finally {
  await rm(output, { recursive: true, force: true });
}
