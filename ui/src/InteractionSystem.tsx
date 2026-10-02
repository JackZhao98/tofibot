import { TofiIcon } from "./icons";
import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";

export type ThemePreference = "system" | "light" | "dark";
export function useAppearance() {
  const [preference, setPreference] = useState<ThemePreference>(() => {
    try { const value = localStorage.getItem("tofi:appearance") ?? window.tofiDesktop?.themePreference; return value === "light" || value === "dark" ? value : "system"; } catch { return "system"; }
  });
  const [systemDark, setSystemDark] = useState(() => matchMedia("(prefers-color-scheme: dark)").matches);
  useEffect(() => {
    const media = matchMedia("(prefers-color-scheme: dark)");
    const change = () => setSystemDark(media.matches);
    media.addEventListener("change", change);
    const sync = (event: StorageEvent) => { if (event.key === "tofi:appearance") setPreference(event.newValue === "dark" || event.newValue === "light" ? event.newValue : "system"); };
    window.addEventListener("storage", sync);
    return () => { media.removeEventListener("change", change); window.removeEventListener("storage", sync); };
  }, []);
  const theme = preference === "system" ? (systemDark ? "dark" : "light") : preference;
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
    const pageColor = getComputedStyle(document.documentElement).getPropertyValue("--bg").trim();
    if (pageColor) document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')?.setAttribute("content", pageColor);
  }, [theme]);
  useEffect(() => { void window.tofiDesktop?.appearance(preference); }, [preference]);
  function choose(value: ThemePreference) { setPreference(value); try { localStorage.setItem("tofi:appearance", value); window.dispatchEvent(new StorageEvent("storage", { key: "tofi:appearance", newValue: value })); } catch { /* Keep in-memory preference. */ } }
  return { preference, theme, choose };
}

/** Preserve the outgoing surface so closing has the same continuity as opening. */
export function useSurfacePresence<T>(value: T | null, duration = 220) {
  const [retained, setRetained] = useState<T | null>(value);
  useEffect(() => {
    if (value !== null) { setRetained(value); return; }
    const timer = window.setTimeout(() => setRetained(null), matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : duration);
    return () => clearTimeout(timer);
  }, [value, duration]);
  return value ?? retained;
}

export function AppearancePicker({ value, onChange }: { value: ThemePreference; onChange: (theme: ThemePreference) => void }) {
  return <section className="appearance-setting"><div><h3>外观</h3></div><div className="appearance-options" role="group" aria-label="外观主题">
    {(["system", "light", "dark"] as const).map((theme) => <button key={theme} aria-pressed={value === theme} onClick={() => onChange(theme)}><span className={`theme-swatch theme-swatch-${theme}`} aria-hidden="true"><i /><i /><i /></span><span>{theme === "system" ? "跟随系统" : theme === "light" ? "浅色" : "深色"}</span></button>)}
  </div></section>;
}

/** Quiet, delayed tooltips for icon-only actions. No timers or listeners per row. */
export function ActionHints() {
  const [hint, setHint] = useState<{ text: string; x: number; y: number; above: boolean } | null>(null);
  const target = useRef<HTMLElement | null>(null);
  useEffect(() => {
    let timer: number | undefined;
    const hide = () => { clearTimeout(timer); target.current = null; setHint(null); };
    const show = (event: Event) => {
      if (event instanceof PointerEvent && event.pointerType !== "mouse") return;
      const element = event.target instanceof Element ? event.target.closest<HTMLElement>("[data-hint]") : null;
      if (!element || element.closest("[inert]")) { hide(); return; }
      if (target.current === element) return;
      hide(); target.current = element;
      timer = window.setTimeout(() => {
        if (!element.isConnected) return;
        const rect = element.getBoundingClientRect();
        setHint({ text: element.dataset.hint || "", x: Math.max(100, Math.min(innerWidth - 100, rect.left + rect.width / 2)), y: rect.top > 62 ? rect.top - 8 : rect.bottom + 8, above: rect.top > 62 });
      }, event.type === "focusin" ? 150 : 550);
    };
    const leave = (event: PointerEvent) => { if (!target.current?.contains(event.relatedTarget as Node | null)) hide(); };
    document.addEventListener("pointerover", show);
    document.addEventListener("pointerout", leave);
    document.addEventListener("focusin", show);
    document.addEventListener("focusout", hide);
    document.addEventListener("pointerdown", hide);
    document.addEventListener("keydown", hide);
    window.addEventListener("scroll", hide, true);
    return () => { hide(); document.removeEventListener("pointerover", show); document.removeEventListener("pointerout", leave); document.removeEventListener("focusin", show); document.removeEventListener("focusout", hide); document.removeEventListener("pointerdown", hide); document.removeEventListener("keydown", hide); window.removeEventListener("scroll", hide, true); };
  }, []);
  return hint ? createPortal(<div className="action-hint" role="tooltip" style={{ left: hint.x, top: hint.y, transform: `translate(-50%, ${hint.above ? "-100%" : "0"})` }}>{hint.text}</div>, document.body) : null;
}

/** Keep content mounted through collapse; inert removes closed controls from tab order. */
export function Disclosure({ title, children, defaultOpen = false }: { title: ReactNode; children: ReactNode; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  const id = useId();
  return <div className="disclosure" data-open={open}>
    <button className="disclosure-toggle" type="button" aria-expanded={open} aria-controls={id} onClick={() => setOpen(value => !value)}><span>{title}</span><TofiIcon className="disclosure-chevron" name="chevron-down" size={16} /></button>
    <div className="disclosure-region" id={id} inert={!open || undefined} aria-hidden={!open}><div className="disclosure-inner">{children}</div></div>
  </div>;
}

/** Destructive actions stay beside their content and always offer a way back. */
export function ConfirmAction({ label, question, disabled, onConfirm }: { label: string; question: string; disabled?: boolean; onConfirm: () => Promise<void> }) {
  const [confirming, setConfirming] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const trigger = useRef<HTMLButtonElement>(null);
  const cancelButton = useRef<HTMLButtonElement>(null);
  const wasConfirming = useRef(false);
  useEffect(() => {
    if (confirming) cancelButton.current?.focus();
    else if (wasConfirming.current) trigger.current?.focus();
    wasConfirming.current = confirming;
  }, [confirming]);
  function cancel() { if (pending || disabled) return; setError(""); setConfirming(false); }
  async function confirm() {
    if (pending || disabled) return;
    setPending(true); setError("");
    try { await onConfirm(); setConfirming(false); }
    catch (cause) { setError(cause instanceof Error ? cause.message : "操作失败，请重试。"); }
    finally { setPending(false); }
  }
  return <span className="confirm-action">
    <button ref={trigger} type="button" className="danger-link" disabled={disabled} hidden={confirming} onClick={() => setConfirming(true)}>{label}</button>
    {confirming && <span className="inline-confirmation" role="group" aria-label={question} onKeyDown={event => { if (event.key === "Escape") { event.stopPropagation(); cancel(); } }}>
      <span>{question}</span><button type="button" className="danger-link" disabled={disabled || pending} onClick={() => void confirm()}>{pending ? "处理中…" : `确认${label}`}</button><button ref={cancelButton} type="button" disabled={disabled || pending} onClick={cancel}>取消</button>{error && <span className="error-text" role="alert">{error}</span>}
    </span>}
  </span>;
}
