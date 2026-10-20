// A delta event carries the whole draft only at its first revision and at
// periodic resync points; every other one carries just the new text. Applying
// a text-only delta to a draft that is not at the previous revision would
// splice text into the wrong place or twice, so such a delta is ignored and
// the draft waits for the next resync point.
export function draftDeltaApplies(oldRevision: number | undefined, revision: number, carriesContent: boolean) {
  if (oldRevision !== undefined && revision > 0 && revision <= oldRevision) return false;
  if (!carriesContent && revision > 0 && revision !== (oldRevision ?? 0) + 1) return false;
  return true;
}
