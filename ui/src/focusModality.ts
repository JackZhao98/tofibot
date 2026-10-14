/**
 * Input modality for focus rings. A ring is drawn only after keyboard navigation.
 * `<html data-input>` is "pointer" until a navigation key is pressed and returns to "pointer" on any mouse, pen or
 * touch press. focus-ring.css hides every ring while it reads "pointer", so a click, a tap, a dialog opened by one,
 * or script focus that follows one never frames the control, whatever the browser's own :focus-visible heuristic says.
 * Escape and modifier shortcuts do not flip it, so closing a pointer-opened dialog returns focus quietly.
 */
type Modality = "pointer" | "keyboard";
const QUIET_KEYS = new Set(["Escape", "Shift", "Control", "Alt", "Meta", "CapsLock", "OS", "ContextMenu"]);
let installed = false;

export function installFocusModality(root: Document = document) {
  if (installed || typeof root === "undefined") return;
  installed = true;
  const html = root.documentElement;
  const set = (value: Modality) => { if (html.dataset.input !== value) html.dataset.input = value; };
  set("pointer");
  for (const type of ["pointerdown", "mousedown", "touchstart"]) root.addEventListener(type, () => set("pointer"), { capture: true, passive: true });
  root.addEventListener("keydown", event => {
    if (QUIET_KEYS.has(event.key) || event.ctrlKey || event.metaKey || event.altKey) return;
    set("keyboard");
  }, { capture: true, passive: true });
}
