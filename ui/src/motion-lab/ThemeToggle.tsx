import { useEffect, useState, type MouseEvent } from "react";
import { flushSync } from "react-dom";
import { TofiIcon } from "../icons";
import { prefersReducedMotion } from "./lib/hooks";

type Theme = "light" | "dark";
type ViewTransitionDocument = Document & {
  startViewTransition?: (update: () => void) => { ready: Promise<void>; finished: Promise<void> };
};

const STORAGE_KEY = "tofi:motion-lab:theme";

function initialTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "light" || stored === "dark") return stored;
  } catch {
    // Storage can be blocked; fall back to the system preference.
  }
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme;
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", theme === "dark" ? "#14120f" : "#f1efeb");
  try { localStorage.setItem(STORAGE_KEY, theme); } catch { /* per-viewer convenience only */ }
}

/** Light/dark switch that reveals the new theme as a circle growing from the button. */
export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(initialTheme);
  useEffect(() => { applyTheme(theme); }, [theme]);
  // Several toggles can live on the page; each follows the document's current theme.
  useEffect(() => {
    const root = document.documentElement;
    const observer = new MutationObserver(() => {
      const current: Theme = root.dataset.theme === "dark" ? "dark" : "light";
      setTheme((value) => (value === current ? value : current));
    });
    observer.observe(root, { attributes: true, attributeFilter: ["data-theme"] });
    return () => observer.disconnect();
  }, []);

  const toggle = (event: MouseEvent<HTMLButtonElement>) => {
    const next: Theme = theme === "dark" ? "light" : "dark";
    const doc = document as ViewTransitionDocument;
    if (!doc.startViewTransition || prefersReducedMotion()) { setTheme(next); return; }
    const rect = event.currentTarget.getBoundingClientRect();
    const x = rect.left + rect.width / 2;
    const y = rect.top + rect.height / 2;
    const radius = Math.hypot(Math.max(x, window.innerWidth - x), Math.max(y, window.innerHeight - y));
    // The circle shows a live render of the new page. Without this flag every coloured element
    // (~400 on this page) runs its own colour transition inside it, and the reveal stalls midway.
    const root = document.documentElement;
    root.dataset.themeSwitching = "";
    const transition = doc.startViewTransition(() => {
      flushSync(() => setTheme(next));
      applyTheme(next);
    });
    void transition.ready.then(() => {
      root.animate(
        { clipPath: [`circle(0px at ${x}px ${y}px)`, `circle(${radius}px at ${x}px ${y}px)`] },
        { duration: 500, easing: "ease-out", pseudoElement: "::view-transition-new(root)" },
      );
    }).catch(() => undefined);
    void transition.finished.finally(() => { delete root.dataset.themeSwitching; });
  };

  const label = theme === "dark" ? "切换到浅色" : "切换到深色";
  return (
    <button type="button" className={`theme-toggle is-${theme}`} onClick={toggle} aria-label={label} title={label}>
      <span className="theme-icon sun"><TofiIcon name="sun" size={20} /></span>
      <span className="theme-icon moon"><TofiIcon name="moon" size={20} /></span>
    </button>
  );
}
