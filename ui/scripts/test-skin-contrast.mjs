// Contrast guard for the V3 skin (V3 PLAN 7.7): the text/ground pairs the V3.0 brand book promises, the ones palette.py
// checks, computed from skin/v3.tokens.json in both themes. Run: npm --prefix ui run test:skin-contrast
import { contrast, loadSource, resolveSource } from "./skin-lib.mjs";

const { light, dark, common } = resolveSource(loadSource());
const HUES = ["tomato", "mustard", "teal", "lime", "grape", "cobalt", "danger"];
const GROUNDS = ["bg", "surface", "surface-2"];

// [text role, ground role, minimum ratio]
const pairs = [];
for (const text of ["ink", "ink-soft", "muted"]) for (const ground of GROUNDS) pairs.push([text, ground, 4.5]);
for (const hue of HUES) {
  for (const ground of GROUNDS) pairs.push([`${hue}-text`, ground, 4.5]);
  pairs.push([hue === "danger" ? "on-danger" : "on-block", hue, 4.5]); // the label on the filled block
}
pairs.push(["ink", "bubble-user", 4.5], ["ink", "bubble-bot", 4.5], ["code-ink", "code-bg", 4.5], ["tooltip-ink", "tooltip-bg", 4.5],
  ["focus", "bg", 3], ["focus", "surface", 3], ["edge", "bg", 3], ["edge", "surface", 3], ["ink", "sunk", 4.5]);

const failures = [];
let checked = 0;
for (const [theme, roles] of [["light", light], ["dark", dark]]) {
  const value = name => {
    const hex = roles[name] ?? common[name];
    if (!/^#[0-9a-f]{6}$/i.test(hex ?? "")) throw new Error(`${theme} --${name} is not a hex colour: ${hex}`);
    return hex;
  };
  for (const [text, ground, minimum] of pairs) {
    const ratio = contrast(value(text), value(ground));
    checked++;
    if (ratio < minimum) failures.push(`${theme}: --${text} ${value(text)} on --${ground} ${value(ground)} is ${ratio.toFixed(2)}:1, needs ${minimum}:1`);
  }
  // The two reds stay apart by lightness: the primary colour is not the error colour (PLAN 7.7: 2.2:1).
  const apart = contrast(value("tomato"), value("danger"));
  checked++;
  if (apart < 2.2) failures.push(`${theme}: --tomato and --danger are ${apart.toFixed(2)}:1 apart, need 2.2:1`);
  // The Letter stays light paper in dark mode: its ink must read on its paper.
  checked++;
  const letter = contrast(value("letter-ink"), value("letter-paper"));
  if (letter < 4.5) failures.push(`${theme}: --letter-ink on --letter-paper is ${letter.toFixed(2)}:1, needs 4.5:1`);
}
if (failures.length) {
  console.error(`skin contrast: FAIL\n  ${failures.join("\n  ")}`);
  process.exit(1);
}
console.log(`skin contrast: ${checked} checks in light and dark PASS`);
