// Bundles pure TypeScript modules for a Node test: extensionless relative imports resolve like the app's own build, no DOM needed.
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

/** Returns one module namespace with the exports of every entry (paths are relative to the ui folder). */
export async function bundleModules(root, entries) {
  const { rolldown } = await import("rolldown");
  const dir = await mkdtemp(join(tmpdir(), "tofi-bundle-"));
  try {
    const entry = join(dir, "entry.ts");
    await writeFile(entry, entries.map(path => `export * from ${JSON.stringify(resolve(root, path))};`).join("\n"));
    const bundle = await rolldown({ input: entry, logLevel: "silent" });
    await bundle.write({ file: join(dir, "out.mjs"), format: "esm" });
    return await import(pathToFileURL(join(dir, "out.mjs")));
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}
