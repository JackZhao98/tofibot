/**
 * Motion parameters that the owner can tune live in a dev build (the Tune panel, `src/dev/TuningPanel.tsx`). A production build always reads
 * the default: nothing is stored and the panel is not bundled. Read a value at the moment it is used (when a motion starts), so a change in
 * the panel applies to the next motion without a reload.
 */
export type Tunable = { key: string; group: string; label: string; value: number; min: number; max: number; step: number };

const defs = new Map<string, Tunable>();
const overrides = new Map<string, number>();
const listeners = new Set<() => void>();
const STORE_KEY = "tofi.devTunables";

if (import.meta.env.DEV) {
  try {
    const saved = JSON.parse(localStorage.getItem(STORE_KEY) ?? "{}") as Record<string, unknown>;
    for (const [key, value] of Object.entries(saved)) if (typeof value === "number" && Number.isFinite(value)) overrides.set(key, value);
  } catch { /* no saved values */ }
}

/** Registers a parameter and returns a reader for its current value. */
export function tunable(def: Tunable): () => number {
  defs.set(def.key, def);
  listeners.forEach(listener => listener());
  return () => (import.meta.env.DEV ? overrides.get(def.key) ?? def.value : def.value);
}

export const tunables = (): Tunable[] => [...defs.values()];
export const tunableValue = (key: string): number => overrides.get(key) ?? defs.get(key)?.value ?? 0;

export function setTunable(key: string, value: number | null) {
  if (!import.meta.env.DEV) return;
  if (value === null) overrides.delete(key); else overrides.set(key, value);
  try { localStorage.setItem(STORE_KEY, JSON.stringify(Object.fromEntries(overrides))); } catch { /* storage blocked */ }
  listeners.forEach(listener => listener());
}

export function onTunables(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; }
