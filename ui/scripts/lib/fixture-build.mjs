// Builds a test fixture once and serves the built files, instead of serving it from a Vite dev server.
// A dev server hands the browser every module on its own request (and transforms each on first use); a fresh browser
// context, which every page of these suites opens, pays that again. The built fixture is a handful of files.
// The fixture folder (inside ui/) holds index.html and main.tsx; it may import from ../src like the dev fixture did. With
// `app: true` the app's own index.html is built too and served at "/". The build uses the product's own plugins (React and
// the CSS layer order) with ui/ as its root, so public files are served as the dev server served them.
import {mkdtemp, rm} from "node:fs/promises";
import {tmpdir} from "node:os";
import {basename, join} from "node:path";

/** Returns {origin, path, close}: the origin serving the build, the fixture page's path on it, and a function that stops it and deletes the build. */
export async function buildAndServeFixture(ui, fixture, {app = false} = {}) {
  const {build, preview} = await import("vite");
  const {default: react} = await import("@vitejs/plugin-react");
  const {cssLayerOrderPlugin} = await import("../../cssLayerOrderPlugin.ts");
  const outDir = await mkdtemp(join(tmpdir(), "tofi-fixture-dist-"));
  // vite build sets NODE_ENV=production for the whole process; put it back so nothing after the build changes behaviour.
  const nodeEnv = process.env.NODE_ENV;
  try {
    const input = {fixture: join(fixture, "index.html"), ...(app ? {app: join(ui, "index.html")} : {})};
    await build({configFile: false, root: ui, plugins: [react(), cssLayerOrderPlugin()], logLevel: "error", build: {outDir, emptyOutDir: true, chunkSizeWarningLimit: 100_000, rolldownOptions: {input}}});
  } catch (error) {
    await rm(outDir, {recursive: true, force: true});
    throw error;
  } finally {
    if (nodeEnv === undefined) delete process.env.NODE_ENV; else process.env.NODE_ENV = nodeEnv;
  }
  // mpa: an unknown path is a 404, never index.html.
  const server = await preview({configFile: false, root: ui, appType: "mpa", build: {outDir}, preview: {host: "127.0.0.1", port: 0}, logLevel: "error"});
  return {
    origin: `http://127.0.0.1:${server.httpServer.address().port}`,
    path: `/${basename(fixture)}/index.html`,
    close: async () => { await new Promise(done => server.httpServer.close(done)); await rm(outDir, {recursive: true, force: true}); },
  };
}
