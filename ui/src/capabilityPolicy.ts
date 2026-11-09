import { useSyncExternalStore } from "react";
import type { CapabilitiesView, CapabilityId, CapabilityView } from "./types";

/**
 * What the organization decides for this account, from `capabilities` in GET /api/config (user groups, design 5.2). The app
 * writes it once per config read; Settings, Onboarding and the pages read it here, the way computerCapability.ts does for the
 * computer. `null` means the server did not say (an older server, or a single-account install): everything is the person's own.
 * There is no admin fork: an administrator sees their own account's policy like anybody else.
 */
let current: CapabilitiesView | null = null;
const listeners = new Set<() => void>();

export function setCapabilityPolicy(next: CapabilitiesView | undefined | null) {
  const value = next ?? null;
  if (JSON.stringify(value) === JSON.stringify(current)) return;
  current = value;
  listeners.forEach((listener) => listener());
}

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
};

/** What an account without a policy does: the person's own resource; the computer is the host's. */
const DEFAULTS: CapabilitiesView = {
  ai_models: { mode: "own", source: "own" },
  computer: { mode: "host", source: "own" },
  web_search: { mode: "own", source: "own" },
  web_fetch: { mode: "own", source: "own" },
};

export type Policy = {
  /** One capability; a missing one reads as the person's own. */
  of: (id: CapabilityId) => CapabilityView;
  /** The organization provides this capability. */
  provided: (id: CapabilityId) => boolean;
  /** The capability is switched off or fixed to the built-in for this account (nothing to set up). */
  off: (id: CapabilityId) => boolean;
  /** Some capability is provided or restricted by the organization. */
  managed: boolean;
  /** The first capability the organization manages, in Settings order, for a link into the page that explains it. */
  first: CapabilityId | null;
};

function build(view: CapabilitiesView | null): Policy {
  const of = (id: CapabilityId): CapabilityView => view?.[id] ?? DEFAULTS[id];
  const ids: CapabilityId[] = ["ai_models", "web_search", "web_fetch", "computer"];
  const first = ids.find((id) => of(id).source !== "own") ?? null;
  return { of, provided: (id) => of(id).source === "organization", off: (id) => of(id).source === "none", managed: first !== null, first };
}

const NONE = build(null);
/** The policy a config reading stands for, without the hook (tests, and anything that is not a component). */
export const policyFrom = (view: CapabilitiesView | null | undefined): Policy => build(view ?? null);

let cache: { view: CapabilitiesView | null; policy: Policy } | null = null;
const read = (): Policy => {
  if (!cache || cache.view !== current) cache = { view: current, policy: build(current) };
  return cache.policy;
};

export function usePolicy(): Policy {
  return useSyncExternalStore(subscribe, read, () => NONE);
}
