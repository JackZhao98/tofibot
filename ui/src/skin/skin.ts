// The one place that sets a document's skin and theme (V3 PLAN 7.4). Skins are selected by data-skin on <html>; the
// theme by data-theme. "v3" is the default and the only product skin (skin/v3.css, with skin/roles.css, skin/v2-bridge.css and
// skin/base.css, all in skin/default.css, which every entry loads). The V2 skin ("legacy") was deleted in PR 2.1.
// The two probe skins are for the catalog's sensitivity runs only.
// "System" appearance stays a JS choice (useAppearance), so CSS only ever sees light or dark.

export type SkinId = "v3";
export type ProbeSkinId = "probe-a" | "probe-b";
export type SkinTheme = "light" | "dark";
export const DEFAULT_SKIN: SkinId = "v3";

/** Writes data-skin, data-theme and the matching color-scheme on root (the document element unless given). */
export function applySkin(theme: SkinTheme, skin: SkinId | ProbeSkinId = DEFAULT_SKIN, root: HTMLElement = document.documentElement): void {
  root.dataset.skin = skin;
  root.dataset.theme = theme;
  root.style.colorScheme = theme;
}

/** The skin a dev build asked for with ?skin=v3. Always null in a production build: the check is a compile-time constant.
 *  The sheet is eager, so the attribute is the whole switch. */
export function devSkin(): SkinId | null {
  if (!import.meta.env.DEV) return null;
  const asked = new URLSearchParams(location.search).get("skin");
  return asked === "v3" ? asked : null;
}
