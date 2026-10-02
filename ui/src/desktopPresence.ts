import { useEffect, useState } from "react";
import { request } from "./api";

export type DesktopPointer = { x: number; y: number; width: number; height: number; action: string; updated_at: string; bot_id: string; run_id: string };
export type DesktopOwnership = {
  owner: { bot_id: string; run_id: string; kind: "bot" | "human"; conversation_id?: string } | null;
  waiting: Array<{ bot_id: string; run_id: string; queued_at: string }>;
  pointer?: DesktopPointer | null;
};
export type DesktopPresenceState = { ownership: DesktopOwnership | null; unavailable: boolean };

export function useDesktopPresence(enabled = true): DesktopPresenceState {
  const [state, setState] = useState<DesktopPresenceState>({ ownership: null, unavailable: false });
  useEffect(() => {
    if (!enabled) return;
    let alive = true;
    let timer = 0;
    let failures = 0;
    const abort = new AbortController();
    const poll = async () => {
      try {
        if (document.hidden) return;
        const ownership = await request<DesktopOwnership>("/api/computer/desktop-ownership", { signal: abort.signal });
        failures = 0;
        if (alive) setState({ ownership, unavailable: false });
      } catch {
        failures++;
        // A single missed HTTPS poll is normal during tab/Bot switches. Keep
        // the last confirmed owner until two consecutive polls fail so the UI
        // does not flash "control unavailable" for a few hundred milliseconds.
        if (alive && failures >= 2) setState(previous => ({ ...previous, unavailable: true }));
      } finally {
        if (alive) timer = window.setTimeout(poll, 1000);
      }
    };
    void poll();
    return () => { alive = false; abort.abort(); window.clearTimeout(timer); };
  }, [enabled]);
  return state;
}

// Marker geometry follows object-fit:contain for the real 1280×800 desktop.
// Input coordinates can be from a smaller screenshot, as accepted by the guest.
export function desktopPointerPosition(pointer: DesktopPointer, boxWidth: number, boxHeight: number) {
  if (![pointer.x, pointer.y, pointer.width, pointer.height, boxWidth, boxHeight].every(Number.isFinite) || pointer.width <= 0 || pointer.height <= 0 || boxWidth <= 0 || boxHeight <= 0) return null;
  const scale = Math.min(boxWidth / 1280, boxHeight / 800);
  const width = 1280 * scale, height = 800 * scale;
  return {
    left: (boxWidth - width) / 2 + Math.min(1279, Math.max(0, Math.trunc(pointer.x * 1280 / pointer.width))) * scale,
    top: (boxHeight - height) / 2 + Math.min(799, Math.max(0, Math.trunc(pointer.y * 800 / pointer.height))) * scale,
  };
}

export function visibleDesktopPointer(presence: DesktopPresenceState, humanControlled: boolean, timestamp = Date.now(), maxAgeMs = 5000): DesktopPointer | null {
  const { owner, pointer } = presence.ownership ?? {};
  if (presence.unavailable || humanControlled || owner?.kind !== "bot" || !pointer || pointer.action !== "click" || pointer.bot_id !== owner.bot_id || pointer.run_id !== owner.run_id) return null;
  const age = timestamp - Date.parse(pointer.updated_at);
  return Number.isFinite(age) && age >= 0 && age < maxAgeMs ? pointer : null;
}
