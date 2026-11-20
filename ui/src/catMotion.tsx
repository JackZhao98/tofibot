import type { ReactNode } from "react";
import { LazyMotion, MotionConfig } from "motion/react";
import { EASE, MOTION } from "./motion";

/**
 * The providers behind the header cat's travel (PLAN 8.1): `m` is tiny and sits in the entry; the layout features (shared layout,
 * `layoutId`) are a lazy chunk that loads as soon as the first `m` element renders, long before anyone taps the cat. Reduced motion
 * makes the cat jump (Motion drops the transform animations and keeps opacity).
 */
const features = () => import("./catMotionFeatures").then(module => module.default);
export function CatMotion({ children }: { children: ReactNode }) {
  return <LazyMotion features={features} strict><MotionConfig reducedMotion="user">{children}</MotionConfig></LazyMotion>;
}

/** The cat's shared-layout transition: the panel-in duration on the ease-out curve, no overshoot, so it never passes its resting place. */
const bezier = EASE.out.match(/[\d.]+/g)!.map(Number) as [number, number, number, number];
export const CAT_TRAVEL = { duration: MOTION.panelIn / 1000, ease: bezier } as const;
export const CAT_TRAVEL_OUT = { duration: MOTION.panelOut / 1000, ease: bezier } as const;
/** The Bot card's own height: opens in 280, closes in 180 (the existing panel presence timings). */
export const CARD_IN = CAT_TRAVEL;
export const CARD_OUT = CAT_TRAVEL_OUT;
