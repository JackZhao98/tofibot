import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "../api";

/** The local plugin service's own facts (state, tool count). The page never shows them; it needs only which plugins exist. */
export type Plugin = { id: string; state: "sleeping" | "starting" | "ready"; tool_count: number; kind?: string; package?: string; version?: string };
export type GoogleStatus = { connected: boolean; email: string; scope?: "readonly" | "read-send"; /** Services the account authorised (gmail, calendar, drive). */ services?: string[] };
export type LocalState = {
  loaded: boolean;
  /** False when the server has no local plugin service: the directory then offers no local connectors. */
  available: boolean;
  plugins: Plugin[];
  /** The Google account behind the personal Gmail plugin; null when there is no such plugin or it cannot be read. */
  google: GoogleStatus | null;
};
const EMPTY: LocalState = { loaded: false, available: false, plugins: [], google: null };

/** Local plugin facts for the Connections page, read once, on focus and when settings change. `enabled` is false in the desktop app. */
export function useLocalPlugins(enabled: boolean, refreshToken: number) {
  const [state, setState] = useState<LocalState>(enabled ? EMPTY : { ...EMPTY, loaded: true });
  const version = useRef(0);
  const refresh = useCallback(async () => {
    if (!enabled) return;
    const current = ++version.current;
    let next: LocalState;
    try {
      const status = await request<{ available: boolean; plugins?: Plugin[] }>("/api/extensions/local-mcp");
      const plugins = status.available ? status.plugins ?? [] : [];
      let google: GoogleStatus | null = null;
      if (plugins.some(plugin => plugin.id === "gog")) {
        try { google = await request<GoogleStatus>("/api/extensions/local-mcp/gog/oauth/status"); } catch { google = null; }
      }
      next = { loaded: true, available: Boolean(status.available), plugins, google };
    } catch { next = { loaded: true, available: false, plugins: [], google: null }; }
    if (current === version.current) setState(next);
  }, [enabled]);
  useEffect(() => { void refresh(); return () => { version.current++; }; }, [refresh, refreshToken]);
  useEffect(() => {
    if (!enabled) return;
    const focus = () => { if (!document.hidden) void refresh(); };
    window.addEventListener("focus", focus);
    return () => window.removeEventListener("focus", focus);
  }, [enabled, refresh]);
  return { local: state, refreshLocal: refresh };
}
