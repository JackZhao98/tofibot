import curl from './shapes/curl.js';
import loaf from './shapes/loaf.js';
import tall from './shapes/tall.js';
import rice from './shapes/rice.js';
import bean from './shapes/bean.js';
import puddle from './shapes/puddle.js';
import stand from './shapes/stand.js';
import {createCoat,patterns,paletteOptions as choices} from './appearance.js';
import {clamp,ease} from './math.js';
import {performances,poseFields,readableContour,deformPoint} from './performances.js';

export const version='0.1.0';
const shapes={curl,loaf,tall,rice,bean,puddle,stand};
const actions=['look','curious','double-take','slow-blink','happy','stand','blink-look','work','work-end','knead','bat','wave','sleep','dream','breathe','wake'];
const defaults={schemaVersion:1,shape:'curl',pattern:'solid',palette:'iris'};
const fields=poseFields;
const rest=Object.fromEntries(fields.map(key=>[key,0]));
const svgNS='http://www.w3.org/2000/svg';
let instance=0;

export const catalog=Object.freeze({
  shapes:Object.freeze(Object.values(shapes).map(s=>Object.freeze({id:s.key,name:s.name}))),
  patterns:Object.freeze(patterns.map(p=>Object.freeze({id:p.id,name:p.name,mode:p.mode,note:p.note}))),
  actions:Object.freeze([...actions])
});
export function paletteOptions(pattern){
  if(!patterns.some(p=>p.id===pattern))throw new Error(`Unknown cat pattern: ${pattern}`);
  return choices(pattern).map(p=>({...p}));
}
export function normalizeConfig(value={}){
  const config={...defaults,...value};
  if(config.schemaVersion!==1)throw new Error('Unsupported cat avatar schemaVersion');
  if(!Object.hasOwn(shapes,config.shape))throw new Error(`Unknown cat shape: ${config.shape}`);
  const options=paletteOptions(config.pattern);
  const palette=options.find(p=>p.id===config.palette)||options[0];
  return {schemaVersion:1,shape:config.shape,pattern:config.pattern,palette:palette.id};
}
function element(name,attributes={}){
  const node=document.createElementNS(svgNS,name);
  for(const [key,value] of Object.entries(attributes))node.setAttribute(key,String(value));
  return node;
}
function tokens(d){
  return d.match(/[MLCZ]|-?(?:\d*\.)?\d+(?:e[-+]?\d+)?/gi)
    .map(v=>/^[MLCZ]$/i.test(v)?v:Number(v));
}
// Lowest body edge at a given x (flattened cubic path), to seat paws and legs.
function edge(d){
  const t=d.match(/[MLCZ]|-?\d*\.?\d+/gi);let i=0,cur=[0,0];const pts=[];
  while(i<t.length){const c=t[i++];
    if(c==='M'||c==='L'){cur=[+t[i++],+t[i++]];pts.push(cur);}
    else if(c==='C'){const a=[+t[i++],+t[i++]],b=[+t[i++],+t[i++]],e=[+t[i++],+t[i++]];
      for(let u=0;u<=1;u+=.04){const m=1-u;pts.push([m*m*m*cur[0]+3*m*m*u*a[0]+3*m*u*u*b[0]+u*u*u*e[0],m*m*m*cur[1]+3*m*m*u*a[1]+3*m*u*u*b[1]+u*u*u*e[1]]);}cur=e;}}
  return x=>{let y=-1;for(const p of pts)if(Math.abs(p[0]-x)<4&&p[1]>y)y=p[1];return y<0?260:y;};
}
function pose(row){
  return Object.fromEntries(fields.map((key,i)=>[key,row[i+1]??0]));
}
// Per-keyframe curves (optional 12th column). Default stays the quintic ease.
const curves={
  io:ease,
  out:t=>1-(1-t)**3,
  in:t=>t*t*t,
  snap:t=>1-(1-t)**5,
  back:t=>{const c=1.9;return 1+(c+1)*(t-1)**3+c*(t-1)**2;},
  spring:t=>1-Math.exp(-5*t)*Math.cos(t*Math.PI*2.5),
  hold:t=>t<1?0:1,
  lin:t=>t
};
// Default: monotone cubic through neighbouring keys, so motion flows through
// a keyframe instead of stopping dead at every one.
function spline(score,i,t){
  const a=score[Math.max(0,i-2)],p=score[i-1],q=score[i],b=score[Math.min(score.length-1,i+1)];
  const dt=q[0]-p[0],t2=t*t,t3=t2*t;
  const h00=2*t3-3*t2+1,h10=t3-2*t2+t,h01=-2*t3+3*t2,h11=t3-t2;
  const out={};
  fields.forEach((k,j)=>{
    const P0=a[j+1]??0,P1=p[j+1]??0,P2=q[j+1]??0,P3=b[j+1]??0;
    let m1=(P2-P0)/Math.max(1e-6,q[0]-a[0])*dt,m2=(P3-P1)/Math.max(1e-6,b[0]-p[0])*dt;
    if((P1-P0)*(P2-P1)<=0||a===p)m1=0;
    if((P2-P1)*(P3-P2)<=0||b===q)m2=0;
    const lim=3*Math.abs(P2-P1);m1=Math.max(-lim,Math.min(lim,m1));m2=Math.max(-lim,Math.min(lim,m2));
    out[k]=h00*P1+h10*m1+h01*P2+h11*m2;
  });
  return out;
}
const mix=(a,b,t)=>Object.fromEntries(fields.map(k=>[k,a[k]+(b[k]-a[k])*t]));
function sample(score,time){
  if(time<=score[0][0])return pose(score[0]);
  for(let i=1;i<score.length;i++){
    if(time<=score[i][0]){
      const t=(time-score[i-1][0])/(score[i][0]-score[i-1][0]);
      const c=score[i][fields.length+1];
      if(c)return mix(pose(score[i-1]),pose(score[i]),(curves[c]||ease)(t));
      return spline(score,i,t);
    }
  }
  return pose(score.at(-1));
}

/** Mount into a dedicated HTML element. No fetch, inline script, or eval. */
export function mountCat(host,options={}){
  if(!host||typeof host.append!=='function')throw new Error('mountCat requires a DOM element');
  let config=normalizeConfig(options),shape=shapes[config.shape];
  let generalPalette=choices('solid').some(p=>p.id===config.palette)?config.palette:'iris';
  let pointPalette=choices('tipped').some(p=>p.id===config.palette)?config.palette:'coffee';
  let idle=options.initialState??'asleep';
  if(!['asleep','awake'].includes(idle))throw new Error('initialState must be asleep or awake');
  const preference=options.reducedMotion??'system';
  if(![true,false,'system'].includes(preference))throw new Error('Invalid reducedMotion option');
  const media=window.matchMedia('(prefers-reduced-motion: reduce)');
  const reduced=()=>preference==='system'?media.matches:preference;
  let speed=clamp(Number(options.speed)||1,.25,2);
  let current={...rest,o:idle==='awake'?1:0,k:idle==='awake'?0:1};
  let tailRig=null,svg,body,marksGroup,eyesGroup,outline,fillNode,parts=[],eyes=[],bodyG,legsG,pawsG,legs=[],paws=[],floor=null;
  let job=null,raf=0,destroyed=false;
  // v2 life layer: tiny continuous motion on top of the resting pose while no
  // clip plays (breath, auto-blink, eye drift, ear twitch). Off with life:false.
  let lifeOn=options.life!==false,lifeRaf=0,lifeT0=null,lifeOff={...rest},nextBlink=0,blinkAt=-9,blinkN=1,nextTwitch=0,twitchAt=-9,twitchDir=1,drift=0,driftTo=0,nextDrift=0;
  let auto=!!options.autoplay,nextAuto=0;
  const rnd=(a,b)=>a+Math.random()*(b-a);
  let inView=true;
  const canAnimate=()=>!document.hidden&&inView;
  const uid='tofi-avatar-'+(++instance)+'-'+Math.random().toString(36).slice(2,9);
  function alive(){if(destroyed)throw new Error('This cat avatar has been destroyed');}
  function getState(){
    return {phase:job?'playing':idle==='awake'?'awakeIdle':'sleepIdle',idle,
      action:job?.action??null,reducedMotion:reduced(),speed,config:{...config}};
  }
  function emit(type,detail={}){
    const state=getState();
    svg?.dispatchEvent(new CustomEvent('tofi:'+type,{bubbles:true,detail:{...detail,state}}));
    options.onState?.(state);
  }
  function materialPath(data,q){
    let d='';
    for(let i=0;i<data.length;){
      if(typeof data[i]==='string'){d+=data[i++]+' ';continue;}
      const p=deformPoint(shape,performances[shape.key],data[i++],data[i++],q);
      d+=p[0].toFixed(3)+' '+p[1].toFixed(3)+' ';
    }
    return d.trim();
  }
  function paint(){
    const coat=createCoat(shape,config.pattern,config.palette);
    fillNode.setAttribute('fill',coat.fill);
    svg.setAttribute('aria-label',coat.label);
    svg.querySelector('title').textContent=coat.label;
    outline.style.display=coat.outline?'':'none';
    marksGroup.replaceChildren();
    parts=[{node:body,d:shape.body,data:tokens(shape.body)}];
    for(const mark of coat.marks){
      const node=element('path',{d:mark.d,fill:mark.fill});
      marksGroup.append(node);
      parts.push({node,d:mark.d,data:tokens(mark.d)});
    }
    const limbFill=typeof coat.fill==='string'&&!coat.fill.startsWith('url')?coat.fill:(coat.body||'#FFFCF6');
    legs.forEach(l=>l.n.setAttribute('fill',limbFill));
    if(tailRig){const tipFill=(coat.marks.find(m=>m.fill!==coat.fill)||{}).fill||coat.fill;
      for(const c of [tailRig.back,tailRig.front,tailRig.over]){c.f.setAttribute('stroke',coat.fill);c.tip.setAttribute('stroke',tipFill);c.o.style.display=coat.outline&&c!==tailRig.over?'':'none';c.tip.style.display=tipFill===coat.fill?'none':'';}}paws.forEach(p=>p.pad.setAttribute('fill',limbFill));
    eyes.forEach((eye,i)=>{
      const ink=coat.eyeColors[i];
      eye.node.setAttribute('fill',ink);eye.ink=ink;
      // A thin opposing keyline keeps an enlarged/moving eye readable when it
      // straddles a coat boundary; the original black/white ink rule is intact.
      eye.node.setAttribute('stroke',ink==='#FFFFFF'?'#000000':'#FFFFFF');
    });
  }
  function scene(){
    svg?.remove();
    svg=element('svg',{xmlns:svgNS,viewBox:'0 0 320 320',width:'100%',height:'100%',role:'img','data-tofi-avatar':config.shape});
    svg.style.display='block';
    const title=element('title'),defs=element('defs');
    body=element('path',{id:uid+'-body',d:shape.body});
    const clip=element('clipPath',{id:uid+'-clip'});
    clip.append(element('use',{href:'#'+uid+'-body'}));
    defs.append(body,clip);
    fillNode=element('use',{href:'#'+uid+'-body'});
    marksGroup=element('g',{'clip-path':'url(#'+uid+'-clip)'});
    eyesGroup=element('g',{'clip-path':'url(#'+uid+'-clip)'});
    eyes=shape.eyes.map(eye=>{
      const id=uid+'-'+eye.part;
      const node=element('path',{id,d:eye.d,'data-eye':eye.part,'stroke-width':1.8,'stroke-linejoin':'round','paint-order':'stroke fill'});
      const cp=element('clipPath',{id:id+'-c'});cp.append(element('use',{href:'#'+id}));defs.append(cp);
      // v2 eye decorations: iris pupil + catchlights (clipped to the eye),
      // and expression glyphs (hearts, ^ ^) drawn over it.
      const inner=element('g',{'clip-path':'url(#'+id+'-c)'});
      const pupil=element('ellipse',{fill:'#1d1f22'});
      const hi1=element('circle',{fill:'#FFFFFF'}),hi2=element('circle',{fill:'#FFFFFF'});
      inner.append(pupil,hi1,hi2);
      const glyph=element('path',{fill:'none','stroke-linecap':'round','stroke-linejoin':'round'});
      eyesGroup.append(node,inner,glyph);
      return {...eye,node,inner,pupil,hi1,hi2,glyph};
    });
    outline=element('use',{href:'#'+uid+'-body',fill:'none',stroke:'#292B29','stroke-width':4.5,'stroke-linejoin':'round'});
    // v2 limbs: legs sit behind the body, paws in front. Hidden at rest so the
    // original silhouette is untouched.
    legsG=element('g');pawsG=element('g');bodyG=element('g');
    const lim=performances[shape.key].limbs||{paws:[],legs:[]};
    floor=edge(shape.body);
    legs=lim.legs.map(x=>{const n=element('rect',{width:20,rx:10,'stroke-width':4,stroke:'#292B29'});legsG.append(n);return {x,y:floor(x),n};});
    paws=lim.paws.map((x,i)=>{
      const g=element('g');
      const pad=element('rect',{x:-13,y:-10,width:26,height:19,rx:9.5,'stroke-width':3.6,stroke:'#292B29'});
      const toes=element('path',{d:'M -4 3 L -4 8 M 4 3 L 4 8',stroke:'#292B29','stroke-width':2.2,'stroke-linecap':'round',opacity:.55});
      g.append(pad,toes);pawsG.append(g);return {x,y:floor(x)-5,g,pad,i};
    });
    bodyG.append(fillNode,marksGroup,eyesGroup,outline,pawsG);
    // v2 tail rig (踏踏): a rope drawn as strokes, one copy behind the body and
    // one copy clipped to the floor strip in front of it.
    tailRig=null;
    const TR=lim.tail;
    if(TR){
      const mk=()=>{const g=element('g');const o=element('path',{fill:'none',stroke:'#292B29','stroke-linecap':'round','stroke-linejoin':'round','stroke-width':TR.w*2+9});
        const f=element('path',{fill:'none','stroke-linecap':'round','stroke-linejoin':'round','stroke-width':TR.w*2});
        const tip=element('path',{fill:'none','stroke-linecap':'round','stroke-linejoin':'round','stroke-width':TR.w*2});g.append(o,f,tip);return {g,o,f,tip};};
      const back=mk(),front=mk(),over=mk();
      // Fill-only copy drawn over the body: hides the body's outline where the
      // tail joins it, so tail and body read as one contour.
      over.o.style.display='none';
      const m=element('mask',{id:uid+'-tailover',maskUnits:'userSpaceOnUse',x:-40,y:-40,width:400,height:400});
      m.append(element('rect',{x:-40,y:-40,width:400,height:400,fill:'#fff'}),element('use',{href:'#'+uid+'-body',fill:'#000',stroke:'#fff','stroke-width':7}));
      defs.append(m);over.g.setAttribute('mask','url(#'+uid+'-tailover)');
      const fc=element('clipPath',{id:uid+'-tailfront'});const [x0,y0,x1,y1]=TR.front;fc.append(element('rect',{x:x0,y:y0,width:x1-x0,height:y1-y0}));defs.append(fc);
      // front copy: only inside the body AND on the floor strip (tail lying over the paws)
      const inner=front.g;const outer=element('g',{'clip-path':'url(#'+uid+'-tailfront)'});
      inner.setAttribute('clip-path','url(#'+uid+'-clip)');outer.append(inner);front.g=outer;
      tailRig={TR,back,front,over};
    }
    svg.append(title,defs,legsG,...(tailRig?[tailRig.back.g]:[]),bodyG,...(tailRig?[tailRig.over.g,tailRig.front.g]:[]));
    host.append(svg);
    paint();
  }
  function lifeOffset(t){
    const L=performances[shape.key].life||{};
    const o={...rest};
    const asleep=idle==='asleep';
    const [ba,bp]=asleep?(L.sleepBreath||[2.4,3.6]):(L.breath||[.7,3]);
    o.b=ba*(.5-.5*Math.cos(t*2*Math.PI/bp));
    o.y=-o.b*(asleep?.15:.25);
    if(asleep){
      if(t>nextTwitch){twitchAt=t;twitchDir=Math.random()<.5?-1:1;nextTwitch=t+rnd(...(L.twitchEvery||[5,11]));}
      const k=t-twitchAt;if(k<.6)o.e=Math.max(0,Math.sin(k/.6*Math.PI))*(L.twitch||2)*(twitchDir>0?1:.6);
      o.t=(L.sway||0)*.6*Math.sin(t*2*Math.PI/(bp*2.3));
      return o;
    }
    if(t>nextBlink){blinkAt=t;blinkN=Math.random()<(L.doubleBlink||.2)?2:1;nextBlink=t+rnd(...(L.blinkEvery||[2.6,6]));}
    const k=t-blinkAt,bd=L.blinkDur||.2;
    if(k<bd*blinkN){const p=(k%bd)/bd;o.o=-Math.sin(p*Math.PI);o.lid=Math.sin(p*Math.PI);}
    if(t>nextDrift){driftTo=rnd(-1,1)*(L.drift||.35);nextDrift=t+rnd(1.2,3.5);}
    drift+=(driftTo-drift)*.08;o.g=drift;o.pd=.3;
    o.t=(L.sway||0)*(Math.sin(t*2*Math.PI/5.3)+.4*Math.sin(t*2*Math.PI/2.1+1));
    return o;
  }
  function lifeTick(now){
    lifeRaf=0;
    if(destroyed||job||!lifeOn||reduced())return;
    if(lifeT0===null)lifeT0=now;
    const t=(now-lifeT0)/1000*speed;
    if(auto&&t>nextAuto){
      const pool=(performances[shape.key].director||{})[idle];
      nextAuto=t+rnd(...((performances[shape.key].director||{}).every||[4,9]));
      if(pool&&pool.length){
        let sum=pool.reduce((a,[,w])=>a+w,0),r=Math.random()*sum,pick=pool[0][0];
        for(const [name,w] of pool){if((r-=w)<=0){pick=name;break;}}
        return void play(pick);
      }
    }
    const base={...rest,o:idle==='awake'?1:0,k:idle==='awake'?0:1};
    const off=lifeOffset(t);
    const ramp=Math.min(1,t/.9),rr=ramp*ramp*(3-2*ramp);
    soft(Object.fromEntries(fields.map(k=>[k,base[k]+off[k]*(k==='o'||k==='lid'?1:rr)])));
    if(canAnimate())lifeRaf=requestAnimationFrame(lifeTick);
  }
  function startLife(){if(!lifeRaf&&lifeOn&&!job&&!destroyed&&canAnimate()&&!reduced()){lifeT0=null;drift=0;driftTo=0;nextBlink=rnd(.8,2.5);nextTwitch=rnd(2,5);nextAuto=rnd(...((performances[shape.key].director||{}).every||[4,9]))*.6;lifeRaf=requestAnimationFrame(lifeTick);}}
  function stopLife(){cancelAnimationFrame(lifeRaf);lifeRaf=0;}
  function limbs(q){
    const L=(performances[shape.key].limbs||{}).L||20,u=clamp(q.u||0),h=clamp(q.h||0),lift=u*L;
    bodyG.setAttribute('transform',lift?'translate(0 '+(-lift).toFixed(2)+')':'');
    legsG.style.display=u>.02?'':'none';
    for(const l of legs){l.n.setAttribute('x',l.x-10);l.n.setAttribute('y',(l.y-16-lift).toFixed(2));l.n.setAttribute('height',(16+lift+2).toFixed(2));}
    pawsG.style.display=h>.02?'':'none';
    const face=(performances[shape.key].clips&&personalityFace())||-1;
    for(const p of paws){
      const lead=(face<0?p.i===0:p.i===1);
      const up=(p.i===0?q.pl:q.pr)||0, sw=lead?(q.ps||0):0;
      const dx=(q.x||0)*.35+sw*face*1.3, dy=-up-sw*2.4+(1-h)*10;
      const rot=sw*face*-2.2;
      p.g.setAttribute('transform','translate('+(p.x+dx).toFixed(2)+' '+(p.y+dy).toFixed(2)+') rotate('+rot.toFixed(1)+') scale(1 '+h.toFixed(3)+')');
    }
  }
  function tail(q){
    if(!tailRig)return;
    const {TR}=tailRig,k=clamp(q.k||0),n=TR.up.length;
    const root=deformPoint(shape,performances[shape.key],TR.root[0],TR.root[1],q);
    const now=performance.now()/1000,awake=1-k;
    const pts=[root];let x=root[0],y=root[1];
    for(let i=0;i<n;i++){
      // follow-through: segments near the root move first, the tip lags behind
      const u=clamp((k-(n-1-i)*.05)/.6),e=u*u*(3-2*u);
      // alive tail: a wave travels from root to tip, the tip curls most
      const f=i/(n-1),wave=Math.sin(now*2.1-i*.55)*(3+f*16)+Math.sin(now*.9-i*.3)*f*8+(q.t||0)*(1+f*2);
      const h=(TR.up[i]+(TR.sleep[i]-TR.up[i])*e+wave*awake*(1-e))*Math.PI/180;
      x+=TR.seg*Math.cos(h);y+=TR.seg*Math.sin(h);pts.push([x,y]);
    }
    let d='M '+pts[0][0].toFixed(2)+' '+pts[0][1].toFixed(2);
    for(let i=1;i<pts.length-1;i++){const m=[(pts[i][0]+pts[i+1][0])/2,(pts[i][1]+pts[i+1][1])/2];d+=' Q '+pts[i][0].toFixed(2)+' '+pts[i][1].toFixed(2)+' '+m[0].toFixed(2)+' '+m[1].toFixed(2);}
    d+=' L '+pts.at(-1)[0].toFixed(2)+' '+pts.at(-1)[1].toFixed(2);
    const a=pts[n-2],b=pts[n];let td='M '+((a[0]+pts[n-1][0])/2).toFixed(2)+' '+((a[1]+pts[n-1][1])/2).toFixed(2)+' Q '+pts[n-1][0].toFixed(2)+' '+pts[n-1][1].toFixed(2)+' '+b[0].toFixed(2)+' '+b[1].toFixed(2);
    for(const c of [tailRig.back,tailRig.front,tailRig.over]){c.o.setAttribute('d',d);c.f.setAttribute('d',d);c.tip.setAttribute('d',td);}
  }
  function personalityFace(){return performances[shape.key].face;}
  // Eye styles. dot = original ink bean. The others keep the same eye contour
  // (so blinks/sleep arcs still work) and paint inside it.
  // Comic eye styles, black & white only. size: per-eye scale [left,right].
  const eyeStyles={
    dot:{},
    shine:{shine:true},
    ring:{iris:'#FFFCF6',shine:true},
    mismatch:{iris:[null,'#FFFCF6'],size:[.9,1.3],shine:true},
    tiny:{size:[.62,.62]},
    big:{size:[1.35,1.35],shine:true},
  };
  let eyeStyle=eyeStyles[options.eyes]?options.eyes:'dot';
  function heart(cx,cy,r){return `M ${cx} ${cy+r*.9} C ${cx-r*1.6} ${cy-r*.1} ${cx-r*.9} ${cy-r*1.3} ${cx} ${cy-r*.45} C ${cx+r*.9} ${cy-r*1.3} ${cx+r*1.6} ${cy-r*.1} ${cx} ${cy+r*.9} Z`;}
  function decorate(eye,index,q,tx,ty){
    const st=eyeStyles[eyeStyle],o=clamp(q.o),hh=clamp(q.hh||0),jj=clamp(q.jj||0);
    let bb;try{bb=eye.node.getBBox();}catch{bb=null;}
    if(!bb||!bb.width){eye.inner.style.display='none';eye.glyph.style.display='none';return;}
    const cx=bb.x+bb.width/2+tx,cy=bb.y+bb.height/2+ty,w=bb.width,h=Math.max(bb.height,1);
    const R=Math.max(w,h)/2,ang=q.r;
    const ink=eye.ink||'#000000';
    const expr=Math.max(hh,jj);
    let iris=Array.isArray(st.iris)?st.iris[index]:st.iris;
    // white eyes on a dark face: give them a black dot so they read as eyes
    if(!iris&&st.shine&&ink==='#FFFFFF')iris='#FFFFFF';
    const irisOn=!!iris&&o>.45&&expr<.5;
        eye.node.setAttribute('fill',irisOn?iris:ink);
    eye.node.setAttribute('stroke',irisOn?'#292B29':(ink==='#FFFFFF'?'#000000':'#FFFFFF'));
    eye.node.setAttribute('stroke-width',irisOn?2.4:1.8);
    eye.node.style.opacity=String(1-expr);
    eye.inner.style.opacity=String((1-expr)*clamp((o-.35)/.35));
    eye.inner.style.display=(st.shine||iris)&&o>.35&&expr<.99?'':'none';
    if(irisOn){
      const pd=clamp(q.pd||0);
      const rx=R*(.36+.22*pd),ry=Math.min(h*.5,R*(.36+.22*pd));
      eye.pupil.style.display='';
      eye.pupil.setAttribute('cx',cx.toFixed(2));eye.pupil.setAttribute('cy',cy.toFixed(2));
      eye.pupil.setAttribute('rx',rx.toFixed(2));eye.pupil.setAttribute('ry',ry.toFixed(2));
      eye.pupil.setAttribute('transform',`rotate(${ang.toFixed(1)} ${cx.toFixed(2)} ${cy.toFixed(2)})`);
    }else eye.pupil.style.display='none';
    const hiOn=st.shine&&ink!=='#FFFFFF'||irisOn;
    eye.hi1.style.display=eye.hi2.style.display=hiOn?'':'none';
    eye.hi1.setAttribute('cx',(cx+R*.36).toFixed(2));eye.hi1.setAttribute('cy',(cy-h*.26).toFixed(2));eye.hi1.setAttribute('r',(R*.2).toFixed(2));
    eye.hi2.setAttribute('cx',(cx-R*.34).toFixed(2));eye.hi2.setAttribute('cy',(cy+h*.24).toFixed(2));eye.hi2.setAttribute('r',(R*.09).toFixed(2));
    if(expr>.01){
      const k=expr,gc=ink==='#FFFFFF'?'#FFFFFF':'#292B29';
      eye.glyph.style.display='';
      if(hh>=jj){
        const r=9*(.4+.6*k)*(1+.15*Math.sin(k*Math.PI));
        eye.glyph.setAttribute('d',heart(cx,cy,r));eye.glyph.setAttribute('fill','#E0566B');
        eye.glyph.setAttribute('stroke','#292B29');eye.glyph.setAttribute('stroke-width',2);
      }else{
        const r=7.5*(.5+.5*k);
        eye.glyph.setAttribute('d',`M ${cx-r} ${cy+r*.35} Q ${cx} ${cy-r*1.1} ${cx+r} ${cy+r*.35}`);
        eye.glyph.setAttribute('fill','none');eye.glyph.setAttribute('stroke',gc);eye.glyph.setAttribute('stroke-width',3.6);
      }
      eye.glyph.style.opacity=String(k);
    }else eye.glyph.style.display='none';
  }
  // Follower: every animated frame eases toward its target, so switching
  // clips (or clip → idle life) never snaps, whatever the new clip's first key.
  let shown=null,shownAt=0;
  const fast={o:.035,lid:.035,hh:.05,jj:.05};
  function soft(q){
    const now=performance.now();
    if(!shown){render(q);shownAt=now;return;}
    const dt=Math.min(.1,Math.max(0,(now-shownAt)/1000));shownAt=now;
    const next={};
    for(const k of fields){const a=1-Math.exp(-dt/(fast[k]||.085));next[k]=shown[k]+((q[k]??0)-shown[k])*a;}
    render(next);
  }
  function render(q){
    current={...q};shown={...q};
    if(bodyG){limbs(q);tail(q);}
    const home=q.x===0&&q.y===0&&q.r===0&&q.s===0&&q.b===0&&q.e===0&&!q.t&&!q.u&&!q.k&&!q.pl&&!q.pr&&!q.ps;
    for(const part of parts)part.node.setAttribute('d',home?part.d:materialPath(part.data,q));
    for(const [index,eye] of eyes.entries()){
      const direction=clamp(q.g,-1,1);
      const travel=direction*(direction<0?shape.gaze.left:shape.gaze.right)*.65;
      const angle=(eye.angle+q.r)*Math.PI/180;
      if(travel===0)eye.node.removeAttribute('transform');
      else eye.node.setAttribute('transform','translate('+(travel*Math.cos(angle)).toFixed(3)+' '+(travel*Math.sin(angle)).toFixed(3)+')');
      const contour=readableContour(eye,shape.eyeCenters[index],performances[shape.key].eyeScale,
        clamp(q.o),clamp(q.lid),q.a*(index===0?1:-1));
      const sz=(eyeStyles[eyeStyle].size||[1,1])[index];
      if(sz!==1){const [ecx,ecy]=shape.eyeCenters[index];for(let i=0;i<contour.length;){if(typeof contour[i]==='string'){i++;continue;}contour[i]=ecx+(contour[i]-ecx)*sz;contour[i+1]=ecy+(contour[i+1]-ecy)*sz;i+=2;}}
      eye.node.setAttribute('d',materialPath(contour,q));
      decorate(eye,index,q,travel*Math.cos(angle),travel*Math.sin(angle));
    }
  }
  function cancel(reason='interrupted'){
    cancelAnimationFrame(raf);raf=0;
    if(job){const previous=job;job=null;previous.resolve({action:previous.action,cancelled:true,reason});}
  }
  function finish(active,skipped=false){
    if(job!==active)return;
    cancelAnimationFrame(raf);raf=0;
    // Completion and reduced motion must land on the exact terminal pose;
    // otherwise exponential follow can misclassify sleep as still awake.
    render(pose(active.score.at(-1)));
    idle=current.o===0?'asleep':'awake';job=null;startLife();
    const result={action:active.action,cancelled:false,state:idle,skipped};
    try{emit('end',{action:active.action,skipped});}
    finally{active.resolve(result);}
  }
  function play(action){
    alive();
    if(!performances[shape.key].clips[action])return Promise.reject(new Error(`Unknown cat action: ${action}`));
    stopLife();
    cancel();
    const score=performances[shape.key].clips[action].map(row=>[...row]);
    // Begin exactly where the interrupted clip left off. Never rewind to its
    // canonical start pose (or reopen a half-closed eye before sleeping).
    score[0]=[0,...fields.map(key=>current[key])];
    for(const row of score.slice(1)){
      if(action==='sleep')row[7]=Math.min(row[7],current.o);
      if(action==='wake')row[7]=Math.max(row[7],current.o);
    }
    let resolve;
    const promise=new Promise(done=>{resolve=done;});
    const active={action,score,resolve,elapsed:0,last:null,duration:score.at(-1)[0]};
    job=active;
    active.tick=now=>{
      if(job!==active||destroyed)return;
      if(active.last!==null)active.elapsed+=Math.min((now-active.last)/1000,.075)*speed;
      active.last=now;
      const time=Math.min(active.elapsed,active.duration);
      soft(sample(score,time));
      if(time>=active.duration)finish(active);
      else raf=requestAnimationFrame(active.tick);
    };
    emit('start',{action});
    if(job!==active||destroyed)return promise;
    if(reduced())finish(active,true);
    else if(canAnimate())raf=requestAnimationFrame(active.tick);
    return promise;
  }
  function visibility(){
    cancelAnimationFrame(raf);raf=0;
    if(job){job.last=null;if(canAnimate())raf=requestAnimationFrame(job.tick);}
    else{stopLife();startLife();}
  }
  function motionPreference(){
    if(reduced()){
      stopLife();
      if(job)finish(job,true);
      else render({...rest,o:idle==='awake'?1:0,k:idle==='awake'?0:1});
    }else startLife();
  }
  const observer=typeof IntersectionObserver==='function'?new IntersectionObserver(entries=>{
    if(destroyed)return;
    inView=entries.at(-1)?.isIntersecting??true;
    visibility();
  }):null;
  const api={
    play,
    setLife(on){alive();lifeOn=!!on;if(on)startLife();else{stopLife();if(!job)render({...rest,o:idle==='awake'?1:0,k:idle==='awake'?0:1});}},
    setEyes(style){alive();if(eyeStyles[style]){eyeStyle=style;render(current);}return eyeStyle;},
    eyeStyles(){return Object.keys(eyeStyles);},
    setAutoplay(on){alive();const enabled=!!on;if(auto===enabled)return;auto=enabled;if(auto&&!job){stopLife();startLife();}},
    pose(p){alive();cancel('pose');stopLife();lifeOn=false;render({...rest,...p});},
    clips(){return Object.keys(performances[shape.key].clips);},
    getConfig(){return {...config};},
    getState,
    setAppearance(patch){
      alive();
      const requested={...config,...patch};
      if(patch.pattern&&patch.pattern!==config.pattern&&patch.palette===undefined){
        const kind=patterns.find(p=>p.id===patch.pattern);
        if(kind?.mode==='general')requested.palette=generalPalette;
        else if(patch.pattern==='tipped')requested.palette=pointPalette;
      }
      const next=normalizeConfig(requested);
      const changedShape=next.shape!==config.shape;
      if(changedShape)cancel('shape-changed');
      config=next;
      if(patterns.find(p=>p.id===config.pattern).mode==='general')generalPalette=config.palette;
      if(config.pattern==='tipped')pointPalette=config.palette;
      if(changedShape){shape=shapes[config.shape];current={...rest,o:idle==='awake'?1:0,k:idle==='awake'?0:1};scene();}
      else paint();
      render(current);emit('appearance');return {...config};
    },
    setIdle(value){
      alive();if(!['awake','asleep'].includes(value))throw new Error('Idle must be awake or asleep');
      cancel('idle-changed');stopLife();idle=value;render({...rest,o:idle==='awake'?1:0,k:idle==='awake'?0:1});emit('idle');startLife();
    },
    reset(){this.setIdle('asleep');},
    setSpeed(value){alive();speed=clamp(Number(value)||1,.25,2);return speed;},
    exportSVG(){alive();const copy=svg.cloneNode(true);copy.setAttribute('width','320');copy.setAttribute('height','320');return new XMLSerializer().serializeToString(copy);},
    destroy(){
      if(destroyed)return;
      cancel('destroyed');stopLife();destroyed=true;
      document.removeEventListener('visibilitychange',visibility);
      media.removeEventListener('change',motionPreference);
      observer?.disconnect();
      svg.remove();
    }
  };
  scene();render(current);
  document.addEventListener('visibilitychange',visibility);
  media.addEventListener('change',motionPreference);
  observer?.observe(host);
  emit('ready');
  startLife();
  return api;
}
