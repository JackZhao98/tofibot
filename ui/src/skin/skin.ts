// The one place that sets a document's skin and theme (V3 PLAN 7.4). Skins are selected by data-skin on <html>; the
// theme by data-theme. "v3" is the default (skin/v3.css, with skin/roles.css, skin/v2-bridge.css and skin/base.css);
// "legacy" is V2 (skin/legacy.css). Both are in skin/default.css, which every entry loads. Setting DEFAULT_SKIN back to
// "legacy" is the rollback until PR 2.1 deletes legacy.css. The two probe skins are for the catalog's sensitivity runs only.
// "System" appearance stays a JS choice (useAppearance), so CSS only ever sees light or dark.

export type SkinId = "legacy" | "v3";
export type ProbeSkinId = "probe-a" | "probe-b";
export type SkinTheme = "light" | "dark";
export const DEFAULT_SKIN: SkinId = "v3";

/** Writes data-skin, data-theme and the matching color-scheme on root (the document element unless given). */
export function applySkin(theme: SkinTheme, skin: SkinId | ProbeSkinId = DEFAULT_SKIN, root: HTMLElement = document.documentElement): void {
  root.dataset.skin = skin;
  root.dataset.theme = theme;
  root.style.colorScheme = theme;
}

/** The skin a dev build asked for with ?skin=legacy (V2, until PR 2.1) or ?skin=v3. Always null in a production build: the
 *  check is a compile-time constant. Both sheets are eager, so the attribute is the whole switch. */
export function devSkin(): SkinId | null {
  if (!import.meta.env.DEV) return null;
  const asked = new URLSearchParams(location.search).get("skin");
  return asked === "legacy" || asked === "v3" ? asked : null;
}
