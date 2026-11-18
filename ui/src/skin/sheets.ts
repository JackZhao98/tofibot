// The lazy skin sheets (V3 PLAN 7.4). The default skin is eager (skin/default.css, every entry), so only the probe
// skins are here. A skin is one <link> to its generated sheet; applySkin flips data-skin after loadSkin resolves, so the
// restyle is one frame. Only the catalog imports this file, so a production entry never reaches these sheets.
import probeA from "./probe-a.css?url";
import probeB from "./probe-b.css?url";
import type { ProbeSkinId, SkinId } from "./skin";

const SHEETS: Record<ProbeSkinId, string[]> = { "probe-a": [probeA], "probe-b": [probeB] };
const loading = new Map<string, Promise<void>>();

/** Resolves when the skin's stylesheet is in the document. The default skin is already there. */
export function loadSkin(skin: SkinId | ProbeSkinId): Promise<void> {
  if (skin === "v3") return Promise.resolve();
  const known = loading.get(skin);
  if (known) return known;
  const links = SHEETS[skin].map(href => {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = href;
    link.dataset.skinSheet = skin;
    return link;
  });
  const done = Promise.all(links.map(link => new Promise<void>((resolve, reject) => {
    link.addEventListener("load", () => resolve(), { once: true });
    link.addEventListener("error", () => reject(new Error(`skin ${skin}: its stylesheet did not load`)), { once: true });
  }))).then(() => undefined, error => { links.forEach(link => link.remove()); loading.delete(skin); throw error; });
  document.head.append(...links);
  loading.set(skin, done);
  return done;
}

/** A skin name from the address bar, or null when it names none. The catalog accepts all of them. */
export function skinFromParam(value: string | null): SkinId | ProbeSkinId | null {
  return value === "v3" || value === "probe-a" || value === "probe-b" ? value : null;
}
