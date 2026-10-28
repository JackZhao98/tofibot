import {useEffect, useState} from "react";
import {request} from "../api";

/** Dispatched by the Connections page after it reads a fresh list. */
export const CONNECTIONS_CHANGED = "tofi:connections-changed";

type Server = {name: string; enabled?: boolean; oauth?: {connected?: boolean}; status?: {ok?: boolean}};

/** A connection needs the person when its sign-in is missing or lapsed, or its last check failed (an API that is off, a server that does not answer). A paused one is on purpose. */
export const serverNeedsAttention = (server: Server) => server.enabled !== false && (Boolean(server.oauth && !server.oauth.connected) || server.status?.ok === false);

/**
 * Count of saved connections that need attention: sign-in missing or lapsed, or the last stored check failed
 * (error or API not enabled). It reads the saved list once on mount and again whenever `refreshToken` changes
 * (App bumps it on the `config` workspace event). No Runner, VM or per-service probe is made, so opening Settings
 * never wakes a hibernated computer.
 */
export function useConnectionAttention(refreshToken = 0): number {
  const [count, setCount] = useState(0);
  const [bump, setBump] = useState(0);
  // The Connections page announces a changed list (a check finished, a sign-in completed), so the badge never lags the page.
  useEffect(() => { const again = () => setBump(value => value + 1); window.addEventListener(CONNECTIONS_CHANGED, again); return () => window.removeEventListener(CONNECTIONS_CHANGED, again); }, []);
  useEffect(() => {
    const controller = new AbortController();
    request<{servers?: Server[]}>("/api/extensions/mcp", {signal: controller.signal})
      .then(value => setCount((value.servers ?? []).filter(serverNeedsAttention).length))
      .catch(() => { if (!controller.signal.aborted) setCount(0); });
    return () => controller.abort();
  }, [refreshToken, bump]);
  return count;
}
