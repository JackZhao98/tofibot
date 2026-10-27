/**
 * State of the two right-hand layers (docs/design/side-panels.md).
 * Outside panel: one view at a time (`panel`); opening another replaces it. `file` carries the file the "file"
 * view shows. Side panel: only a per-viewer "hidden" preference, kept in localStorage.
 */
export type PanelState<P extends string, F = unknown> = { panel: P | null; file: F | null };
export const emptyPanelState = <P extends string, F = unknown>(): PanelState<P, F> => ({ panel: null, file: null });

/** Opens `next` in the outside panel (replacing the current view); null closes it. */
export function openPanel<P extends string, F>(state: PanelState<P, F>, next: P | null): PanelState<P, F> {
  if (next === state.panel) return state;
  return { panel: next, file: next === "file" ? state.file : null };
}
/** Opens the "file" view for `file`. */
export const openFileView = <P extends string, F>(file: F): PanelState<P | "file", F> => ({ panel: "file", file });
/** Closes the outside panel when it shows `id`. */
export const closePanel = <P extends string, F>(state: PanelState<P, F>, id: string): PanelState<P, F> => state.panel === id ? emptyPanelState<P, F>() : state;

const HIDDEN_KEY = "tofi.sidePanel.hidden";
export function readSidePanelHidden(): boolean {
  try { return localStorage.getItem(HIDDEN_KEY) === "1"; } catch { return false; }
}
export function writeSidePanelHidden(hidden: boolean) {
  try { localStorage.setItem(HIDDEN_KEY, hidden ? "1" : "0"); } catch { /* the preference only lasts for this page */ }
}
