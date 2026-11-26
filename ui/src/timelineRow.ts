/**
 * Equality for a timeline row's props. A row is skipped only when everything it draws is unchanged, so
 * the comparison looks through containers that the workspace rebuilds on every render (a filtered
 * array, a map of errors, a receipt) but stops at the records themselves: those keep their identity until
 * they really change. It can only err toward drawing again, never toward showing something stale.
 */
export function looseEqual(a: unknown, b: unknown, depth = 2): boolean {
  if (Object.is(a, b)) return true;
  if (depth <= 0 || typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (!looseEqual(a[i], b[i], depth - 1)) return false;
    return true;
  }
  const protoA = Object.getPrototypeOf(a);
  if ((protoA !== Object.prototype && protoA !== null) || Object.getPrototypeOf(b) !== protoA) return false;
  const keysA = Object.keys(a);
  if (keysA.length !== Object.keys(b).length) return false;
  for (const key of keysA) {
    if (!(key in b) || !looseEqual((a as Record<string, unknown>)[key], (b as Record<string, unknown>)[key], depth - 1)) return false;
  }
  return true;
}

/** Shallow identity comparison of two signatures. */
function sameSig(a: readonly unknown[], b: readonly unknown[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (!Object.is(a[i], b[i])) return false;
  return true;
}

/**
 * A row model that carries rendered nodes (a task block anchored to the row) holds nodes that are new every render, so they are not
 * compared. It carries `ownerSig` beside them: the identities of every record those nodes draw from (see taskBlockSig). The row is
 * skipped only when the signatures match; a model with nodes and no signature is never equal.
 */
export function rowModelEqual<T extends { ownerNodes?: unknown; ownerSig?: readonly unknown[] }>(a: T, b: T): boolean {
  if (a === b) return true;
  if (a.ownerNodes || b.ownerNodes) {
    if (!a.ownerSig || !b.ownerSig || !sameSig(a.ownerSig, b.ownerSig)) return false;
  }
  const keys = Object.keys(a) as (keyof T)[];
  if (keys.length !== Object.keys(b).length) return false;
  for (const key of keys) {
    if (key === "ownerNodes" || key === "ownerSig") continue;
    if (!looseEqual(a[key], b[key])) return false;
  }
  return true;
}
