import { useEffect, useRef, useState, useSyncExternalStore, type RefObject } from "react";

const REDUCED_QUERY = "(prefers-reduced-motion: reduce)";

export function prefersReducedMotion(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia(REDUCED_QUERY).matches;
}

function subscribeReduced(onChange: () => void) {
  if (typeof window.matchMedia !== "function") return () => undefined;
  const media = window.matchMedia(REDUCED_QUERY);
  media.addEventListener("change", onChange);
  return () => media.removeEventListener("change", onChange);
}

export function useReducedMotion(): boolean {
  return useSyncExternalStore(subscribeReduced, prefersReducedMotion, () => false);
}

/** True once the element has been at least `threshold` visible; stays true. */
export function useSeenOnce<T extends Element>(threshold = 0.35): [RefObject<T | null>, boolean] {
  const ref = useRef<T | null>(null);
  const [seen, setSeen] = useState(false);
  useEffect(() => {
    const node = ref.current;
    if (!node || seen) return;
    if (typeof IntersectionObserver !== "function") { setSeen(true); return; }
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) { setSeen(true); observer.disconnect(); }
    }, { threshold });
    observer.observe(node);
    return () => observer.disconnect();
  }, [seen, threshold]);
  return [ref, seen];
}

/** Promise-based pause that resolves early (with false) once the signal aborts. */
export function wait(ms: number, signal: AbortSignal): Promise<boolean> {
  return new Promise((resolve) => {
    if (signal.aborted) { resolve(false); return; }
    const timer = window.setTimeout(() => { signal.removeEventListener("abort", stop); resolve(true); }, ms);
    function stop() { window.clearTimeout(timer); resolve(false); }
    signal.addEventListener("abort", stop, { once: true });
  });
}

/** Await a Web Animation; a cancelled animation resolves instead of throwing. */
export async function settled(animation: Animation): Promise<void> {
  try { await animation.finished; } catch { /* cancelled by a replay or unmount */ }
}
