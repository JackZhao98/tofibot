import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import type { Plugin } from "vite";

const structure = fileURLToPath(new URL("./src/skin/structure.css", import.meta.url));
const skin = fileURLToPath(new URL("./src/skin/legacy.css", import.meta.url));
const styles = fileURLToPath(new URL("./src/oauth/connection.css", import.meta.url));
const script = fileURLToPath(new URL("./src/oauth/connection.js", import.meta.url));

/** Stable callback assets, built from the same structure tokens and skin as the SPA. The page has no cascade.css: the
 *  first @layer statement here (tokens, then skin) gives the same order as the app. */
export function oauthAssetsPlugin(): Plugin {
  const assets = () => new Map([
    ["/oauth/connection.css", { type: "text/css; charset=utf-8", source: readFileSync(structure, "utf8") + "\n" + readFileSync(skin, "utf8") + "\n" + readFileSync(styles, "utf8") }],
    ["/oauth/connection.js", { type: "text/javascript; charset=utf-8", source: readFileSync(script, "utf8") }],
  ]);
  return {
    name: "tofi-oauth-assets",
    buildStart() { for (const file of [structure, skin, styles, script]) this.addWatchFile(file); },
    generateBundle() {
      for (const [url, asset] of assets()) this.emitFile({ type: "asset", fileName: url.slice(1), source: asset.source });
    },
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const asset = assets().get((req.url || "").split("?")[0]);
        if (!asset) return next();
        res.setHeader("Content-Type", asset.type);
        res.setHeader("Cache-Control", "no-store");
        res.setHeader("X-Content-Type-Options", "nosniff");
        res.end(asset.source);
      });
    },
  };
}
