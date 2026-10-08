import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";

export const uiRoot = dirname(dirname(fileURLToPath(import.meta.url)));

/**
 * Load ui/src modules through Vite's SSR loader so they resolve exactly like the
 * app: i18n catalogs (JSON, import.meta.glob), TSX and package imports. Prefer
 * this over compiling single files with `tsc --ignoreConfig` into a temp dir,
 * which cannot resolve packages or catalogs.
 *
 * The UI language is pinned (default zh-CN, the shipped copy most assertions use).
 * Call `close()` in a finally block.
 */
export async function openUiModules({ language = "zh-CN" } = {}) {
  const server = await createServer({ configFile: false, root: uiRoot, server: { middlewareMode: true, hmr: false, ws: false }, logLevel: "error" });
  try {
    const i18n = await server.ssrLoadModule("/src/i18n/index.ts");
    await i18n.i18nReady;
    // English stays loaded as well, for cases that pin locale "en".
    await i18n.loadLanguage("en");
    await i18n.setLanguage(language);
    return { load: path => server.ssrLoadModule(path), setLanguage: next => i18n.setLanguage(next), close: () => server.close() };
  } catch (cause) {
    await server.close();
    throw cause;
  }
}
