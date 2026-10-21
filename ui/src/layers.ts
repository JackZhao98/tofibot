// The JS mirror of the layer scale in design-tokens.css (--z-* tokens, docs/design/tokens.md §9.3) and the one portal
// root. scripts/test-layers.mjs fails if the two drift apart, if TS code writes a literal z-index or if an overlay is
// portaled straight into document.body.
export const Z = { base: 0, raised: 1, edgeBlur: 10, chrome: 20, pill: 30, panel: 40, popover: 50, menu: 60, cover: 70, dialog: 80, toast: 90, tooltip: 100, drag: 110 } as const;

const LAYER_ROOT_ID = "tofi-layers";

/** The element every overlay portals into: a plain <div id="tofi-layers"> after #root in index.html. It is not a
 * stacking context (no z-index, isolation, transform, filter or opacity), so each overlay's own --z-* layer competes
 * with the boot screen and the app chrome in the root stacking context. Created on first use where a page has none. */
export function layerRoot(): HTMLElement {
  const existing = document.getElementById(LAYER_ROOT_ID);
  if (existing) return existing;
  const root = document.createElement("div");
  root.id = LAYER_ROOT_ID;
  document.body.append(root);
  return root;
}
