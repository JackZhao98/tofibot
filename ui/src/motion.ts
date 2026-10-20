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

// Arrival cascade (useConversationScroll + arrivalCascade.ts): a whole message arriving under a reader who follows the
// bottom. The scroll lands exactly at once; the messages in view ride a visual `translate` offset that settles to 0 as a
// spring chain: the new message first, each message further up starting STAGGER later, each overshooting its true place by
// a few px and settling back. Tunable live: assign window.__TOFI_CASCADE = { staggerMs: 25 } in the console (merged over
// these values on the next arrival).
export const CASCADE = {
  staggerMs: 15,        // start delay between neighbouring messages, nearest the new one first
  durationMs: 240,      // one message's whole motion, its own delay excluded (last of 12: 11 * 15 + 240 = 405 ms)
  crossShare: 0.45,     // share of durationMs until a message first reaches its true place; it passes it by 2-4 px at ~0.75 and is home by 1
  leadSpeedup: 0.85,    // the new message's crossing time as a share of the others' (it is the impulse)
  overshootPx: 3.8,     // how far the nearest message passes its true place; 2-4 px by design
  overshootTaperPx: 0.13, // less for every link further up
  overshootMinPx: 2.4,
  enterExtraPx: 24,     // the new message starts this much lower than the content it pushes, so it rises in from below
  maxLinks: 12,         // the chain staggers over this many links; every further message in view shares the last link's delay
  maxShiftPx: 130,      // a longer hop jumps the excess at once; this much is animated
  samples: 26,          // keyframes per message (linear between)
} as const;
