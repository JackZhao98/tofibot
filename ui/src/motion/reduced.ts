const REDUCED_QUERY = "(prefers-reduced-motion: reduce)";

/** True when the person asked the system for reduced motion. */
export function prefersReducedMotion(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia(REDUCED_QUERY).matches;
}
