import { useSyncExternalStore, type ReactNode } from "react";
import { createPortal } from "react-dom";

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

/** The topmost open modal <dialog>, if any. `showModal()` puts a dialog in the browser's top layer, above every z-index,
 * so a tooltip or toast in #tofi-layers would sit under it (tokens.md Z7): they render inside it instead. Two native
 * dialogs remain after migration step 3, the image lightbox and the file preview. */
export function topLayerDialog(): HTMLElement | null {
  const open = document.querySelectorAll<HTMLDialogElement>("dialog[open]");
  for (let i = open.length - 1; i >= 0; i--) {
    try { if (open[i].matches(":modal")) return open[i]; } catch { return open[i]; }
  }
  return null;
}

// A dialog closes by losing `open`, or by being removed while open (its owner unmounted).
function watchDialogs(change: () => void) {
  const observer = new MutationObserver(change);
  observer.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ["open"] });
  return () => observer.disconnect();
}

/** Renders its children in place, or inside the top-layer dialog while one is open. Watches the DOM only while mounted,
 * so it costs nothing unless the toast it wraps is showing. */
export function InTopLayer({ children }: { children: ReactNode }) {
  const host = useSyncExternalStore(watchDialogs, topLayerDialog, () => null);
  return host ? createPortal(children, host) : children;
}
