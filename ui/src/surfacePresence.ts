import { useEffect, useState } from "react";

const reduced = () => typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;
const same = <T>(a: readonly T[], b: readonly T[]) => a.length === b.length && a.every((item, index) => item === b[index]);

/**
 * The widgets the person has open, plus the ones that closed less than `ms` ago, so each can play its exit before it
 * leaves the tree. `shown` follows `order`; `leaving` names the ones on their way out. With reduced motion nothing
 * lingers. The retained set is worked out during render (not in an effect), so the frame that removes a widget from
 * `ids` already has it, flagged, and never paints an empty hole.
 */
export function useLeaving<T extends string>(ids: readonly T[], order: readonly T[], ms: number): { shown: T[]; leaving: ReadonlySet<T> } {
  const [seen, setSeen] = useState<readonly T[]>(ids);
  const [gone, setGone] = useState<readonly T[]>([]);
  if (seen !== ids && !same(seen, ids)) {
    const left = seen.filter(id => !ids.includes(id));
    setSeen(ids);
    setGone(current => [...current.filter(id => !ids.includes(id)), ...(reduced() ? [] : left.filter(id => !current.includes(id)))]);
  } else if (seen !== ids) setSeen(ids);
  useEffect(() => {
    if (!gone.length) return;
    const timer = window.setTimeout(() => setGone([]), ms);
    return () => window.clearTimeout(timer);
  }, [gone, ms]);
  const leaving = new Set(gone.filter(id => !ids.includes(id)));
  return { shown: order.filter(id => ids.includes(id) || leaving.has(id)), leaving };
}

/**
 * The value that `value` replaced, kept for `ms` after a change from one non-null value to another: the sidebar cross-fades
 * from the old panel to the new. Closing (to null) and opening (from null) are the surface's own enter and exit, so they
 * return null here.
 */
export function usePreviousWhileFading<T>(value: T | null, ms: number): T | null {
  const [seen, setSeen] = useState<T | null>(value);
  const [previous, setPrevious] = useState<T | null>(null);
  if (seen !== value) {
    setSeen(value);
    setPrevious(seen !== null && value !== null && !reduced() ? seen : null);
  }
  useEffect(() => {
    if (previous === null) return;
    const timer = window.setTimeout(() => setPrevious(null), ms);
    return () => window.clearTimeout(timer);
  }, [previous, ms]);
  return previous;
}
