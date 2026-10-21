// Version counter behind the shared model catalog. It has no imports so api.ts can bump it
// without a cycle: provider changes (Codex sign-in, disconnect, API key save or removal) call
// invalidateModelCatalog(), and every mounted picker (useModelCatalog) refetches.
let version = 0;
const listeners = new Set<() => void>();

export function modelCatalogVersion(): number { return version; }

export function subscribeModelCatalog(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

/** The set of usable providers changed: whatever the catalog showed is stale. */
export function invalidateModelCatalog(): void {
  version++;
  for (const listener of [...listeners]) listener();
}
