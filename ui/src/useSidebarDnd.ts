import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { DndContext, Options, Pending } from "./sidebarDndEngine";

export type { DndAnnounce, DndCommit } from "./sidebarDndEngine";

/**
 * Sidebar drag, drop and Alt+Arrow reordering. The engine (sidebarDndEngine.ts: pointer tracking, rect maths, the settle
 * animation) is a separate chunk fetched in idle time after the list first mounts, so it stays out of the shell entry; the
 * hook only carries what must survive renders.
 */
export function useSidebarDnd(options: Options) {
  const live = useRef(options);
  live.current = options;
  const pending = useRef<Pending | null>(null);
  const settleTimer = useRef<number | undefined>(undefined);
  const engine = useRef<typeof import("./sidebarDndEngine") | null>(null);
  const ctx = useRef<DndContext>({ live, pending, settleTimer }).current;

  // The list mounts after the boot screen: follow the element rather than the ref object.
  const [mounted, setMounted] = useState<HTMLElement | null>(null);
  useEffect(() => { const current = options.scrollRef.current; if (current !== mounted) setMounted(current); });

  useLayoutEffect(() => {
    if (mounted && pending.current) engine.current?.settleSidebarDnd(mounted, ctx);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [options.signature]);

  useEffect(() => {
    if (!mounted || !options.enabled) return;
    let detach: (() => void) | undefined;
    let cancelled = false;
    if (engine.current) { const stop = engine.current.mountSidebarDnd(mounted, ctx); return stop; }
    const load = () => import("./sidebarDndEngine").then(module => { if (cancelled) return; engine.current = module; detach = module.mountSidebarDnd(mounted, ctx); });
    const idle = (window as { requestIdleCallback?: (callback: () => void, options?: { timeout: number }) => number }).requestIdleCallback;
    const handle = idle ? idle(() => void load(), { timeout: 1200 }) : window.setTimeout(() => void load(), 400);
    return () => { cancelled = true; if (idle) (window as { cancelIdleCallback?: (id: number) => void }).cancelIdleCallback?.(handle); else window.clearTimeout(handle); detach?.(); };
  }, [options.enabled, mounted, ctx]);
}
