// The one place that sets a document's skin and theme (V3 PLAN 7.4). Skins are selected by data-skin on <html>; the
// theme by data-theme. "legacy" is V2 (skin/legacy.css); "v3" arrives in V3 PR 0.4 and becomes the default in 1.1.
// "System" appearance stays a JS choice (useAppearance), so CSS only ever sees light or dark.

export type SkinId = "legacy";
export type SkinTheme = "light" | "dark";
export const DEFAULT_SKIN: SkinId = "legacy";

/** Writes data-skin, data-theme and the matching color-scheme on root (the document element unless given). */
export function applySkin(theme: SkinTheme, skin: SkinId = DEFAULT_SKIN, root: HTMLElement = document.documentElement): void {
  root.dataset.skin = skin;
  root.dataset.theme = theme;
  root.style.colorScheme = theme;
}
