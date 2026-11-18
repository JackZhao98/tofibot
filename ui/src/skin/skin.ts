// The one place that sets a document's skin and theme (V3 PLAN 7.4). Skins are selected by data-skin on <html>; the
// theme by data-theme. "legacy" is V2 (skin/legacy.css, loaded by every entry); "v3" is skin/v3.css, a lazy sheet that
// becomes the default in 1.1. The two probe skins are for the catalog's sensitivity runs only.
// "System" appearance stays a JS choice (useAppearance), so CSS only ever sees light or dark.

export type SkinId = "legacy" | "v3";
export type ProbeSkinId = "probe-a" | "probe-b";
export type SkinTheme = "light" | "dark";
export const DEFAULT_SKIN: SkinId = "legacy";

/** Writes data-skin, data-theme and the matching color-scheme on root (the document element unless given). */
export function applySkin(theme: SkinTheme, skin: SkinId | ProbeSkinId = DEFAULT_SKIN, root: HTMLElement = document.documentElement): void {
  root.dataset.skin = skin;
  root.dataset.theme = theme;
  root.style.colorScheme = theme;
}

/** The skin a dev build asked for with ?skin=v3. Always null in a production build: the check is a compile-time constant,
 *  so the skin sheets are not reachable from the product bundle. */
export function devSkin(): SkinId | null {
  if (!import.meta.env.DEV) return null;
  const asked = new URLSearchParams(location.search).get("skin");
  return asked === "v3" ? asked : null;
}

/** Loads the lazy sheets of a non-default skin (dev builds only; the catalog imports skin/sheets directly). */
export async function loadDevSkin(skin: SkinId): Promise<void> {
  if (!import.meta.env.DEV) return;
  const { loadSkin } = await import("./sheets");
  await loadSkin(skin);
}
