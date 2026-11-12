// Standalone entry point: build the real App and own the disposable Go fixture.
// An externally managed manifest (ui-suites-all.sh) keeps its existing lifecycle.
import {spawn} from "node:child_process";
import {mkdtemp, readFile, rm} from "node:fs/promises";
import {tmpdir} from "node:os";
import {dirname, join, resolve} from "node:path";
import {fileURLToPath} from "node:url";

const ui = resolve(dirname(fileURLToPath(import.meta.url)), "..");
function start(command, args, options = {}) {
  const child = spawn(command, args, {cwd: ui, stdio: "inherit", ...options});
  const done = new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (code, signal) => code === 0 ? resolve() : reject(new Error(`${command} exited ${code ?? signal}`)));
  });
  // The readiness promise may still be waiting when a fixture fails.
  done.catch(() => {});
  return {child, done};
}

if (process.env.TOFI_ROUTE_BROWSER_MANIFEST) {
  await import("./test-conversation-route-browser.mjs");
} else {
  await start("npm", ["run", "-s", "build"]).done;
  const directory = await mkdtemp(join(tmpdir(), "tofi-route-browser-"));
  const manifest = join(directory, "manifest.json");
  const env = {...process.env, TOFI_ROUTE_BROWSER_MANIFEST: manifest};
  const {child, done} = start("go", ["test", "./internal/app", "-run", "^TestConversationRouteBrowserFixture$", "-count=1", "-v"], {
    cwd: resolve(ui, ".."), env, stdio: ["ignore", "pipe", "inherit"], detached: true,
  });
  let origin;
  try {
    // Go logs readiness only after writing the complete manifest. No polling or settle delay.
    await new Promise((resolve, reject) => {
      let output = "";
      child.stdout.on("data", chunk => {
        process.stdout.write(chunk);
        output += chunk;
        if (output.includes("synthetic conversation route browser fixture:")) resolve();
      });
      done.then(() => reject(new Error("route fixture exited before readiness")), reject);
    });
    ({origin} = JSON.parse(await readFile(manifest, "utf8")));
    process.env.TOFI_ROUTE_BROWSER_MANIFEST = manifest;
    await import("./test-conversation-route-browser.mjs");
  } finally {
    try {
      if (origin) {
        const response = await fetch(origin + "/acceptance/stop", {method: "POST"});
        if (!response.ok) throw new Error(`route fixture stop: HTTP ${response.status}`);
        await done;
      }
    } finally {
      // Also clean up go test and its child if startup or the stop request failed.
      if (child.exitCode === null) {
        try { process.kill(-child.pid, "SIGTERM"); } catch (error) { if (error.code !== "ESRCH") throw error; }
      }
      await rm(directory, {recursive: true, force: true});
    }
  }
}
