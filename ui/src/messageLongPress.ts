/**
 * Touch has no action bar: holding a message opens the same actions as a right click. One delegated listener for the whole page,
 * so a row carries nothing for it. The hold is the sidebar's 380 ms; a slide past the slop is a scroll and cancels it, and the
 * browser's own press-to-select is cleared when the menu opens.
 */
const HOLD_MS = 380;
const SLOP = 8;
const SKIP = "a,button,summary,input,textarea,[role=button],.message-hover-actions";

/** Whether text was selected when the last pointer went down: a right click on a selection keeps the browser's own menu (to copy that text),
 * and the press itself must not count, since some browsers select the word under a right click. */
let selectedAtPress = false;
export const hadSelectionAtPress = () => selectedAtPress;

export function attachMessageLongPress(root: Document, open: (messageId: string, x: number, y: number) => void) {
  let timer = 0, x = 0, y = 0, fired = false;
  const cancel = () => { window.clearTimeout(timer); timer = 0; };
  const down = (event: PointerEvent) => {
    selectedAtPress = Boolean(window.getSelection()?.toString());
    if (event.pointerType !== "touch" || !event.isPrimary) return;
    const target = event.target as HTMLElement | null;
    const block = target?.closest?.<HTMLElement>(".message-block[data-actions]");
    if (!block || !target?.closest(".tb-bubble, .tb-msg-attach") || target.closest(SKIP)) return;
    cancel(); fired = false; x = event.clientX; y = event.clientY;
    timer = window.setTimeout(() => {
      timer = 0; fired = true;
      window.getSelection()?.removeAllRanges();
      open(block.dataset.messageId ?? "", x, y);
    }, HOLD_MS);
  };
  const move = (event: PointerEvent) => { if (timer && Math.hypot(event.clientX - x, event.clientY - y) > SLOP) cancel(); };
  const up = () => { cancel(); if (fired) window.setTimeout(() => { fired = false; }, 300); };
  // The lift after a hold must not also press whatever sits under the finger.
  const click = (event: MouseEvent) => { if (fired) { event.preventDefault(); event.stopPropagation(); } };
  const context = (event: Event) => { if (fired) event.preventDefault(); };
  root.addEventListener("pointerdown", down, { passive: true, capture: true });
  root.addEventListener("pointermove", move, { passive: true });
  root.addEventListener("pointerup", up, { passive: true });
  root.addEventListener("pointercancel", up, { passive: true });
  root.addEventListener("click", click, true);
  root.addEventListener("contextmenu", context, true);
  return () => {
    cancel();
    root.removeEventListener("pointerdown", down, true); root.removeEventListener("pointermove", move);
    root.removeEventListener("pointerup", up); root.removeEventListener("pointercancel", up);
    root.removeEventListener("click", click, true); root.removeEventListener("contextmenu", context, true);
  };
}
