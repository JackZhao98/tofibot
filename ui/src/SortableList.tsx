import { useEffect, useRef, useSyncExternalStore, type CSSProperties, type KeyboardEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { closestCenter, DndContext, KeyboardCode, type CollisionDetection, KeyboardSensor, MouseSensor, TouchSensor, useSensor, useSensors } from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { restrictToParentElement, restrictToVerticalAxis } from "@dnd-kit/modifiers";
import { layerRoot } from "./layers";
import { EASE, MOTION } from "./motion";

const modifiers = [restrictToVerticalAxis, restrictToParentElement];
// A pointer drag is over the card its middle is nearest (the design's "middle crosses middle"). A keyboard step lines the
// card up with the next one: tops when it moves up, bottoms when it moves down. Cards differ a lot in height (Usage is
// several times Artifacts), so for the keyboard the slot with the nearest top or bottom edge is the one it is over.
const nearestEdge: CollisionDetection = ({ collisionRect, droppableContainers, droppableRects }) => droppableContainers
  .flatMap(container => { const rect = droppableRects.get(container.id); return rect ? [{ id: container.id, data: { droppableContainer: container, value: Math.min(Math.abs(rect.top - collisionRect.top), Math.abs(rect.bottom - collisionRect.bottom)) } }] : []; })
  .sort((a, b) => a.data.value - b.data.value);
const noLayoutAnimation = () => false;
const reducedQuery = () => matchMedia("(prefers-reduced-motion: reduce)");
const watchReduced = (change: () => void) => {
  const query = reducedQuery();
  query.addEventListener("change", change);
  return () => query.removeEventListener("change", change);
};
const isReduced = () => reducedQuery().matches;

/** Shared only by lazy panels. The library owns activation, displacement, scrolling and cancellation; callers own order. */
export function SortableList({ items, onMove, announce, instructions, onDragging, children }: {
  items: string[]; onMove: (id: string, to: number) => void; announce: (id: string, index: number) => string;
  instructions: string; onDragging?: (dragging: boolean) => void; children: ReactNode;
}) {
  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 6 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 400, tolerance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates,
      keyboardCodes: { start: [KeyboardCode.Space], cancel: [KeyboardCode.Esc], end: [KeyboardCode.Space, KeyboardCode.Enter, KeyboardCode.Tab] } }),
  );
  useEffect(() => {
    // Claim Escape before surface dismissal listeners run. Both library sensors still receive it and own cancellation.
    const claimEscape = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape" && document.documentElement.hasAttribute("data-sortable-dragging")) event.preventDefault();
    };
    document.addEventListener("keydown", claimEscape, true);
    return () => {
      document.removeEventListener("keydown", claimEscape, true);
      document.documentElement.removeAttribute("data-sortable-dragging");
      onDragging?.(false);
    };
  }, [onDragging]);
  // Which kind of drag this is, set by the event that started it.
  const byKeyboard = useRef(false);
  const collision: CollisionDetection = args => byKeyboard.current ? nearestEdge(args) : closestCenter(args);
  const finish = () => { document.documentElement.removeAttribute("data-sortable-dragging"); onDragging?.(false); };
  return <DndContext sensors={sensors} collisionDetection={collision} modifiers={modifiers}
    autoScroll={{ threshold: { x: 0, y: 0.1 } }}
    accessibility={{ container: layerRoot(), screenReaderInstructions: { draggable: instructions }, announcements: {
      onDragStart: ({ active }) => announce(String(active.id), items.indexOf(String(active.id))),
      onDragOver: ({ active, over }) => over ? announce(String(active.id), items.indexOf(String(over.id))) : undefined,
      onDragEnd: ({ active, over }) => announce(String(active.id), items.indexOf(String(over?.id ?? active.id))),
      onDragCancel: ({ active }) => announce(String(active.id), items.indexOf(String(active.id))),
    } }}
    onDragStart={({ activatorEvent }) => { byKeyboard.current = activatorEvent instanceof KeyboardEvent; document.documentElement.setAttribute("data-sortable-dragging", ""); onDragging?.(true); }}
    onDragCancel={finish} onDragEnd={({ active, over }) => {
      finish();
      if (over && active.id !== over.id) onMove(String(active.id), items.indexOf(String(over.id)));
    }}>
    <SortableContext items={items} strategy={verticalListSortingStrategy}>{children}</SortableContext>
  </DndContext>;
}

export function SortableStatus({ children }: { children: ReactNode }) {
  return createPortal(<span className="sr-only" role="status" aria-live="polite">{children}</span>, layerRoot());
}

export function useSortableItem(id: string, disabled: boolean, moveStep: (step: -1 | 1) => void, dropDisabled = disabled) {
  const reduced = useSyncExternalStore(watchReduced, isReduced, () => false);
  const sortable = useSortable({ id, disabled: { draggable: disabled, droppable: dropDisabled }, animateLayoutChanges: noLayoutAnimation,
    transition: { duration: reduced ? 0 : MOTION.base, easing: EASE.settle } });
  const draggedUntil = useRef(0);
  useEffect(() => {
    if (sortable.isDragging) return () => { draggedUntil.current = Date.now() + 400; };
  }, [sortable.isDragging]);
  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (!disabled && event.altKey && !sortable.active && (event.key === "ArrowUp" || event.key === "ArrowDown")) {
      event.preventDefault(); event.stopPropagation();
      moveStep(event.key === "ArrowUp" ? -1 : 1);
    } else sortable.listeners?.onKeyDown?.(event);
  };
  const transform = sortable.transform;
  const style: CSSProperties = {
    transform: transform ? `translate3d(${transform.x}px, ${transform.y}px, 0)` : undefined,
    transition: reduced ? "none" : sortable.transition ?? (sortable.isDragging ? "none" : `transform ${MOTION.base}ms ${EASE.settle}`),
  };
  return { ...sortable, style, activator: { ...sortable.listeners, onKeyDown },
    wasDragged: () => sortable.isDragging || Date.now() < draggedUntil.current };
}
