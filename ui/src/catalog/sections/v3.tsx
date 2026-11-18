// The V3 component kit (src/ds/), every variant and state. Open it under the V3 skin (Skin switch, or #k=v3 in the address).
// Nothing in the product imports the kit yet: only this catalog does (V3 PLAN 0.6).
import type { CatalogSection } from "../types";
import { actions } from "./v3-actions";
import { forms } from "./v3-forms";
import { identity } from "./v3-identity";
import { status } from "./v3-status";

export const v3: CatalogSection = {
  id: "v3", number: "V3", title: "V3 components",
  states: [...actions, ...status, ...forms, ...identity],
};
