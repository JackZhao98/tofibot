import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import type { Plugin } from "vite";

const cascade = fileURLToPath(new URL("./src/cascade.css", import.meta.url));
const source = fileURLToPath(new URL("./src/", import.meta.url));

/** The first sheet to name a cascade layer fixes its rank, and Vite orders CSS by module and chunk, so a component's
 *  sheet can reach the page before cascade.css. Every layered source sheet therefore restates the order line. */
export function cssLayerOrderPlugin(): Plugin {
  const order = readFileSync(cascade, "utf8").replace(/\/\*[\s\S]*?\*\//g, "").match(/@layer [^;]+;/)?.[0];
  if (!order) throw new Error("src/cascade.css has no @layer order line");
  return {
    name: "tofi-css-layer-order",
    enforce: "pre",
    transform(code, id) {
      if (!id.startsWith(source) || !id.endsWith(".css") || id === cascade || !/@layer\s|\slayer\(/.test(code)) return null;
      return { code: `${order}\n${code}`, map: null };
    },
  };
}
