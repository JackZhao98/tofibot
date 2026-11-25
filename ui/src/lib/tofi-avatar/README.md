# Tofi avatar motion v2

This library renders the seven cat shapes (`curl`, `loaf`, `tall`, `rice`, `bean`, `puddle`, `stand`) with the v2 motion system. `mountCat`, the existing action/state/configuration methods, `catalog`, and schema-version-1 appearance records remain supported. No chat or application activity policy lives here: callers decide when to wake, sleep, or play a work action.

## API

```js
import {mountCat} from './index.js';

const cat = mountCat(host, {
  schemaVersion: 1,
  shape: 'stand',
  pattern: 'solid',
  palette: 'iris',
  initialState: 'asleep',
  life: true,
  autoplay: false,
  eyes: 'dot',
});
await cat.play('wake');
cat.setIdle('awake');
cat.setLife(true);
cat.setAutoplay(true);
cat.setEyes('shine');
cat.destroy();
```

The original six shapes, coat/palette restrictions, `play`, `setIdle`, `setAppearance`, `reset`, `setSpeed`, `getConfig`, `getState`, `exportSVG`, and `destroy` keep their public signatures. V2 adds the `stand` shape, more clips, eye styles, `setLife`, `setAutoplay`, `setEyes`, `eyeStyles`, `clips`, and `pose`. `stand` as an **action** is unavailable for the `curl` and `stand` shapes; callers can check `clips()` first.

`catFraming(shape, headY?)` returns where to place a 320-unit cat host inside a square frame (multiples of the frame edge: `size`, `left`, `top`) so the head fills it and the body is cropped at the bottom: the portrait crop of a round or bare avatar frame. Additive; the group avatar (`GroupAvatar.tsx`) is its caller.

Clips are `look`, `curious`, `double-take`, `slow-blink`, `happy`, `stand`, `blink-look`, `work`, `work-end`, `knead`, `bat`, `wave`, `sleep`, `dream`, `breathe`, and `wake`. `work` is a finite clip; repeat it in the host if the task remains active. Autonomous idle motion can be disabled with `life: false`, while `autoplay` controls the separate, weighted idle gestures. System reduced-motion preferences, hidden documents, and offscreen hosts suspend the animation loop. After `IDLE_PAUSE_MS` (20 s) without pointer, keyboard, wheel or touch input every cat settles into its rest pose and its life layer stops; the next input resumes it within a frame. Playing clips are not affected.

## Files and verification

- `index.js`: public controller, lifecycle, SVG rendering, idle motion and interruption.
- `appearance.js`: eye-style rendering and appearance helpers.
- `performances.js`: v2 keyframes, personalities, motion curves and life director.
- `shapes/stand.js`: seventh cat silhouette.
- `index.d.ts`: public v2 types, including the preserved v1 configuration schema.
- `tests/avatar.test.mjs`: deterministic API, lifecycle, appearance and clip tests.

Run `node --test ui/src/lib/tofi-avatar/tests/avatar.test.mjs` from the repository root. The synthetic DOM/RAF tests verify behavior but do not establish pixel-level equivalence with `ui/design-v2/cat-motion/cat-motion-lab.html`; visual acceptance must be performed in a browser against that reference.
