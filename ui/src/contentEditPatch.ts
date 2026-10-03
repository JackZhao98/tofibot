import type { ContentPatch, MemoryInput } from "./types";

/** Compare to edit-open values, never refreshed props or whether a field was
 * ever touched. A restored field is unchanged and must stay omitted. */
export function contentEditPatch(baseline: MemoryInput, draft: MemoryInput): ContentPatch | undefined {
  const patch: ContentPatch = {};
  const expected: Partial<MemoryInput> = {};
  for (const key of ["title", "description", "content"] as const) {
    if (draft[key] !== baseline[key]) {
      patch[key] = draft[key];
      expected[key] = baseline[key];
    }
  }
  return Object.keys(expected).length ? { ...patch, expected } : undefined;
}
