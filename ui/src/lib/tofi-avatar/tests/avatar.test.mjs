import test from 'node:test';
import assert from 'node:assert/strict';
import {mountCat as mountAvatar, catalog, normalizeConfig, paletteOptions} from '../index.js';
import {performances, readableContour, deformPoint, poseFields} from '../performances.js';

// A deterministic DOM/RAF boundary, not a browser rendering substitute. The
// acceptance page imports the same production module for actual visual checks.
class Target extends EventTarget {
  listeners = new Map();
  addEventListener(type, fn) { super.addEventListener(type, fn); this.listeners.set(fn, type); }
  removeEventListener(type, fn) { super.removeEventListener(type, fn); this.listeners.delete(fn); }
}
class Element extends Target {
  constructor(tag) { super(); this.tag = tag; this.attributes = {}; this.children = []; this.style = {}; }
  setAttribute(k, v) { this.attributes[k] = String(v); }
  removeAttribute(k) { delete this.attributes[k]; }
  append(...nodes) { nodes.forEach(n => { n.parent = this; this.children.push(n); }); }
  remove() { if (this.parent) this.parent.children = this.parent.children.filter(n => n !== this); }
  replaceChildren(...nodes) { this.children = []; this.append(...nodes); }
  querySelector(tag) { return this.children.find(n => n.tag === tag) ?? this.children.map(n => n.querySelector(tag)).find(Boolean); }
  getBBox() {
    const values = (this.attributes.d || '').match(/-?\d+(?:\.\d+)?/g)?.map(Number) || [];
    const xs = values.filter((_,i)=>i%2===0), ys = values.filter((_,i)=>i%2===1);
    if (!xs.length) return {x:0,y:0,width:0,height:0};
    const x=Math.min(...xs), y=Math.min(...ys);
    return {x,y,width:Math.max(...xs)-x,height:Math.max(...ys)-y};
  }
  cloneNode() {
    const n = new Element(this.tag); n.attributes = {...this.attributes}; n.style = {...this.style};
    n.textContent = this.textContent; n.append(...this.children.map(c => c.cloneNode())); return n;
  }
}
function markup(node) {
  return `<${node.tag} ${Object.entries(node.attributes).map(([k,v])=>`${k}="${v}"`).join(' ')}>${node.textContent ?? ''}${node.children.map(markup).join('')}</${node.tag}>`;
}
function environment() {
  const doc = new Target(), media = new Target(), frames = new Map(), observers = [];
  doc.hidden = false; doc.createElementNS = (_, tag) => new Element(tag); media.matches = false;
  let id = 0, now = 0;
  Object.assign(globalThis, {
    document: doc, window: {matchMedia: () => media},
    requestAnimationFrame: fn => { frames.set(++id, fn); return id; },
    cancelAnimationFrame: id => frames.delete(id),
    XMLSerializer: class { serializeToString(node) { return markup(node); } },
    IntersectionObserver: class {
      constructor(fn) { this.fn = fn; observers.push(this); }
      observe() { this.connected = true; }
      disconnect() { this.connected = false; }
      visible(value) { this.fn([{isIntersecting: value}]); }
    }
  });
  return {doc, media, frames, observers, host: new Element('span'),
    step(ms = 50) { now += ms; const pending = [...frames.values()]; frames.clear(); pending.forEach(fn => fn(now)); },
    drain() { for (let i = 0; frames.size && i < 2000; i++) this.step(); assert.equal(frames.size, 0, 'animation must terminate'); }
  };
}
// Legacy API checks run without perpetual idle RAFs; the life layer is tested
// separately below with its real default enabled.
const mountCat = (host, options) => mountAvatar(host, {life:false, ...options});
const rest = Object.fromEntries(poseFields.map(k => [k, 0]));
const shapes = {};
for (const {id} of catalog.shapes) shapes[id] = (await import(`../shapes/${id}.js`)).default;

test('seven personality scores and v2 action catalog retain readable eyes', () => {
  for (const [key, p] of Object.entries(performances)) {
    for (const action of catalog.actions) {
      if (action === 'stand' && ['curl','stand'].includes(key)) continue;
      assert.ok(p.clips[action], `${key}: ${action}`);
    }
    for (const [action, rows] of Object.entries(p.clips)) {
      rows.forEach((row, i) => {
        assert.ok(row.length === poseFields.length + 1 || row.length === poseFields.length + 2);
        assert.ok(row.slice(0, poseFields.length + 1).every(Number.isFinite));
        if (i) assert.ok(row[0] > rows[i-1][0]);
      });
      assert.equal(rows.at(-1)[7], ['sleep','dream','breathe'].includes(action) ? 0 : 1);
      if (action === 'breathe') assert.ok(rows.every(row=>row[7]===0));
    }
    assert.ok(Math.abs(p.clips.look[1][6]) >= .8);
    assert.equal(p.clips.look[1][1], 0, 'eyes lead body');
    shapes[key].eyes.forEach((eye, i) => {
      const contour = readableContour(eye, shapes[key].eyeCenters[i], p.eyeScale, 1, 0);
      const widths = data => {
        const coords = data.filter(v=>typeof v === 'number');
        const a = eye.angle*Math.PI/180;
        const xs = coords.filter((_,i)=>i%2===0).map((x,i)=>x*Math.cos(a)+coords[2*i+1]*Math.sin(a));
        return Math.max(...xs)-Math.min(...xs);
      };
      assert.ok(widths(contour)/widths(eye.open) > 2, `${key}: twice the original eye width`);
      assert.notDeepEqual(readableContour(eye, shapes[key].eyeCenters[i], p.eyeScale, 0, 0),
        readableContour(eye, shapes[key].eyeCenters[i], p.eyeScale, 0, 1), 'sleep and blink distinct');
    });
  }
  assert.equal(catalog.shapes.length, 7);
  assert.equal(new Set(Object.values(performances).map(p=>JSON.stringify(p.clips.look))).size, 7);
});

test('v2 deformation remains finite for all authored poses', () => {
  const anchors = {curl:[157,56], loaf:[260,230], tall:[260,208], rice:[150,270], bean:[129,250], puddle:[270,223]};
  for (const [key, point] of Object.entries(anchors)) {
    for (const rows of Object.values(performances[key].clips)) for (const row of rows) {
      const q = Object.fromEntries(poseFields.map((k,i)=>[k,row[i+1]]));
      assert.ok(deformPoint(shapes[key], performances[key], ...point, q).every(Number.isFinite), key);
    }
  }
});

test('every finite v2 clip finishes with its correct idle state', async () => {
  for (const {id} of catalog.shapes) for (const action of catalog.actions.filter(a=>a!=='work')) {
    if (!performances[id].clips[action]) continue;
    const env = environment(), cat = mountCat(env.host, {shape:id});
    cat.setIdle(['sleep','look','blink-look'].includes(action) ? 'awake' : 'asleep');
    const done = cat.play(action); env.drain();
    const result = await done;
    assert.equal(result.cancelled, false);
    assert.equal(result.state, ['sleep','dream','breathe'].includes(action) ? 'asleep' : 'awake');
    assert.doesNotMatch(cat.exportSVG(), /NaN|undefined|Infinity/);
    assert.equal(cat.getState().action, null); cat.destroy();
  }
});

test('interruption preserves the exact frame and resolves the previous promise', async () => {
  const env = environment(), cat = mountCat(env.host, {shape:'tall',initialState:'awake'});
  const sleeping = cat.play('sleep'); env.step(); env.step(70); env.step(70);
  const before = cat.exportSVG(), waking = cat.play('wake'); env.step();
  assert.equal(cat.exportSVG(), before);
  assert.deepEqual(await sleeping, {action:'sleep',cancelled:true,reason:'interrupted'});
  env.drain(); assert.equal((await waking).state, 'awake'); cat.destroy();
});

const bodyPath = text => text.match(/<path id="[^"]+-body" d="([^"]+)"/)[1].match(/-?\d+(?:\.\d+)?/g).map(Number);

test('appearance changes preserve motion; shape changes cancel and use last settled idle', async () => {
  const env = environment(), cat = mountCat(env.host, {shape:'curl',initialState:'awake',palette:'ocean'});
  const playing = cat.play('look'); env.step(); env.step();
  cat.setAppearance({pattern:'cowpatch'});
  assert.equal(cat.getState().action, 'look'); assert.equal(cat.getConfig().palette, 'cow');
  cat.setAppearance({pattern:'solid'}); assert.equal(cat.getConfig().palette, 'ocean');
  cat.setAppearance({pattern:'tipped',palette:'pointrose'});
  cat.setAppearance({pattern:'solid'}); cat.setAppearance({pattern:'tipped'});
  assert.equal(cat.getConfig().palette, 'pointrose');
  cat.setAppearance({shape:'bean'});
  assert.equal((await playing).reason, 'shape-changed');
  assert.equal(cat.getState().idle, 'awake'); assert.equal(env.host.children.length, 1);
  assert.equal(env.frames.size, 0); cat.destroy();
});

test('reduced motion applies terminal states with no frames; system changes settle in-flight work', async () => {
  for (const {id} of catalog.shapes) for (const action of catalog.actions) {
    if (!performances[id].clips[action]) continue;
    const env = environment(), cat = mountCat(env.host, {shape:id,reducedMotion:true});
    assert.equal((await cat.play(action)).skipped, true); assert.equal(env.frames.size, 0); cat.destroy();
  }
  const env = environment(), cat = mountCat(env.host);
  const done = cat.play('wake'); env.step();
  env.media.matches = true; env.media.dispatchEvent(new Event('change'));
  assert.equal((await done).skipped, true); assert.equal(env.frames.size, 0); cat.destroy();
});

test('hidden/offscreen pause and resume; elapsed hidden time does not jump animation', async () => {
  const env = environment(), cat = mountCat(env.host);
  const done = cat.play('wake'); env.step(); env.step(); const before = cat.exportSVG();
  env.doc.hidden = true; env.doc.dispatchEvent(new Event('visibilitychange'));
  assert.equal(env.frames.size, 0); env.step(100000);
  env.doc.hidden = false; env.doc.dispatchEvent(new Event('visibilitychange')); env.step();
  // Resume may advance one bounded frame, but not the hidden 100 seconds.
  const after = cat.exportSVG();
  const delta = Math.max(...bodyPath(after).map((n,i)=>Math.abs(n-bodyPath(before)[i])));
  assert.ok(delta < 3, `hidden resume jumped by ${delta} SVG units`);
  env.observers[0].visible(false); assert.equal(env.frames.size, 0);
  env.observers[0].visible(true); env.drain(); assert.equal((await done).state, 'awake'); cat.destroy();
});

test('destroy is idempotent, settles pending work, removes all listeners and observers', async () => {
  const env = environment(), cat = mountCat(env.host);
  const done = cat.play('look'); cat.destroy(); cat.destroy();
  assert.equal((await done).reason, 'destroyed');
  assert.equal(env.frames.size, 0); assert.equal(env.host.children.length, 0);
  assert.equal(env.doc.listeners.size, 0); assert.equal(env.media.listeners.size, 0);
  assert.equal(env.observers[0].connected, false);
  env.observers[0].visible(true); assert.equal(env.frames.size, 0);
  assert.throws(()=>cat.play('wake'), /destroyed/);
});

test('setIdle cancels; reentrant end callbacks cannot change the completed result', async () => {
  const env = environment(); let cat;
  cat = mountCat(env.host, {onState(state) { if (state.phase==='awakeIdle') cat?.setIdle('asleep'); }});
  const done = cat.play('wake'); env.drain();
  assert.equal((await done).state, 'awake'); assert.equal(cat.getState().idle, 'asleep');
  const cancelled = cat.play('look'); cat.setIdle('asleep');
  assert.equal((await cancelled).reason, 'idle-changed'); cat.destroy();
});

test('reentrant start callback can replace an action without orphan frames', async () => {
  const env = environment(); let cat, replacement;
  cat = mountCat(env.host, {onState(state) { if (state.action==='look') replacement=cat.play('sleep'); }});
  const replaced = cat.play('look'); env.drain();
  assert.equal((await replaced).cancelled, true); assert.equal((await replacement).state, 'asleep');
  assert.equal(env.frames.size, 0); cat.destroy();
});

test('all legal coats retain palette rules, black/white eye ink, unique SVG references', () => {
  const env = environment(); const ids = new Set();
  for (const {id:shape} of catalog.shapes) for (const {id:pattern} of catalog.patterns) for (const {id:palette} of paletteOptions(pattern)) {
    const config = normalizeConfig({shape,pattern,palette});
    assert.equal(config.palette, palette);
    const cat = mountCat(env.host, {...config,initialState:'awake'});
    const svg = cat.exportSVG();
    assert.equal((svg.match(/data-eye=/g)||[]).length, 2);
    for (const path of svg.match(/<path[^>]*data-eye=[^>]*>/g)) assert.match(path, /fill="#(?:FFFFFF|000000)"/);
    const id = svg.match(/id="([^"]+-body)"/)[1]; assert.ok(!ids.has(id)); ids.add(id);
    assert.doesNotMatch(svg, /NaN|undefined|Infinity|<script/); cat.destroy();
  }
  assert.equal(normalizeConfig({pattern:'cowpatch',palette:'ocean'}).palette, 'cow');
  assert.throws(()=>normalizeConfig({shape:'unknown'}), /Unknown/);
  assert.equal(env.frames.size, 0);
});

test('legacy schemaVersion 1 configs and public methods remain usable', async () => {
  const env=environment();
  for(const shape of ['curl','loaf','tall','rice','bean','puddle']) {
    const config=normalizeConfig({schemaVersion:1,shape,pattern:'solid',palette:'iris'});
    assert.equal(config.shape,shape);
    const cat=mountCat(env.host,config);
    assert.equal(cat.getConfig().schemaVersion,1);
    assert.equal(cat.getState().phase,'sleepIdle');
    assert.equal(cat.setSpeed(1.4),1.4);
    cat.setAppearance({palette:'ocean'});
    assert.equal(cat.getConfig().palette,'ocean');
    cat.setIdle('awake');
    const done=cat.play('blink-look');env.drain();
    assert.equal((await done).state,'awake');
    cat.reset();cat.destroy();
  }
  assert.equal(normalizeConfig({schemaVersion:1,shape:'stand'}).shape,'stand');
});

test('v2 supports every offered clip and rejects unavailable stand gesture', async () => {
  for(const {id} of catalog.shapes){
    const env=environment(),cat=mountCat(env.host,{shape:id});
    assert.deepEqual(cat.clips().sort(),Object.keys(performances[id].clips).sort());
    for(const action of catalog.actions){
      if(!cat.clips().includes(action))continue;
      cat.setIdle(['sleep','dream','breathe'].includes(action)?'asleep':'awake');
      const done=cat.play(action);env.drain();
      assert.equal((await done).cancelled,false,`${id}:${action}`);
    }
    if(['curl','stand'].includes(id))await assert.rejects(cat.play('stand'),/Unknown cat action/);
    cat.destroy();
  }
});

test('every cat has a keyboard loop, paw accents, and a hand-retracting work exit', () => {
  const h=poseFields.indexOf('h')+1;
  const left=poseFields.indexOf('pl')+1;
  const right=poseFields.indexOf('pr')+1;
  for(const [shape,{clips}] of Object.entries(performances)){
    assert.ok(clips.work.some(row=>row[h]>0 && (row[left]>0 || row[right]>0)),`${shape}: alternating keyboard paws`);
    assert.ok(clips.knead.some(row=>row[h]>0),`${shape}: knead`);
    assert.ok(clips.bat.some(row=>row[h]>0),`${shape}: bat`);
    assert.equal(clips['work-end'].at(-1)[h],0,`${shape}: paws retract at work end`);
  }
});

test('life, autoplay, visibility and reduced motion own only their RAFs', async () => {
  const env=environment(),cat=mountAvatar(env.host,{shape:'stand',initialState:'awake'});
  assert.ok(env.frames.size>0,'life starts at rest');
  env.step(100);env.step(100);
  const alive=cat.exportSVG();
  cat.setLife(false);assert.equal(env.frames.size,0);
  cat.setLife(true);assert.ok(env.frames.size>0);
  env.observers[0].visible(false);assert.equal(env.frames.size,0);
  env.observers[0].visible(true);assert.ok(env.frames.size>0);
  cat.setAutoplay(true);
  env.step(16);assert.equal(cat.getState().phase,'awakeIdle','director waits before its first gesture');
  env.step(10000);env.step(10000);
  assert.equal(cat.getState().phase,'playing');
  cat.setAutoplay(false);cat.setIdle('asleep');
  env.media.matches=true;env.media.dispatchEvent(new Event('change'));
  assert.equal(env.frames.size,0);
  const sleep=cat.play('sleep');assert.equal((await sleep).skipped,true);
  assert.equal(env.frames.size,0);
  env.media.matches=false;env.media.dispatchEvent(new Event('change'));
  assert.ok(env.frames.size>0);
  assert.notEqual(cat.exportSVG(),alive);
  cat.destroy();assert.equal(env.frames.size,0);
});

test('eye styles, expressions and static SVG include stand tail and limbs', () => {
  const env=environment(),cat=mountCat(env.host,{shape:'stand',initialState:'awake'});
  assert.deepEqual(cat.eyeStyles(),['dot','shine','ring','mismatch','tiny','big']);
  for(const style of cat.eyeStyles()){
    assert.equal(cat.setEyes(style),style);
    assert.doesNotMatch(cat.exportSVG(),/NaN|undefined|Infinity/);
  }
  cat.pose({o:1,hh:1,pl:4,pr:5,ps:12});
  assert.match(cat.exportSVG(),/fill="#E0566B"/);
  cat.pose({o:1,jj:1,u:1,h:1});
  const svg=cat.exportSVG();
  assert.match(svg,/tailover/);
  assert.match(svg,/transform="translate\(0 -20\.00\)"/);
  assert.doesNotMatch(svg,/NaN|undefined|Infinity/);
  cat.destroy();
  const paw=mountCat(env.host,{shape:'loaf',initialState:'awake'});
  paw.pose({o:1,u:1,h:1,pl:4,pr:5});
  assert.match(paw.exportSVG(),/scale\(1 1\.000\)/);
  assert.match(paw.exportSVG(),/<rect[^>]*height="[^"]+"/);
  paw.destroy();
});
