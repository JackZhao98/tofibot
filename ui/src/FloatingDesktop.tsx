import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { TofiIcon } from "./icons";

/** Activity announces availability; only an explicit opening creates a dialog. */
export function FloatingDesktop({ expanded, activityLabel, onOpen, onClose, children }: {
  expanded: boolean; activityLabel?: string; onOpen: () => void; onClose: () => void; children: ReactNode;
}) {
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
  const preview = !expanded && !dismissed && (hovered || focused);
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
        else document.querySelector<HTMLElement>('[aria-label="打开共享电脑"]')?.focus({ preventScroll: true });
        restoringFocusRef.current = false;
      }
      returnFocusRef.current = null;
    };
  }, [expanded]);

  return <div ref={shellRef} className={`desktop-floating-shell${expanded ? " is-expanded" : ""}`}
    role={expanded ? "dialog" : undefined} aria-modal={expanded || undefined} aria-label={expanded ? "共享电脑" : undefined} tabIndex={expanded ? -1 : undefined}
    onPointerEnter={event => { if (event.pointerType === "mouse" && supportsPreview()) { setDismissed(false); setHovered(true); } }}
    onPointerLeave={() => setHovered(false)}>
    {!expanded && <button ref={launcherRef} type="button" className="desktop-presence-launcher"
      aria-label={`打开共享电脑${activityLabel ? `：${activityLabel}` : ""}`} aria-expanded={false} aria-controls={preview ? contentId : undefined}
      onFocus={() => { if (window.matchMedia("(min-width: 768px)").matches) { if (!restoringFocusRef.current) setDismissed(false); setFocused(true); } }} onBlur={() => setFocused(false)}
      onKeyDown={event => { if (event.key === "Escape" && preview) { event.preventDefault(); setDismissed(true); } }}
      onClick={openDesktop}>
      <TofiIcon name="monitor" size={22} /><span className="desktop-presence-dot" aria-hidden="true" />
    </button>}
    {(expanded || preview) && <div id={contentId} className={expanded ? "desktop-presence-content" : "desktop-presence-peek"}
      onPointerDownCapture={!expanded ? () => { pointerFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; } : undefined}
      onClick={!expanded ? openDesktop : undefined}>
      <div className="desktop-presence-canvas" inert={!expanded || undefined} aria-hidden={!expanded || undefined}>{children}</div>
      {!expanded && <p className="desktop-presence-peek-hint">仅查看 · 点击放大</p>}
    </div>}
  </div>;
}
