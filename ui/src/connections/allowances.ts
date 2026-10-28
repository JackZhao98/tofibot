import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "../api";
import type { StandingAllowance } from "../StandingAllowances";

/**
 * The "Always allowed" rows of one connection: the same list Settings > Permissions shows, filtered to this server. Read only
 * while the connection's page is open (`active`), again when an approval card adds one. `revoke` rejects with the request error;
 * `drop` removes a row from view once its chip has finished leaving.
 */
export function useConnectionAllowances(server: string, active: boolean) {
  const [allowances, setAllowances] = useState<StandingAllowance[]>([]);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const load = useCallback(async () => {
    try {
      const body = await request<{ allowances?: StandingAllowance[] }>("/api/standing-allowances");
      if (alive.current) setAllowances((body.allowances ?? []).filter(item => item.server === server));
    } catch { /* The chips are a convenience; Settings > Permissions lists the same rows. */ }
  }, [server]);
  useEffect(() => {
    if (!active) return;
    void load();
    const changed = () => void load();
    window.addEventListener("tofi:standing-allowances-changed", changed);
    return () => window.removeEventListener("tofi:standing-allowances-changed", changed);
  }, [active, load]);
  const revoke = useCallback(async (item: StandingAllowance) => { await request(`/api/standing-allowances/${encodeURIComponent(item.id)}`, { method: "DELETE" }); }, []);
  const drop = useCallback((id: string) => setAllowances(list => list.filter(item => item.id !== id)), []);
  return { allowances, revoke, drop };
}
