import { useEffect, useEffectEvent, useLayoutEffect, useState, type RefObject } from "react";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";

/**
 * A window-level drop target; files are only added to the current draft. While files are dragged over the
 * window the chat stays where it is: a soft scrim dims the pane and the composer lifts and shows "Drop to add"
 * (state: `data-file-drag` on the composer form and its chat pane, styled in web-file-drop.css). A drop anywhere adds.
 */
export function WebFileDropOverlay({ inputRef, enabled, onDrop }: {
  inputRef: RefObject<HTMLTextAreaElement | null>;
  enabled: boolean;
  onDrop: (files: FileList, origin: { x: number; y: number }) => void;
}) {
  const { t } = useTranslation("chat");
  const [dragging, setDragging] = useState(false);
  const receiveFiles = useEffectEvent(onDrop);

  useEffect(() => {
    setDragging(false);
    if (!enabled) return;
    // dragenter/dragleave fire for every element the pointer crosses: count them instead of trusting one leave.
    let depth = 0;
    const clear = () => { depth = 0; setDragging(false); };
    const available = () => {
      const input = inputRef.current;
      return input && !input.disabled && !input.closest("[inert]") && input.getClientRects().length > 0;
    };
    const hasFiles = (event: DragEvent) => event.dataTransfer?.types.includes("Files");
    const enter = (event: DragEvent) => {
      if (!hasFiles(event) || !available() || event.defaultPrevented) return;
      depth += 1;
      event.preventDefault();
      setDragging(true);
    };
    const over = (event: DragEvent) => {
      if (!hasFiles(event) || event.defaultPrevented) return;
      if (!available()) { clear(); return; }
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
      setDragging(true);
    };
    const leave = (event: DragEvent) => {
      depth = Math.max(0, depth - 1);
      // Counting is the rule; a pointer reported beyond the window edge (some engines skip the last leave) ends it too.
      const beyond = event.clientX < 0 || event.clientY < 0 || event.clientX > window.innerWidth || event.clientY > window.innerHeight;
      if (depth === 0 || beyond) clear();
    };
    const drop = (event: DragEvent) => {
      clear();
      if (!event.dataTransfer?.files.length || !available() || event.defaultPrevented) return;
      event.preventDefault();
      receiveFiles(event.dataTransfer.files, { x: event.clientX, y: event.clientY });
      inputRef.current?.focus();
    };
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") clear(); };
    window.addEventListener("dragenter", enter);
    window.addEventListener("dragover", over);
    window.addEventListener("dragleave", leave);
    window.addEventListener("drop", drop);
    window.addEventListener("dragend", clear);
    window.addEventListener("blur", clear);
    window.addEventListener("keydown", escape);
    return () => {
      window.removeEventListener("dragenter", enter);
      window.removeEventListener("dragover", over);
      window.removeEventListener("dragleave", leave);
      window.removeEventListener("drop", drop);
      window.removeEventListener("dragend", clear);
      window.removeEventListener("blur", clear);
      window.removeEventListener("keydown", escape);
    };
  }, [enabled, inputRef]);

  const active = enabled && dragging;
  // The composer form and the pane own the visual state; React does not manage these attributes, so no re-render of the Composer.
  useLayoutEffect(() => {
    const form = inputRef.current?.closest<HTMLElement>("form.composer");
    const pane = form?.closest<HTMLElement>(".chat-pane");
    for (const node of [form, pane]) { if (!node) continue; if (active) node.dataset.fileDrag = "on"; else delete node.dataset.fileDrag; }
    return () => { for (const node of [form, pane]) if (node) delete node.dataset.fileDrag; };
  }, [active, inputRef]);

  return <div className="composer-drop" aria-hidden={!active} role={active ? "status" : undefined}>
    <span className="composer-drop-icon"><TofiIcon name="upload" size={20} /></span>
    <span className="composer-drop-text">{t("composer.drop_add")}</span>
  </div>;
}
