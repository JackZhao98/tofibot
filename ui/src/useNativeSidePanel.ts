import { useCallback, useEffect, useRef, useState } from "react";
import type { BrowserPanelState } from "./desktop";

/** Reasons the Mac app names when the panel cannot open. A Mac that is still connecting reads as one plain reason. */
const NAMED = ["browser_not_enabled", "browser_clear_failed", "browser_control_lost"] as const;
const CONNECTING = ["no_connection", "offline", "browser_transport_unavailable", "browser_session_changed"];
export type PanelFailure = typeof NAMED[number] | "no_connection";
export function panelFailure(error: unknown): PanelFailure {
  const text = String((error as Error)?.message ?? error);
  if (CONNECTING.some(code => text.includes(code))) return "no_connection";
  return NAMED.find(code => text.includes(code)) ?? "browser_control_lost";
}

/** The native panel owns its state; a person's action only requests a change. The Mac app narrows this page's viewport
 * while the panel is open, so the layout needs no room of its own here. */
export function useNativeSidePanel() {
  const api = window.tofiDesktop?.botBrowser;
  // Older desktop artifacts have no panel contract.
  const bridge = typeof api?.panel === "function" && typeof api.onPanel === "function" && typeof api.panelState === "function" ? api : undefined;
  const [open, setOpen] = useState(false);
  const [failure, setFailure] = useState<PanelFailure | null>(null);
  useEffect(() => {
    if (!bridge) return;
    let alive = true;
    let pushed = false;
    const apply = (state: BrowserPanelState) => { if (alive) setOpen(state.open); };
    const off = bridge.onPanel(state => { pushed = true; apply(state); });
    void bridge.panelState().then(state => { if (!pushed) apply(state); }, () => {});
    return () => { alive = false; off(); };
  }, [bridge]);
  useEffect(() => {
    if (!failure) return;
    const timer = window.setTimeout(() => setFailure(null), 6000);
    return () => window.clearTimeout(timer);
  }, [failure]);
  // Only the latest request may report a failure: an older one that fails late is stale.
  const latest = useRef(0);
  const request = useCallback((next: boolean) => {
    if (!bridge) return;
    const id = ++latest.current;
    setFailure(null);
    void bridge.panel(next).catch(error => { if (id === latest.current) setFailure(panelFailure(error)); });
  }, [bridge]);
  return { bridge, open, request, failure };
}
