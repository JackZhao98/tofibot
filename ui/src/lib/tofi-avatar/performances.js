// Tofi cat motion v2 — personality scores.
// Row: [time, x, y, r, s, b, g, o, lid, a, e, curve?]. Curves: io (default),
// out, in, snap, back (overshoot), spring (settles with a wobble), hold, lin.
// Every clip ends at the rest pose so the life layer can take over seamlessly.
export const poseFields = ['x', 'y', 'r', 's', 'b', 'g', 'o', 'lid', 'a', 'e', 't', 'u', 'h', 'pl', 'pr', 'ps', 'hh', 'jj', 'pd', 'k'];
const f = (time, o, v = {}, curve) => {
  const row = [time, ...poseFields.map(k => k === 'o' ? o : v[k] ?? 0)];
  if (curve) row.push(curve);
  return row;
};
const shut = (t, v = {}, c) => f(t, 0, {lid: 1, ...v}, c);
// Stretch time for a personality without touching the shape of the gesture.
const tempo = (clip, k) => clip.map(r => [r[0] * k, ...r.slice(1)]);

/* ---------- shared gesture builders, tuned per cat ---------- */
// 东张西望: eyes lead, head follows with overshoot, blink on the big turn,
// a small re-aim while holding, then the other side, then home.
function lookAround(p) {
  const L = p.left, R = p.right, A = p.amp;
  return [
    f(0, 1),
    f(.12, 1, {g: -1, e: p.ear}, 'snap'),
    f(.42, 1, {g: -1, x: L.x*A, y: L.y, r: L.r*A, s: p.lift, e: p.ear, t: 3}, 'back'),
    f(.9, 1, {g: -.8, x: L.x*A*.9, y: L.y, r: L.r*A*1.15, s: p.lift}),
    f(1.0, 1, {g: -1, x: L.x*A*.95, y: L.y-1, r: L.r*A*.9, s: p.lift}, 'snap'),
    f(1.35, 1, {g: -1, x: L.x*A*.95, y: L.y-1, r: L.r*A*.9, s: p.lift}),
    shut(1.47, {x: L.x*A*.4, y: L.y*.5, r: L.r*.3}, 'in'),
    f(1.62, 1, {g: 1, x: R.x*A*.3, y: R.y*.5, r: R.r*.3, e: p.ear*.3}, 'out'),
    f(1.92, 1, {g: 1, x: R.x*A, y: R.y, r: R.r*A, s: p.lift, e: p.ear*.5, t: -3}, 'back'),
    f(2.6, 1, {g: 1, x: R.x*A, y: R.y, r: R.r*A, s: p.lift}),
    f(2.7, 1, {g: .6, x: R.x*A*.9, y: R.y+1, r: R.r*A*.8, s: p.lift, e: 1.0}, 'snap'),
    f(3.0, 1, {g: .6, x: R.x*A*.9, y: R.y+1, r: R.r*A*.8, s: p.lift}),
    f(3.55, 1, {}, 'spring'),
  ];
}
// Head tilt: “嗯？” — ears perk, head cocks, a tiny second cock, back.
function curious(p) {
  const t = p.tilt, d = p.tiltDir;
  return [
    f(0, 1),
    f(.14, 1, {e: 2.5, y: -2, s: 1}, 'snap'),
    f(.46, 1, {r: t*d, x: 3*d, y: -4, s: 2, e: 2.0, g: .3*d, pd: .7}, 'back'),
    f(1.1, 1, {r: t*d, x: 3*d, y: -4, s: 2, e: 1.5, g: .3*d}),
    f(1.24, 1, {r: t*d*1.35, x: 4*d, y: -5, s: 2, e: 2.5, g: .4*d}, 'snap'),
    f(1.9, 1, {r: t*d*1.35, x: 4*d, y: -5, s: 2, e: 2.5, g: .4*d}),
    f(2.15, 1, {r: -t*d*.5, x: -2*d, y: -3, s: 1, e: 1.0, g: -.3*d}, 'out'),
    f(2.8, 1, {}, 'spring'),
  ];
}
// Perk: something made a sound. Ears snap, head pops up, freeze, relax.
function perk(p) {
  return [
    f(0, 1),
    f(.1, 1, {e: -0.6}, 'out'),
    f(.22, 1, {e: -0.6, y: -p.pop, s: p.pop*.5, g: p.side}, 'snap'),
    f(.95, 1, {e: -0.6, y: -p.pop, s: p.pop*.5, g: p.side}),
    f(1.2, 1, {e: 0.75, y: -p.pop*.8, s: p.pop*.4, g: p.side}),
    f(1.6, 1, {e: 0.75, y: -p.pop*.8, s: p.pop*.4, g: p.side}),
    f(2.1, 1, {}, 'spring'),
  ];
}
// Double take: glance, return, WAIT— snap back and stare.
function doubleTake(p) {
  const s = p.side;
  return [
    f(0, 1),
    f(.14, 1, {g: s, x: 3*s, r: 2*s}, 'out'),
    f(.36, 1, {g: s, x: 3*s, r: 2*s}),
    f(.52, 1, {g: -.2*s}, 'out'),
    f(.72, 1, {g: -.2*s}),
    f(.8, 1, {g: s, x: 9*s, y: -6, r: 6*s, s: 3, e: -1.5}, 'snap'),
    f(1.02, 1, {g: s, x: 8*s, y: -5, r: 5*s, s: 3, e: -1.5}, 'back'),
    f(1.7, 1, {g: s, x: 8*s, y: -5, r: 5*s, s: 3, e: -0.3}),
    shut(1.8, {x: 6*s, y: -4, r: 4*s, s: 2}, 'in'),
    f(1.95, 1, {g: s*.8, x: 6*s, y: -4, r: 4*s, s: 2}, 'out'),
    f(2.6, 1, {}, 'spring'),
  ];
}
// Cat “I love you” slow blink.
function slowBlink(p) {
  return [
    f(0, 1),
    f(.5, .35, {y: 1, lid: .3}, 'io'),
    f(1.05, 0, {y: 2, lid: 1, b: 1}),
    f(1.5, 0, {y: 2, lid: 1, b: 1}),
    f(2.1, 1, {y: 0}, 'out'),
    f(2.4, 1),
  ];
}
// Wake: eyes crack, anticipation squash, big stretch up (overshoot),
// shake-it-off wobble, look around quickly, settle.
function wake(p) {
  const w = p.wobble;
  return [
    f(0, 0),
    f(.35, .25, {a: .2, e: 1.0}),
    f(.55, 0, {}, 'in'),
    f(.9, .5, {a: -.1}),
    f(1.15, .7, {y: 3, s: -3, b: 1}, 'io'),
    f(1.55, .55, {y: -p.stretch, s: p.stretch*.45, b: 3, e: 1.5, r: p.lean, t: 4}, 'back'),
    f(2.05, .6, {y: -p.stretch, s: p.stretch*.45, b: 3.2, e: 1.5, r: p.lean, t: 5}),
    f(2.3, 1, {y: -p.stretch*.4, s: 2, e: -0.9}, 'snap'),
    f(2.42, 1, {y: -3, r: w, x: w*.6, e: 0.5, t: -w*.6}),
    f(2.54, 1, {y: -3, r: -w, x: -w*.6, e: 1.0, t: w*.6}),
    f(2.66, 1, {y: -2, r: w*.6, x: w*.3, e: -0.9}),
    f(2.78, 1, {y: -2, r: -w*.3, e: 1.0}),
    f(3.0, 1, {g: -.8}, 'out'),
    f(3.3, 1, {g: .8}, 'snap'),
    f(3.9, 1, {}, 'spring'),
  ];
}
// Sleep: yawn-ish lift, eyes droop, nod, jerk awake, give up, sink.
function sleep(p) {
  return [
    f(0, 1),
    f(.35, .6, {y: -2, s: 1, lid: .2}),
    f(.8, .35, {y: 2, r: p.lean*.5, lid: .5}),
    f(1.3, .12, {y: p.nod, r: p.lean, s: -2, lid: .8}, 'in'),
    f(1.42, .7, {y: -2, r: 0, e: -1.2}, 'snap'),
    f(1.8, .6, {y: -1}),
    f(2.3, .2, {y: 3, r: p.lean*.6, lid: .7}),
    f(2.9, 0, {y: p.nod*.9, r: p.lean, s: -3, b: 1}, 'io'),
    f(3.7, 0, {y: 2, s: -1, b: 2}),
    f(4.4, 0),
  ];
}
// Dream while asleep: paws-twitch energy shown through ears and a tiny jolt.
function dream(p) {
  return [
    f(0, 0),
    f(.14, 0, {e: 1.25, y: -1, t: 3}, 'out'),
    f(.34, 0, {e: 0.25, t: -2}),
    f(.54, 0, {e: 1.0, y: -.5, t: 2}),
    f(.8, 0, {e: 0.0, t: 0}),
    f(.9, 0, {a: .12, lid: 0}),
    f(1.0, 0, {x: 1, y: -2, s: 1}, 'snap'),
    f(1.3, 0, {}, 'spring'),
    f(2.2, 0, {b: 3.5, y: -1, s: 1}),
    f(3.4, 0, {b: 0}, 'io'),
  ];
}
// Deep sigh while asleep (keeps the old “breathe” action name).
function sigh(p) {
  return [
    f(0, 0),
    f(1.4, 0, {b: 4, y: -1.5, s: 1.5}),
    f(1.7, 0, {b: 4.2, y: -1.6, s: 1.5}),
    f(3.1, 0, {b: 0, y: 1, s: -1}, 'out'),
    f(3.3, 0, {e: 1.5}, 'snap'),
    f(3.6, 0, {}, 'spring'),
  ];
}
// Happy bounce (task done).
function happy(p) {
  return [
    f(0, 1),
    f(.12, 1, {y: 3, s: -3}, 'in'),
    f(.3, 1, {y: -p.hop, s: 4, e: -1.2}, 'out'),
    f(.46, 1, {y: 2, s: -2}, 'in'),
    f(.62, 1, {y: -p.hop*.5, s: 2, e: 1.0, t: -4}, 'out'),
    f(.76, 1, {y: 1, s: -1}, 'in'),
    f(.9, 1, {jj: 1, y: -1}, 'snap'),
    f(1.5, 1, {jj: 1}),
    f(1.8, 1, {}, 'out'),
  ];
}
// Blink-look kept as the quick reaction to being clicked.
function blinkLook(p) {
  return [f(0, 1), shut(.1, {y: .5}, 'snap'), f(.24, 1, {g: p.side*.6, e: 1.5}, 'out'),
    f(.5, 1, {g: p.side*.6, e: 1.5}), f(.9, 1, {}, 'spring')];
}

// ---- paws & legs ----
// h: paws out (0–1) · pl/pr: lift each paw (px) · ps: swipe reach · u: stand (0–1)
function work(p) {            // 打字：低头专注，两只小手交替敲
  const k = [f(0, 1), f(.25, 1, {h: 1, y: 3, g: .15*p.face, lid: .2}, 'out')];
  let t = .25;
  const beats = [[5, 0], [0, 5], [4, 0], [0, 3], [6, 0], [0, 6], [3, 2], [0, 5], [5, 0], [0, 4]];
  for (const [l, r] of beats) {
    t += .13; k.push(f(t, 1, {h: 1, y: 3, g: .15*p.face, lid: .2, pl: l, pr: r}, 'snap'));
    t += .09; k.push(f(t, 1, {h: 1, y: 3, g: .15*p.face, lid: .2}, 'in'));
  }
  k.push(f(t + .25, 1, {h: 1, y: 2, g: -.4*p.face, lid: .1, t: 2}));   // glance at the screen
  k.push(f(t + .7, 1, {h: 1, y: 2, g: -.4*p.face, lid: .1}));
  k.push(f(t + .95, 1, {h: 1, y: 3, g: .15*p.face, lid: .2}));
  return k;                     // ends with paws out: chain it, then play 'work-end'
}
function workEnd(p) {
  return [f(0, 1, {h: 1, y: 3, lid: .2}), f(.2, 1, {h: 1, y: -2, s: 1, pl: 3, pr: 3}, 'out'),
    f(.55, 1, {h: 0, e: 1}, 'in'), f(.9, 1, {}, 'spring')];
}
function knead(p) {           // 踩奶：眯眼，左右交替往下踩
  const k = [f(0, 1), f(.3, .45, {h: 1, lid: .4, y: 1}, 'out')];
  let t = .3;
  for (let i = 0; i < 6; i++) {
    t += .28; k.push(f(t, .4, {h: 1, lid: .45, y: 1 + (i % 2), pl: i % 2 ? 0 : 5, pr: i % 2 ? 5 : 0, r: (i % 2 ? 1 : -1), t: i % 2 ? 1 : -1}));
  }
  k.push(f(t + .3, 1, {h: 1, jj: 1, y: 2, b: 1}));
  k.push(f(t + .9, 1, {h: 1, jj: 1, y: 2, b: 1}));
  k.push(f(t + 1.3, 1, {h: 0}, 'out'), f(t + 1.6, 1, {}, 'spring'));
  return k;
}
function bat(p) {             // 伸爪拍：盯住—屁股扭—啪！再补一下
  const d = p.face;
  return [
    f(0, 1), f(.2, 1, {g: d, e: -.6, y: 2, s: -1, pd: 1}, 'out'),
    f(.45, 1, {g: d, y: 3, s: -2, x: -2*d, h: 1, t: 2, pd: 1}),
    f(.6, 1, {g: d, y: 3, s: -2, x: -1*d, h: 1, t: -2, pd: 1}),
    f(.75, 1, {g: d, y: 3, s: -2, x: -2*d, h: 1, t: 2, pd: 1}),
    f(.9, 1, {g: d, y: 3, s: -2, x: -1*d, h: 1, t: -1, pd: 1}),
    f(1.02, 1, {g: d, y: -4, s: 3, x: 5*d, r: 4*d, h: 1, ps: 16, e: 1, pd: .8}, 'snap'),
    f(1.25, 1, {g: d, y: -2, s: 1, x: 3*d, r: 2*d, h: 1, ps: 4}, 'in'),
    f(1.33, 1, {g: d, y: -4, s: 3, x: 5*d, r: 4*d, h: 1, ps: 13, pd: .8}, 'snap'),
    f(1.6, 1, {g: .6*d, y: 0, h: 1, ps: 0}, 'out'),
    shut(1.75, {h: 1}), f(1.9, 1, {h: 1, g: .3*d}, 'out'),
    f(2.3, 1, {h: 0}), f(2.6, 1, {}, 'spring'),
  ];
}
function wave(p) {            // 打招呼：举起小手晃两下
  const d = p.face;
  const k = [f(0, 1), f(.25, 1, {h: 1, e: 1, y: -2, s: 1}, 'out'), f(.5, 1, {h: 1, ps: 12, e: 1.5, y: -3, r: 3*d, s: 1}, 'back')];
  let t = .5;
  for (let i = 0; i < 4; i++) { t += .16; k.push(f(t, 1, {h: 1, ps: i % 2 ? 12 : 9, pr: i % 2 ? 0 : 3, e: 1.5, y: -3, r: (i % 2 ? 3 : 1)*d, s: 1})); }
  k.push(f(t + .2, 1, {h: 1, ps: 10, y: -3, hh: 1}, 'back'), f(t + .7, 1, {h: 1, y: -2, hh: 1}), f(t + .95, 1, {h: 1, y: -1}, 'out'), f(t + 1.25, 1, {h: 0}), f(t + 1.55, 1, {}, 'spring'));
  return k.map(r => r);
}
function stand(p) {           // 站起来：撑起四条小短腿，伸懒腰，看一圈，坐回去
  const d = p.face;
  return [
    f(0, 1), f(.25, 1, {y: 2, s: -2, e: 1}, 'in'),
    f(.7, 1, {u: 1, y: -2, s: 2, t: 4, e: 1.5}, 'back'),
    f(1.1, 1, {u: 1, y: -6, s: 4, b: 2, t: 6, e: 1.5, lid: .3}),
    f(1.5, 1, {u: 1, y: -3, s: 2, t: 5}, 'out'),
    f(1.8, 1, {u: 1, g: d, x: 5*d, r: 4*d, t: 3}, 'back'),
    f(2.4, 1, {u: 1, g: d, x: 5*d, r: 4*d, t: 5}),
    f(2.65, 1, {u: 1, g: -d, x: -4*d, r: -3*d, t: 2}, 'back'),
    f(3.2, 1, {u: 1, g: -d, x: -4*d, r: -3*d, t: 4}),
    f(3.5, 1, {u: 1, t: 5}),
    f(3.62, 1, {u: .85, y: 1, t: 3}, 'in'), f(3.74, 1, {u: .9, t: 5}),
    f(4.2, 1, {u: 0, y: 2, s: -2}, 'in'), f(4.6, 1, {}, 'spring'),
  ];
}

// Asleep poses carry k=1 (only shapes that read k use it, e.g. 踏踏 lies down).
const K = poseFields.indexOf('k') + 1;
const asleepClip = c => c.map(r => { const n = [...r]; n[K] = 1; return n; });
function sleepTuck(c) {             // tuck in over the last 60% of the clip
  const end = c.at(-1)[0];
  return c.map(r => { const n = [...r]; const u = Math.max(0, Math.min(1, (r[0] / end - .4) / .6)); n[K] = u * u * (3 - 2 * u); return n; });
}
function wakeUntuck(c) {            // stay tucked until the stretch, then stand
  const i = c.findIndex(r => r[2] < -3);  // first row with lift (y < -3)
  const t = c[Math.max(1, i)][0];
  return c.map(r => { const n = [...r]; n[K] = r[0] < t * .7 ? 1 : 0; return n; });
}
function build(p) {
  const clips = buildRaw(p);
  // Curl cannot uncoil into this gesture; 踏踏 is already standing.
  if (p === personalities.curl || p === personalities.stand) delete clips.stand;
  clips.sleep = sleepTuck(clips.sleep);
  clips.wake = wakeUntuck(clips.wake);
  for (const n of ['dream', 'breathe']) clips[n] = asleepClip(clips[n]);
  return clips;
}
function buildRaw(p) {
  const k = p.tempo;
  return {
    wake: tempo(wake(p), k), look: tempo(lookAround(p), k), 'blink-look': blinkLook(p),
    sleep: tempo(sleep(p), k), breathe: tempo(sigh(p), k),
    curious: tempo(curious(p), k), 'double-take': tempo(doubleTake(p), k),
    'slow-blink': tempo(slowBlink(p), k), dream: dream(p), happy: happy(p),
    work: work(p), 'work-end': workEnd(p), knead: tempo(knead(p), k), bat: bat(p), wave: wave(p), stand: tempo(stand(p), k),
  };
}

const base = {face: -1, tempo: 1, amp: 1, ear: 1.5, lift: 2, tilt: 8, tiltDir: 1, pop: 7, side: 1,
  stretch: 12, lean: 2, wobble: 5, nod: 6, hop: 9,
  left: {x: -8, y: -5, r: -6}, right: {x: 8, y: -5, r: 6}};
const P = (o) => ({...base, ...o, left: {...base.left, ...o.left}, right: {...base.right, ...o.right}});

export const personalities = {
  curl: P({tempo: 1.15, amp: .85, ear: 2.0, tilt: 7, tiltDir: -1, pop: 5, side: -1, stretch: 9, lean: -3, wobble: 3, nod: 5, hop: 6,
    left: {x: -8, y: -6, r: -7}, right: {x: 5, y: -6, r: 5}}),
  loaf: P({tempo: 1.35, amp: 1, ear: 1.0, lift: 2, tilt: 5, pop: 5, stretch: 15, lean: 0, wobble: 4, nod: 5, hop: 7,
    left: {x: -9, y: -7, r: -3}, right: {x: 9, y: -7, r: 3}}),
  tall: P({tempo: .8, amp: 1.2, ear: 2.5, lift: 4, tilt: 9, pop: 11, side: -1, stretch: 17, lean: -2, wobble: 6, nod: 10, hop: 12,
    left: {x: -11, y: -8, r: -6}, right: {x: 12, y: -9, r: 6}}),
  rice: P({face: 1, tempo: 1, amp: 1, ear: 1.5, lift: 1, tilt: 13, pop: 6, stretch: 9, lean: 3, wobble: 8, nod: 5, hop: 9,
    left: {x: -5, y: -4, r: -10}, right: {x: 5, y: -4, r: 10}}),
  bean: P({face: 1, tempo: .78, amp: 1.15, ear: 2.0, lift: 3, tilt: 9, tiltDir: -1, pop: 9, side: -1, stretch: 14, lean: -3, wobble: 7, nod: 7, hop: 13,
    left: {x: -9, y: -8, r: -7}, right: {x: 9, y: -5, r: 6}}),
  stand: P({face: -1, tempo: .9, amp: 1.1, ear: 4, lift: 3, tilt: 10, pop: 8, stretch: 12, lean: -2, wobble: 6, nod: 6, hop: 12,
    left: {x: -9, y: -6, r: -7}, right: {x: 8, y: -6, r: 6}}),
  puddle: P({tempo: 1.5, amp: .9, ear: 1.0, lift: 1, tilt: 6, pop: 6, stretch: 10, lean: 0, wobble: 3, nod: 3, hop: 6,
    left: {x: -7, y: -4, r: -2}, right: {x: 8, y: -5, r: 2}}),
};

// Idle director: how often each cat does something on its own, and what.
const directors = {
  curl:   {every: [5, 10], awake: [['look', 3], ['curious', 2], ['slow-blink', 2], ['knead', 2], ['double-take', 1]], asleep: [['dream', 2], ['breathe', 3]]},
  loaf:   {every: [7, 13], awake: [['slow-blink', 4], ['look', 2], ['curious', 1], ['knead', 2]], asleep: [['breathe', 4], ['dream', 1]]},
  tall:   {every: [3.5, 7], awake: [['look', 4], ['bat', 2], ['double-take', 3], ['curious', 1], ['wave', 1]], asleep: [['dream', 2], ['breathe', 2]]},
  rice:   {every: [4.5, 9], awake: [['curious', 4], ['look', 2], ['wave', 2], ['slow-blink', 1]], asleep: [['breathe', 3], ['dream', 2]]},
  bean:   {every: [3, 6], awake: [['double-take', 3], ['look', 3], ['bat', 3], ['curious', 1], ['happy', 1], ['stand', 1]], asleep: [['dream', 3], ['breathe', 2]]},
  stand:  {every: [3.5, 7], awake: [['look', 3], ['curious', 2], ['happy', 2], ['bat', 2], ['wave', 1], ['double-take', 1]], asleep: [['dream', 2], ['breathe', 3]]},
  puddle: {every: [8, 15], awake: [['slow-blink', 4], ['look', 2], ['knead', 2]], asleep: [['breathe', 4], ['dream', 1]]},
};
// Always-on micro life per cat: breath [amp, period], blink cadence, eye drift.
const lives = {
  curl:   {sway: 1.6, breath: [.8, 3.2], sleepBreath: [2.6, 3.8], blinkEvery: [2.5, 5.5], doubleBlink: .25, drift: .45, twitch: 2, twitchEvery: [4, 9]},
  loaf:   {sway: .6, breath: [1, 3.8], sleepBreath: [3, 4.4], blinkEvery: [3.5, 7], doubleBlink: .1, blinkDur: .28, drift: .25, twitch: 2, twitchEvery: [6, 12]},
  tall:   {sway: 1, breath: [.6, 2.6], sleepBreath: [2.2, 3.4], blinkEvery: [2, 4.5], doubleBlink: .45, blinkDur: .16, drift: .6, twitch: 2, twitchEvery: [3, 7]},
  rice:   {sway: .8, breath: [.8, 3], sleepBreath: [2.8, 3.6], blinkEvery: [2.5, 5], doubleBlink: .2, drift: .4, twitch: 2, twitchEvery: [4, 9]},
  bean:   {sway: 1.3, breath: [.7, 2.4], sleepBreath: [2.4, 3.2], blinkEvery: [1.8, 4], doubleBlink: .5, blinkDur: .15, drift: .7, twitch: 2, twitchEvery: [2.5, 6]},
  stand:  {sway: 2.2, breath: [.7, 2.8], sleepBreath: [2.4, 3.4], blinkEvery: [2.2, 5], doubleBlink: .3, drift: .5, twitch: 2, twitchEvery: [4, 8]},
  puddle: {sway: .5, breath: [1.1, 4.2], sleepBreath: [3.2, 4.8], blinkEvery: [4, 8], doubleBlink: .05, blinkDur: .34, drift: .2, twitch: 2, twitchEvery: [7, 14]},
};
// Where paws and legs sit (x in the 320 viewBox; y is found from the body edge).
export const limbs = {
  curl: {paws: [100, 134], legs: [96, 124, 208, 236], L: 22},
  loaf: {paws: [92, 128], legs: [92, 124, 215, 245], L: 20},
  tall: {paws: [126, 166], legs: [122, 160, 200, 232], L: 18},
  rice: {paws: [130, 170], legs: [98, 130, 175, 210], L: 18},
  bean: {paws: [150, 180], legs: [80, 108, 148, 172], L: 20},
  stand: {paws: [], legs: [], L: 0, tail: {
    root: [262, 180], seg: 12.5, w: 10.5,
    // chain headings in degrees (0 = right, 90 = down). Stand: up with a lazy S.
    up:    [-84, -87, -91, -95, -93, -87, -80, -74],
    // Asleep: hug the rump, then run forward along the floor in front of the paws.
    sleep: [30, 70, 112, 152, 176, 181, 183, 188],
    front: [0, 240, 320, 320]   // clip box (x0,y0,x1,y1) where the tail is drawn over the body
  }},
  puddle: {paws: [108, 150], legs: [80, 118, 200, 240], L: 16},
};
const meta = {
  curl:   ['卷卷 · 害羞的观察者：慢半拍探头，耳朵先听，看完会缩回去；梦里耳朵抖得最多。', [2.05, 1.4], [[82, 146], [126, 144]]],
  loaf:   ['糯米 · 慵懒的大枕头：动作最慢最软，最爱对你慢眨眼；醒来伸一个大大的懒腰。', [2.05, 1.42], [[90, 119], [148, 127]]],
  tall:   ['年糕 · 警觉的哨兵：头转得快、停得准，爱“回头确认”；困了会点头再惊醒。', [2.1, 1.4], [[120, 67], [178, 59]]],
  rice:   ['饭团 · 好奇宝宝：动不动就歪头“嗯？”，歪完还要再歪一下；醒来晃得最厉害。', [2.05, 1.5], [[116, 97], [179, 103]]],
  bean:   ['豆包 · 小疯子：节奏最快，眼睛乱瞟、双眨、会开心地弹两下；睡着也不老实。', [2.05, 1.4], [[192, 71], [241, 77]]],
  stand:  ['踏踏 · 散步家：四条腿站着，尾巴一直翘着摇；爱东张西望、开心蹦跶，好奇了就伸爪子。', [2.05, 1.5], [[98, 104], [154, 100]]],
  puddle: ['小饼 · 一滩液体猫：能不动就不动，眼睛先动脸再跟；呼吸最深，眨眼最慢。', [2.15, 1.25], [[77, 187], [105, 172]]],
};

export const performances = Object.fromEntries(Object.entries(meta).map(([key, [description, eyeScale, ears]]) =>
  [key, {description, eyeScale, ears, clips: build(personalities[key]), face: personalities[key].face, limbs: limbs[key], life: lives[key], director: directors[key]}]));

export function readableContour(eye, center, scale, openness, lid, asymmetry = 0) {
  const open = Math.max(0, Math.min(1, openness + asymmetry));
  const angle = eye.angle * Math.PI / 180, c = Math.cos(angle), s = Math.sin(angle);
  const contour = eye.closed.map((v, i) => typeof v === 'string' ? v :
    v + (eye.blinkClosed[i] - v) * lid +
    (eye.open[i] - v - (eye.blinkClosed[i] - v) * lid) * open);
  for (let i = 0; i < contour.length;) {
    if (typeof contour[i] === 'string') { i++; continue; }
    const dx = contour[i] - center[0], dy = contour[i + 1] - center[1];
    const x = (dx*c + dy*s) * (1.2 + (scale[0] - 1.2)*open);
    const y = (-dx*s + dy*c) * (1.35 + (scale[1] - 1.35)*open);
    contour[i++] = center[0] + x*c - y*s;
    contour[i++] = center[1] + x*s + y*c;
  }
  return contour;
}

/* ---------- 卷卷 uncurl: thin-plate spline from curled → standing ---------- */
const curlSrc = [[157.3,55.6],[137.1,58.1],[117.5,63.7],[99.9,73.8],[90.9,91.4],[100.2,109],[117.3,119.9],[136.7,126.1],[156.7,130.4],[175.9,137.1],[191.3,150.2],[197.4,169.3],[191.2,188.4],[173.5,196],[158.4,183.1],[150.1,164.5],[140.5,146.5],[123.5,136.9],[110.6,151.2],[93.9,148.3],[75.4,141],[57.6,150.1],[46.5,167.1],[42.9,187],[47.4,206.8],[57.4,224.6],[71,239.7],[87.8,251.2],[106.7,258.8],[126.6,263],[147,264.7],[167.3,264.2],[187.6,261.6],[207.3,256.4],[225.9,248.1],[242.3,236.1],[256.1,221],[266.8,203.7],[274.1,184.7],[277.1,164.6],[276.1,144.2],[271.1,124.5],[262.4,106.1],[250,89.8],[234.8,76.4],[217.1,66.2],[197.8,59.7],[177.7,56.3],
  [96,205.8],[130.8,193.3]];
const curlDst = [[230,64],[218,62],[207,66],[200,77],[203,88],[212,94],[219,102],[224,114],[226,128],[222,141],[210,149],[194,152],[180,153],[163,152],[151,150],[142,147],[134,143],[123.5,136.9],[110.6,151.2],[93.9,148.3],[75.4,141],[57.6,150.1],[46.5,167.1],[42.9,187],[47.4,206.8],[57.4,224.6],[71,239.7],[87.8,251.2],[106.7,258.8],[126.6,260],[147,261],[167.3,261],[187.6,260],[207.3,258],[226,252],[243,241],[255,224],[261,205],[262,188],[260,174],[256,162],[253,150],[252,137],[255,123],[258,108],[256,93],[250,80],[241,70],
  [96,205.8],[130.8,193.3]];
function makeTPS(src, dst) {
  const n = src.length, U = r2 => r2 === 0 ? 0 : r2 * Math.log(r2);
  const N = n + 3, A = Array.from({length: N}, () => new Float64Array(N));
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < n; j++) A[i][j] = U((src[i][0]-src[j][0])**2 + (src[i][1]-src[j][1])**2);
    A[i][n] = 1; A[i][n+1] = src[i][0]; A[i][n+2] = src[i][1];
    A[n][i] = 1; A[n+1][i] = src[i][0]; A[n+2][i] = src[i][1];
  }
  const solve = (b) => {           // Gaussian elimination with partial pivoting
    const M = A.map((r, i) => [...r, b[i]]);
    for (let c = 0; c < N; c++) {
      let p = c; for (let r = c+1; r < N; r++) if (Math.abs(M[r][c]) > Math.abs(M[p][c])) p = r;
      [M[c], M[p]] = [M[p], M[c]];
      for (let r = 0; r < N; r++) if (r !== c) { const f = M[r][c] / M[c][c]; if (f) for (let k = c; k <= N; k++) M[r][k] -= f * M[c][k]; }
    }
    return M.map((r, i) => r[N] / r[i]);
  };
  const wx = solve([...dst.map(d => d[0] - 0), 0, 0, 0]), wy = solve([...dst.map(d => d[1]), 0, 0, 0]);
  return (x, y) => {
    let X = wx[n] + wx[n+1]*x + wx[n+2]*y, Y = wy[n] + wy[n+1]*x + wy[n+2]*y;
    for (let i = 0; i < n; i++) { const u = U((x-src[i][0])**2 + (y-src[i][1])**2); X += wx[i]*u; Y += wy[i]*u; }
    return [X, Y];
  };
}
let curlTPS = null;
const unroll = {curl: () => curlTPS ||= makeTPS(curlSrc, curlDst)};

/* ---------- v2 deformation ---------- */
const sm = (a, b, x) => { const u = Math.max(0, Math.min(1, (x - a) / (b - a))); return u * u * (3 - 2 * u); };
const boxes = new WeakMap();
function box(shape) {
  let b = boxes.get(shape);
  if (!b) {
    const n = shape.body.match(/-?\d+(?:\.\d+)?/g).map(Number);
    const xs = n.filter((_, i) => i % 2 === 0), ys = n.filter((_, i) => i % 2);
    b = {x0: Math.min(...xs), x1: Math.max(...xs), y0: Math.min(...ys), y1: Math.max(...ys)};
    b.cx = (b.x0 + b.x1) / 2; b.h = b.y1 - b.y0; b.w = b.x1 - b.x0;
    boxes.set(shape, b);
  }
  return b;
}
// Tail tips that may flick on their own (t). Center = pivot, r = reach.
const tails = {curl: {cx: 150, cy: 112, r: 62, x0: 80, x1: 175, y0: 50, y1: 132}};
function rot(px, py, cx, cy, deg) {
  const a = deg * Math.PI / 180, c = Math.cos(a), s = Math.sin(a);
  return [cx + (px - cx) * c - (py - cy) * s, cy + (px - cx) * s + (py - cy) * c];
}
export function deformPoint(shape, performance, x, y, q) {
  const B = box(shape);
  // 1. tail flick (before the head so the head pocket is untouched)
  let px = x, py = y;
  const T = tails[shape.key];
  if (T && q.t) {
    const w = sm(T.x1, T.x0, x) * sm(T.y1, T.y0, y);
    if (w) [px, py] = rot(px, py, T.cx, T.cy, q.t * (T.k || 2.2) * w);
  }
  // 2. authored local head deformation
  const point = shape.deform(px, py, q);
  // 2b. uncurl (卷卷 stands up: the ring straightens into back + raised tail)
  if (unroll[shape.key] && q.u > 0) {
    const u = 0, [X, Y] = unroll[shape.key]()(point[0], point[1]);
    point[0] += (X - point[0]) * u; point[1] += (Y - point[1]) * u;
  }
  // 3. ears: rotate the lobe about its base — keeps length, never spikes
  const e = Math.max(-2, Math.min(4, q.e || 0));
  if (e) for (const [i, [ex, ey]] of performance.ears.entries()) {
    const d = Math.hypot((x - ex) / 22, (y - ey) / 20);
    if (d >= 1) continue;
    const w = sm(1, .15, d);
    const bx = ex + (i ? -6 : 6), by = ey + 16;
    const [rx, ry] = rot(point[0], point[1], bx, by, e * 3.2 * w * (i ? 1 : -1));
    point[0] = rx; point[1] = ry;
  }
  // 4. whole body follows: breath swells from the floor, lean and squash carry
  //    into the back half so nothing looks frozen.
  const up = Math.max(0, (B.y1 - y) / B.h);          // 0 at floor, 1 at top
  const side = (x - B.cx) / (B.w / 2);
  point[0] += (q.b || 0) * .9 * side * (1 - up) * .6 + ((q.x || 0) * .12 + (q.r || 0) * .35 + (q.t || 0) * .5) * up;
  point[1] -= ((q.b || 0) * .55 + (q.s || 0) * .22) * up;
  return point;
}
