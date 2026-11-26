// A Vite dev server for a test gets its own dependency cache. By default every server in this repo writes to ui/node_modules/.vite, so suites that run
// side by side (the browser queue runs five at once) re-optimize dependencies under each other: a "failed dynamic import" or two copies of React in a test
// that touched nothing. Pass the result as `cacheDir` of createServer(...); the directory is removed when the process exits.
import {mkdtempSync, rmSync} from "node:fs";
import {tmpdir} from "node:os";
import {join} from "node:path";

export function viteCacheDir(label = "ui") {
  const dir = mkdtempSync(join(tmpdir(), `tofi-${label}-vite-`));
  process.on("exit", () => { try { rmSync(dir, {recursive: true, force: true}); } catch { /* best effort */ } });
  return dir;
}
