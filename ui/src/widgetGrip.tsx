import { createContext, useContext, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";

/**
 * What a widget needs to be moved in the column (docs/design/widgets-and-sidebar.md section 2.1): the grip button's label and the two
 * handlers. A card with a header draws its own grip; the bare computer has no header, so `ContextCard` offers the same grip through this
 * context and the monitor draws it in its hover cluster next to the close button. Tiny on purpose: the contextCard and desktop chunks share it.
 */
export type WidgetGripApi = {
  label: string; hint: string;
  onPointerDown: (event: ReactPointerEvent<HTMLElement>) => void;
  onKeyDown: (event: ReactKeyboardEvent<HTMLElement>) => void;
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
  return <button type="button" className={`${className} widget-grip`} aria-label={grip.label} data-hint={grip.hint} aria-keyshortcuts="ArrowUp ArrowDown Alt+ArrowUp Alt+ArrowDown" onPointerDown={grip.onPointerDown} onKeyDown={grip.onKeyDown}><GripGlyph /></button>;
}
