import { useEffect, useRef } from "react";
import { mountCat } from "../../lib/tofi-avatar/index.js";
import "./primitives.css";

const WAKE_AFTER = 350;

/** Replaces the spinner: a small cat that is asleep, and wakes up if the wait goes on. */
export function LoadingCat({ size = 30 }: { size?: number }) {
  const host = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (!host.current) return;
    const cat = mountCat(host.current, { schemaVersion: 1, shape: "curl", pattern: "calico", palette: "calico", initialState: "asleep" });
    let alive = true;
    const timer = window.setTimeout(async () => {
      try {
        const result = await cat.play("wake");
        if (alive && !result.cancelled) cat.setAutoplay(true);
      } catch {
        // Unmounted mid-clip: nothing left to animate.
      }
    }, WAKE_AFTER);
    return () => { alive = false; window.clearTimeout(timer); cat.destroy(); };
  }, []);
  return <span className="loading-cat" style={{ width: size, height: size }} ref={host} aria-hidden="true" />;
}
