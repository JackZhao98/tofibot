import type { ReactNode } from "react";
import { LazyMotion, MotionConfig } from "motion/react";
import * as m from "motion/react-m";
import { EASE, MOTION } from "./motion";

/**
 * The providers behind the header cat's travel (PLAN 8.1): `m` is tiny and sits in the entry; the layout features (shared layout,
 * `layoutId`) are a lazy chunk that loads as soon as the first `m` element renders, long before anyone taps the cat. Reduced motion
 * makes the cat jump (Motion drops the transform animations and keeps opacity).
 */
// Once the header's cat has loaded the features, every later provider (the Bot card's) gets them synchronously, so the card's cat is laid out
// with layout animation on its very first frame; a provider that had to wait for the import would miss the start of the flight.
let ready: typeof import("./catMotionFeatures").default | undefined;
const features = () => import("./catMotionFeatures").then(module => (ready = module.default));
export function CatMotion({ children }: { children: ReactNode }) {
  return <LazyMotion features={ready ?? features} strict><MotionConfig reducedMotion="user">{children}</MotionConfig></LazyMotion>;
}

/** The cat's shared-layout transition: the panel-in duration on the ease-out curve, no overshoot, so it never passes its resting place. */
const bezier = EASE.out.match(/[\d.]+/g)!.map(Number) as [number, number, number, number];
export const CAT_TRAVEL = { duration: MOTION.panelIn / 1000, ease: bezier } as const;
export const CAT_TRAVEL_OUT = { duration: MOTION.panelOut / 1000, ease: bezier } as const;
/** The Bot card's own height: opens in 280, closes in 180 (the existing panel presence timings). */
export const CARD_IN = CAT_TRAVEL;
export const CARD_OUT = CAT_TRAVEL_OUT;

/** The cat itself: one element that carries the shared layout id, in the header and in the Bot card. It carries its own providers, so each user of it
 *  stays in a lazy chunk (the header's menus chunk, the Bot card's views chunk) and the entry carries no Motion. */
export function CatTravel({ children, className }: { children: ReactNode; className?: string }) {
  return <CatMotion><m.span layoutId="bot-cat" className={className} transition={CAT_TRAVEL}>{children}</m.span></CatMotion>;
}
