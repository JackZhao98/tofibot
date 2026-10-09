import {useEffect, useState} from "react";
import {request} from "../api";

type Server = {name: string; oauth?: {connected?: boolean}};

/**
 * Count of saved connections that need attention (sign-in missing or lapsed).
 * It reads the saved list once on mount and again whenever `refreshToken` changes
 * (App bumps it on the `config` workspace event). No Runner, VM or per-service probe
 * is made, so opening Settings never wakes a hibernated computer.
 */
export function useConnectionAttention(refreshToken = 0): number {
  const [count, setCount] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    request<{servers?: Server[]}>("/api/extensions/mcp", {signal: controller.signal})
      .then(value => setCount((value.servers ?? []).filter(server => server.oauth && !server.oauth.connected).length))
      .catch(() => { if (!controller.signal.aborted) setCount(0); });
    return () => controller.abort();
  }, [refreshToken]);
  return count;
}
