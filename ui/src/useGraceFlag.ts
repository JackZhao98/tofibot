import { useEffect, useState } from "react";

/** True once `active` has held for `graceMs`; false the moment it clears. A blip shorter than the grace shows nothing. */
export function useGraceFlag(active: boolean, graceMs: number): boolean {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!active) { setShown(false); return; }
    const timer = window.setTimeout(() => setShown(true), graceMs);
    return () => window.clearTimeout(timer);
  }, [active, graceMs]);
  return shown;
}
