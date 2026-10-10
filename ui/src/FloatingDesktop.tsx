import { useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { TofiIcon } from "./icons";
import { i18n, useTranslation } from "./i18n";

/** Keeps the mini screen beside the composer's right edge, above the perched cat. */
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
      setStyle({ right: Math.max(16, window.innerWidth - box.right), bottom: Math.max(16, window.innerHeight - top + 64) });
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

/** Activity shows a small live screen; only an explicit opening creates a dialog. */
export function FloatingDesktop({ expanded, small = false, sheet = false, activityLabel, onOpen, onShrink, onClose, children }: {
  /** The big window (dialog). `small` is the floating picture-in-picture window; `sheet` is the narrow-screen full-screen form of the big one. */
  expanded: boolean; small?: boolean; sheet?: boolean; activityLabel?: string; onOpen: () => void; onShrink?: () => void; onClose: () => void; children: ReactNode;
}) {
  const { t } = useTranslation("computer");
  const shellRef = useRef<HTMLDivElement>(null);
  const launcherRef = useRef<HTMLButtonElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const pointerFocusRef = useRef<HTMLElement | null>(null);
  const restoringFocusRef = useRef(false);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  // Escape steps big -> small; only a window with no small form (the sheet) closes outright.
  const escapeRef = useRef<() => void>(onClose);
  escapeRef.current = !sheet && onShrink ? onShrink : onClose;
  const windowed = expanded || small;
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState(false);
  const contentId = useId();
  // The mini screen is the collapsed form while a Bot works; hiding it leaves the launcher icon.
  const [miniHidden, setMiniHidden] = useState(false);
  const mini = !windowed && Boolean(activityLabel) && !miniHidden && window.matchMedia("(min-width: 768px)").matches;
  const anchor = useComposerAnchor(mini || small);
  const preview = !windowed && !mini && !dismissed && (hovered || focused);
  const supportsPreview = () => window.matchMedia("(min-width: 768px) and (hover: hover) and (pointer: fine)").matches;
  function openDesktop() {
    const focusedElement = document.activeElement;
    // Clicking the passive, non-focusable preview can blur the composer before
    // click fires. Preserve the focus observed before that pointer's default.
    returnFocusRef.current = focusedElement instanceof HTMLElement && focusedElement !== document.body ? focusedElement : pointerFocusRef.current;
    pointerFocusRef.current = null;
    setDismissed(true);
    onOpen();
  }

  useEffect(() => {
    const media = window.matchMedia("(min-width: 768px)");
    const sync = () => { if (!media.matches) { setHovered(false); setFocused(false); } };
    media.addEventListener("change", sync);
    return () => media.removeEventListener("change", sync);
  }, []);

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
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        setDismissed(true);
        escapeRef.current();
      } else if (event.key === "Tab") {
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
    // Remote keyboard input stops bubbling. Escape still closes this viewer;
    // composition cancellation and normal guest keys keep their input path.
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") keyboard(event); };
    window.addEventListener("keydown", escape, true);
    window.addEventListener("keydown", keyboard);
    return () => {
      window.cancelAnimationFrame(frame);
      window.removeEventListener("keydown", escape, true);
      window.removeEventListener("keydown", keyboard);
      setDismissed(true);
      // Do not override focus moved deliberately to another workspace surface.
      if (document.activeElement === document.body || shell?.contains(document.activeElement)) {
        const trigger = returnFocusRef.current;
        restoringFocusRef.current = true;
        if (trigger?.isConnected && trigger !== document.body) trigger.focus({ preventScroll: true });
        else if (launcherRef.current) launcherRef.current.focus({ preventScroll: true });
        else document.querySelector<HTMLElement>(`[aria-label="${CSS.escape(i18n.t("computer:floating.open"))}"]`)?.focus({ preventScroll: true });
        restoringFocusRef.current = false;
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
      ], { duration: 240, easing: "cubic-bezier(.2,.8,.2,1)" });
    } else if (windowMode === "small") {
      detail.animate([{ opacity: 0, transform: "translateY(14px) scale(.96)" }, { opacity: 1, transform: "none" }], { duration: 240, easing: "cubic-bezier(.2,.8,.2,1)" });
    }
  }, [windowMode]);

  if (mini) return <div ref={shellRef} className="desktop-floating-shell is-mini" style={anchor}>
    <div className="desktop-mini">
      <button type="button" className="desktop-mini-open" aria-label={t("floating.open_activity", { activity: activityLabel })} onClick={openDesktop} />
      <div className="desktop-presence-canvas" inert aria-hidden="true">{children}</div>
      <div className="desktop-mini-controls">
        <button type="button" className="desktop-mini-control" aria-label={t("desktop.expand_aria")} title={t("desktop.expand")} onClick={openDesktop}><TofiIcon name="external-link" size={14} /></button>
        <button type="button" className="desktop-mini-control" aria-label={t("floating.hide_mini_aria")} title={t("floating.collapse")} onClick={() => setMiniHidden(true)}><TofiIcon name="minus" size={14} /></button>
      </div>
      <p className="desktop-mini-status"><span className="desktop-presence-dot" aria-hidden="true" />{activityLabel}</p>
    </div>
  </div>;

  return <div ref={shellRef} className={`desktop-floating-shell${expanded ? " is-expanded" : ""}${small ? " is-small" : ""}${expanded && sheet ? " is-sheet" : ""}`} style={small ? anchor : undefined}
    role={expanded ? "dialog" : small ? "group" : undefined} aria-modal={expanded || undefined} aria-label={windowed ? t("desktop.title") : undefined} tabIndex={expanded ? -1 : undefined}
    onPointerEnter={event => { if (event.pointerType === "mouse" && supportsPreview()) { setDismissed(false); setHovered(true); } }}
    onPointerLeave={() => setHovered(false)}>
    {!windowed && <button ref={launcherRef} type="button" className="desktop-presence-launcher"
      aria-label={activityLabel ? t("floating.open_activity", { activity: activityLabel }) : t("floating.open")} aria-expanded={false} aria-controls={preview ? contentId : undefined}
      onFocus={() => { if (window.matchMedia("(min-width: 768px)").matches) { if (!restoringFocusRef.current) setDismissed(false); setFocused(true); } }} onBlur={() => setFocused(false)}
      onKeyDown={event => { if (event.key === "Escape" && preview) { event.preventDefault(); setDismissed(true); } }}
      onClick={openDesktop}>
      <TofiIcon name="monitor" size={22} /><span className="desktop-presence-dot" aria-hidden="true" />
    </button>}
    {(windowed || preview) && <div id={contentId} className={windowed ? "desktop-presence-content" : "desktop-presence-peek"}
      onPointerDownCapture={!windowed ? () => { pointerFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; } : undefined}
      onClick={!windowed ? openDesktop : undefined}>
      <div className="desktop-presence-canvas" inert={!windowed || undefined} aria-hidden={!windowed || undefined}>{children}</div>
      {!windowed && <p className="desktop-presence-peek-hint">{t("floating.peek_hint")}</p>}
    </div>}
  </div>;
}
