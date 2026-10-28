import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "../api";
import { endpoint, message } from "./shared";

export type ConnectionBot = { bot_id: string; bot_name: string; enabled: boolean };
export type ConnectionBots = { state: "loading" | "ready" | "error"; all: boolean; bots: ConnectionBot[]; saving: boolean; error: string };

/**
 * Which Bots may use one connection (`GET/PUT /api/extensions/mcp/{name}/bots`). Read while the connection's page is open. "All Bots" is the
 * server's default (no limits); "Chosen Bots" lists the current Bots with a checkbox each. A Bot added later starts allowed.
 */
export function useConnectionBots(server: string, active: boolean) {
  const [value, setValue] = useState<ConnectionBots>({ state: "loading", all: true, bots: [], saving: false, error: "" });
  const alive = useRef(true);
  const path = `${endpoint(server)}/bots`;
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const load = useCallback(async () => {
    try {
      const body = await request<{ all: boolean; bots?: ConnectionBot[] }>(path);
      if (alive.current) setValue(current => ({ ...current, state: "ready", all: body.all, bots: body.bots ?? [], error: "" }));
    } catch (e) { if (alive.current) setValue(current => ({ ...current, state: current.state === "ready" ? "ready" : "error", error: message(e) })); }
  }, [path]);
  useEffect(() => { if (active) void load(); }, [active, load]);
  const put = useCallback(async (body: { all: true } | { bot_ids: string[] }, optimistic: Partial<ConnectionBots>) => {
    setValue(current => ({ ...current, ...optimistic, saving: true, error: "" }));
    try { await request(path, { method: "PUT", body: JSON.stringify(body) }); }
    catch (e) { if (alive.current) setValue(current => ({ ...current, error: message(e) })); }
    finally { if (alive.current) { setValue(current => ({ ...current, saving: false })); await load(); } }
  }, [path, load]);
  /** Open the connection to every Bot again. */
  const setAll = useCallback(() => put({ all: true }, { all: true, bots: value.bots.map(bot => ({ ...bot, enabled: true })) }), [put, value.bots]);
  /** Allow exactly the Bots in `ids` (every current Bot is listed, so the others are closed). */
  const setChosen = useCallback((ids: string[]) => put({ bot_ids: ids }, { all: false, bots: value.bots.map(bot => ({ ...bot, enabled: ids.includes(bot.bot_id) })) }), [put, value.bots]);
  return { value, setAll, setChosen };
}
