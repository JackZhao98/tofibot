import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {mkdtempSync,writeFileSync,rmSync} from 'node:fs';
import {join,resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
// Compiled inside ui/node_modules so the emitted files resolve react and friends.
const root=resolve(import.meta.dirname,'..'), out=mkdtempSync(join(root,'node_modules','.tofi-flight-'));
try {
 execFileSync(join(root,'node_modules/.bin/tsc'),['src/sendFlight.ts','--ignoreConfig','--target','ES2022','--module','ES2022','--moduleResolution','Bundler','--outDir',out,'--skipLibCheck'],{cwd:root});
 // Node's ESM resolver needs an extension on every compiled relative import (./motion is both motion.js and a folder).
 const {readFileSync,readdirSync}=await import('node:fs');
 for (const name of readdirSync(out,{recursive:true})) if (String(name).endsWith('.js')) { const path=join(out,String(name)); writeFileSync(path,readFileSync(path,'utf8').replace(/(from\s*['"])(\.{1,2}\/[^'"]+?)(['"])/g,(m,a,spec,c)=>/\.[cm]?js$/.test(spec)?m:a+spec+'.js'+c)); }
 const file=join(out,'sendFlight.js');
 const {flySentMessage}=await import(pathToFileURL(file));
 let reduced=false,id=0;const frames=new Map(),ghosts=new Set();
 globalThis.window={matchMedia:()=>({matches:reduced})};
 globalThis.requestAnimationFrame=cb=>{frames.set(++id,cb);return id};
 globalThis.cancelAnimationFrame=id=>frames.delete(id);
 globalThis.getComputedStyle=()=>({padding:'8px',font:'14px sans-serif',lineHeight:'20px',color:'white',backgroundColor:'black',border:'none',borderColor:'black',borderRadius:'8px'});
 // sendFlight portals into the #tofi-layers root (layers.ts); the stub hands back one that collects the ghosts.
 const layer={append:g=>ghosts.add(g)};
 globalThis.document={getElementById:()=>layer,body:{append:g=>ghosts.add(g)}};
 function target(){return {isConnected:true,style:{visibility:''},rect:{left:20,top:80,width:300,height:60},getBoundingClientRect(){return this.rect},cloneNode(){const g={style:{},setAttribute(){},remove(){ghosts.delete(g)},animate(keys){g.keys=keys;let done,fail;g.animation={finished:new Promise((a,b)=>{done=a;fail=b}),cancel(){fail(new Error('cancelled'))},finish(){done()}};return g.animation}};this.ghost=g;return g}}}
 const tick=()=>{const pending=[...frames.values()];frames.clear();pending.forEach(cb=>cb())};
 const origin={left:30,top:400};
 const a=target();flySentMessage(origin,a);assert.equal(ghosts.size,1);assert.equal(a.style.visibility,'hidden');assert.match(a.ghost.keys.at(-1).transform,/scale\(1\.000\)/);
 a.rect={...a.rect,left:40,top:120,width:180,height:100};tick();assert.equal(a.ghost.style.left,'40px');assert.equal(a.ghost.style.top,'120px','scroll/composer movement updates landing');assert.equal(a.ghost.style.width,'180px');assert.equal(a.ghost.style.minHeight,'100px','reflow updates clone wrapping and height');
 const b=target();flySentMessage(origin,b);assert.equal(ghosts.size,2,'consecutive sends have independent overlays');
 a.ghost.animation.finish();await Promise.resolve();assert.equal(a.style.visibility,'');assert.equal(ghosts.size,1);
 b.isConnected=false;tick();await Promise.resolve();assert.equal(ghosts.size,0);assert.equal(frames.size,0,'unmount cancels tracking');
 reduced=true;const c=target();flySentMessage(origin,c);assert.equal(ghosts.size,0);assert.equal(c.style.visibility,'','reduced motion never hides target');
 reduced=false;const d=target();d.style.visibility='visible';flySentMessage(origin,d);reduced=true;tick();await Promise.resolve();assert.equal(d.style.visibility,'visible');assert.equal(ghosts.size,0,'motion change cancels safely');
 console.log('send flight checks: PASS (identity landing, moving destination, consecutive overlays, unmount, reduced motion, visibility restoration)');
} finally {rmSync(out,{recursive:true,force:true})}
