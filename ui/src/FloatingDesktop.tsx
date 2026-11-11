import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import * as ContextMenu from "@radix-ui/react-context-menu";
import { createPortal } from "react-dom";
import { useTranslation } from "./i18n";
import { MOTION, EASE } from "./motion";
import { layerRoot } from "./layers";
import { createPipEngine, translate, type PipEngine } from "./pipDrag";
import { PIP_SIZES, defaultPipStored, nudgePipPoint, parsePipStored, pipPlaceFromPoint, resolvePipPlace, snapPipSide, type PipCorner, type PipPlace, type PipStored } from "./floatingDesktopPosition";
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
export function FloatingDesktop({ expanded, small = false, sheet = false, docked = false, folded = false, onClose, children }: {
  /** The big window (dialog). `small` is the floating picture-in-picture window; `sheet` is the narrow-screen full-screen form of the big one. */
  expanded: boolean; small?: boolean; sheet?: boolean; /** The small form sits in the context column's card instead of floating over the chat. */ docked?: boolean; /** Docked and folded: the monitor is a 56px strip (the video stays mounted). */ folded?: boolean; onClose: () => void; children: ReactNode;
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
  const engineRef = useRef<PipEngine | null>(null);
  engineRef.current ??= createPipEngine({ element: () => shellRef.current, measure, controls: ".desktop-window-controls, .desktop-control-button", commit: place });
  useEffect(() => () => engineRef.current?.dispose(), []);
  // Placement is the one write to the shell: before the FLIP below measures it, and again whenever the stored place, the viewport or the composer changes.
  useLayoutEffect(() => {
    const el = shellRef.current;
    if (!el) return;
    if (!pipActive) { el.style.transform = ""; return; }
    if (engineRef.current?.busy()) return;
    const m = measure();
    if (m) el.style.transform = translate(resolvePipPlace(pip, m.size, m.viewport, m.composerTop));
  }, [pipActive, pip, tick, measure]);
  useEffect(() => {
    if (!pipActive) return;
    let frame = 0;
    const again = () => { if (!frame) frame = requestAnimationFrame(() => { frame = 0; if (!engineRef.current?.busy()) setTick(value => value + 1); }); };
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

  const shell = <div ref={shellRef} className={`desktop-floating-shell${expanded ? " is-expanded" : ""}${folded && docked && !expanded ? " is-folded" : ""}${small ? " is-small" : ""}${expanded && sheet ? " is-sheet" : ""}${docked ? " is-docked" : ""}`} style={pipActive ? { "--pip-width": `${pipWidth}px` } as CSSProperties : undefined}
    onPointerDown={pipActive ? event => engineRef.current?.press(event.nativeEvent) : undefined} onKeyDown={arrowKeys}
    onClickCapture={pipActive ? event => { if (engineRef.current?.wasDragged()) { event.preventDefault(); event.stopPropagation(); } } : undefined}
    role={expanded ? "dialog" : "group"} aria-modal={expanded || undefined} aria-label={t("desktop.title")} tabIndex={expanded ? -1 : undefined}>
    <div id={contentId} className="desktop-presence-content">
      <div className="desktop-presence-canvas">{children}</div>
    </div>
  </div>;
  const corners: { corner: PipCorner; key: "tl" | "tr" | "bl" | "br" }[] = [{ corner: "tl", key: "tl" }, { corner: "tr", key: "tr" }, { corner: "bl", key: "bl" }, { corner: "br", key: "br" }];
  // The shell is always wrapped (the trigger is only disabled outside the small form), so the live video never remounts when the form changes.
  const wrapped = <ContextMenu.Root modal={false}>
    <ContextMenu.Trigger asChild disabled={!pipActive}>{shell}</ContextMenu.Trigger>
    {pipActive && <ContextMenu.Portal container={layerRoot()}>
      <ContextMenu.Content className="pip-menu" aria-label={t("desktop.pip.menu")} collisionPadding={8}
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
