import { useEffect, useEffectEvent, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { TofiIcon } from "./icons";

/** A window-level drop target; files are only added to the current draft. */
export function WebFileDropOverlay({ inputRef, enabled, onDrop }: {
  inputRef: RefObject<HTMLTextAreaElement | null>;
  enabled: boolean;
  onDrop: (files: FileList, origin: { x: number; y: number }) => void;
}) {
  const [dragging, setDragging] = useState(false);
  const receiveFiles = useEffectEvent(onDrop);

  useEffect(() => {
    setDragging(false);
    if (!enabled) return;
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
    const leave = () => {
      depth = Math.max(0, depth - 1);
      if (depth === 0) setDragging(false);
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

  // A body portal escapes the composer's blur and the chat container's bounds.
  return createPortal(
    <div className={`web-drop-overlay${enabled && dragging ? " is-dragging" : ""}`} aria-hidden={!enabled || !dragging}>
      <div className="web-drop-zone" role="status">
        <span className="web-drop-arrow"><TofiIcon name="upload" size={26} /></span>
        <span>松手，添加到消息</span>
      </div>
    </div>,
    document.body,
  );
}
