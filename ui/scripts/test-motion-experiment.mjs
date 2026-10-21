// Migration step 4 (docs/design/migration.md; decision D35): the gated experiment, Motion 14.4.0 against TOFI's own Web
// Animations code, on the same pages and the same budgets. It builds the production bundle three times (as shipped, with
// the Motion swipe-back settle, with the Motion arrival cascade; motionExperimentPlugin swaps the modules) and reports:
//  1. bundle: entry gzip and the lazy chunks each variant adds;
//  2. swipe-back (390 px, touch): frame gaps, script per finger move and script from lift to commit, at 1x and 4x CPU;
//     settle time; how the pane leaves the finger (speed handed over, largest step); a catch mid-flight (no jump);
//  3. arrival cascade (1180 x 1150, a dozen short messages in view): frame gaps and script during one arrival and during
//     an arrival that interrupts a running one, overshoot, settle time, and how much the interrupted link's velocity jumps
//     (px/ms) when the second arrival restarts it.
// Both variants must pass the functional checks (they share test:swipe-back's scenarios); the numbers are printed as a table
// and, with MOTION_EXPERIMENT_OUT=/path.json, written out. The gate itself (who wins) is decided in docs, not asserted here,
// except that Motion must stay out of the shipped build.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:motion-experiment
// MOTION_EXPERIMENT_RUNS=3 (repeats per measurement, default 3), MOTION_EXPERIMENT_ENGINES=chromium,webkit,
// MOTION_EXPERIMENT_ONLY=swipe,cascade.
import assert from "node:assert/strict";
import { writeFileSync } from "node:fs";
import { buildUI, bundleSizes, cleanup, drag, flick, line, measureCost, reopenChat, openChat, restState, serve, startTrace, stopTrace } from "./lib/swipe-harness.mjs";

const pw = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const RUNS = Number(process.env.MOTION_EXPERIMENT_RUNS || 3);
const ENGINES = (process.env.MOTION_EXPERIMENT_ENGINES || "chromium,webkit").split(",");
const ONLY = (process.env.MOTION_EXPERIMENT_ONLY || "swipe,cascade").split(",");
const median = values => { const v = values.filter(Number.isFinite).sort((a, b) => a - b); return v.length ? v[Math.floor(v.length / 2)] : NaN; };
const r1 = value => Math.round(value * 10) / 10;
const report = { motion: "14.4.0", date: new Date().toISOString(), bundle: {}, swipe: {}, cascade: {} };

// ---- 1. bundles
const builds = { shipped: await buildUI(), swipeMotion: await buildUI({ experiment: "swipe" }), cascadeMotion: await buildUI({ experiment: "cascade" }) };
const sizes = Object.fromEntries(Object.entries(builds).map(([name, dist]) => [name, bundleSizes(dist, /./)]));
const base = sizes.shipped;
const stem = file => file.replace(/^assets\//, "").replace(/-[\w-]+\.js$/, "");
for (const [name, s] of Object.entries(sizes)) {
  const baseStems = new Set(base.files.map(stem));
  const added = s.lazy.filter(c => !baseStems.has(stem(c.file)));
  const removed = base.lazy.filter(c => !s.files.map(stem).includes(stem(c.file)));
  report.bundle[name] = { entry: s.entry, entryDelta: s.entry - base.entry, totalJs: s.total, totalDelta: s.total - base.total, added: added.map(c => ({ chunk: stem(c.file), gzip: c.gzip })), removed: removed.map(c => ({ chunk: stem(c.file), gzip: c.gzip })) };
}
const swipeChunk = base.lazy.find(c => /phoneSwipeEngine/.test(c.file));
report.bundle.shipped.swipeChunk = swipeChunk?.gzip;
assert.ok(!base.files.some(file => /phoneSwipeMotion|arrivalCascadeMotion/.test(file)), "the shipped build carries no Motion variant");
assert.ok(!base.files.map(file => file).some(file => /motion\b|framer/i.test(file) && !/motion-lab/.test(file)), "the shipped build carries no Motion chunk");
console.log("bundle", JSON.stringify(report.bundle));

// ---- 2. swipe-back
const W = 390, Y = 420;
// The variants are measured interleaved (run 1 of each, then run 2 of each...), so a busy machine slows both alike.
const newSwipeOut = () => ({ functional: [], cost1: [], cost4: [], settleMs: [], leaveRatio: [], maxStep: [], catchStep: [], priorStep: [] });
async function swipeFunctional(browser, engineName, server, out) {
    // Functional parity: the four outcomes.
    for (const [label, path, want, fast] of [
      ["past half, at rest", [...line(40, 300, Y, 26), [300, Y, 140]], "list", false],
      ["short of half, at rest", [...line(40, 160, Y, 14), [160, Y, 140]], "chat", false],
      ["flick right", line(40, 160, Y, 5, 10), "list", true],
      ["flick left past half", [...line(40, 280, Y, 22), [280, Y, 80], ...line(280, 220, Y, 4, 12).slice(1)], "chat", true],
    ]) {
      const { page, context, finger, errors } = await openChat(browser, engineName, server.origin);
      if (fast) { await flick(page, finger, path, { reset: () => reopenChat(page) }); await stopTrace(page); } else await drag(finger, path);
      await page.waitForTimeout(700);
      const state = await restState(page);
      const got = state.listOpen && !state.chatShown ? "list" : state.chatShown && !state.listOpen ? "chat" : "mixed";
      out.functional.push({ label, want, got, clean: state.inline === "" && state.swipe === "" && state.animations === 0, errors: errors.length });
      await context.close();
    }
}
async function swipeOnce(browser, engineName, server, out) {
    {
      // Release feel: a flick from a third of the way (fast last moves); per-frame chat positions from the lift until home.
      const { page, context, finger } = await openChat(browser, engineName, server.origin);
      const speed = await flick(page, finger, [...line(40, 120, Y, 4, 16), ...line(120, 200, Y, 3, 10).slice(1)]);
      await page.waitForTimeout(700);
      const trace = await stopTrace(page);
      const lift = trace.findIndex((f, k) => k > 0 && trace[k - 1].finger != null && f.finger == null);
      const after = trace.slice(lift - 1);
      const leave = after.length > 2 ? (after[2].chat - after[0].chat) / (after[2].t - after[0].t) : NaN;
      const home = after.findIndex(f => f.chat >= W - 0.5 || f.cv === "hidden");
      out.settleMs.push(home > 0 ? after[home].t - after[0].t : NaN);
      out.leaveRatio.push(leave / speed);
      let step = 0; for (let k = 1; k < after.length; k++) if (after[k].cv === "visible") step = Math.max(step, Math.abs(after[k].chat - after[k - 1].chat));
      out.maxStep.push(step);
      await context.close();
      // A catch mid-flight: the jump between the last frame before the second touch and the first frame after it.
      const c = await openChat(browser, engineName, server.origin);
      await startTrace(c.page);
      await drag(c.finger, [...line(40, 270, Y, 20), [270, Y, 120]]);
      await c.page.waitForTimeout(40);
      const at = await c.page.evaluate(() => document.querySelector(".chat-pane").getBoundingClientRect().x);
      await drag(c.finger, [[at + 30, Y + 40], ...line(at + 30, at - 120, Y + 40, 12).slice(1), [at - 120, Y + 40, 120]]);
      await c.page.waitForTimeout(700);
      const ct = await stopTrace(c.page);
      const second = ct.findIndex((f, k) => k > 0 && ct[k - 1].finger == null && f.finger != null && f.t > ct[0].t + 300);
      const steps = ct.map((f, k) => k ? Math.abs(f.chat - ct[k - 1].chat) : 0);
      out.catchStep.push(second > 1 ? steps[second] : NaN);
      out.priorStep.push(second > 3 ? Math.max(...steps.slice(second - 3, second)) : NaN);
      await c.context.close();
      // Cost at 1x and (Chromium) 4x.
      for (const cpu of engineName === "chromium" ? [1, 4] : []) {
        const k = await openChat(browser, engineName, server.origin, { cpu });
        (cpu === 1 ? out.cost1 : out.cost4).push(await measureCost(k.page, k.finger, k.cdp, { y: Y }));
        await k.context.close();
      }
    }
}
function swipeSummary(out) {
  const cost = list => list.length ? { slowFrames: median(list.map(r => r.slow)), worstFrameMs: r1(median(list.map(r => r.worst))), dragScriptPerMoveMs: +median(list.map(r => r.dragScriptPerMove)).toFixed(2), dragLayouts: median(list.map(r => r.dragLayouts)), releaseScriptMs: r1(median(list.map(r => r.settleScript))), releaseLayouts: median(list.map(r => r.settleLayouts)) } : null;
  return { functional: out.functional, cost1x: cost(out.cost1), cost4x: cost(out.cost4), settleMs: r1(median(out.settleMs)), leaveSpeedRatio: +median(out.leaveRatio).toFixed(2), maxStepPx: r1(median(out.maxStep)), catchStepPx: r1(median(out.catchStep)), stepBeforeCatchPx: r1(median(out.priorStep)) };
}
if (ONLY.includes("swipe")) for (const engineName of ENGINES) {
  const browser = await pw[engineName].launch({ headless: true });
  const variants = [["waapi", await serve(builds.shipped)], ["motion", await serve(builds.swipeMotion)]].map(([name, server]) => ({ name, server, out: newSwipeOut() }));
  for (const v of variants) await swipeFunctional(browser, engineName, v.server, v.out);
  for (let i = 0; i < RUNS; i++) for (const v of variants) await swipeOnce(browser, engineName, v.server, v.out);
  await browser.close();
  for (const v of variants) {
    v.server.close();
    const result = swipeSummary(v.out);
    (report.swipe[engineName] ??= {})[v.name] = result;
    console.log(`swipe ${engineName} ${v.name}`, JSON.stringify(result));
    for (const f of result.functional) assert.equal(f.got, f.want, `${engineName} ${v.name}: ${f.label} ends on the ${f.want} (got ${f.got})`);
    for (const f of result.functional) assert.ok(f.clean && !f.errors, `${engineName} ${v.name}: ${f.label} leaves nothing behind`);
  }
}

// ---- 3. arrival cascade
async function cascadeOnce(browser, engineName, server, mode, cpu, kind, out) {
    {
      const { page, context, cdp, errors } = await openChat(browser, engineName, server.origin, { width: 1180, height: 1150, compact: true, cpu, init: mode ? `window.__TOFI_CASCADE_MOTION = ${JSON.stringify(mode)}` : undefined });
      if (cdp) await cdp.send("Performance.enable");
      const metric = async () => cdp ? Object.fromEntries((await cdp.send("Performance.getMetrics")).metrics.map(m => [m.name, m.value])) : { ScriptDuration: NaN };
      await page.evaluate(() => {
        window.__ct = []; window.__ctOn = true;
        const tick = t => { const b = document.querySelector('[data-message-id="dm_2_m40"]'), lead = document.querySelector('[data-message-id="cascade_1"]'); window.__ct.push({ t, y: b ? b.getBoundingClientRect().top : NaN, lead: lead ? lead.getBoundingClientRect().top : NaN, has2: Boolean(document.querySelector('[data-message-id="cascade_2"]')) }); if (window.__ctOn) requestAnimationFrame(tick); };
        requestAnimationFrame(tick);
      });
      await page.waitForTimeout(150);
      const m0 = await metric();
      const emit = n => page.evaluate(n => { window.__emitAt ??= {}; window.__emitAt[n] = performance.now(); window.__emit("message", { id: `cascade_${n}`, conversation_id: "dm_2", seq: 40 + n, role: "assistant", sender_bot_id: "bot_2", run_id: `run_c${n}`, content: `Short reply ${n}: the totals match.`, created_at: new Date().toISOString() }); }, n);
      await emit(1);
      // Mid-cascade: 90 ms after the first reply is on the page, while every link is still travelling.
      if (kind === "interrupted") { await page.waitForFunction(() => document.querySelector('[data-message-id="cascade_1"]')); await page.waitForTimeout(90); await emit(2); }
      await page.waitForTimeout(900);
      const m1 = await metric();
      const trace = await page.evaluate(() => { window.__ctOn = false; return { frames: window.__ct, at: window.__emitAt }; });
      const end = trace.frames.at(-1).y;
      const frames = trace.frames.filter(f => f.t >= trace.at[1] - 20);
      const gaps = frames.slice(1).map((f, k) => f.t - frames[k].t);
      const off = frames.map(f => f.y - end);
      const overshoot = -Math.min(...off);
      const settled = frames.findLast((f, k) => Math.abs(off[k]) > 0.5);
      const row = { cpu, slowFrames: gaps.filter(g => g > 25).length, worstFrameMs: Math.max(...gaps), scriptMs: (m1.ScriptDuration - m0.ScriptDuration) * 1000, overshootPx: overshoot, settleMs: settled ? settled.t - trace.at[1] : 0, errors: errors.length };
      if (kind === "interrupted") {
        // The message above the first reply: its on-screen speed (px/ms) over the two frames before the second reply lands
        // and the two after. The scroll jump never shows (the cascade offsets it), so a velocity carried through the
        // interruption shows as a small change and a restart at rest as a large one.
        const k2 = frames.findIndex(f => f.has2);
        const speed = (i, j) => (off[j] - off[i]) / (frames[j].t - frames[i].t);
        const a = k2 > 2 ? speed(k2 - 3, k2 - 1) : NaN, b = k2 > 0 && k2 + 2 < frames.length ? speed(k2, k2 + 2) : NaN;
        row.velocityBefore = a; row.velocityAfter = b; row.velocityJump = Math.abs(b - a);
        row.visualJumpPxPerMs = k2 > 0 ? Math.abs(off[k2] - off[k2 - 1]) / (frames[k2].t - frames[k2 - 1].t) : NaN;
      }
      out[kind].push(row);
      await context.close();
    }
}
function cascadeSummary(out) {
  const sum = (rows, cpu) => {
    const at = rows.filter(r => r.cpu === cpu);
    if (!at.length) return null;
    const m = key => r1(median(at.map(r => r[key])));
    return { slowFrames: m("slowFrames"), worstFrameMs: m("worstFrameMs"), scriptMs: m("scriptMs"), overshootPx: m("overshootPx"), settleMs: m("settleMs"), ...(at[0].velocityJump !== undefined ? { velocityBeforePxPerMs: +median(at.map(r => r.velocityBefore)).toFixed(3), velocityAfterPxPerMs: +median(at.map(r => r.velocityAfter)).toFixed(3), velocityJumpPxPerMs: +median(at.map(r => r.velocityJump)).toFixed(3), stepAtInterruptPxPerMs: +median(at.map(r => r.visualJumpPxPerMs)).toFixed(3) } : {}), errors: Math.max(...at.map(r => r.errors)) };
  };
  return { single1x: sum(out.single, 1), single4x: sum(out.single, 4), interrupted1x: sum(out.interrupted, 1), interrupted4x: sum(out.interrupted, 4) };
}
if (ONLY.includes("cascade")) for (const engineName of ENGINES) {
  const browser = await pw[engineName].launch({ headless: true });
  const shipped = await serve(builds.shipped), withMotion = await serve(builds.cascadeMotion);
  const variants = [["waapi", shipped, ""], ["motion-waapi", withMotion, "waapi"], ["motion-js", withMotion, "js"]].map(([name, server, mode]) => ({ name, server, mode, out: { single: [], interrupted: [] } }));
  for (const cpu of engineName === "chromium" ? [1, 4] : [1]) for (let i = 0; i < RUNS; i++) for (const kind of ["single", "interrupted"]) for (const v of variants) await cascadeOnce(browser, engineName, v.server, v.mode, cpu, kind, v.out);
  await browser.close(); shipped.close(); withMotion.close();
  for (const v of variants) {
    const result = cascadeSummary(v.out);
    (report.cascade[engineName] ??= {})[v.name] = result;
    console.log(`cascade ${engineName} ${v.name}`, JSON.stringify(result));
    assert.equal(result.single1x.errors, 0, `${engineName} ${v.name}: no page errors`);
  }
}

for (const dist of Object.values(builds)) cleanup(dist);
if (process.env.MOTION_EXPERIMENT_OUT) writeFileSync(process.env.MOTION_EXPERIMENT_OUT, JSON.stringify(report, null, 2));
console.log("motion experiment: PASS (both variants work; numbers above)");
