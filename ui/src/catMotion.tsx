import type { ReactNode } from "react";
import { LazyMotion, MotionConfig, type Transition } from "motion/react";
import * as m from "motion/react-m";
import { EASE, MOTION } from "./motion";
import { tunable } from "./tunables";

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

/** The cat's shared-layout travel is a spring with a small overshoot, so it lands with a little bounce (owner 2026-11-23). Tunable in a dev build. */
const stiffness = tunable({ key: "cat.stiffness", group: "Header cat", label: "Spring stiffness", value: 380, min: 80, max: 1200, step: 10 });
const damping = tunable({ key: "cat.damping", group: "Header cat", label: "Spring damping", value: 30, min: 4, max: 80, step: 1 });
const mass = tunable({ key: "cat.mass", group: "Header cat", label: "Spring mass", value: 1, min: 0.2, max: 4, step: 0.05 });
const catSpring = (): Transition => ({ type: "spring", stiffness: stiffness(), damping: damping(), mass: mass(), restDelta: 0.5 });
/** Read when the cat renders, so a value changed in the Tune panel applies to the next flight. */
export const catTravel = catSpring;
export const catTravelOut = catSpring;
/** How long the flight state stays on: until the spring's envelope has decayed (to 1/2000 of the distance), never less than the flight token. Read when a flight starts. */
export const catSettleMs = () => Math.max(MOTION.flight, Math.ceil(1000 * Math.log(2000) / (damping() / (2 * mass()))) + 60);
/** The Bot card's own height: opens in 280, closes in 180 (the existing panel presence timings). */
const bezier = EASE.inout.match(/[\d.]+/g)!.map(Number) as [number, number, number, number];
export const CARD_IN = { duration: MOTION.panelIn / 1000, ease: bezier } as const;
export const CARD_OUT = { duration: MOTION.panelOut / 1000, ease: bezier } as const;

/** The workspace marks the tap that opens the card; a card that mounts without it (a conversation switch brings back an open card) shows at once. */
let flightAt = -Infinity;
export const markFlight = () => { flightAt = performance.now(); };
export const takeFlight = () => performance.now() - flightAt < 3000;

/** The cat itself: one element that carries the shared layout id, in the header and in the Bot card. The id includes the conversation, so a switch
 *  of conversation never pairs one conversation's cat with another's. It carries its own providers, so each user of it stays in a lazy chunk. */
export function CatTravel({ children, className, id, transition }: { children: ReactNode; className?: string; id: string; transition?: Transition }) {
  return <CatMotion><m.span layoutId={`bot-cat-${id}`} className={className} transition={transition ?? catTravel()}>{children}</m.span></CatMotion>;
}
