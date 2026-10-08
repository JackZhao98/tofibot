import { useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { TofiIcon } from "./icons";
import { i18n, useTranslation } from "./i18n";

/** Keeps the mini screen beside the composer's right edge, above the perched cat. */
function useComposerAnchor(active: boolean) {
  const [style, setStyle] = useState<CSSProperties>();
  useLayoutEffect(() => {
    if (!active) return;
    const place = () => {
      // .composer spans the column; .composer-row is the visible bordered box.
      const composer = document.querySelector<HTMLElement>(".composer .composer-row") ?? document.querySelector<HTMLElement>(".composer");
      if (!composer) { setStyle(undefined); return; }
      const box = composer.getBoundingClientRect();
      // The cat perches on the composer's top edge; the screen sits above it.
      setStyle({ right: Math.max(16, window.innerWidth - box.right), bottom: Math.max(16, window.innerHeight - box.top + 64) });
    };
    place();
    const composer = document.querySelector(".composer .composer-row") ?? document.querySelector(".composer");
    const observer = composer ? new ResizeObserver(place) : undefined;
    if (composer) observer?.observe(composer);
    window.addEventListener("resize", place);
    return () => { observer?.disconnect(); window.removeEventListener("resize", place); };
  }, [active]);
  return style;
}

/** Activity shows a small live screen; only an explicit opening creates a dialog. */
export function FloatingDesktop({ expanded, activityLabel, onOpen, onClose, children }: {
  expanded: boolean; activityLabel?: string; onOpen: () => void; onClose: () => void; children: ReactNode;
}) {
  const { t } = useTranslation("computer");
  const shellRef = useRef<HTMLDivElement>(null);
  const launcherRef = useRef<HTMLButtonElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const pointerFocusRef = useRef<HTMLElement | null>(null);
  const restoringFocusRef = useRef(false);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState(false);
  const contentId = useId();
  // The mini screen is the collapsed form while a Bot works; hiding it leaves the launcher icon.
  const [miniHidden, setMiniHidden] = useState(false);
  const mini = !expanded && Boolean(activityLabel) && !miniHidden && window.matchMedia("(min-width: 768px)").matches;
  const anchor = useComposerAnchor(mini);
  const preview = !expanded && !mini && !dismissed && (hovered || focused);
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
        closeRef.current();
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

  return <div ref={shellRef} className={`desktop-floating-shell${expanded ? " is-expanded" : ""}`}
    role={expanded ? "dialog" : undefined} aria-modal={expanded || undefined} aria-label={expanded ? t("desktop.title") : undefined} tabIndex={expanded ? -1 : undefined}
    onPointerEnter={event => { if (event.pointerType === "mouse" && supportsPreview()) { setDismissed(false); setHovered(true); } }}
    onPointerLeave={() => setHovered(false)}>
    {!expanded && <button ref={launcherRef} type="button" className="desktop-presence-launcher"
      aria-label={activityLabel ? t("floating.open_activity", { activity: activityLabel }) : t("floating.open")} aria-expanded={false} aria-controls={preview ? contentId : undefined}
      onFocus={() => { if (window.matchMedia("(min-width: 768px)").matches) { if (!restoringFocusRef.current) setDismissed(false); setFocused(true); } }} onBlur={() => setFocused(false)}
      onKeyDown={event => { if (event.key === "Escape" && preview) { event.preventDefault(); setDismissed(true); } }}
      onClick={openDesktop}>
      <TofiIcon name="monitor" size={22} /><span className="desktop-presence-dot" aria-hidden="true" />
    </button>}
    {(expanded || preview) && <div id={contentId} className={expanded ? "desktop-presence-content" : "desktop-presence-peek"}
      onPointerDownCapture={!expanded ? () => { pointerFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; } : undefined}
      onClick={!expanded ? openDesktop : undefined}>
      <div className="desktop-presence-canvas" inert={!expanded || undefined} aria-hidden={!expanded || undefined}>{children}</div>
      {!expanded && <p className="desktop-presence-peek-hint">{t("floating.peek_hint")}</p>}
    </div>}
  </div>;
}
