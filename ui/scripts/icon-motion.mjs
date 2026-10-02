// Short, one-shot motions. Profiles are shared; every icon has an explicit assignment.
const frames = values => values.map((transform, i) => ({ offset: i / (values.length - 1), transform }));
const motion = (label, duration, values, origin = '12px 12px') => ({ label, duration, origin, frames: frames(values) });
export const motions = {
  settle: motion('轻盈落定', 460, ['translateY(0px) scale(1)', 'translateY(-1.5px) scale(1.06)', 'translateY(.5px) scale(.98)', 'translateY(0px) scale(1)']),
  pop: motion('弹性展开', 440, ['scale(1)', 'scale(.88)', 'scale(1.12)', 'scale(.98)', 'scale(1)']),
  pulse: motion('柔和呼吸', 720, ['scale(1)', 'scale(1.1)', 'scale(.98)', 'scale(1)']),
  hello: motion('点头招呼', 640, ['translateY(0px) rotate(0deg)', 'translateY(-1px) rotate(-8deg)', 'translateY(-1px) rotate(6deg)', 'translateY(.4px) rotate(-2deg)', 'translateY(0px) rotate(0deg)']),
  left: motion('向左引导', 440, ['translateX(0px)', 'translateX(1px)', 'translateX(-3px)', 'translateX(0px)']),
  right: motion('向右引导', 440, ['translateX(0px)', 'translateX(-1px)', 'translateX(3px)', 'translateX(0px)']),
  up: motion('向上轻推', 440, ['translateY(0px)', 'translateY(1px)', 'translateY(-3px)', 'translateY(0px)']),
  down: motion('向下落入', 440, ['translateY(0px)', 'translateY(-1px)', 'translateY(3px)', 'translateY(0px)']),
  scan: motion('轻扫寻找', 660, ['translate(0px,0px) rotate(0deg)', 'translate(-1.6px,-1px) rotate(-9deg)', 'translate(1.5px,-.6px) rotate(8deg)', 'translate(0px,0px) rotate(0deg)']),
  unfold: motion('柔软展开', 560, ['scale(1,1)', 'scale(1.06,.87)', 'scale(.98,1.08)', 'scale(1,1)'], '12px 20px'),
  write: motion('轻划书写', 560, ['translate(0px,0px) rotate(0deg)', 'translate(-1px,1px) rotate(-7deg)', 'translate(1.3px,-1px) rotate(4deg)', 'translate(0px,0px) rotate(0deg)']),
  copy: motion('错位叠合', 500, ['translate(0px,0px) scale(1)', 'translate(-1px,-1px) scale(.95)', 'translate(1px,1px) scale(1.04)', 'translate(0px,0px) scale(1)']),
  tip: motion('轻倾回正', 540, ['rotate(0deg)', 'rotate(-10deg)', 'rotate(7deg)', 'rotate(-2deg)', 'rotate(0deg)'], '12px 19px'),
  launch: motion('蓄力发送', 620, ['translateY(0px) scale(1)', 'translateY(1.8px) scale(.92)', 'translateY(-3px) scale(1.04)', 'translateY(.5px) scale(.99)', 'translateY(0px) scale(1)']),
  expand: motion('向外打开', 520, ['translate(0px,0px) scale(1)', 'translate(-.7px,.7px) scale(.94)', 'translate(1.5px,-1.5px) scale(1.05)', 'translate(0px,0px) scale(1)']),
  turn: motion('轻转一圈', 820, ['rotate(0deg)', 'rotate(80deg)', 'rotate(280deg)', 'rotate(360deg)']),
  rock: motion('双向摆动', 640, ['rotate(0deg)', 'rotate(-8deg)', 'rotate(8deg)', 'rotate(-4deg)', 'rotate(0deg)']),
  nod: motion('轻轻点头', 540, ['translateY(0px) scale(1,1)', 'translateY(1px) scale(1.04,.93)', 'translateY(-.8px) scale(.99,1.03)', 'translateY(0px) scale(1,1)']),
  sleep: motion('困意呼吸', 1000, ['translateY(0px) rotate(0deg) scale(1)', 'translateY(.8px) rotate(-6deg) scale(.96)', 'translateY(0px) rotate(0deg) scale(1)']),
  wake: motion('伸展唤醒', 620, ['scale(1,1)', 'scale(1.05,.9)', 'scale(.95,1.12)', 'scale(1.02,.98)', 'scale(1,1)']),
  sparkle: motion('灵感闪动', 640, ['rotate(0deg) scale(1)', 'rotate(-12deg) scale(.88)', 'rotate(10deg) scale(1.16)', 'rotate(-3deg) scale(.97)', 'rotate(0deg) scale(1)']),
  gather: motion('聚合靠拢', 560, ['scale(1,1)', 'scale(.85,1.02)', 'scale(1.07,.98)', 'scale(1,1)']),
  tick: motion('确认回弹', 460, ['rotate(0deg) scale(1)', 'rotate(-8deg) scale(.86)', 'rotate(3deg) scale(1.12)', 'rotate(0deg) scale(1)']),
  pause: motion('收束停靠', 450, ['scale(1,1)', 'scale(.84,1.04)', 'scale(1.04,.98)', 'scale(1,1)']),
  shake: motion('轻摇提醒', 540, ['translateX(0px)', 'translateX(-1.7px)', 'translateX(1.7px)', 'translateX(-.8px)', 'translateX(.5px)', 'translateX(0px)']),
  ring: motion('铃铛轻摇', 780, ['rotate(0deg)', 'rotate(15deg)', 'rotate(-13deg)', 'rotate(9deg)', 'rotate(-5deg)', 'rotate(0deg)'], '12px 4px'),
  link: motion('连接咬合', 600, ['rotate(0deg) scale(1)', 'rotate(-7deg) scale(1.07)', 'rotate(4deg) scale(.94)', 'rotate(0deg) scale(1)']),
  switch: motion('滑动调节', 520, ['translateX(0px)', 'translateX(1.5px)', 'translateX(-1px)', 'translateX(0px)']),
  beat: motion('心跳反馈', 680, ['scale(1)', 'scale(1.1)', 'scale(1)', 'scale(1.06)', 'scale(1)']),
  peek: motion('眨眼回应', 620, ['scale(1,1)', 'scale(1.02,.85)', 'scale(1,1.04)', 'scale(1,1)']),
  float: motion('轻柔漂浮', 800, ['translateY(0px) rotate(0deg)', 'translateY(-1.8px) rotate(3deg)', 'translateY(-.5px) rotate(-2deg)', 'translateY(0px) rotate(0deg)']),
};
export const iconMotion = {};
const assign = (preset, names) => names.split(' ').forEach(name => { iconMotion[name] = { preset }; });
assign('settle', 'home workspace file file-text file-code file-image file-audio file-video clipboard calendar calendar-add note database monitor laptop phone terminal browser server keyboard mcp source');
assign('pop', 'layout-grid plus chat-new user-add group-add bot-add memory-add');
assign('switch', 'sidebar menu settings sliders filter bot-config');
assign('scan', 'search bot-thinking help');
assign('left', 'arrow-left chevron-left reply');
assign('right', 'arrow-right chevron-right forward bot-handoff logout');
assign('up', 'arrow-up upload');
assign('down', 'arrow-down chevron-down download inbox archive');
assign('pause', 'minus close pause stop bot-pause offline');
assign('write', 'edit');
assign('copy', 'copy layers');
assign('tip', 'trash pin cursor');
assign('expand', 'share external-link screen-share');
assign('pulse', 'more chat chat-typing memory online');
assign('ring', 'chat-unread bell');
assign('turn', 'repeat retry progress loading sync');
assign('launch', 'send play bot-run');
assign('link', 'mention attachment link puzzle plug unplug device-pair webhook api');
assign('hello', 'bot emoji');
assign('gather', 'bot-team group group-add merge workflow');
assign('nod', 'bot-chat user id-card queue checklist thumbs-up');
assign('sleep', 'bot-sleep');
assign('wake', 'bot-wake');
assign('sparkle', 'bot-spark star lightbulb sparkles skill');
assign('unfold', 'folder folder-open book-open markdown');
assign('float', 'bookmark tag image cloud moon');
assign('rock', 'chat-history clock timer history code globe language');
assign('beat', 'wifi activity');
assign('tick', 'check check-circle shield-check lock');
assign('shake', 'alert error bell-off thumbs-down');
assign('peek', 'eye eye-off');
assign('tip', 'key unlock');
assign('up', 'mouse');
assign('pulse', 'info sun');
// Animate wrapped parts so authored SVG transforms and filled cutouts stay intact.
const part = (indices, values, duration = 500, extra = {}) => ({ indices, frames: frames(values), duration, ...extra });
iconMotion.bot.parts = [
  part([0], ['scaleY(1)', 'scaleY(.72)', 'scaleY(1.15)', 'scaleY(1)'], 560),
  part([2, 3], ['scaleY(1)', 'scaleY(1)', 'scaleY(.16)', 'scaleY(1)'], 620, { variant: 'outline' }),
];
iconMotion['chat-typing'].parts = [1, 2, 3].map((index, i) => part([index], ['translateY(0px)', 'translateY(-2px)', 'translateY(0px)'], 360, { variant: 'outline', delay: i * 90 }));
iconMotion.more.parts = [0, 1, 2].map((index, i) => part([index], ['translateY(0px)', 'translateY(-2px)', 'translateY(0px)'], 360, { delay: i * 80 }));
iconMotion.settings.parts = [part([1, 3], ['translateX(0px)', 'translateX(1.4px)', 'translateX(0px)']), part([2], ['translateX(0px)', 'translateX(-1.4px)', 'translateX(0px)'])];
iconMotion.sliders.parts = [part([1, 3], ['translateY(0px)', 'translateY(-1.4px)', 'translateY(0px)']), part([2], ['translateY(0px)', 'translateY(1.4px)', 'translateY(0px)'])];
iconMotion.clock.parts = [part([1], ['rotate(0deg)', 'rotate(35deg)', 'rotate(0deg)'], 640, { variant: 'outline' })];
iconMotion.mouse.parts = [part([1], ['translateY(0px)', 'translateY(1.2px)', 'translateY(0px)'], 420, { variant: 'outline' })];
iconMotion['layout-grid'].parts = [0, 1, 3, 2].map((index, i) => part([index], ['translateY(0px)', 'translateY(-1.2px)', 'translateY(0px)'], 320, { delay: i * 55 }));

// v2.2: calm trajectories, with semantic details instead of rubber-band scaling.
Object.assign(motions, {
 settle: motion('轻轻抬起', 900, ['translateY(0)', 'translateY(-1.2px)', 'translateY(-1.2px)', 'translateY(0)']),
 pop: motion('渐次展开', 900, ['scale(1)', 'scale(1.035)', 'scale(1.035)', 'scale(1)']),
 pulse: motion('柔和呼吸', 1200, ['scale(1)', 'scale(1.035)', 'scale(1)']),
 sparkle: motion('灵感亮起', 1200, ['translateY(0)', 'translateY(-.6px)', 'translateY(0)']),
 tick: motion('确认落定', 850, ['translateY(0)', 'translateY(-1px)', 'translateY(0)']),
 gather: motion('聚拢协作', 1000, ['scaleX(1)', 'scaleX(.97)', 'scaleX(1)']),
 pause: motion('平稳停靠', 800, ['scale(1)', 'scale(.97)', 'scale(1)']),
 launch: motion('向上送出', 1000, ['translateY(0)', 'translateY(-2px)', 'translateY(-2px)', 'translateY(0)']),
 expand: motion('向外打开', 1000, ['translate(0,0)', 'translate(1px,-1px)', 'translate(1px,-1px)', 'translate(0,0)']),
 unfold: motion('缓缓展开', 1000, ['scaleY(1)', 'scaleY(.96)', 'scaleY(1)'], '12px 20px'),
 copy: motion('错位叠合', 950, ['translate(0,0)', 'translate(1px,1px)', 'translate(0,0)']),
 nod: motion('轻轻回应', 950, ['translateY(0)', 'translateY(1px)', 'translateY(0)']),
 wake: motion('伸展唤醒', 1100, ['scaleY(1)', 'scaleY(1.045)', 'scaleY(1)']),
 still: motion('局部细节', 1100, ['translate(0,0)', 'translate(0,0)']),
});
const rays = ['M3 5 1.5 3.5','M12 2V.2','M21 5l1.5-1.5','M2 12H.2','M22 12h1.8'];
const accent = (d, delay=0) => ({nodes:[['path',{d,fill:'none',stroke:'currentColor','stroke-width':'1.4','stroke-linecap':'round'}]],duration:1200,delay,frames:[{opacity:0,transform:'scale(.85)',offset:0},{opacity:.85,transform:'scale(1)',offset:.32},{opacity:.7,transform:'scale(1)',offset:.68},{opacity:0,transform:'scale(1.12)',offset:1}]});
for (const name of ['lightbulb','bot-spark','sparkles','skill','star']) iconMotion[name].accents=rays.map((d,i)=>accent(d,i*65));
for (const name of ['chat-new','bot-add','user-add','memory-add']) iconMotion[name].accents=[accent('M20 1v3M18.5 2.5h3',180)];
for (const name of ['chat-typing','more','settings','sliders','clock','layout-grid','bot','emoji']) iconMotion[name].preset='still';
iconMotion['chat-typing'].parts=[1,2,3].map((i)=>part([i],['translateY(0)','translateY(-1.7px)','translateY(0)'],650,{variant:'outline',delay:(i-1)*160}));
iconMotion.more.parts=[0,1,2].map(i=>part([i],['translateY(0)','translateY(-1.5px)','translateY(0)'],650,{delay:i*160}));
iconMotion.emoji.parts=[part([1,2],['scaleY(1)','scaleY(1)','scaleY(.2)','scaleY(1)'],1100,{variant:'outline',origin:'12px 9px'})];
iconMotion['bot-thinking'].accents=[0,1,2].map(i=>({nodes:[['circle',{cx:String(17+i*3),cy:'2',r:'.7',fill:'currentColor',stroke:'none'}]],duration:1100,delay:i*180,frames:[{opacity:0},{opacity:1},{opacity:0}]}));
Object.assign(motions, {
 hello: motion('轻轻招呼',1100,['rotate(0deg)','rotate(-3deg)','rotate(0deg)']),
 scan: motion('左右观察',1200,['translateX(0)','translateX(-1.2px)','translateX(1.2px)','translateX(0)']),
 rock: motion('缓缓回望',1100,['rotate(0deg)','rotate(-5deg)','rotate(0deg)']),
 link: motion('连接靠拢',1000,['rotate(0deg)','rotate(-4deg)','rotate(0deg)']),
 beat: motion('信号呼吸',1200,['scale(1)','scale(1.04)','scale(1)']),
 peek: motion('轻轻眨眼',1100,['scaleY(1)','scaleY(1)','scaleY(.88)','scaleY(1)']),
 tip: motion('轻倾回正',1000,['rotate(0deg)','rotate(-5deg)','rotate(0deg)'],'12px 19px'),
});
// Controls with moving parts keep the outer silhouette steady.
iconMotion.send.preset='still';
iconMotion.send.parts=[part([1],['translateY(0)','translateY(-2px)','translateY(0)'],1000,{variant:'outline'})];
iconMotion['chat-unread'].preset='still';
iconMotion['chat-unread'].parts=[{indices:[1],duration:1200,variant:'outline',frames:[{opacity:1},{opacity:.3},{opacity:1}]}];
