import { useSyncExternalStore } from "react";
import type { ComputerCapability } from "./types";

/**
 * What this workspace's computer can do, from `computer` in GET /api/config
 * (also GET /api/computer/capabilities). The app writes it once per config
 * read; Settings, the message list and the header read it here. `null` means
 * the server did not say (an older server): behave as if a computer exists.
 */
let current: ComputerCapability | null = null;
const listeners = new Set<() => void>();

export function setComputerCapability(next: ComputerCapability | undefined | null) {
  const value = next ?? null;
  if (JSON.stringify(value) === JSON.stringify(current)) return;
  current = value;
  listeners.forEach((listener) => listener());
}

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
};

export function useComputerCapability(): ComputerCapability | null {
  return useSyncExternalStore(subscribe, () => current, () => null);
}

/** False only when the server states it has no computer backend. */
export function useHasComputer(): boolean {
  return useComputerCapability()?.backend !== "none";
}

/**
 * True when this server can run a computer, even if this account's is detached. The Attach and Detach surfaces and the
 * Admin console's Computer section follow this; tools and computer-only settings follow useHasComputer.
 */
export function useHostHasComputer(): boolean {
  const cap = useComputerCapability();
  return (cap?.host_backend ?? cap?.backend) !== "none";
}

export const computerHostCommand = "sudo tofi computer add kvm";
