import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import * as ContextMenu from "@radix-ui/react-context-menu";
import { FocusScope } from "@radix-ui/react-focus-scope";
import { createPortal } from "react-dom";
import { useTranslation } from "./i18n";
import { animate, drag, readTransformValue, type AnimationPlaybackControls } from "motion";
import { MOTION, EASE, SPRING } from "./motion";
import { layerRoot } from "./layers";
import { PIP_SIZES, clampPipPoint, defaultPipStored, nearestPipCorner, projectPipPoint, nudgePipPoint, parsePipStored, pipPlaceFromPoint, resolvePipPlace, snapPipSide, type PipCorner, type PipPlace, type PipStored } from "./floatingDesktopPosition";
import "./desktop-shell.css";

const PIP_KEY = "tofi.pip.v1";
const loadPip = (): PipStored => { try { return parsePipStored(localStorage.getItem(PIP_KEY)); } catch { return defaultPipStored; } };
const savePip = (value: PipStored) => { try { localStorage.setItem(PIP_KEY, JSON.stringify(value)); } catch { /* private mode: the position lasts for the page */ } };

/** The highest visible top edge of the composer (a notice above its row counts): the bottom corners end above it. */
function composerTop(): number | undefined {
  const container = document.querySelector<HTMLElement>(".composer");
  if (!container) return undefined;
  const row = container.querySelector<HTMLElement>(".composer-row") ?? container;
  return Array.from(container.children).reduce((min, child) => { const r = (child as HTMLElement).getBoundingClientRect(); return r.height > 0 ? Math.min(min, r.top) : min; }, row.getBoundingClientRect().top);
}

/** The window opens only on the user's explicit request (the header computer button). */
export function FloatingDesktop({ expanded, small = false, sheet = false, docked = false, folded = false, onClose, children, label }: {
  /** The big window (dialog). `small` is the floating picture-in-picture window; `sheet` is the narrow-screen full-screen form of the big one. */
  expanded: boolean; small?: boolean; sheet?: boolean; /** The small form sits in the context column's card instead of floating over the chat. */ docked?: boolean; /** Docked and folded: the monitor is a 56px strip (the video stays mounted). */ folded?: boolean; onClose: () => void; children: ReactNode; label?: string;
}) {
  const { t } = useTranslation("computer");
  const shellRef = useRef<HTMLDivElement>(null);
  // The shell lives in a host element of its own, which this component moves with plain DOM calls (React never sees the move). Docked, the host sits
  // in the widget's slot; while the window is big it is a direct child of `.workspace`, the same place the narrow-screen window has always had. A fixed
  // dialog left inside the widget column sits under a scroll container, a clip-path / translate animation, `overflow: clip` and a drag `filter`, and
  // in WebKit on iPad that painted nothing while the shell's scrim still took every tap. Moving the host keeps the same nodes (the live video is
  // not remounted); `data-moved` tells the CRT boot animation not to replay.
  const [host] = useState(() => { const element = document.createElement("div"); element.className = "desktop-host"; element.style.display = "contents"; return element; });
  const slotRef = useRef<HTMLDivElement | null>(null);
  const attachSlot = useCallback((slot: HTMLDivElement | null) => {
    slotRef.current = slot;
    if (slot) slot.appendChild(host); else host.remove();
  }, [host]);
  useLayoutEffect(() => {
    const slot = slotRef.current;
    if (!slot) return;
    const workspace = docked && expanded ? slot.closest<HTMLElement>(".workspace") : null;
    const target = workspace ?? slot;
    if (host.parentNode === target) return;
    if (workspace) {
      // The widget keeps its place in the column while the big window is up.
      slot.style.minHeight = `${Math.ceil(slot.getBoundingClientRect().height)}px`;
      host.dataset.moved = "";
    } else slot.style.minHeight = "";
    target.appendChild(host);
  }, [docked, expanded, host]);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const contentId = useId();
  // The mini window (docs/design/computer-pip.md section 5): the floating small form is dragged, snapped to a corner and remembered.
  const pipActive = small && !docked;
  const [pip, setPip] = useState<PipStored>(loadPip);
  const [tick, setTick] = useState(0);
  const pipRef = useRef(pip);
  pipRef.current = pip;
  const measure = useCallback(() => {
    const el = shellRef.current;
    if (!el) return null;
    const rect = el.getBoundingClientRect();
    return { size: { width: el.offsetWidth, height: el.offsetHeight }, viewport: { width: window.innerWidth, height: window.innerHeight }, composerTop: composerTop(), point: { left: rect.left, top: rect.top } };
  }, []);
  const place = useCallback((next: PipPlace | { size: number }) => { const value = { ...pipRef.current, ...next }; savePip(value); setPip(value); }, []);
  const busyRef = useRef(false);
  const draggedRef = useRef(false);
  // Where the last press began: a release that lands elsewhere ended a drag, even one too quick for Motion's onStart.
  const pressAtRef = useRef<{ x: number; y: number } | null>(null);
  const snapRef = useRef<AnimationPlaybackControls | null>(null);
  // Motion owns the shell's transform in every form, including restoring its stored place.
  useLayoutEffect(() => {
    const el = shellRef.current;
    if (!el || busyRef.current) return;
    const m = measure();
    if (!m) return;
    const point = pipActive ? resolvePipPlace(pip, m.size, m.viewport, m.composerTop) : { left: 0, top: 0 };
    // Out of the mini form (big window, docked card) the shell has no placement, at once: Motion writes on the next frame,
    // and a leftover translate would show the big window offset for that frame and make its fixed children relative to it.
    if (!pipActive) el.style.transform = "none";
    animate(el, { x: point.left, y: point.top }, { duration: 0 });
  }, [pipActive, pip, tick, measure]);
  useLayoutEffect(() => {
    const el = shellRef.current;
    if (!pipActive || !el) return;
    let disposed = false;
    let pointerId: number | undefined;
    let captureTarget: Element | undefined;
    const reduced = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const constraints = { left: 0, right: 0, top: 0, bottom: 0 };
    const end = () => { busyRef.current = false; delete el.dataset.pip; el.style.willChange = ""; };
    // Runs before Motion's listener. Controls must remain plain clicks, and capture keeps
    // Electron delivering moves across the native title-bar drag strip.
    const press = (event: PointerEvent) => {
      draggedRef.current = false;
      pressAtRef.current = { x: event.clientX, y: event.clientY };
      if ((event.target as Element).closest(".desktop-window-controls, .desktop-control-button")) {
        event.stopImmediatePropagation();
        return;
      }
      if (event.button !== 0 || !event.isPrimary) return;
      snapRef.current?.stop();
      snapRef.current = null;
      end();
      const m = measure();
      if (!m) return;
      const min = clampPipPoint({ left: -Infinity, top: -Infinity }, m.size, m.viewport, m.composerTop);
      const max = clampPipPoint({ left: Infinity, top: Infinity }, m.size, m.viewport, m.composerTop);
      Object.assign(constraints, { left: min.left, right: max.left, top: min.top, bottom: max.top });
      pointerId = event.pointerId;
      // Capture on the pressed descendant so a short click still reaches the enlarge button.
      captureTarget = event.target as Element;
      captureTarget.setPointerCapture(pointerId);
    };
    el.addEventListener("pointerdown", press);
    const stopDrag = drag(el, {
      constraints, elastic: false, momentum: false,
      onStart: () => {
        if (disposed) return;
        busyRef.current = true;
        draggedRef.current = true;
        el.dataset.pip = "drag";
        el.style.willChange = "transform";
      },
      onEnd: (event, info) => {
        if (disposed) return;
        const m = measure();
        if (!m) { end(); return; }
        const point = { left: Number(readTransformValue(el, "x")), top: Number(readTransformValue(el, "y")) };
        const velocity = event.type === "pointercancel" ? { x: 0, y: 0 } : info.velocity;
        const next = event.altKey ? pipPlaceFromPoint(point, m.size, m.viewport, m.composerTop)
          : { corner: nearestPipCorner(projectPipPoint(point, velocity), m.size, m.viewport, m.composerTop), dx: 0, dy: 0 };
        const target = resolvePipPlace(next, m.size, m.viewport, m.composerTop);
        el.dataset.pip = "snap";
        // A per-value transition replaces the shared one, so each axis carries the whole spring and its own release velocity.
        // At rest means both axes are done; a press during the snap stops it and its result is dropped.
        const spring = { type: "spring" as const, ...SPRING.pipSnap };
        const run = animate(el, { x: target.left, y: target.top }, reduced() ? { duration: 0 } : { x: { ...spring, velocity: velocity.x }, y: { ...spring, velocity: velocity.y } });
        snapRef.current = run;
        // Motion draws a value on its next frame, after `finished` resolves: wait for that frame before calling it at rest.
        void run.finished.then(() => requestAnimationFrame(() => { if (!disposed && snapRef.current === run) { snapRef.current = null; end(); place(next); } }));
      },
    });
    return () => {
      disposed = true;
      stopDrag();
      snapRef.current?.stop();
      snapRef.current = null;
      el.removeEventListener("pointerdown", press);
      if (pointerId !== undefined && captureTarget?.hasPointerCapture(pointerId)) captureTarget.releasePointerCapture(pointerId);
      end();
    };
  }, [pipActive, measure, place]);
  useEffect(() => {
    if (!pipActive) return;
    let frame = 0;
    const again = () => { if (!frame) frame = requestAnimationFrame(() => { frame = 0; if (!busyRef.current) setTick(value => value + 1); }); };
    const composer = document.querySelector<HTMLElement>(".composer");
    const observer = composer ? new ResizeObserver(again) : undefined;
    if (composer) for (const target of new Set([composer, ...Array.from(composer.children) as HTMLElement[]])) observer?.observe(target);
    window.addEventListener("resize", again);
    return () => { observer?.disconnect(); window.removeEventListener("resize", again); cancelAnimationFrame(frame); };
  }, [pipActive]);
  // The step rows' "look" button on an already open window: bring it forward and flash the bezel.
  useEffect(() => {
    const flash = () => {
      const el = shellRef.current;
      if (!el) return;
      el.dataset.flash = "";
      window.setTimeout(() => { delete el.dataset.flash; }, 900);
    };
    window.addEventListener("tofi:computer-look", flash);
    return () => window.removeEventListener("tofi:computer-look", flash);
  }, []);
  const arrowKeys = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (!pipActive || event.defaultPrevented || event.ctrlKey || event.metaKey || !event.key.startsWith("Arrow") || !(event.target as Element).closest(".desktop-enlarge")) return;
    const m = measure();
    if (!m) return;
    event.preventDefault();
    event.stopPropagation();
    place(event.altKey ? { corner: snapPipSide(pipRef.current.corner, event.key), dx: 0, dy: 0 } : pipPlaceFromPoint(nudgePipPoint(m.point, event.key, event.shiftKey), m.size, m.viewport, m.composerTop));
  };
  const pipWidth = Math.min(pip.size, window.innerWidth - 24);

  useEffect(() => {
    if (!expanded) return;
    const focusedElement = document.activeElement;
    if (!returnFocusRef.current) returnFocusRef.current = focusedElement instanceof HTMLElement ? focusedElement : null;
    const shell = shellRef.current;
    const frame = window.requestAnimationFrame(() => {
      shell?.querySelector<HTMLElement>('button:not([disabled])')?.focus({ preventScroll: true });
    });
    return () => {
      window.cancelAnimationFrame(frame);
      // Do not override focus moved deliberately to another workspace surface.
      if (document.activeElement === document.body || shell?.contains(document.activeElement)) {
        const trigger = returnFocusRef.current;
        if (trigger?.isConnected && trigger !== document.body) trigger.focus({ preventScroll: true });
        else document.querySelector<HTMLElement>("[data-computer-toggle]")?.focus({ preventScroll: true });
      }
      returnFocusRef.current = null;
    };
  }, [expanded]);

  // Window forms appear in place; never fly between the mini window and a corner.
  const windowMode = expanded ? "big" : small ? "small" : "none";
  const modeRef = useRef(windowMode);
  const detailSelector = ".desktop-presence-canvas > .computer-detail";
  useLayoutEffect(() => {
    const previous = modeRef.current;
    modeRef.current = windowMode;
    const detail = shellRef.current?.querySelector<HTMLElement>(detailSelector);
    if (!detail || windowMode === "none" || previous === windowMode || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    detail.animate([{ opacity: 0 }, { opacity: 1 }], { duration: MOTION.quick, easing: EASE.out });
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

  const shell = <div ref={shellRef} className={`desktop-floating-shell${expanded ? " is-expanded" : ""}${folded && docked && !expanded ? " is-folded" : ""}${small ? " is-small" : ""}${expanded && sheet ? " is-sheet" : ""}${docked ? " is-docked" : ""}`} style={pipActive ? { "--pip-width": `${pipWidth}px` } as CSSProperties : undefined}
    onKeyDown={arrowKeys}
    onClickCapture={pipActive ? event => {
      const at = pressAtRef.current, moved = at !== null && Math.hypot(event.clientX - at.x, event.clientY - at.y) > 3;
      if (draggedRef.current || moved) { draggedRef.current = false; event.preventDefault(); event.stopPropagation(); }
    } : undefined}
    role={expanded ? "dialog" : "group"} aria-modal={expanded || undefined} aria-label={label ?? t("desktop.title")} tabIndex={expanded ? -1 : undefined}>
    <div id={contentId} className="desktop-presence-content">
      <div className="desktop-presence-canvas">{children}</div>
    </div>
  </div>;
  const corners: { corner: PipCorner; key: "tl" | "tr" | "bl" | "br" }[] = [{ corner: "tl", key: "tl" }, { corner: "tr", key: "tr" }, { corner: "bl", key: "bl" }, { corner: "br", key: "br" }];
  // The shell is always wrapped (the trigger is only disabled outside the small form), so the live video never remounts when the form changes.
  const wrapped = <ContextMenu.Root modal={false}>
    {/* The big window keeps Tab inside (Radix FocusScope; Escape is useSurfaceEscape's). Entry focus and return stay in the effect above. */}
    <ContextMenu.Trigger asChild disabled={!pipActive}><FocusScope asChild trapped={expanded} loop>{shell}</FocusScope></ContextMenu.Trigger>
    {pipActive && <ContextMenu.Portal container={layerRoot()}>
      <ContextMenu.Content className="tofi-pop pip-menu" aria-label={t("desktop.pip.menu")} collisionPadding={8}
        onCloseAutoFocus={event => { event.preventDefault(); shellRef.current?.querySelector<HTMLElement>(".desktop-enlarge")?.focus({ preventScroll: true }); }}>
        {corners.map(({ corner, key }) => <ContextMenu.Item key={corner} className="pip-menu-item" onSelect={() => place({ corner, dx: 0, dy: 0 })}>{t(`desktop.pip.corner_${key}`)}</ContextMenu.Item>)}
        <ContextMenu.Separator className="pip-menu-separator" />
        <ContextMenu.RadioGroup value={String(pip.size)} onValueChange={value => place({ size: Number(value) })}>
          {PIP_SIZES.map((size, index) => <ContextMenu.RadioItem key={size} value={String(size)} className="pip-menu-item">{t(`desktop.pip.size_${(["small", "medium", "large"] as const)[index]}`)}<ContextMenu.ItemIndicator className="pip-menu-check" /></ContextMenu.RadioItem>)}
        </ContextMenu.RadioGroup>
        <ContextMenu.Separator className="pip-menu-separator" />
        <ContextMenu.Item className="pip-menu-item" onSelect={() => closeRef.current()}>{t("desktop.close")}</ContextMenu.Item>
      </ContextMenu.Content>
    </ContextMenu.Portal>}
  </ContextMenu.Root>;
  return <><div ref={attachSlot} className="desktop-host-slot" style={docked ? undefined : { display: "contents" }} />{createPortal(wrapped, host)}</>;
}
