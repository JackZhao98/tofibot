import { useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { useTranslation } from "./i18n";
import { MOTION, EASE } from "./motion";
import "./desktop-shell.css";

/** Docks the small screen in the right gutter (the viewport edge, past the message column), above the composer and its perched cat. */
function useComposerAnchor(active: boolean) {
  const [style, setStyle] = useState<CSSProperties>();
  useLayoutEffect(() => {
    if (!active) return;
    // .composer spans the column; .composer-row is the visible bordered box. A notice (e.g. "Connect a model")
    // can sit above the row inside .composer, so the top edge is the highest visible child, not the row.
    const parts = () => {
      const container = document.querySelector<HTMLElement>(".composer");
      const row = container?.querySelector<HTMLElement>(".composer-row") ?? container;
      return { container, row, children: container ? Array.from(container.children) as HTMLElement[] : [] };
    };
    const place = () => {
      const { row, children } = parts();
      if (!row) { setStyle(undefined); return; }
      const box = row.getBoundingClientRect();
      const top = children.reduce((min, child) => { const r = child.getBoundingClientRect(); return r.height > 0 ? Math.min(min, r.top) : min; }, box.top);
      // The cat perches on the composer's top edge; the screen sits above it.
      setStyle({ right: 16, bottom: Math.max(16, window.innerHeight - top + 64) });
    };
    place();
    const { container, row, children } = parts();
    const observer = row ? new ResizeObserver(place) : undefined;
    for (const target of new Set([container, row, ...children])) if (target) observer?.observe(target);
    window.addEventListener("resize", place);
    return () => { observer?.disconnect(); window.removeEventListener("resize", place); };
  }, [active]);
  return style;
}

/** The window opens only on the user's explicit request (the header computer button). */
export function FloatingDesktop({ expanded, small = false, sheet = false, docked = false, folded = false, onClose, children }: {
  /** The big window (dialog). `small` is the floating picture-in-picture window; `sheet` is the narrow-screen full-screen form of the big one. */
  expanded: boolean; small?: boolean; sheet?: boolean; /** The small form sits in the context column's card instead of floating over the chat. */ docked?: boolean; /** Docked and folded: the monitor is a 56px strip (the video stays mounted). */ folded?: boolean; onClose: () => void; children: ReactNode;
}) {
  const { t } = useTranslation("computer");
  const shellRef = useRef<HTMLDivElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const contentId = useId();
  const anchor = useComposerAnchor(small && !docked);
  // Beside the small window the messages reflow instead of sitting under it (desktop widths only).
  useLayoutEffect(() => {
    if (!small || docked) return;
    const root = document.documentElement;
    const apply = () => {
      const width = shellRef.current?.getBoundingClientRect().width ?? 0;
      if (window.innerWidth >= 1024 && width) { root.style.setProperty("--desktop-reserve", `${Math.ceil(width) + 32}px`); root.dataset.desktopReserve = ""; }
      else { root.style.removeProperty("--desktop-reserve"); delete root.dataset.desktopReserve; }
    };
    apply();
    window.addEventListener("resize", apply);
    return () => { window.removeEventListener("resize", apply); root.style.removeProperty("--desktop-reserve"); delete root.dataset.desktopReserve; };
  }, [small, docked]);

  useEffect(() => {
    if (!expanded) return;
    const focusedElement = document.activeElement;
    if (!returnFocusRef.current) returnFocusRef.current = focusedElement instanceof HTMLElement ? focusedElement : null;
    const shell = shellRef.current;
    const frame = window.requestAnimationFrame(() => {
      shell?.querySelector<HTMLElement>('button:not([disabled])')?.focus({ preventScroll: true });
    });
    const keyboard = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || !shell) return;
      // Escape is useSurfaceEscape's: the big window steps back to the compact one there.
      if (event.key === "Tab") {
        const items = Array.from(shell.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), a[href], summary, [tabindex]:not([tabindex="-1"])'))
          .filter(element => !element.closest('[inert]') && element.getClientRects().length > 0);
        if (!items.length) { event.preventDefault(); shell.focus(); return; }
        const index = items.indexOf(document.activeElement as HTMLElement);
        if (index < 0 || event.shiftKey && index === 0 || !event.shiftKey && index === items.length - 1) {
          event.preventDefault();
          (event.shiftKey ? items.at(-1) : items[0])?.focus();
        }
      }
    };
    window.addEventListener("keydown", keyboard);
    return () => {
      window.cancelAnimationFrame(frame);
      window.removeEventListener("keydown", keyboard);
      // Do not override focus moved deliberately to another workspace surface.
      if (document.activeElement === document.body || shell?.contains(document.activeElement)) {
        const trigger = returnFocusRef.current;
        if (trigger?.isConnected && trigger !== document.body) trigger.focus({ preventScroll: true });
        else document.querySelector<HTMLElement>("[data-computer-toggle]")?.focus({ preventScroll: true });
      }
      returnFocusRef.current = null;
    };
  }, [expanded]);

  // Small <-> big: the screen grows from (or shrinks back into) the other form's rectangle.
  const windowMode = expanded ? "big" : small ? "small" : "none";
  const modeRef = useRef(windowMode);
  const fromRectRef = useRef<DOMRect | null>(null);
  const detailSelector = ".desktop-presence-canvas > .computer-detail";
  // The DOM still shows the previous form while this render runs, so this is the "first" of the FLIP.
  if (modeRef.current !== windowMode && modeRef.current !== "none" && windowMode !== "none") fromRectRef.current = shellRef.current?.querySelector<HTMLElement>(detailSelector)?.getBoundingClientRect() ?? null;
  useLayoutEffect(() => {
    const previous = modeRef.current;
    modeRef.current = windowMode;
    const from = fromRectRef.current;
    fromRectRef.current = null;
    const detail = shellRef.current?.querySelector<HTMLElement>(detailSelector);
    if (!detail || windowMode === "none" || previous === windowMode || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    if (from && previous !== "none") {
      const to = detail.getBoundingClientRect();
      if (!from.width || !to.width) return;
      detail.animate([
        { transformOrigin: "0 0", transform: `translate(${from.left - to.left}px, ${from.top - to.top}px) scale(${from.width / to.width})` },
        { transformOrigin: "0 0", transform: "none" },
      ], { duration: MOTION.base, easing: EASE.settle });
    } else if (windowMode === "small") {
      detail.animate([{ opacity: 0, transform: "translateY(14px) scale(.96)" }, { opacity: 1, transform: "none" }], { duration: MOTION.base, easing: EASE.settle });
    }
  }, [windowMode]);

  // Compact <-> strip: the bezel's height, the screen (its box animates from the old size) and the chin's contents move with --m-base.
  const foldedRef = useRef(folded);
  const foldFromRef = useRef<{ detail: DOMRect; margin: number; wrap?: DOMRect } | null>(null);
  if (foldedRef.current !== folded && !foldFromRef.current) {
    const detail = shellRef.current?.querySelector<HTMLElement>(detailSelector);
    foldFromRef.current = detail ? { detail: detail.getBoundingClientRect(), margin: parseFloat(getComputedStyle(detail).marginBottom) || 0, wrap: detail.querySelector<HTMLElement>(".computer-screen-wrap, .desktop-launch-surface")?.getBoundingClientRect() } : null;
  }
  useLayoutEffect(() => {
    const was = foldedRef.current;
    foldedRef.current = folded;
    const from = foldFromRef.current;
    foldFromRef.current = null;
    const detail = shellRef.current?.querySelector<HTMLElement>(detailSelector);
    if (was === folded || !from || !detail || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    // Measure the new form before any animation starts: an animated bezel height squeezes the screen inside it.
    const to = detail.getBoundingClientRect();
    const margin = parseFloat(getComputedStyle(detail).marginBottom) || 0;
    const wrap = detail.querySelector<HTMLElement>(".computer-screen-wrap, .desktop-launch-surface");
    const now = wrap?.getBoundingClientRect();
    const timing = { duration: MOTION.base, easing: EASE.settle };
    detail.animate([{ height: `${from.detail.height}px`, marginBottom: `${from.margin}px`, overflow: "hidden" }, { height: `${to.height}px`, marginBottom: `${margin}px`, overflow: "hidden" }], timing);
    // The screen's own box grows or shrinks (its width and height, not a scale: the corners and the picture keep their shape).
    if (wrap && now && from.wrap && from.wrap.width && now.width) wrap.animate([
      { width: `${from.wrap.width}px`, height: `${from.wrap.height}px`, aspectRatio: "auto", clipPath: `inset(0 round ${folded ? 12 : 8}px)` },
      { width: `${now.width}px`, height: `${now.height}px`, aspectRatio: "auto", clipPath: `inset(0 round ${folded ? 8 : 12}px)` },
    ], timing);
    // The strip's text and switch, or the chin, arrive after the screen has mostly moved.
    for (const item of detail.querySelectorAll<HTMLElement>(folded ? ".desktop-chin-text, .desktop-driver-switch" : ".desktop-chin")) item.animate([{ opacity: 0 }, { opacity: 1 }], { ...timing, delay: MOTION.base * 0.4, fill: "backwards" });
  }, [folded]);

  return <div ref={shellRef} className={`desktop-floating-shell${expanded ? " is-expanded" : ""}${folded && docked && !expanded ? " is-folded" : ""}${small ? " is-small" : ""}${expanded && sheet ? " is-sheet" : ""}${docked ? " is-docked" : ""}`} style={small && !docked ? anchor : undefined}
    role={expanded ? "dialog" : "group"} aria-modal={expanded || undefined} aria-label={t("desktop.title")} tabIndex={expanded ? -1 : undefined}>
    <div id={contentId} className="desktop-presence-content">
      <div className="desktop-presence-canvas">{children}</div>
    </div>
  </div>;
}
