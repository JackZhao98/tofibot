// Approved coat library, extracted without the previous motion implementation.
  const palettes = [
    {id:'calico',name:'经典三花',body:'#FFF9F0',mark:'#262A2F',accent:'#DB8745',white:true},
    {id:'cow',name:'黑白奶牛',body:'#FFFCF6',mark:'#25292E',accent:'#25292E',white:true},
    {id:'tabby',name:'蓝灰狸花',body:'#9AAABE',mark:'#283345',accent:'#586A82'},
    {id:'inkred',name:'墨黑朱红',body:'#363B49',mark:'#DE624C',accent:'#F2A063'},
    {id:'coffee',name:'海豹棕',body:'#FFFCF6',mark:'#63504A',accent:'#63504A',white:true},
    {id:'pointblue',name:'蓝灰',body:'#FFFCF6',mark:'#556D8A',accent:'#556D8A',white:true},
    {id:'pointlilac',name:'丁香紫',body:'#FFFCF6',mark:'#8C7F9F',accent:'#8C7F9F',white:true},
    {id:'pointcinnamon',name:'肉桂',body:'#FFFCF6',mark:'#B87F59',accent:'#B87F59',white:true},
    {id:'pointcharcoal',name:'炭灰',body:'#FFFCF6',mark:'#50545D',accent:'#50545D',white:true},
    {id:'pointrose',name:'烟玫瑰',body:'#FFFCF6',mark:'#AA7584',accent:'#AA7584',white:true},
    {id:'ivory',name:'奶白',body:'#FFFCF6',mark:'#9C7E65',accent:'#C8A580',white:true},
    {id:'inkviolet',name:'紫电墨蓝',body:'#797BC4',mark:'#303953',accent:'#BE85D9'},
    {id:'iris',name:'鸢尾紫',body:'#C9B5E4',mark:'#8161B1',accent:'#AD86CC'},
    {id:'caramel',name:'焦糖杏',body:'#EAB582',mark:'#A9663C',accent:'#CF8D52'},
    {id:'ocean',name:'海盐蓝',body:'#A0C2DA',mark:'#4F789D',accent:'#759EBF'},
    {id:'moss',name:'苔藓绿',body:'#ADCCB0',mark:'#60876A',accent:'#83AD83'},
    {id:'berry',name:'莓果粉',body:'#DFA8B9',mark:'#9F5F7A',accent:'#C27F9C'},
    {id:'slate',name:'烟灰蓝',body:'#AAB3C4',mark:'#58677F',accent:'#7F8EA7'},
    {id:'honey',name:'蜂蜜金',body:'#E9CA81',mark:'#AC793C',accent:'#CC9B51'},
    {id:'lagoon',name:'湖水青',body:'#8BC5BD',mark:'#417F7A',accent:'#62A69B'}
  ];
  const patterns = [
    {id:'solid',name:'纯色',note:'完整底色 · 没有花纹'},
    {id:'calico',name:'大块三花',note:'黑橘双耳 · 大块拼花',palette:'calico'},
    {id:'cowblaze',name:'奶牛 · 八字脸',note:'额头连色 · 鼻梁圆顶留白',palette:'cow'},
    {id:'cowpatch',name:'奶牛 · 偏分脸',note:'单侧眼罩 · 不对称黑斑',palette:'cow'},
    {id:'tabby',name:'狸花',note:'额头山纹 · 身体虎纹',palette:'tabby'},
    {id:'stripes',name:'圆条纹',note:'圆头条纹 · 自由换色'},
    {id:'beans',name:'大斑点',note:'头耳与全身错落斑点'},
    {id:'halfmask',name:'阴阳脸',note:'半张脸 · 头尾撞色',palette:'inkviolet'},
    {id:'mask',name:'小面罩',note:'双眼面罩 · 尾端呼应',palette:'inkred'},
    {id:'tipped',name:'重点色',note:'深耳面颊 · 深色尾端',palette:'coffee'},
    {id:'tortie',name:'碎玳瑁',note:'脸部与身体交错碎花'},
    {id:'cloud',name:'云朵斑',note:'耳部小云 · 背部大云'},
    {id:'split',name:'双色拼花',note:'半脸与大块拼色'},
    {id:'saddle',name:'背鞍',note:'耳帽与大片背色'}
  ];
  const fixedPalettes={calico:'calico',cowblaze:'cow',cowpatch:'cow',tabby:'tabby'};
  const pointIds=['coffee','pointblue','pointlilac','pointcinnamon','pointcharcoal','pointrose'];
  const generalIds=['ivory','iris','caramel','ocean','moss','berry','slate','honey','lagoon','inkred','inkviolet'];
  for(const p of patterns){p.mode=(fixedPalettes[p.id]||p.id==='tipped')?'special':'general';}
  function paletteOptions(patternId){
    const ids=patternId==='tipped'?pointIds:fixedPalettes[patternId]?[fixedPalettes[patternId]]:generalIds;
    return ids.map(id=>palettes.find(p=>p.id===id));
  }
  function resolveAppearance(style,paletteId,patternId){
    const options=paletteOptions(patternId),chosen=options.find(p=>p.id===paletteId)||options[0];
    const colors=patternId==='tipped'?{...chosen,body:'#FFFCF6',accent:chosen.mark,white:true}:chosen;
    const plain=style==='A'||patternId==='solid',white=style!=='A'&&(style==='C'||colors.white);
    return {colors,plain,white,fill:style==='A'?colors.mark:white?'#FFFCF6':colors.body};
  }
  // Back, head, and visible-tail regions are placed separately for each silhouette.
  const coatRegions={
    '01-curl':[212,77,92,182], '02-loaf':[167,141,133,124],
    '03-tall':[142,154,139,134], '04-rice':[181,175,120,112],
    '05-bean':[20,116,126,145], '06-puddle':[177,178,131,91],
    '07-stand':[160,140,120,96]
  };
  const path=(d,c)=>`<path d="${d}" fill="${c}"/>`;
  function coat(kind,p,a){
    switch(kind){
      case 'saddle':return path('M 66 -7 C 93 -12 117 8 112 31 C 121 53 118 82 98 96 C 81 111 41 105 28 87 C 12 68 38 56 25 40 C 10 21 40 -2 66 -7 Z',p);
      case 'tabby':return [
        'M 20 -8 C 32 10 40 18 66 20 C 76 22 70 31 57 29 C 30 27 14 16 8 0 Z',
        'M 32 30 C 46 44 61 47 89 42 C 100 41 99 50 88 53 C 64 61 42 52 29 43 C 20 36 23 28 32 30 Z',
        'M 46 63 C 60 74 76 75 103 70 L 109 84 C 87 93 61 87 44 77 C 33 70 36 57 46 63 Z'
      ].map(d=>path(d,p)).join('');
      case 'cloud':return path('M 48 2 C 33 -2 20 9 24 22 C 7 28 10 47 27 50 C 26 66 45 74 57 64 C 72 76 90 61 86 48 C 104 38 96 17 80 19 C 78 1 61 -7 48 2 Z',p)+path('M 28 77 C 16 66 0 76 5 88 C -5 101 13 112 24 104 C 43 108 46 87 28 77 Z',a);
      case 'tortie':return path('M 11 -7 C 29 -3 38 12 28 23 C 19 29 22 42 8 43 L -11 35 Z',p)+path('M 61 2 C 77 -3 98 13 86 29 C 99 46 82 61 69 49 C 48 52 38 35 49 23 C 43 14 48 5 61 2 Z',a)+path('M 23 52 C 39 41 58 48 51 67 C 66 80 53 96 37 89 C 18 105 5 87 15 74 C 5 65 11 56 23 52 Z',p)+path('M 89 70 C 105 66 115 81 110 99 L 89 112 C 72 106 68 89 80 85 C 71 78 80 71 89 70 Z',a);
      case 'beans':return path('M 28 11 C 38 6 45 13 40 23 C 33 32 21 27 23 19 C 20 16 23 12 28 11 Z',p)+path('M 70 20 C 83 17 89 28 80 35 C 69 44 58 33 64 26 C 63 24 66 21 70 20 Z',a)+path('M 39 48 C 48 41 58 51 52 61 C 45 72 31 61 36 55 C 32 53 34 50 39 48 Z',p)+path('M 82 62 C 95 58 101 72 91 80 C 81 88 68 76 77 70 C 72 67 76 63 82 62 Z',p)+path('M 15 80 C 26 76 33 87 25 95 C 18 102 5 93 10 87 C 8 84 12 81 15 80 Z',a);
      case 'split':return path('M 67 -8 C 98 -13 126 14 115 38 C 128 65 111 99 89 106 C 60 119 23 106 22 86 C 17 66 53 61 40 42 C 26 18 37 -1 67 -8 Z',p)+path('M 100 12 C 118 6 130 23 123 43 C 136 68 120 92 100 105 C 79 118 54 109 58 91 C 60 76 88 64 76 46 C 64 30 80 14 100 12 Z',a);
      default:return '';
    }
  }
  const headRegions={
    '01-curl':[45,135,120,120,-20], '02-loaf':[51,111,122,133,0],
    '03-tall':[92,52,113,143,0], '04-rice':[94,86,106,167,0],
    '05-bean':[163,62,97,110,20], '06-puddle':[72,164,104,90,0],
    '07-stand':[50,98,118,122,0]
  };
  const tailRegions={'01-curl':[86,51,141,85,-3],'03-tall':[229,181,55,82,-12]};
  const transform=([x,y,w,h,angle=0])=>`translate(${x} ${y}) rotate(${angle} ${w/2} ${h/2}) scale(${w/100} ${h/100})`;
  // Fit the narrow blaze to the gap between the complete eye paths.
  const faceCache=new Map();
  function faceMasks(cat){
    if(faceCache.has(cat.id))return faceCache.get(cat.id);
    const [x,y,w,h,angle=0]=headRegions[cat.id],a=-angle*Math.PI/180;
    const eyes=cat.eyes.map(d=>{
      const n=d.match(/-?\d*\.?\d+/g).map(Number),points=[];
      for(let i=0;i<n.length;i+=2){
        const dx=n[i]-x-w/2,dy=n[i+1]-y-h/2;
        points.push([(dx*Math.cos(a)-dy*Math.sin(a)+w/2)*100/w,(dx*Math.sin(a)+dy*Math.cos(a)+h/2)*100/h]);
      }
      return {minX:Math.min(...points.map(p=>p[0])),maxX:Math.max(...points.map(p=>p[0])),minY:Math.min(...points.map(p=>p[1])),maxY:Math.max(...points.map(p=>p[1]))};
    }).sort((a,b)=>a.minX-b.minX);
    const gapL=eyes[0].maxX+3,gapR=eyes[1].minX-3,c=(gapL+gapR)/2;
    const half=Math.max(1.2,Math.min(3.1,(gapR-gapL)/2));
    const top=Math.min(...eyes.map(e=>e.minY))-8,low=Math.max(...eyes.map(e=>e.maxY))+7;
    const l=c-half,r=c+half;
    const notch=`C ${r} ${low-2} ${r} ${top+8} ${r} ${top+5} C ${r} ${top+1} ${c+1.4} ${top} ${c} ${top} C ${c-1.4} ${top} ${l} ${top+1} ${l} ${top+5} C ${l} ${top+8} ${l} ${low-2} ${l} ${low}`;
    const faceMask=`M -20 -22 L 122 -22 L 122 48 C 120 70 105 94 84 94 C 71 94 ${r} ${low+10} ${r} ${low} ${notch} C ${l} ${low+10} 34 94 17 92 C -6 90 -20 68 -20 44 Z`;
    const rightMask=`M ${c} -22 L 122 -22 L 122 48 C 120 70 105 94 84 94 C 71 94 ${r} ${low+10} ${r} ${low} C ${r} ${low-2} ${r} ${top+8} ${r} ${top+5} C ${r} ${top+1} ${c+1.4} ${top} ${c} ${top} C ${c-3} 28 ${c+3} 7 ${c} -22 Z`;
    const result={faceMask,rightMask,center:c};faceCache.set(cat.id,result);return result;
  }
  function headCoat(kind,p,a,cat){
    const {faceMask,rightMask}=faceMasks(cat);
    if(kind==='calico')return path(faceMask,p)+path(rightMask,a);
    if(kind==='cowblaze')return path(faceMask,p);
    if(kind==='cowpatch')return path('M -18 -15 L 53 -15 C 66 8 46 23 51 42 C 61 62 51 82 31 84 C 2 90 -19 68 -18 39 Z',p);
    if(kind==='halfmask'||kind==='split')return path('M -18 -15 L 50 -15 C 41 17 60 26 51 49 C 42 73 50 87 30 98 C 9 112 -6 109 -18 93 Z',p);
    if(kind==='mask')return path('M -10 -14 L 111 -14 L 107 10 C 91 20 94 38 77 42 C 59 45 54 34 44 38 C 28 51 15 40 10 25 L -10 11 Z',a)+path('M 13 42 C 29 31 43 50 53 48 C 68 35 90 40 94 55 C 101 76 77 91 60 80 C 51 72 47 80 37 82 C 16 89 1 58 13 42 Z',p);
    if(kind==='tabby')return [
      'M 33 -8 C 34 9 42 13 45 25 C 48 36 38 40 34 28 C 29 14 22 8 23 -8 Z',
      'M 51 -8 C 54 8 54 19 51 31 C 48 42 43 35 45 24 C 46 13 42 2 42 -8 Z',
      'M 72 -8 C 66 9 62 15 62 26 C 61 38 52 36 55 23 C 58 7 62 -1 63 -8 Z',
      'M -9 62 C 3 65 10 68 19 71 C 28 76 26 81 16 80 C 7 79 -2 77 -9 74 Z',
      'M 108 66 C 95 70 87 70 79 75 C 72 80 81 85 91 81 L 108 77 Z'
    ].map(d=>path(d,p)).join('');
    if(kind==='tipped')return path(faceMask,p)+path('M 24 35 C 45 22 79 25 89 45 C 106 70 82 95 58 94 C 30 102 11 74 24 35 Z',p);
    if(kind==='beans')return path('M -9 -9 L 27 -9 C 41 10 31 27 17 31 C -1 38 -17 17 -9 -9 Z',p)+path('M 64 25 C 76 17 85 26 81 38 C 80 50 67 51 62 43 C 55 36 56 30 64 25 Z',a)+path('M 13 72 C 26 66 37 77 30 87 C 20 101 5 88 9 82 C 5 76 8 74 13 72 Z',p);
    if(kind==='tortie')return path('M -12 -14 L 49 -14 C 66 1 57 20 43 22 C 48 39 26 42 20 29 C 1 40 -14 18 -12 -14 Z',p)+path('M 75 -12 L 115 -12 L 114 51 C 93 65 75 53 82 39 C 64 31 61 6 75 -12 Z',a)+path('M 22 52 C 41 44 54 59 46 75 C 28 89 13 70 22 52 Z',a);
    if(kind==='cloud')return path('M -8 -14 L 42 -14 C 60 4 43 17 34 14 C 34 30 15 32 8 21 C -13 22 -23 4 -8 -14 Z',a);
    return path('M -12 -14 L 33 -14 C 37 11 25 29 6 27 L -12 19 Z',p);
  }
  function tailCoat(kind,p,a){
    if(kind==='tabby')return [
      'M 5 -15 L 18 -15 C 12 15 19 53 33 105 L 19 111 C 4 73 -5 17 5 -15 Z',
      'M 36 -15 L 47 -15 C 43 19 52 60 62 105 L 49 111 C 36 72 28 13 36 -15 Z',
      'M 70 -15 L 81 -15 C 79 32 86 72 94 112 L 81 112 C 69 66 62 21 70 -15 Z'
    ].map(d=>path(d,p)).join('');
    if(kind==='beans')return path('M 5 -10 C 32 -18 43 6 35 27 C 37 48 17 60 2 44 L -12 30 Z',p)+path('M 59 36 C 75 31 83 46 77 57 C 68 70 52 61 56 50 C 50 43 53 39 59 36 Z',a);
    if(kind==='tortie'||kind==='calico')return path('M -12 -14 L 34 -14 C 46 7 28 24 40 43 C 55 64 33 105 3 110 L -12 99 Z',a)+path('M 64 -9 C 83 -18 110 -8 118 14 L 115 58 C 97 76 71 60 78 46 C 60 29 49 11 64 -9 Z',p);
    return path('M -16 -14 L 39 -14 C 53 10 31 30 45 51 C 60 77 33 107 8 112 L -16 103 Z',kind==='halfmask'||kind==='split'?a:p);
  }
  function bodyCoat(kind,p,a){
    if(kind==='calico')return coat('tortie',p,a);
    if(kind==='cowblaze'||kind==='cowpatch')return path('M 57 7 C 74 -2 96 13 85 30 C 101 43 88 61 75 59 C 68 83 44 73 44 59 C 23 53 33 31 47 30 C 40 18 46 10 57 7 Z',p)+path('M 12 86 C 29 80 40 98 24 111 L -11 109 C -14 94 0 87 12 86 Z',p);
    if(kind==='halfmask'||kind==='mask')return coat('split',p,a);
    if(kind==='tipped')return '';
    return coat(kind,p,a);
  }
  // Round paint boundaries as closed cubic splines. Only coat paths pass
  // through this helper: original silhouette and eye geometry stay untouched.
  const roundedPaths=new Map();
  function roundMarkPath(d){
    if(roundedPaths.has(d))return roundedPaths.get(d);
    const tokens=d.match(/[MLCZ]|-?\d*\.?\d+(?:e[-+]?\d+)?/gi);
    const points=[];let cursor=0,current=[0,0],start=[0,0];
    const point=()=>[Number(tokens[cursor++]),Number(tokens[cursor++])];
    const distance=(a,b)=>Math.hypot(a[0]-b[0],a[1]-b[1]);
    function line(end){
      const from=current,n=Math.max(1,Math.ceil(distance(from,end)/2));
      for(let j=1;j<=n;j++)points.push([from[0]+(end[0]-from[0])*j/n,from[1]+(end[1]-from[1])*j/n]);
      current=end;
    }
    while(cursor<tokens.length){
      const command=tokens[cursor++].toUpperCase();
      if(command==='M'){current=point();start=current;points.push(current);}
      else if(command==='L')line(point());
      else if(command==='C'){
        const from=current,c1=point(),c2=point(),end=point();
        const n=Math.max(4,Math.ceil((distance(from,c1)+distance(c1,c2)+distance(c2,end))/2));
        for(let j=1;j<=n;j++){
          const t=j/n,u=1-t;
          points.push([u*u*u*from[0]+3*u*u*t*c1[0]+3*u*t*t*c2[0]+t*t*t*end[0],u*u*u*from[1]+3*u*u*t*c1[1]+3*u*t*t*c2[1]+t*t*t*end[1]]);
        }
        current=end;
      } else if(command==='Z')line(start);
    }
    const lengths=[0];for(let i=1;i<points.length;i++)lengths.push(lengths[i-1]+distance(points[i-1],points[i]));
    const total=lengths.at(-1),count=Math.max(12,Math.ceil(total/8)),ring=[];let edge=1;
    for(let i=0;i<count;i++){
      const target=i*total/count;while(edge<lengths.length-1&&lengths[edge]<target)edge++;
      const t=(target-lengths[edge-1])/(lengths[edge]-lengths[edge-1]||1),a=points[edge-1],b=points[edge];
      ring.push([a[0]+(b[0]-a[0])*t,a[1]+(b[1]-a[1])*t]);
    }
    const mix=(a,b,c)=>[(a[0]+4*b[0]+c[0])/6,(a[1]+4*b[1]+c[1])/6];
    const fmt=p=>p.map(v=>Number(v.toFixed(3))).join(' ');
    let result='M '+fmt(mix(ring.at(-1),ring[0],ring[1]));
    for(let i=0;i<count;i++){
      const a=ring[i],b=ring[(i+1)%count],c=ring[(i+2)%count];
      result+=' C '+fmt([(2*a[0]+b[0])/3,(2*a[1]+b[1])/3])+' '+fmt([(a[0]+2*b[0])/3,(a[1]+2*b[1])/3])+' '+fmt(mix(a,b,c));
    }
    result+=' Z';roundedPaths.set(d,result);return result;
  }
  function dark(color){
    const v=color.slice(1).match(/../g).map(x=>parseInt(x,16)/255).map(x=>x<=.04045?x/12.92:((x+.055)/1.055)**2.4);
    return .2126*v[0]+.7152*v[1]+.0722*v[2]<.21;
  }
  function renderParts(cat,style,paletteId,patternId,id){
    const {colors,plain,white,fill}=resolveAppearance(style,paletteId,patternId);
    const coatId=patternId==='stripes'?'tabby':patternId;
    const pieces=[];
    function add(svg,region){
      for(const m of svg.matchAll(/<path d="([^"]+)" fill="([^"]+)"\/>/g))pieces.push({d:roundMarkPath(m[1]),fill:m[2],transform:transform(region),region});
    }
    if(!plain){
      if(patternId==='tabby'){
        add(path('M 10 74 C 28 59 47 79 61 83 C 80 86 95 74 108 88 C 123 109 89 125 55 123 C 24 127 -10 101 10 74 Z','#FFFCF6'),coatRegions[cat.id]);
        add(path('M 16 78 C 31 71 38 82 51 83 C 65 85 78 71 92 79 C 109 94 86 113 57 115 C 30 119 1 98 16 78 Z','#FFFCF6'),headRegions[cat.id]);
      }
      add(bodyCoat(coatId,colors.mark,colors.accent),coatRegions[cat.id]);
      add(headCoat(coatId,colors.mark,colors.accent,cat),headRegions[cat.id]);
      if(tailRegions[cat.id])add(tailCoat(coatId,colors.mark,colors.accent),tailRegions[cat.id]);
    }
    const marks=pieces.map(p=>`<path d="${p.d}" fill="${p.fill}" transform="${p.transform}"/>`).join('');
    const bakedMarks=pieces.map(bakeMark),eyeSpecs=cat.eyeCenters;
    const eyes=cat.eyes.map((d,i)=>path(d,eyeInk(fill,bakedMarks,eyeSpecs[i]))).join('');
    const outline=white?`<path d="${cat.body}" fill="none" stroke="#292B29" stroke-width="4.5" stroke-linejoin="round"/>`:'';
    const label=`${cat.name} · ${plain?'纯色':patterns.find(p=>p.id===patternId)?.name||'花纹'} · ${colors.name}`;
    return {pieces,bakedMarks,fill,label,marks,eyes,outline,defs:`<clipPath id="${id}-coat"><path d="${cat.body}"/></clipPath>`};
  }
  function readCurves(d){
    const tokens=d.match(/[MLCZ]|-?\d*\.?\d+(?:e[-+]?\d+)?/gi)||[];
    const curves=[];let i=0,current=[0,0],start=[0,0];
    const point=()=>[Number(tokens[i++]),Number(tokens[i++])];
    function line(to){
      const a=current;
      curves.push([a,[(2*a[0]+to[0])/3,(2*a[1]+to[1])/3],[(a[0]+2*to[0])/3,(a[1]+2*to[1])/3],to]);
      current=to;
    }
    while(i<tokens.length){
      const op=tokens[i++];
      if(op==='M'){current=point();start=current;}
      else if(op==='C'){const b=point(),c=point(),to=point();curves.push([current,b,c,to]);current=to;}
      else if(op==='L')line(point());
      else if(op==='Z'&&(current[0]!==start[0]||current[1]!==start[1]))line(start);
    }
    return curves;
  }
  const number=n=>String(Number(n.toFixed(5)));
  const pair=p=>`${number(p[0])} ${number(p[1])}`;
  function writeCurves(curves){
    return `M ${pair(curves[0][0])}`+curves.map(q=>` C ${pair(q[1])} ${pair(q[2])} ${pair(q[3])}`).join('')+' Z';
  }
  function bakeMark(piece){
    const [x,y,w,h,angle=0]=piece.region,a=angle*Math.PI/180,c=Math.cos(a),s=Math.sin(a);
    const map=([px,py])=>{
      const dx=px*w/100-w/2,dy=py*h/100-h/2;
      return [x+w/2+c*dx-s*dy,y+h/2+s*dx+c*dy];
    };
    return {d:writeCurves(readCurves(piece.d).map(q=>q.map(map))),fill:piece.fill};
  }

  const paintPolygons=new Map();
  function containsPaint(d,x,y){
    let points=paintPolygons.get(d);
    if(!points){
      points=[];
      for(const q of readCurves(d)){
        for(let i=0;i<12;i++){
          const t=i/12,u=1-t;
          points.push([u*u*u*q[0][0]+3*u*u*t*q[1][0]+3*u*t*t*q[2][0]+t*t*t*q[3][0],u*u*u*q[0][1]+3*u*u*t*q[1][1]+3*u*t*t*q[2][1]+t*t*t*q[3][1]]);
        }
      }
      paintPolygons.set(d,points);
    }
    let inside=false;
    for(let i=0,j=points.length-1;i<points.length;j=i++){
      const a=points[i],b=points[j];
      if((a[1]>y)!==(b[1]>y)&&x<(b[0]-a[0])*(y-a[1])/(b[1]-a[1])+a[0])inside=!inside;
    }
    return inside;
  }
  function eyeInk(base,marks,eye){
    let color=base;
    for(const mark of marks)if(containsPaint(mark.d,eye[0],eye[1]))color=mark.fill;
    return dark(color)?'#FFFFFF':'#000000';
  }

export {palettes,patterns,paletteOptions};
export function createCoat(shape,pattern,palette){
  const cat={...shape,eyes:shape.eyes.map(eye=>eye.d)};
  const parts=renderParts(cat,'D',palette,pattern,'material');
  const appearance=resolveAppearance('D',palette,pattern);
  return {fill:parts.fill,marks:parts.bakedMarks,label:parts.label,
    outline:appearance.white,
    eyeColors:shape.eyeCenters.map(eye=>eyeInk(parts.fill,parts.bakedMarks,eye))};
}
