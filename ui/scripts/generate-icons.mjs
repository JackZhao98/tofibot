import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { roundedIcons } from './rounded-icon-shapes.mjs';
import { zip } from './icon-zip.mjs';
import { iconMotion, motions } from './icon-motion.mjs';

const root=fileURLToPath(new URL('../',import.meta.url));
const output=path.join(root,'public/icons');
const metadata=JSON.parse(await readFile(new URL('./icon-metadata.json',import.meta.url),'utf8'));
const {categories}=metadata;
const names=Object.keys(metadata.icons);
const variants=['outline','filled'];
const properties='fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"';
const escape=value=>String(value).replaceAll('&','&amp;').replaceAll('"','&quot;').replaceAll('<','&lt;').replaceAll('>','&gt;');
const nodes=markup=>Array.from(markup.matchAll(/<(path|rect|circle|ellipse)\s+([^>]*?)\/>/g),match=>[
  match[1],Object.fromEntries(Array.from(match[2].matchAll(/([\w-]+)="([^"]*)"/g),attr=>[attr[1].replace(/-([a-z])/g,(_,letter)=>letter.toUpperCase()),attr[2]]))
]);
const icons=Object.fromEntries(names.map(name=>{
  const shape=roundedIcons[name];
  if(!shape)throw new Error(`Missing rounded drawing: ${name}`);
  if(!iconMotion[name] || !motions[iconMotion[name].preset])throw new Error(`Missing motion assignment: ${name}`);
  return [name,{...metadata.icons[name],motion:iconMotion[name],nodes:nodes(shape.outline),filledNodes:nodes(shape.filled)}];
}));
const manifest={name:'Tofi Rounded Icons',version:'2.1.0',grid:24,strokeWidth:1.8,variants,categories,motions,icons};
const svg=(name,variant='outline')=>`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" ${properties} data-tofi-icon="${name}" data-tofi-variant="${variant}">${roundedIcons[name][variant]}</svg>\n`;
const sprite=`<svg xmlns="http://www.w3.org/2000/svg"><defs>${names.flatMap(name=>variants.map(variant=>`<symbol id="tofi-${name}${variant==='filled'?'-filled':''}" viewBox="0 0 24 24" ${properties}>${roundedIcons[name][variant]}</symbol>`)).join('')}</defs></svg>\n`;
const draw=(name,variant,x,y,size,color='#262b32')=>`<g transform="translate(${x} ${y}) scale(${size/24})" color="${color}" ${properties}>${roundedIcons[name][variant]}</g>`;
function sheet(selected,page){
  const width=1440,height=242+selected.length*390,margin=60,cw=220;
  let result=`<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}"><title>Tofi Rounded Icons / ${page} / outline and filled</title><rect width="100%" height="100%" fill="#fff"/><g fill="#262b32" font-family="Inter, Helvetica Neue, Arial, Hiragino Sans GB, sans-serif"><text x="60" y="78" font-size="36" font-weight="600" letter-spacing="-1">tofi <tspan font-weight="350">/ rounded icons</tspan></text><text x="1380" y="75" text-anchor="end" font-family="SFMono-Regular, Menlo, monospace" font-size="14" fill="#8a929c">COLLECTION 02 / ${page}</text><text x="60" y="123" font-size="18" fill="#65707c">144 个命名图标 · 描边 + 实心 · 24 × 24 SVG</text><text x="1380" y="123" text-anchor="end" font-size="15" fill="#65707c">每组左：描边　右：实心</text><line x1="60" y1="157" x2="1380" y2="157" stroke="#e8ebef"/>`;
  selected.forEach((category,index)=>{
    const top=196+index*390;
    result+=`<text x="60" y="${top}" font-size="19" font-weight="550">${String(categories.indexOf(category)+1).padStart(2,'0')}　${category.label}<tspan fill="#8a929c" font-size="15" font-weight="400">　/ ${escape(category.english)}</tspan></text>`;
    names.filter(name=>icons[name].category===category.id).forEach((name,i)=>{
      const x=margin+(i%6)*cw,y=top+24+Math.floor(i/6)*161;
      result+=`<rect x="${x+4}" y="${y}" width="${cw-8}" height="149" rx="19" fill="#f7f8fa"/>`;
      result+=draw(name,'outline',x+42,y+24,44)+draw(name,'filled',x+132,y+24,44);
      result+=`<text x="${x+cw/2}" y="${y+101}" text-anchor="middle" font-family="SFMono-Regular, Menlo, monospace" font-size="13.5">${name}</text><text x="${x+cw/2}" y="${y+124}" text-anchor="middle" font-size="12.5" fill="#8a929c">${escape(icons[name].label)}</text>`;
    });
  });
  result+=`<line x1="60" y1="${height-66}" x2="1380" y2="${height-66}" stroke="#e8ebef"/><text x="60" y="${height-30}" font-size="13" fill="#8a929c">圆润基础图标 · 双侧天线 Bot · 四褶皱记忆 · 每枚均有独立 SVG</text><text x="1380" y="${height-30}" text-anchor="end" font-family="SFMono-Regular, Menlo, monospace" font-size="12" fill="#8a929c">TOFI ORIGINALS / v2.1.0</text></g></svg>\n`;
  return result;
}
const json=JSON.stringify(manifest,null,2)+'\n';
const readme=`Tofi Rounded Icons v2.1.0\n\n144 named original icons / 12 categories / 288 SVG files.\n24 × 24 viewBox; 1.8 px outline; round caps and joins; currentColor.\n\nsvg/name.svg                 Outline\nsvg/name-filled.svg          Filled / emphasized\ntofi-sprite.svg              Both variants, ids tofi-name / tofi-name-filled\ntofi-contact-sheet.svg       All 144 outline + filled pairs in a named grid\npreviews/                    Six named paired grids (SVG; PNG when rendered)\nmanifest.json                Labels, categories, tags, nodes and filledNodes\nreact/                       Typed React component, catalog, motion runtime and guide\nmotion.js                    Standalone browser motion controller (Web Animations API)\n\nFilled silhouettes have transparent cutouts, never white overpainting.\nOpen symbols (arrows, plus, links, etc.) use a heavier line in the filled variant.\nInline SVG inherits CSS color; <img> SVG does not inherit its parent's color.\nSprite: <svg width="24" height="24"><use href="/icons/tofi-sprite.svg#tofi-bot-filled"/></svg>\nReact: <TofiIcon name="bot" variant="filled" size={24} animated />\nEvery icon has a one-shot semantic motion; hover, keyboard focus and click trigger it.\nRespects prefers-reduced-motion; static SVG assets remain static.\nKeep aria-label on icon-only buttons.\n\nAll SVG paths are original Tofi artwork, not exported Apple or Material assets.\nOrganic A/C studies are separate optional theme explorations.\n`;
const motionModule=await readFile(path.join(root,'src/icons/motion.mjs'),'utf8');
const motionScript=`window.TofiMotion = (() => { ${motionModule.replace('export function createTofiMotion', 'function createTofiMotion')} return { create: createTofiMotion }; })();\n`;
const entries=names.flatMap(name=>variants.map(variant=>[`svg/${name}${variant==='filled'?'-filled':''}.svg`,svg(name,variant)]));
entries.push(['catalog.js',`window.TOFI_ICONS = ${JSON.stringify(manifest)};\n`],['motion.js',motionScript],['tofi-sprite.svg',sprite],['tofi-contact-sheet.svg',sheet(categories,'ALL 144')],['manifest.json',json],['README.txt',readme]);
for(let i=0;i<6;i++)entries.push([`previews/tofi-rounded-${String(i+1).padStart(2,'0')}.svg`,sheet(categories.slice(i*2,i*2+2),`${String(i+1).padStart(2,'0')} / 06`)]);
await mkdir(path.join(output,'svg'),{recursive:true});
await mkdir(path.join(output,'previews'),{recursive:true});
await mkdir(path.join(root,'src/icons'),{recursive:true});
await Promise.all(entries.map(([name,content])=>writeFile(path.join(output,name),content)));
await writeFile(path.join(root,'src/icons/catalog.json'),json);

if(process.argv.includes('--png')){
  for(const [name] of entries.filter(([name])=>name==='tofi-contact-sheet.svg'||name.startsWith('previews/'))){
    const destination=name.replace(/\.svg$/,'.png');
    execFileSync('rsvg-convert',[path.join(output,name),'-o',path.join(output,destination)]);
  }
}
// Include rendered previews if present; PNG generation is an optional authoring step.
for(const name of ['previews/tofi-motion.gif','tofi-contact-sheet.png',...Array.from({length:6},(_,i)=>`previews/tofi-rounded-${String(i+1).padStart(2,'0')}.png`)]){
  try{entries.push([name,await readFile(path.join(output,name))]);}catch(error){if(error.code!=='ENOENT')throw error;}
}
for(const name of ['index.tsx','catalog.json','README.md','motion.mjs','motion.d.mts'])entries.push([`react/${name}`,await readFile(path.join(root,'src/icons',name))]);
await writeFile(path.join(output,'tofi-icons.zip'),zip(entries));
console.log(`Exported ${names.length} named icons, ${names.length*2} SVGs, ${categories.length} categories, paired grid sheets and ZIP.`);
