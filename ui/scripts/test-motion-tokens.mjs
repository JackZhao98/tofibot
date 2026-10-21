// Motion vocabulary guard.
//  1. design-tokens.css defines the scale (instant 80, press 120, quick 140, base 220, panel-in 280, panel-out 180,
//     flight 640 ms; easings out/settle/spring/inout/in) and src/motion.ts mirrors it for Web Animations callers.
//  2. Outside the tokens, a transition/animation may not carry a literal duration under 800 ms, a literal
//     cubic-bezier() or a bare ease keyword, and TS/TSX may not carry a literal cubic-bezier() or an animate()
//     duration under 800 ms. Existing leftovers are a ceiling in scripts/motion-tokens-baseline.json that can
//     only shrink (npm run test:motion-tokens -- --update-baseline lowers it). Ambient loops of 0.8 s and longer
//     are narrative motion, not interface motion, and are not counted.
//  3. Every backdrop-filter has its -webkit-backdrop-filter twin in the same rule (older Safari ignores the bare one).
//  4. Springs are tokens too (migration step 4, D35): motion.ts exports SPRING, SPRING.push is critically damped (the
//     swipe-back never rings past the pane's edge), and TS outside motion.ts carries no literal stiffness/damping/mass.
// Usage: npm run test:motion-tokens [-- --update-baseline]
import assert from "node:assert/strict";
import { execSync } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(ui, "src");
const baselinePath = join(ui, "scripts", "motion-tokens-baseline.json");
const flag = name => process.argv.includes(name);
const tracked = pattern => execSync(`git ls-files '${pattern}'`, { cwd: src }).toString().trim().split("\n").filter(Boolean);
const skip = file => file.startsWith("motion-lab/") || file.startsWith("lib/tofi-avatar/") || file.includes("acceptance") || file.startsWith("locales/");
const failures = [];
const fail = message => failures.push(message);

// ---- 1. the scale itself
const tokens = readFileSync(join(src, "design-tokens.css"), "utf8");
const SCALE = { "m-instant": 80, "m-press": 120, "m-quick": 140, "m-base": 220, "m-panel-in": 280, "m-panel-out": 180, "m-flight": 640 };
for (const [name, ms] of Object.entries(SCALE)) {
  const found = new RegExp(`--${name}:\\s*(\\d+)ms`).exec(tokens);
  if (!found || Number(found[1]) !== ms) fail(`design-tokens.css: --${name} must be ${ms}ms (found ${found ? found[1] + "ms" : "none"})`);
}
const CURVES = { out: ".22,1,.36,1", settle: ".2,.8,.2,1", spring: ".34,1.3,.64,1", inout: ".45,0,.2,1", in: ".5,0,.75,0" };
for (const [name, curve] of Object.entries(CURVES)) {
  const found = new RegExp(`--ease-${name}:\\s*cubic-bezier\\(([^)]*)\\)`).exec(tokens);
  if (!found || found[1].replace(/\s+/g, "") !== curve) fail(`design-tokens.css: --ease-${name} must be cubic-bezier(${curve})`);
}
const mirror = readFileSync(join(src, "motion.ts"), "utf8");
const camel = { "m-instant": "instant", "m-press": "press", "m-quick": "quick", "m-base": "base", "m-panel-in": "panelIn", "m-panel-out": "panelOut", "m-flight": "flight" };
for (const [name, ms] of Object.entries(SCALE)) {
  const found = new RegExp(`\\b${camel[name]}:\\s*(\\d+)`).exec(mirror);
  if (!found || Number(found[1]) !== ms) fail(`motion.ts: MOTION.${camel[name]} must be ${ms}`);
}
for (const [name, curve] of Object.entries(CURVES)) {
  const found = new RegExp(`\\b${name}:\\s*"cubic-bezier\\(([^)]*)\\)"`).exec(mirror);
  if (!found || found[1].replace(/\s+/g, "") !== curve) fail(`motion.ts: EASE.${name} must be cubic-bezier(${curve})`);
}

const spring = /export const SPRING = \{[\s\S]*?push:\s*\{\s*stiffness:\s*([\d.]+),\s*damping:\s*([\d.]+),\s*mass:\s*([\d.]+)\s*\}/.exec(mirror);
if (!spring) fail("motion.ts: SPRING.push { stiffness, damping, mass } must be exported (springs are tokens)");
else {
  const [stiffness, damping, mass] = spring.slice(1).map(Number);
  if (Math.abs(damping - 2 * Math.sqrt(stiffness * mass)) > 0.5) fail(`motion.ts: SPRING.push must be critically damped (damping ${damping}, want ${(2 * Math.sqrt(stiffness * mass)).toFixed(1)})`);
}

// ---- 2. no vocabulary leaks
const strip = text => text.replace(/\/\*[\s\S]*?\*\//g, " ");
const splitTop = (value, separator) => { const out = []; let depth = 0, current = ""; for (const char of value) { if (char === "(") depth++; if (char === ")") depth--; if (char === separator && depth === 0) { out.push(current); current = ""; } else current += char; } out.push(current); return out; };
const timeRe = /^(-?\d*\.?\d+)(ms|s)$/;
export function cssViolations(text) {
  let count = 0;
  const css = strip(text);
  for (const match of css.matchAll(/(?<![\w-])(transition-duration|animation-duration|transition-timing-function|animation-timing-function|transition|animation)\s*:\s*([^;{}]*)/g)) {
    const prop = match[1];
    const body = match[2].replace(/\s*!important\s*$/, "");
    if (/cubic-bezier\(/.test(body)) count += (body.match(/cubic-bezier\(/g) || []).length;
    for (const item of splitTop(body, ",")) {
      const words = splitTop(item.trim(), " ").filter(Boolean);
      let seenDuration = false;
      for (const word of words) {
        if (/^var\(--(m-[a-z-]+|ease-press|spring-ms)/.test(word)) { seenDuration = true; continue; }
        const time = timeRe.exec(word);
        if (time && /^(transition|animation)(-duration)?$/.test(prop)) {
          if (seenDuration) continue; // the second time value is a delay
          seenDuration = true;
          const ms = time[2] === "s" ? parseFloat(time[1]) * 1000 : parseFloat(time[1]);
          if (ms > 1 && ms < 800) count++; // up to 1 ms is the reduced-motion switch-off, not motion
        } else if (/^(ease|ease-in|ease-out|ease-in-out)$/.test(word)) count++;
      }
    }
  }
  // curves kept in custom properties
  for (const match of css.matchAll(/--[\w-]+\s*:\s*[^;{}]*cubic-bezier\(/g)) count++;
  return count;
}
export function scriptViolations(text) {
  const code = text.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/(^|[^:])\/\/[^\n]*/g, "$1");
  let count = (code.match(/cubic-bezier\(/g) || []).length;
  count += (code.match(/\b(stiffness|damping|mass)\s*:\s*\d/g) || []).length;
  for (const match of code.matchAll(/\bduration\s*:\s*(\d+(?:\.\d+)?)\b(?!\s*\*)/g)) if (Number(match[1]) > 0 && Number(match[1]) < 800) count++;
  return count;
}
const counts = {};
for (const file of tracked("*.css")) if (!skip(file) && file !== "design-tokens.css") { const n = cssViolations(readFileSync(join(src, file), "utf8")); if (n) counts[`src/${file}`] = n; }
for (const file of [...tracked("*.ts"), ...tracked("*.tsx")]) if (!skip(file) && file !== "motion.ts" && !file.endsWith(".d.ts")) { const n = scriptViolations(readFileSync(join(src, file), "utf8")); if (n) counts[`src/${file}`] = n; }
const baseline = existsSync(baselinePath) ? JSON.parse(readFileSync(baselinePath, "utf8")).files ?? {} : {};
if (flag("--update-baseline")) {
  const raised = Object.entries(counts).filter(([file, n]) => n > (baseline[file] ?? 0));
  if (raised.length && existsSync(baselinePath) && !flag("--force")) { console.error(`refusing to raise the baseline for: ${raised.map(([file]) => file).join(", ")}`); process.exit(1); }
  const lines = Object.keys(counts).sort().map(file => `    ${JSON.stringify(file)}: ${counts[file]}`);
  writeFileSync(baselinePath, `{\n  "note": "Ceiling of motion values outside the vocabulary (literal durations under 800 ms, cubic-bezier(), bare ease keywords) per file. Only ever shrinks; regenerate with npm run test:motion-tokens -- --update-baseline.",\n  "files": {\n${lines.join(",\n")}\n  }\n}\n`);
  console.log(`baseline updated: ${lines.length} file(s), ${Object.values(counts).reduce((a, b) => a + b, 0)} value(s)`);
}
for (const file of Object.keys(counts)) if (counts[file] > (baseline[file] ?? 0) && !flag("--update-baseline")) fail(`${file}: ${counts[file]} motion value(s) outside the vocabulary (baseline ${baseline[file] ?? 0}); use var(--m-*) / var(--ease-*) in CSS or MOTION / EASE / SPRING from motion.ts`);
const shrinkable = Object.keys(baseline).filter(file => (counts[file] ?? 0) < baseline[file]);

// ---- 3. -webkit-backdrop-filter pairing
function backdropGaps(text) {
  const css = strip(text);
  const gaps = [];
  for (const rule of css.matchAll(/([^{}]*)\{([^{}]*)\}/g)) {
    const body = rule[2];
    if (/(^|[;\s])backdrop-filter\s*:/.test(body) && !/-webkit-backdrop-filter\s*:/.test(body)) gaps.push(rule[1].trim().split("\n").pop().trim().slice(0, 80));
  }
  return gaps;
}
export const checkBackdrop = backdropGaps;
for (const file of tracked("*.css")) {
  if (file.includes("acceptance")) continue;
  for (const selector of backdropGaps(readFileSync(join(src, file), "utf8"))) fail(`src/${file}: backdrop-filter without -webkit-backdrop-filter in "${selector}"`);
}

if (failures.length) { for (const message of failures) console.error(`not ok - ${message}`); process.exit(1); }
if (shrinkable.length) console.log(`  below baseline (run --update-baseline to ratchet): ${shrinkable.join(", ")}`);
console.log(`motion tokens: PASS (scale and mirror match; ${Object.values(counts).reduce((a, b) => a + b, 0)} legacy value(s) in ${Object.keys(counts).length} file(s) held at the ceiling; every backdrop-filter has its -webkit- twin)`);
