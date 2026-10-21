import { fileURLToPath } from "node:url";
import type { Plugin } from "vite";

/*
 * Migration step 4 (docs/design/migration.md, decision D35): swaps the Motion variants in for the shipped motion code, so
 * the two can be measured against each other on the same page and felt side by side. Parts: "swipe" replaces
 * ./phoneSwipeEngine (the swipe-back settle), "cascade" replaces ./arrivalCascade (the arrival cascade); "motion" is both.
 *   - npm run test:motion-experiment builds with it (scripts/test-motion-experiment.mjs).
 *   - TOFI_MOTION_EXPERIMENT=motion npm run dev:remote serves the app with it (vite.config.ts applies it to the dev server
 *     only, never to a build, so `motion` stays a devDependency and no production bundle contains it).
 */
const SWAPS = {
  swipe: ["./phoneSwipeEngine", "src/motion-lab/experiments/phoneSwipeMotion.ts"],
  cascade: ["./arrivalCascade", "src/motion-lab/experiments/arrivalCascadeMotion.ts"],
} as const;

export function motionExperimentPlugin(spec: string): Plugin {
  const parts = new Set(spec.split(",").flatMap(part => part.trim() === "motion" ? ["swipe", "cascade"] : [part.trim()]));
  const map = new Map<string, string>(Object.entries(SWAPS).filter(([part]) => parts.has(part)).map(([, [from, to]]) => [from, fileURLToPath(new URL(to, import.meta.url))]));
  return {
    name: "tofi-motion-experiment",
    enforce: "pre",
    resolveId(source, importer) {
      if (!importer || importer.includes("/motion-lab/experiments/")) return null;
      return map.get(source) ?? null;
    },
  };
}
