# V3.0 values

A vendored copy of the frozen Tofi Bot Design System V3.0 (2026-11-17), so CI can check the skin source without reading
`docs/` (untracked).

- `tokens.css` is `project/tokens.css` of the frozen folder, byte for byte.
- `values.json` holds every custom property the V3.0 skin defines, resolved: `tokens.css` plus the final custom properties of
  `project/components/bundle.css` (the last rule wins), and the bundle literals that PLAN S1 turns into named tokens
  (`--bot-tint`, `--bot-edge`, `--stamp-tilt`, `--driver-*`, the arrival and mark timings, `--ease-pen`, `--ease-stamp`).
  `letter` is the Letter's light scope in dark mode.

`test:skin-sync` compares `src/skin/v3.tokens.json` with `values.json`. A value may differ only when `changes[]` names it.
These files change only with a V3.1 decision, never to make a test pass.
