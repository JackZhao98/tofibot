import { useEffect, useRef, useState, type KeyboardEvent, type MouseEvent, type PointerEvent, type ReactNode } from "react";
import { clampFloatingPosition, dockFloatingPosition, type FloatingPoint } from "./floatingDesktopPosition";

type Drag = { pointerId: number; startX: number; startY: number; origin: FloatingPoint; moved: boolean };

/** The collapsed screen itself is the drag surface; its visible controls remain clickable. */
export function FloatingDesktop({ expanded, children }: { expanded: boolean; children: ReactNode }) {
  const shellRef = useRef<HTMLDivElement>(null);
  const dragRef = useRef<Drag | null>(null);
  const suppressClickRef = useRef(false);
  const suppressTimer = useRef<number | undefined>(undefined);
  const settleTimer = useRef<number | undefined>(undefined);
  const [position, setPosition] = useState<FloatingPoint | null>(null);
  const [dragging, setDragging] = useState(false);
  const [settling, setSettling] = useState(false);

  function dimensions() {
    const rect = shellRef.current?.getBoundingClientRect();
    return rect ? { width: rect.width, height: rect.height } : null;
  }

  function dock(point: FloatingPoint) {
    const size = dimensions();
    if (!size) return point;
    const composerTop = document.querySelector(".composer")?.getBoundingClientRect().top ?? window.innerHeight;
    return dockFloatingPosition(point, size, { width: window.innerWidth, height: window.innerHeight }, composerTop);
  }

  useEffect(() => {
    const keepDocked = () => setPosition(current => current ? dock(current) : null);
    window.addEventListener("resize", keepDocked);
    return () => { window.removeEventListener("resize", keepDocked); window.clearTimeout(settleTimer.current); window.clearTimeout(suppressTimer.current); };
  }, []);

  function onPointerDownCapture(event: PointerEvent<HTMLDivElement>) {
    if (expanded || event.button !== 0 || dragRef.current || !shellRef.current) return;
    if ((event.target as Element).closest(".computer-floating-actions,.remote-keyboard-input")) return;
    const rect = shellRef.current.getBoundingClientRect();
    window.clearTimeout(settleTimer.current);
    setSettling(false);
    dragRef.current = { pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, origin: { left: rect.left, top: rect.top }, moved: false };
  }

  function onPointerMove(event: PointerEvent<HTMLDivElement>) {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId || !shellRef.current || expanded) return;
    const dx = event.clientX - drag.startX, dy = event.clientY - drag.startY;
    if (!drag.moved && Math.hypot(dx, dy) < 5) return;
    if (!drag.moved) {
      drag.moved = true;
      event.currentTarget.setPointerCapture(event.pointerId);
      setDragging(true);
    }
    event.preventDefault();
    const size = dimensions();
    if (size) setPosition(clampFloatingPosition({ left: drag.origin.left + dx, top: drag.origin.top + dy }, size, { width: window.innerWidth, height: window.innerHeight }));
  }

  function onPointerEnd(event: PointerEvent<HTMLDivElement>) {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    dragRef.current = null;
    if (drag.moved) {
      suppressClickRef.current = true;
      window.clearTimeout(suppressTimer.current);
      suppressTimer.current = window.setTimeout(() => { suppressClickRef.current = false; }, 350);
      const rect = shellRef.current?.getBoundingClientRect();
      if (rect) { setSettling(true); setPosition(dock({ left: rect.left, top: rect.top })); }
      settleTimer.current = window.setTimeout(() => setSettling(false), 440);
    }
    setDragging(false);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  }

  function onClickCapture(event: MouseEvent<HTMLDivElement>) {
    if (!suppressClickRef.current) return;
    event.preventDefault(); event.stopPropagation(); suppressClickRef.current = false;
  }

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (expanded || event.target !== event.currentTarget) return;
    if (event.key === "Home" || event.key === "ArrowUp" || event.key === "ArrowDown") {
      event.preventDefault(); setSettling(true);
      setPosition(dock({ left: window.innerWidth, top: event.key === "ArrowUp" ? 0 : window.innerHeight }));
      settleTimer.current = window.setTimeout(() => setSettling(false), 440);
    }
  }

  return <div ref={shellRef} role={!expanded ? "group" : undefined} aria-label={!expanded ? "共享电脑预览：拖动屏幕可吸附右上或右下，方向键上下移动" : undefined} tabIndex={!expanded ? 0 : undefined}
    className={`desktop-floating-shell${expanded ? " is-expanded" : ""}${dragging ? " is-dragging" : ""}${settling ? " is-settling" : ""}`}
    style={position && !expanded ? { left: position.left, top: position.top, right: "auto", bottom: "auto" } : undefined}
    onPointerDownCapture={onPointerDownCapture} onPointerMove={onPointerMove} onPointerUp={onPointerEnd} onPointerCancel={onPointerEnd} onLostPointerCapture={onPointerEnd} onClickCapture={onClickCapture} onKeyDown={onKeyDown}>
    {children}
  </div>;
}
