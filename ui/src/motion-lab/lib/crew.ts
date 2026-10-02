import type { AvatarConfig } from "../../lib/tofi-avatar/index.js";

/** Block colours come from the cat-coat accents; `paper` and `ink` are the two neutrals. */
export type Tone = "clay" | "iris" | "honey" | "lagoon" | "green" | "paper" | "ink";

export type CrewMember = Readonly<{
  id: string;
  name: string;
  trait: string;
  tone: Tone;
  cat: Readonly<Partial<AvatarConfig>>;
}>;

// The seven silhouettes from the cat-motion v2 handoff, each with its own personality.
export const CREW: readonly CrewMember[] = [
  { id: "curl", name: "卷卷", trait: "害羞的观察者", tone: "clay", cat: { shape: "curl", pattern: "calico", palette: "calico" } },
  { id: "loaf", name: "糯米", trait: "最爱慢眨眼", tone: "iris", cat: { shape: "loaf", pattern: "solid", palette: "ivory" } },
  { id: "tall", name: "年糕", trait: "警觉的哨兵", tone: "honey", cat: { shape: "tall", pattern: "tabby", palette: "tabby" } },
  { id: "rice", name: "饭团", trait: "好奇宝宝", tone: "lagoon", cat: { shape: "rice", pattern: "cowblaze", palette: "cow" } },
  { id: "bean", name: "豆包", trait: "节奏最快", tone: "green", cat: { shape: "bean", pattern: "mask", palette: "inkred" } },
  { id: "puddle", name: "小饼", trait: "液体猫", tone: "paper", cat: { shape: "puddle", pattern: "stripes", palette: "caramel" } },
  { id: "stand", name: "踏踏", trait: "散步家", tone: "ink", cat: { shape: "stand", pattern: "beans", palette: "ocean" } },
];

/** Bots used by the product-moment demos. */
export const BOTS = {
  research: { name: "研究助理", cat: { shape: "rice", pattern: "cowblaze", palette: "cow" } },
  writer: { name: "写作搭子", cat: { shape: "loaf", pattern: "solid", palette: "iris" } },
  ops: { name: "Ops Watcher", cat: { shape: "tall", pattern: "tabby", palette: "tabby" } },
  archivist: { name: "Archivist", cat: { shape: "puddle", pattern: "stripes", palette: "caramel" } },
  mail: { name: "信差", cat: { shape: "stand", pattern: "solid", palette: "honey" } },
} as const satisfies Record<string, { name: string; cat: Partial<AvatarConfig> }>;
