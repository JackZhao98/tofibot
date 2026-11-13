import { createContext, useContext, type DOMAttributes, type RefCallback } from "react";

/** Lightweight context shared by the lazy column and computer screen. dnd-kit lives in SortableList. */
export type WidgetGripApi = {
  label: string; hint: string;
  ref: RefCallback<HTMLElement>;
  attributes: import("@dnd-kit/core").DraggableAttributes;
  activator: Pick<DOMAttributes<HTMLElement>, "onMouseDown" | "onTouchStart" | "onKeyDown">;
  wasDragged: () => boolean;
};

export const WidgetGripContext = createContext<WidgetGripApi | null>(null);
export const useWidgetGrip = () => useContext(WidgetGripContext);

/** Six dots in two columns: the handle shape people already read as "move". */
export function GripGlyph({ size = 16 }: { size?: number }) {
  return <svg width={size} height={size} viewBox="0 0 16 16" fill="currentColor" aria-hidden="true" focusable="false">
    {[4, 8, 12].map(y => [6, 10].map(x => <circle key={`${x}-${y}`} cx={x} cy={y} r="1.1" />))}
  </svg>;
}

/** The grip button; the same markup in a card header and in the computer's cluster, styled by the container. */
export function WidgetGrip({ grip, className }: { grip: WidgetGripApi; className: string }) {
  return <button ref={grip.ref} type="button" className={`${className} widget-grip`} aria-label={grip.label} data-hint={grip.hint} aria-keyshortcuts="Space Alt+ArrowUp Alt+ArrowDown" {...grip.attributes} {...grip.activator}><GripGlyph /></button>;
}
