import { useEffect, useState, useSyncExternalStore, type ReactNode } from "react";
import { readConversationRoute } from "./conversationRoute";
import "./boot-screen.css";

/*
 * One look for every wait before the workspace exists: the session check, the first
 * list load and the startup-failure screen. A frosted skeleton of the app, the calico
 * mark breathing in the middle, no spinner. Visuals are CSS only.
 *
 * `BootScreen` is the screen. `BootDissolve` (mounted once, beside the app) plays the
 * exit: when a screen that was actually visible goes away, a copy of it dissolves over
 * the real UI, so the hand-off is a reveal rather than a cut.
 */

const PERIOD = 2400;
const FADE_IN_DELAY = 300;
const DISSOLVE_MS = 320;
const epoch = typeof performance === "undefined" ? 0 : performance.now();

// The page may boot before the app has applied the saved appearance; the skeleton must not flash light on a dark profile.
try {
  const root = document.documentElement;
  if (!root.dataset.theme) {
    const saved = localStorage.getItem("tofi:appearance");
    const dark = saved === "dark" || (saved !== "light" && matchMedia("(prefers-color-scheme: dark)").matches);
    root.dataset.theme = dark ? "dark" : "light";
    root.style.colorScheme = dark ? "dark" : "light";
  }
} catch { /* the default light tokens apply */ }

// --- visibility store: how many boot screens are on screen, and whether one just left ---
let shown = 0;
let leaving = false;
let dissolveTimer = 0;
let releaseTimer = 0;
const listeners = new Set<() => void>();
const emit = () => listeners.forEach(listener => listener());
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };

/** Marks one boot screen visible. The returned release starts the dissolve once nothing else took over. */
function claimVisible() {
  shown += 1;
  window.clearTimeout(releaseTimer);
  if (leaving) { leaving = false; window.clearTimeout(dissolveTimer); emit(); }
  return () => {
    shown -= 1;
    // A screen replaced by another boot screen (session check -> list load) is a hand-over, not an exit.
    releaseTimer = window.setTimeout(() => {
      if (shown > 0) return;
      leaving = true; emit();
      dissolveTimer = window.setTimeout(() => { leaving = false; emit(); }, DISSOLVE_MS + 30);
    }, 0);
  };
}

function Backdrop({ view, phase }: { view: "list" | "chat"; phase: string }) {
  return <>
    <div className="boot-skeleton" data-view={view} aria-hidden="true">
      <div className="boot-side">
        <div className="boot-side-top"><i className="boot-block boot-brand" /><i className="boot-block boot-dot" /><i className="boot-block boot-dot" /></div>
        <i className="boot-block boot-search" />
        {[78, 62, 84, 55, 70, 48].map((width, index) => <div className={`boot-row${index === 0 ? " is-on" : ""}`} key={index}><i className="boot-block boot-avatar" /><div className="boot-lines"><i className="boot-block" style={{ width: `${width}%` }} /><i className="boot-block is-faint" style={{ width: `${Math.max(30, width - 22)}%` }} /></div></div>)}
      </div>
      <div className="boot-main">
        <div className="boot-head"><i className="boot-block boot-avatar" /><div className="boot-lines"><i className="boot-block" style={{ width: 132 }} /><i className="boot-block is-faint" style={{ width: 84 }} /></div></div>
        <div className="boot-thread">
          <i className="boot-bubble is-bot" style={{ width: "58%", height: 64 }} />
          <i className="boot-bubble is-me" style={{ width: "38%", height: 40 }} />
          <i className="boot-bubble is-bot" style={{ width: "66%", height: 92 }} />
          <i className="boot-bubble is-me" style={{ width: "46%", height: 40 }} />
          <i className="boot-bubble is-bot" style={{ width: "34%", height: 40 }} />
        </div>
        <div className="boot-composer"><i className="boot-block boot-dot" /><i className="boot-block boot-field" /><i className="boot-block boot-dot" /></div>
      </div>
    </div>
    <div className="boot-frost" aria-hidden="true" />
    <span className="boot-logo-wrap" aria-hidden="true"><img className="boot-logo" src="/brand/tofi-logo-calico-awake.svg?v=awake-1" alt="" width="96" height="96" decoding="async" style={{ "--boot-phase": phase } as React.CSSProperties} /></span>
  </>;
}

const phaseNow = () => `-${Math.round(performance.now() - epoch) % PERIOD}ms`;
const currentView = (): "list" | "chat" => readConversationRoute(window.location.pathname).kind === "home" ? "list" : "chat";

/**
 * The waiting screen. `label` is the screen-reader status (visually hidden). Nothing paints for the first
 * 300 ms so a fast load never flashes; `immediate` (the failure screen) skips that wait. `children` sit on
 * top of the backdrop (message and Retry on failure).
 */
export function BootScreen({ label, immediate = false, children }: { label: string; immediate?: boolean; children?: ReactNode }) {
  // A screen taking over from another visible one shows at once and without a second fade-in.
  const [warm] = useState(() => shown > 0 || leaving);
  const [delayed, setVisible] = useState(warm);
  const visible = delayed || immediate;
  const [view] = useState(currentView);
  const [phase] = useState(phaseNow);
  useEffect(() => {
    if (visible) return;
    const timer = window.setTimeout(() => setVisible(true), FADE_IN_DELAY);
    return () => window.clearTimeout(timer);
  }, [visible]);
  useEffect(() => visible ? claimVisible() : undefined, [visible]);
  return <div className={`boot${children ? " is-failed" : ""}${warm ? " is-warm" : ""}`} aria-busy={children ? undefined : true}>
    {visible && <Backdrop view={view} phase={phase} />}
    {children ? <div className="boot-card startup-error">{children}</div> : null}
    <div className="sr-only" role="status" aria-live="polite">{children ? null : label}</div>
  </div>;
}

/** Mounted once beside the app: plays the dissolve over whatever replaced a visible boot screen. */
export function BootDissolve() {
  const active = useSyncExternalStore(subscribe, () => leaving);
  const [view] = useState(currentView);
  if (!active) return null;
  return <div className="boot boot-dissolve" aria-hidden="true"><Backdrop view={view} phase={phaseNow()} /></div>;
}
