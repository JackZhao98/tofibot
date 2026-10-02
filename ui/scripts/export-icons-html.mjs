import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

// Package the gallery as one portable HTML file, including its downloads.
const directory = fileURLToPath(new URL('../public/icons/', import.meta.url));
const [template, css, script, motionScript, manifestText, archive, contactSheet] = await Promise.all([
  readFile(path.join(directory, 'index.html'), 'utf8'),
  readFile(path.join(directory, 'gallery.css'), 'utf8'),
  readFile(path.join(directory, 'gallery.js'), 'utf8'),
  readFile(path.join(directory, 'motion.js'), 'utf8'),
  readFile(path.join(directory, 'manifest.json'), 'utf8'),
  readFile(path.join(directory, 'tofi-icons.zip')),
  readFile(path.join(directory, 'tofi-contact-sheet.svg')),
]);
const manifest = JSON.parse(manifestText);
const { icons, categories } = manifest;
const names = Object.keys(icons);
const escape = value => String(value).replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
function svg(name, size = 24) {
  const elements = icons[name].nodes.map(([tag, attrs]) => `<${tag} ${Object.entries(attrs).map(([key, value]) => `${key.replace(/[A-Z]/g, letter => `-${letter.toLowerCase()}`)}="${escape(value)}"`).join(' ')}/>`).join('');
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${elements}</svg>`;
}

// Render the complete named grid ahead of time, so viewing requires no scripts.
const sections = categories.map((category, index) => {
  const members = names.filter(name => icons[name].category === category.id);
  return `<section class="icon-section" aria-labelledby="section-${category.id}"><div class="section-heading"><span class="section-index">${String(index + 1).padStart(2, '0')}</span><h3 id="section-${category.id}">${category.label}</h3><span class="section-english">${escape(category.english)}</span><span class="section-count">${members.length} ICONS</span></div><div class="icon-grid">${members.map(name => `<button type="button" class="icon-card" data-name="${name}" aria-label="${icons[name].label}，${name}，查看详情"><span class="card-glyph">${svg(name)}</span><span class="icon-name">${name}</span><span class="icon-label">${icons[name].label}</span></button>`).join('')}</div></section>`;
}).join('');
const sidebar = categories.map(category => `<button type="button" class="category-button" data-category="${category.id}" aria-pressed="false"><span>${svg(category.icon, 17)}</span><span>${category.label}</span><span class="category-count">${names.filter(name => icons[name].category === category.id).length}</span></button>`).join('');

function bundleLinks(content) {
  return content.replace(/href="\.\/(tofi-icons\.zip|tofi-contact-sheet\.svg|manifest\.json)"/g, (_, name) => `href="#bundled-download" data-bundled-file="${name}"`);
}

const downloadsScript = `
(() => {
  const encoded = ${JSON.stringify({ 'tofi-icons.zip': archive.toString('base64'), 'tofi-contact-sheet.svg': contactSheet.toString('base64') })};
  const types = { 'tofi-icons.zip': 'application/zip', 'tofi-contact-sheet.svg': 'image/svg+xml;charset=utf-8', 'manifest.json': 'application/json;charset=utf-8' };
  document.addEventListener('click', event => {
    const link = event.target.closest('[data-bundled-file]');
    if (!link) return;
    event.preventDefault();
    const name = link.dataset.bundledFile;
    const content = name === 'manifest.json'
      ? JSON.stringify(window.TOFI_ICONS, null, 2)
      : Uint8Array.from(atob(encoded[name]), char => char.charCodeAt(0));
    const url = URL.createObjectURL(new Blob([content], { type: types[name] }));
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = name;
    (document.querySelector('dialog[open]') || document.body).append(anchor);
    anchor.click();
    anchor.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  });
})();
`;

let html = template
  .replace('<link rel="icon" type="image/svg+xml" href="./svg/bot.svg" />', `<link rel="icon" type="image/svg+xml" href="data:image/svg+xml;base64,${Buffer.from(svg('bot')).toString('base64')}" />`)
  .replace('<link rel="stylesheet" href="./gallery.css" />', () => `<style>\n${css.replace(/^@charset[^;]+;\s*/, '')}\n</style>`)
  .replace(/\s*<script src="\.\/(?:catalog|motion|gallery)\.js" defer><\/script>/g, '')
  .replace('href="./"', 'href="#hero-title"')
  .replace('<div id="icon-sections"></div>', () => `<div id="icon-sections">${sections}</div>`)
  .replace('<div class="category-list" id="categories"></div>', () => `<div class="category-list" id="categories">${sidebar}</div>`)
  .replace(/<span data-icon="([^"]+)"(?: data-size="(\d+)")?><\/span>/g, (_, name, size) => `<span data-icon="${name}"${size ? ` data-size="${size}"` : ''}>${svg(name, Number(size || 18))}</span>`)
  .replace(/<noscript>[\s\S]*?<\/noscript>/, '<noscript><style>.toolbar,.sidebar,.top-nav,.header-actions,.collection-hint,.variant-bar{display:none}.main{margin-left:0}.icon-card{cursor:default}</style></noscript>');

html = bundleLinks(html).replaceAll('打开完整 SVG 图鉴', '下载完整 SVG 图鉴');
const javascript = `window.TOFI_ICONS = ${JSON.stringify(manifest).replaceAll('<', '\\u003c')};\n${motionScript}\n${downloadsScript}\n${bundleLinks(script).replaceAll('打开 144 枚图标的 SVG 网格图鉴 ↗', '下载 144 枚图标的 SVG 网格图鉴 ↓')}`;
html = html.replace('</body>', () => `<script>\n${javascript.replace(/<\/script/gi, '<\\/script')}\n</script>\n</body>`);

const destination = path.join(directory, 'tofi-icons.html');
await writeFile(destination, html);
console.log(`Created ${destination} (${Math.round(Buffer.byteLength(html) / 1024)} KB); ${names.length} inline SVG icons, no external files or server required.`);
