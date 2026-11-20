import type { ReactNode } from "react";
import { LazyMotion, MotionConfig } from "motion/react";
import * as m from "motion/react-m";
import { EASE, MOTION } from "./motion";

/**
 * The providers behind the header cat's travel (PLAN 8.1): `m` is tiny and sits in the lazy chunks that draw the cat; the layout features
 * (shared layout, `layoutId`) are a lazy chunk of their own. Reduced motion makes the cat jump (Motion drops the transform animations and
 * keeps opacity).
 */
type Features = typeof import("./catMotionFeatures").default;
let ready: Features | undefined;
let loading: Promise<void> | undefined;
/** Loads the layout features once. The workspace waits on this before it lets the cat fly, so a first tap never finds a provider without them
 *  (a cat laid out without features would teleport). Once loaded, every provider gets them synchronously. */
export const ensureCatMotion = (): Promise<void> => (loading ??= import("./catMotionFeatures").then(module => { ready = module.default; }));
const features = () => ensureCatMotion().then(() => ready as Features);
export function CatMotion({ children }: { children: ReactNode }) {
  return <LazyMotion features={ready ?? features} strict><MotionConfig reducedMotion="user">{children}</MotionConfig></LazyMotion>;
}

/** The cat's shared-layout transition: the panel timings on the gentle in-out curve, no overshoot, so it never passes its resting place. */
const bezier = EASE.inout.match(/[\d.]+/g)!.map(Number) as [number, number, number, number];
export const CAT_TRAVEL = { duration: MOTION.panelIn / 1000, ease: bezier } as const;
export const CAT_TRAVEL_OUT = { duration: MOTION.panelOut / 1000, ease: bezier } as const;
/** The Bot card's own height: opens in 280, closes in 180 (the existing panel presence timings). */
export const CARD_IN = CAT_TRAVEL;
export const CARD_OUT = CAT_TRAVEL_OUT;

/** The workspace marks the tap that opens the card; a card that mounts without it (a conversation switch brings back an open card) shows at once. */
let flightAt = -Infinity;
export const markFlight = () => { flightAt = performance.now(); };
export const takeFlight = () => performance.now() - flightAt < 3000;

/** The cat itself: one element that carries the shared layout id, in the header and in the Bot card. The id includes the conversation, so a switch
 *  of conversation never pairs one conversation's cat with another's. It carries its own providers, so each user of it stays in a lazy chunk. */
export function CatTravel({ children, className, id, transition = CAT_TRAVEL }: { children: ReactNode; className?: string; id: string; transition?: typeof CAT_TRAVEL | typeof CAT_TRAVEL_OUT }) {
  return <CatMotion><m.span layoutId={`bot-cat-${id}`} className={className} transition={transition}>{children}</m.span></CatMotion>;
}
