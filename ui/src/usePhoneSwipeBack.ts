import { useEffect, useRef } from "react";

/**
 * The phone swipe-back gesture (D33/D35; phoneSwipeGesture.ts). `back` returns to the list and must commit synchronously
 * (flushSync), because the gesture hands the panes back to CSS in the same frame. The engine is its own chunk, loaded in
 * idle time only once the window is phone-sized, so neither the entry nor a wide window carries it.
 */
export function usePhoneSwipeBack(back: () => void) {
  const live = useRef(back);
  live.current = back;
  useEffect(() => {
    const phone = window.matchMedia("(max-width: 767px)");
    let stop: (() => void) | undefined, off = false, started = false;
    const load = () => {
      if (!phone.matches || started) return;
      started = true;
      phone.removeEventListener("change", load);
      void import("./phoneSwipeEngine").then(module => { if (!off) stop = module.mount(live); });
    };
    phone.addEventListener("change", load);
    const timer = window.setTimeout(load, 500);
    return () => { off = true; window.clearTimeout(timer); phone.removeEventListener("change", load); stop?.(); };
  }, []);
}
