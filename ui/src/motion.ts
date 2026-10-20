// The JS mirror of the motion vocabulary in design-tokens.css (--m-* durations, --ease-* curves). Web Animations
// callers take their numbers from here; scripts/test-motion-tokens.mjs fails if the two drift apart or if new
// code brings in a duration or curve outside the vocabulary.
export const MOTION = { instant: 80, press: 120, quick: 140, base: 220, panelIn: 280, panelOut: 180, flight: 640 } as const;
export const EASE = {
  out: "cubic-bezier(.22,1,.36,1)",
  settle: "cubic-bezier(.2,.8,.2,1)",
  spring: "cubic-bezier(.34,1.3,.64,1)",
  inout: "cubic-bezier(.45,0,.2,1)",
  in: "cubic-bezier(.5,0,.75,0)",
} as const;
