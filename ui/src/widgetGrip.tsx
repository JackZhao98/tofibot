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
