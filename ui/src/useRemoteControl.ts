import {useCallback, useEffect, useRef, useState} from "react";
import type {RemoteControlFailure, RemoteControlReason, RemoteControlResult, RemoteControlStatus} from "./desktop";

/**
 * Remote control this Mac (docs/design/environments-simplify.md 4.3). The bridge exists only in a Mac app that has the one
 * switch; without it (a browser, an older app) there is no switch and the page explains how to add a Mac. The page only
 * asks: the native confirmation, the pairing and the permission checks all happen in the app's main process.
 */
export const failureCodes: RemoteControlFailure[] = ["no_session", "personal_disabled", "rate_limited", "limit_reached", "connection_changed", "keychain_unavailable", "offline", "no_connection", "permission_missing", "failed"];
const reasons: RemoteControlReason[] = [...failureCodes, "half_on", "needs_accessibility", "needs_screen", "needs_both", "needs_relaunch", "connecting", "paused_stop", "paused_takeover", "revoked", "removal_pending"];
const phases = ["idle", "active", "lingering", "paused"];

/** Reads whatever the app sent into the status the page draws; an unknown reason degrades to the generic failure. */
export function normalizeRemoteControl(body: unknown): RemoteControlStatus | null {
  if (!body || typeof body !== "object") return null;
  const raw = body as Record<string, any>;
  const permissions = (raw.permissions ?? {}) as Record<string, unknown>;
  return {
    enabled: raw.enabled === true, paired: raw.paired === true, deviceID: typeof raw.deviceID === "string" && raw.deviceID ? raw.deviceID : null,
    name: typeof raw.name === "string" ? raw.name : "",
    permissions: {screen: permissions.screen === true, accessibility: permissions.accessibility === true},
    needsRelaunch: raw.needsRelaunch === true, phase: phases.includes(raw.phase) ? raw.phase : "idle", botName: typeof raw.botName === "string" ? raw.botName : "",
    pausedUntil: typeof raw.pausedUntil === "number" ? raw.pausedUntil : null, hotkeyOk: raw.hotkeyOk !== false,
    reason: raw.reason == null ? null : reasons.includes(raw.reason) ? raw.reason : "failed",
  };
}

/** The this-Mac row's state word, the one sentence under it and its one inline action. */
export type ThisMacState = {word: "available" | "in_use" | "unavailable"; reason: RemoteControlReason | null; action: "turn_on_both" | "authorize" | "relaunch" | "retry" | "stop" | null};
const retryable: RemoteControlReason[] = ["no_session", "rate_limited", "offline", "failed", "no_connection", "permission_missing"];
export function thisMacState(status: RemoteControlStatus): ThisMacState {
  const reason = status.reason;
  if (reason === "half_on") return {word: "unavailable", reason, action: "turn_on_both"};
  if (reason === "needs_relaunch") return {word: "unavailable", reason, action: "relaunch"};
  if (reason === "needs_accessibility" || reason === "needs_screen" || reason === "needs_both") return {word: "unavailable", reason, action: "authorize"};
  if (reason && retryable.includes(reason)) return {word: "unavailable", reason, action: "retry"};
  if (reason) return {word: "unavailable", reason, action: null};
  if (status.phase === "active" || status.phase === "lingering") return {word: "in_use", reason: null, action: "stop"};
  return {word: "available", reason: null, action: null};
}

type Busy = "" | "set" | "authorize" | "stop" | "relaunch";

/** The switch's state. `active` is whether the settings page is on screen; nothing is read or subscribed while it is not. */
export function useRemoteControl(active: boolean) {
  // Types only from ./desktop: its runtime half binds workspace IPC that the Mac settings page must not touch.
  const bridge = window.tofiDesktop?.remoteControl;
  const [status, setStatus] = useState<RemoteControlStatus | null>(null);
  const [busy, setBusy] = useState<Busy>("");
  const [failure, setFailure] = useState<RemoteControlFailure | null>(null);
  const [denied, setDenied] = useState<{bot_id: string; bot_name: string}[]>([]);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const apply = useCallback((next: unknown) => { const value = normalizeRemoteControl(next); if (value && alive.current) setStatus(value); }, []);
  useEffect(() => {
    if (!bridge || !active) return;
    let closed = false;
    bridge.status().then(next => { if (!closed) apply(next); }, () => {});
    const off = bridge.onStatus(next => { if (!closed) apply(next); });
    return () => { closed = true; off(); };
  }, [bridge, active, apply]);
  const enabled = status?.enabled === true;
  useEffect(() => {
    if (!bridge || !active || !enabled) { setDenied([]); return; }
    let closed = false;
    bridge.deniedBots().then(result => { if (!closed && Array.isArray(result?.bots)) setDenied(result.bots); }, () => {});
    return () => { closed = true; };
  }, [bridge, active, enabled]);

  async function run(kind: Exclude<Busy, "">, work: () => Promise<RemoteControlResult>) {
    if (busy || !bridge) return;
    setBusy(kind); setFailure(null);
    try {
      const result = await work();
      if (!alive.current) return;
      apply(result?.status);
      // A cancel in the macOS dialog is not an error: the switch stays where it was and nothing is said.
      if (result && result.ok === false && (failureCodes as string[]).includes(result.code)) setFailure(result.code as RemoteControlFailure);
    } catch { if (alive.current) setFailure("failed"); }
    finally { if (alive.current) setBusy(""); }
  }
  return {
    available: Boolean(bridge), status, busy, failure, denied,
    set: (on: boolean) => run("set", () => bridge!.set(on)),
    authorize: () => run("authorize", () => bridge!.authorize()),
    stop: () => run("stop", () => bridge!.stop()),
    relaunch: () => run("relaunch", () => bridge!.relaunch()),
    undoDeny: async (botID: string) => {
      try {
        const result = await bridge!.undoDeny(botID);
        if (alive.current && Array.isArray(result?.bots)) setDenied(result.bots);
      } catch { /* the native dialog or the channel refused: the list stays */ }
    },
  };
}
export type RemoteControl = ReturnType<typeof useRemoteControl>;
