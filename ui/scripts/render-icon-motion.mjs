import { readFile, writeFile, mkdir, mkdtemp, rm } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { roundedIcons } from './rounded-icon-shapes.mjs';
import { motions, iconMotion } from './icon-motion.mjs';

// Render a remote-friendly GIF from the same transforms/timing as the SVG motion runtime.
const output=fileURLToPath(new URL('../public/icons/previews/',import.meta.url));
const directory=await mkdtemp(path.join(tmpdir(),'tofi-motion-'));
const selection=['bot','send','bell','search','chat-typing','memory','settings','folder','arrow-right','workflow','loading','check-circle'];
const props='fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"';
const number=/-?(?:\d*\.)?\d+/g;
const cubic=(t,a,b)=>3*(1-t)**2*t*a+3*(1-t)*t*t*b+t*t*t;
function ease(progress){
  if(progress<=0||progress>=1)return progress;
  let low=0,high=1;
  for(let i=0;i<18;i++){const mid=(low+high)/2;if(cubic(mid,.22,.2)<progress)low=mid;else high=mid;}
  return cubic((low+high)/2,.68,1);
}
function transform(track,elapsed){
  const t=ease(Math.min(1,Math.max(0,(elapsed-(track.delay||0))/track.duration)));
  const frames=track.frames;
  let right=frames.findIndex(frame=>frame.offset>=t);
  if(right<1)right=1;
  const a=frames[right-1],b=frames[right];
  const mix=(t-a.offset)/(b.offset-a.offset);
  const av=a.transform.match(number).map(Number),bv=b.transform.match(number).map(Number);
  let index=0;
  const css=a.transform.replace(number,()=>{const value=av[index]+(bv[index]-av[index])*mix;index++;return value.toFixed(4);});
  const svg=css.replaceAll('px','').replaceAll('deg','')
    .replace(/translateX\(([^)]+)\)/g,'translate($1 0)')
    .replace(/translateY\(([^)]+)\)/g,'translate(0 $1)');
  const [x,y]=(track.origin||'12px 12px').match(number).map(Number);
  return `translate(${x} ${y}) ${svg} translate(${-x} ${-y})`;
}
function glyph(name,variant,x,y,elapsed){
  const spec=iconMotion[name],preset=motions[spec.preset];
  const parts=Array.from(roundedIcons[name][variant].matchAll(/<(?:path|circle|rect|ellipse)\s+[^>]*\/>/g),match=>match[0]);
  const content=parts.map((markup,index)=>{
    const track=spec.parts?.find(part=>part.indices.includes(index)&&(!part.variant||part.variant===variant));
    return track?`<g transform="${transform(track,elapsed)}">${markup}</g>`:markup;
  }).join('');
  return `<g transform="translate(${x} ${y}) scale(2.25)" color="#2b3239" ${props}><g transform="${transform(preset,elapsed)}">${content}</g></g>`;
}
await mkdir(output,{recursive:true});
const frames=[];
try{
  for(let frame=0;frame<44;frame++){
    const time=frame*50;
    let svg='<svg xmlns="http://www.w3.org/2000/svg" width="1140" height="840" viewBox="0 0 1140 840"><rect width="100%" height="100%" fill="#fff"/><g fill="#28313a" font-family="Inter, Helvetica Neue, Arial, Hiragino Sans GB, sans-serif"><text x="48" y="66" font-size="32" font-weight="600">tofi / icons in motion</text><text x="1092" y="64" text-anchor="end" font-size="13" fill="#9099a3">MOTION PREVIEW / 2.1</text><text x="48" y="108" font-size="17" fill="#76818b">144 枚均有动效 · 此处选取 12 枚 · 每组左：描边 / 右：实心</text><line x1="48" y1="142" x2="1092" y2="142" stroke="#e9edf1"/>';
    selection.forEach((name,index)=>{
      const x=48+(index%4)*264,y=165+Math.floor(index/4)*197;
      const elapsed=time-90*(index%4);
      svg+=`<rect x="${x}" y="${y}" width="252" height="180" rx="19" fill="#f6f8fa"/>`;
      svg+=glyph(name,'outline',x+44,y+28,elapsed)+glyph(name,'filled',x+152,y+28,elapsed);
      svg+=`<text x="${x+126}" y="${y+119}" text-anchor="middle" font-size="14" font-family="SFMono-Regular, Menlo, monospace">${name}</text><text x="${x+126}" y="${y+148}" text-anchor="middle" font-size="13" fill="#828d98">${motions[iconMotion[name].preset].label}</text>`;
    });
    svg+='<text x="48" y="804" font-size="13" fill="#8a949e">图鉴中悬停或点击播放一次 · 详情支持重播 · 遵循系统减少动态效果设置</text><text x="1092" y="804" text-anchor="end" font-size="12" fill="#8a949e">此 GIF 循环展示</text></g></svg>';
    const source=path.join(directory,'frame.svg'),png=path.join(directory,`frame-${String(frame).padStart(3,'0')}.png`);
    await writeFile(source,svg);
    execFileSync('rsvg-convert',[source,'-o',png]);
    frames.push(png);
    if(frame===8)await writeFile(path.join(output,'tofi-motion-poster.png'),await readFile(png));
  }
  execFileSync('magick',['-delay','5','-loop','0',...frames,'-colors','128','-layers','Optimize',path.join(output,'tofi-motion.gif')]);
  console.log('Rendered tofi-motion.gif: 12 outline/filled pairs, 44 frames.');
}finally{await rm(directory,{recursive:true,force:true});}
