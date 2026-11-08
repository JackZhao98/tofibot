import {useCallback, useEffect, useRef, useState} from "react";
import {request} from "./api";

/**
 * Environments: the places a Bot works (docs/design/environments.md). One aggregated read, `GET /api/environments`, names every
 * environment, what the person may do with it (`actions`) and what can be added (`add`). The page and Bot settings read it
 * here, so neither guesses a state from another endpoint.
 */
export type EnvironmentKind = "vm" | "e2b" | "mac";
export type EnvironmentState = "ready" | "starting" | "resuming" | "asleep" | "stopped" | "standby" | "failed" | "preparing" | "online" | "offline" | "unknown";
export type EnvironmentAccess = "all_bots" | "allowed_on_mac" | "none";
export type EnvironmentAction = "retry" | "wake" | "configure" | "use" | "remove" | "disconnect";
export type Environment = {
  id: string; kind: EnvironmentKind; name: string; state: EnvironmentState; error_code: string;
  platform?: string; last_seen?: string; bot_computer: boolean; access: EnvironmentAccess; actions: EnvironmentAction[];
};
export type EnvironmentsView = {
  environments: Environment[];
  add: {e2b: boolean; mac: boolean; vm_command: string};
  e2b: {configured: boolean; key_hint: string; phase: string; error_code: string; error: string} | null;
  /** Whose Bot computer this is (user groups): `host` and `own` are the person's; `provided` the organization's; `none` has no Bot computer. Absent on an older server. */
  policy?: {mode: string; source: string; via: string} | null;
};

const kinds: EnvironmentKind[] = ["vm", "e2b", "mac"];
const states: EnvironmentState[] = ["ready", "starting", "resuming", "asleep", "stopped", "standby", "failed", "preparing", "online", "offline", "unknown"];
const actions: EnvironmentAction[] = ["retry", "wake", "configure", "use", "remove", "disconnect"];

/** Reads whatever shape the server sent into the view the page draws; an unknown value degrades to the safest one. */
export function normalizeEnvironments(body: unknown): EnvironmentsView {
  const raw = (body ?? {}) as Record<string, any>;
  const list = Array.isArray(raw.environments) ? raw.environments : [];
  const environments = list.filter(item => item && typeof item.id === "string" && kinds.includes(item.kind)).map((item: any): Environment => ({
    id: item.id, kind: item.kind, name: typeof item.name === "string" ? item.name : "",
    state: states.includes(item.state) ? item.state : "unknown", error_code: typeof item.error_code === "string" ? item.error_code : "",
    platform: typeof item.platform === "string" ? item.platform : undefined, last_seen: typeof item.last_seen === "string" ? item.last_seen : undefined,
    bot_computer: item.bot_computer === true,
    access: item.access === "all_bots" || item.access === "allowed_on_mac" ? item.access : "none",
    actions: Array.isArray(item.actions) ? item.actions.filter((action: unknown): action is EnvironmentAction => actions.includes(action as EnvironmentAction)) : [],
  }));
  const add = (raw.add ?? {}) as Record<string, unknown>;
  const e2b = raw.e2b && typeof raw.e2b === "object" ? raw.e2b as Record<string, unknown> : null;
  return {
    environments,
    add: {e2b: add.e2b === true, mac: add.mac === true, vm_command: typeof add.vm_command === "string" ? add.vm_command : ""},
    policy: raw.policy && typeof raw.policy === "object" ? {mode: String(raw.policy.mode ?? "host"), source: String(raw.policy.source ?? "own"), via: String(raw.policy.via ?? "")} : null,
    e2b: e2b ? {configured: e2b.configured === true, key_hint: String(e2b.key_hint ?? ""), phase: String(e2b.phase ?? "none"), error_code: String(e2b.error_code ?? ""), error: String(e2b.error ?? "")} : null,
  };
}

export const readEnvironments = (signal?: AbortSignal) => request<unknown>("/api/environments", {signal}).then(normalizeEnvironments);

/** Something is on its way to ready: only then is the list worth polling. */
export function environmentsSettling(view: EnvironmentsView | null): boolean {
  if (!view) return false;
  if (view.environments.some(item => item.state === "starting" || item.state === "resuming" || item.state === "preparing")) return true;
  return view.e2b?.phase === "validating" || view.e2b?.phase === "preparing";
}

const POLL_MS = 10_000;

/**
 * One read while `active`, again when the window regains focus, and every 10 s only while an environment is starting, waking or
 * being prepared (nothing else on the page changes by itself). A background poll never flips `loading` and keeps the list on screen.
 * `replace` takes the body a write answered with, so the page shows the result without another read.
 */
export function useEnvironments(active = true) {
  const [data, setData] = useState<EnvironmentsView | null>(null);
  const [failed, setFailed] = useState(false);
  const alive = useRef(true);
  const controller = useRef<AbortController | null>(null);
  const load = useCallback(async () => {
    controller.current?.abort();
    const own = new AbortController();
    controller.current = own;
    try {
      const next = await readEnvironments(own.signal);
      if (!alive.current || own.signal.aborted) return;
      setData(next); setFailed(false);
    } catch {
      if (alive.current && !own.signal.aborted) setFailed(true);
    }
  }, []);
  useEffect(() => { alive.current = true; return () => { alive.current = false; controller.current?.abort(); }; }, []);
  useEffect(() => {
    if (!active) return;
    void load();
    const focus = () => { if (!document.hidden) void load(); };
    window.addEventListener("focus", focus);
    document.addEventListener("visibilitychange", focus);
    return () => { window.removeEventListener("focus", focus); document.removeEventListener("visibilitychange", focus); };
  }, [active, load]);
  const settling = environmentsSettling(data);
  useEffect(() => {
    if (!active || !settling) return;
    const timer = window.setInterval(() => { if (!document.hidden) void load(); }, POLL_MS);
    return () => window.clearInterval(timer);
  }, [active, settling, load]);
  const replace = useCallback((body: unknown) => { controller.current?.abort(); setData(normalizeEnvironments(body)); setFailed(false); }, []);
  const retry = useCallback(() => { setFailed(false); void load(); }, [load]);
  return {data, failed, retry, replace, reload: load};
}
