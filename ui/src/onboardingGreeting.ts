import { useEffect, useState } from "react";

/**
 * First-Bot greeting playback: a "setting everything up" screen with a mock progress bar, then the Bot's greeting
 * messages one by one, each after a short typing pause. It plays once per Bot (remembered in localStorage, and in
 * a module Set when storage is unavailable). All timer state lives in this hook's owner, which is the chat pane.
 */

export const GREETING_SETUP_MS = 2500;
const KEY = "tofi:greeted:";
const playedThisPage = new Set<string>();

export function greetingTypingMs(text: string): number {
  return Math.min(1400, Math.max(800, 700 + text.length * 8));
}

function hasPlayed(botId: string): boolean {
  if (playedThisPage.has(botId)) return true;
  try { return localStorage.getItem(KEY + botId) === "1"; } catch { return false; }
}

function markPlayed(botId: string) {
  playedThisPage.add(botId);
  try { localStorage.setItem(KEY + botId, "1"); } catch { /* the module Set still holds it for this page session */ }
}

export type GreetingPlayback = {
  /** "setup": progress screen, "typing": the next message is being typed, "done": render normally. */
  stage: "setup" | "typing" | "done";
  /** How many of the greeting messages are revealed. */
  shown: number;
};

const DONE: GreetingPlayback = { stage: "done", shown: Number.POSITIVE_INFINITY };

/**
 * botId: the Bot whose DM is open. messages: its greeting texts, in order, while the conversation holds only those
 * (empty otherwise). Any change that empties it (a user message, a switch to another chat) ends the playback and
 * reveals the rest at once.
 */
export function useGreetingPlayback(botId: string | undefined, pending: boolean, messages: string[], reduced: boolean): GreetingPlayback {
  const [run, setRun] = useState<{ id: string; step: number } | null>(null);
  const eligible = Boolean(botId && pending && messages.length > 0);
  const count = messages.length;
  useEffect(() => {
    if (!botId || !eligible) { setRun(null); return; }
    if (hasPlayed(botId)) return;
    markPlayed(botId);
    if (!reduced) setRun({ id: botId, step: -1 });
  }, [botId, eligible, reduced]);
  const active = run && run.id === botId && eligible ? run : null;
  const step = active?.step;
  useEffect(() => {
    if (step === undefined || !botId) return;
    if (step >= count) { setRun(null); return; }
    const wait = step < 0 ? GREETING_SETUP_MS : greetingTypingMs(messages[step] ?? "");
    const timer = window.setTimeout(() => setRun(current => current && current.id === botId ? { id: botId, step: step + 1 } : current), wait);
    return () => window.clearTimeout(timer);
    // messages is read for its length only; its contents are fixed for one Bot.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step, botId, count]);
  // The start effect runs after paint; hold the setup screen from the first render so the greeting never flashes.
  if (!active) return eligible && !reduced && botId && !run && !hasPlayed(botId) ? { stage: "setup", shown: 0 } : DONE;
  return active.step < 0 ? { stage: "setup", shown: 0 } : { stage: "typing", shown: active.step };
}
