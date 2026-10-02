import {mountCat, catalog, paletteOptions} from '../index.js';
import {performances} from '../performances.js';

const $ = selector => document.querySelector(selector);
const identities = [
  ['cloud','iris'], ['calico','calico'], ['mask','inkred'],
  ['cowpatch','cow'], ['tabby','tabby'], ['tipped','pointblue']
];
const actions = ['wake','look','blink-look','sleep','breathe'];
let cells = [], generation = 0, timer;
for (const pattern of catalog.patterns) $('#pattern').add(new Option(pattern.name, pattern.id));

function selectedConfig(index) {
  const pattern = $('#pattern').value;
  return pattern === 'identity'
    ? {pattern: identities[index][0], palette: identities[index][1]}
    : {pattern, palette: $('#palette').value};
}
function stopSequence() { generation++; clearTimeout(timer); }
function mount() {
  stopSequence();
  cells.forEach(cell => cell.cats.forEach(cat => cat.destroy()));
  cells = [];
  $('#cats').replaceChildren();
  catalog.shapes.forEach((shape, index) => {
    const row = document.createElement('tr'), label = document.createElement('th');
    label.scope = 'row';
    const name = document.createElement('strong'); name.textContent = shape.name;
    label.append(name, performances[shape.id].description.split(' · ')[1]); row.append(label);
    actions.forEach(action => {
      const cell = document.createElement('td'), pair = document.createElement('div');
      pair.className = 'pair';
      const result = document.createElement('span'); result.className = 'result';
      const cats = ['cat small','cat'].map(className => {
        const host = document.createElement('span'); host.className = className; pair.append(host);
        return mountCat(host, {shape: shape.id, ...selectedConfig(index),
          initialState: ['look','blink-look','sleep'].includes(action) ? 'awake' : 'asleep',
          reducedMotion: $('#reduced').checked ? true : 'system'});
      });
      result.textContent = '待播放'; cell.append(pair, result); row.append(cell);
      cells.push({action, cats, result, index, run: 0});
    });
    $('#cats').append(row);
  });
}
async function playCell(cell, action, reset = false) {
  const run = ++cell.run;
  cell.result.textContent = '播放中 · 离屏时暂停';
  const results = await Promise.all(cell.cats.map(cat => {
    if (reset) cat.setIdle(['look','blink-look','sleep'].includes(action) ? 'awake' : 'asleep');
    return cat.play(action);
  }));
  if (cell.run !== run || !cells.includes(cell)) return;
  cell.result.textContent = results.some(r => r.cancelled) ? '已打断'
    : results.some(r => r.skipped) ? '减少动态 · 已完成' : '已完成';
}
async function playColumn(action) {
  stopSequence();
  const token = generation;
  $('#status').textContent = '正在排练；可以随时切换或打断。';
  await Promise.all(cells.filter(cell => !action || cell.action === action)
    .map(cell => playCell(cell, cell.action, true)));
  if (generation === token) $('#status').textContent = '本轮完成。可切换尺寸、花色或点击另一列。';
}
function updateAppearance() {
  cells.forEach(cell => cell.cats.forEach(cat => cat.setAppearance(selectedConfig(cell.index))));
}
$('#pattern').addEventListener('change', () => {
  $('#palette').replaceChildren();
  const identity = $('#pattern').value === 'identity';
  $('#palette').disabled = identity;
  if (!identity) paletteOptions($('#pattern').value).forEach(p => $('#palette').add(new Option(p.name, p.id)));
  updateAppearance();
});
$('#palette').addEventListener('change', updateAppearance);
$('#size').addEventListener('change', () => document.documentElement.style.setProperty('--small', `${$('#size').value}px`));
$('#dark').addEventListener('change', () => document.body.classList.toggle('dark', $('#dark').checked));
$('#reduced').addEventListener('change', mount);
$('#remount').addEventListener('click', () => { mount(); $('#status').textContent = '旧实例已销毁；六型已重新挂载。'; });
$('#all').addEventListener('click', () => void playColumn());
document.querySelectorAll('[data-action]').forEach(button => button.addEventListener('click', () => void playColumn(button.dataset.action)));
$('#interrupt').addEventListener('click', () => {
  stopSequence();
  cells.forEach(cell => void playCell(cell, 'sleep', true));
  $('#status').textContent = '入睡 180ms 后打断，直接从当前姿态醒来。';
  timer = setTimeout(async () => {
    const token = generation;
    await Promise.all(cells.map(cell => playCell(cell, 'wake')));
    if (token === generation) $('#status').textContent = '打断完成；所有猫应睁眼，身体回到稳定姿态。';
  }, 180);
});
window.addEventListener('pagehide', () => { stopSequence(); cells.forEach(cell => cell.cats.forEach(cat => cat.destroy())); });
window.addEventListener('pageshow', event => { if (event.persisted) mount(); });
mount();
