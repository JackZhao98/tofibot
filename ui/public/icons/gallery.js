(() => {
  'use strict';
  const { icons, categories } = window.TOFI_ICONS;
  const names = Object.keys(icons);
  const root = document.documentElement;
  const search = document.querySelector('#search');
  const dialog = document.querySelector('#detail-dialog');
  const dialogContent = document.querySelector('#dialog-content');
  const toast = document.querySelector('#toast');
  const state = { category: 'all', query: '', size: 24, variant: 'outline', motion: true, stroke: 1.8, color: 'ink', selected: null, codeTab: 'svg' };
  const palette = { ink: 'currentColor', green: '#6c987c', orange: '#c98767', blue: '#6c8fae' };
  let toastTimer;
  const motion = window.TofiMotion.create(window.TOFI_ICONS);
  const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');

  const escape = value => String(value).replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
  const attribute = key => key.replace(/[A-Z]/g, m => `-${m.toLowerCase()}`);
  function svg(name, size = 24, stroke = 1.8, color = 'currentColor', exportable = false, variant = 'outline') {
    const nodes = icons[name][variant === 'filled' ? 'filledNodes' : 'nodes'].map(([tag, attrs], index) => `${exportable ? '' : `<g data-tofi-part="${index}">`}<${tag} ${Object.entries(attrs).map(([key, value]) => `${attribute(key)}="${escape(value)}"`).join(' ')}/>${exportable ? '' : '</g>'}`).join('');
    return `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="${stroke}" stroke-linecap="round" stroke-linejoin="round"${color !== 'currentColor' ? ` color="${color}"` : ''}${exportable ? '' : ' aria-hidden="true" focusable="false"'} data-tofi-icon="${name}" data-tofi-variant="${variant}">${exportable ? nodes : `<g data-tofi-body="">${nodes}</g>`}</svg>`;
  }
  const artwork = (name, size = 24) => svg(name, size, state.stroke, palette[state.color], false, state.variant);
  function hydrate(container = document) {
    container.querySelectorAll('[data-icon]').forEach(element => {
      element.innerHTML = svg(element.dataset.icon, Number(element.dataset.size || 18));
    });
  }
  function notify(message) {
    clearTimeout(toastTimer);
    (dialog.open ? dialog : document.body).append(toast);
    toast.textContent = message;
    toast.classList.add('visible');
    toastTimer = setTimeout(() => toast.classList.remove('visible'), 2600);
  }
  async function copy(text) {
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard API unavailable');
      await navigator.clipboard.writeText(text);
      notify('已复制，带去 Tofi 用吧。');
    } catch {
      const textarea = document.createElement('textarea');
      textarea.value = text;
      textarea.style.cssText = 'position:fixed;left:0;top:0;opacity:0';
      (dialog.open ? dialog : document.body).append(textarea);
      textarea.select();
      let copied = false;
      try { copied = document.execCommand('copy'); } catch { /* Show manual fallback below. */ }
      textarea.remove();
      notify(copied ? '已复制，带去 Tofi 用吧。' : '浏览器限制了复制，请选择代码手动复制。');
    }
  }
  function download(name) {
    const content = svg(name, state.size, state.stroke, palette[state.color], true, state.variant);
    const url = URL.createObjectURL(new Blob([content], { type: 'image/svg+xml;charset=utf-8' }));
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `tofi-${name}${state.variant === 'filled' ? '-filled' : ''}.svg`;
    (dialog.open ? dialog : document.body).append(anchor);
    anchor.click();
    anchor.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  document.querySelector('#categories').innerHTML = categories.map(cat => `<button type="button" class="category-button" data-category="${cat.id}" aria-pressed="false"><span>${svg(cat.icon, 17)}</span><span>${cat.label}</span><span class="category-count">${names.filter(name => icons[name].category === cat.id).length}</span></button>`).join('');
  document.querySelector('#hero-count').textContent = names.length;
  document.querySelector('#all-count').textContent = names.length;

  function render() {
    motion.cancelAll();
    const query = state.query.trim().toLowerCase();
    const words = query.split(/\s+/).filter(Boolean);
    let count = 0;
    const sections = categories.filter(cat => state.category === 'all' || state.category === cat.id).map(cat => {
      const results = names.filter(name => {
        const icon = icons[name];
        const haystack = `${name} ${icon.label} ${icon.tags} ${cat.label} ${cat.english}`.toLowerCase();
        return icon.category === cat.id && words.every(word => haystack.includes(word));
      });
      count += results.length;
      if (!results.length) return '';
      return `<section class="icon-section" aria-labelledby="section-${cat.id}"><div class="section-heading"><span class="section-index">${String(categories.indexOf(cat) + 1).padStart(2, '0')}</span><h3 id="section-${cat.id}">${cat.label}</h3><span class="section-english">${cat.english}</span><span class="section-count">${results.length} ICONS</span></div><div class="icon-grid">${results.map(name => `<button type="button" class="icon-card" data-name="${name}" aria-label="${icons[name].label}，${name}，查看详情"><span class="card-glyph">${artwork(name)}</span><span class="icon-name">${name}</span><span class="icon-label">${icons[name].label}</span></button>`).join('')}</div></section>`;
    });
    document.querySelector('#icon-sections').innerHTML = sections.join('');
    document.querySelector('#empty-state').hidden = count > 0;
    const label = state.category === 'all' ? '全部图标' : categories.find(cat => cat.id === state.category).label;
    document.querySelector('#collection-title').textContent = query ? `${label} · 搜索` : label;
    document.querySelector('#result-count').textContent = count;
    document.querySelectorAll('[data-category]').forEach(button => {
      const selected = button.dataset.category === state.category;
      button.classList.toggle('active', selected);
      button.setAttribute('aria-pressed', String(selected));
    });
  }
  function scrollToCollection() {
    document.querySelector('#collection').scrollIntoView({ behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth' });
  }
  function selectedCode() {
    if (state.codeTab === 'svg') return svg(state.selected, state.size, state.stroke, palette[state.color], true, state.variant);
    return `import { TofiIcon } from './icons';\n\n<TofiIcon name="${state.selected}" size={${state.size}}${state.variant === 'filled' ? ' variant="filled"' : ''}${state.motion ? ' animated' : ''}${state.stroke !== 1.8 ? ` strokeWidth={${state.stroke}}` : ''}${state.color !== 'ink' ? ` color="${palette[state.color]}"` : ''} />`;
  }
  function updateCode() {
    document.querySelector('#icon-code').textContent = selectedCode();
    document.querySelectorAll('[data-code-tab]').forEach(button => {
      const selected = button.dataset.codeTab === state.codeTab;
      button.classList.toggle('active', selected);
      button.setAttribute('aria-pressed', String(selected));
    });
    document.querySelector('#copy-code-label').textContent = state.codeTab === 'svg' ? '复制 SVG' : '复制 React';
  }
  function showDialog() {
    if (!dialog.open) dialog.showModal();
    document.querySelector('#close-dialog').focus();
  }
  function openIcon(name) {
    state.selected = name;
    state.codeTab = 'svg';
    const icon = icons[name];
    const cat = categories.find(cat => cat.id === icon.category);
    document.querySelector('#dialog-eyebrow').textContent = 'A LITTLE CLOSER';
    dialogContent.innerHTML = `<div class="detail-preview">${artwork(name)}</div><div class="motion-detail"><span>${escape(window.TOFI_ICONS.motions[icon.motion.preset].label)} <small>· 单次播放</small></span><button type="button" class="replay-motion" id="replay-motion"><span data-icon="retry" data-size="14"></span>再播放</button></div><div class="detail-title"><h2 id="dialog-title">${name}${state.variant === 'filled' ? '-filled' : ''}</h2><span class="detail-category">${cat.label}</span></div><p class="detail-subtitle">${icon.label} <span>·</span> 24 × 24 网格 <span>·</span> ${state.variant === 'filled' ? '实心 / 加重' : `${state.stroke} px 描边`}</p><div class="size-specimens" aria-label="不同尺寸的图标预览">${[16, 20, 24, 32, 48].map(size => `<div class="size-specimen">${artwork(name, size)}<span>${size} px</span></div>`).join('')}</div><div class="code-tabs" role="group" aria-label="代码格式"><button type="button" class="active" data-code-tab="svg" aria-pressed="true">SVG</button><button type="button" data-code-tab="react" aria-pressed="false">React</button><span>SVG 为静态 · React 可含动效</span></div><pre class="code-block" id="icon-code" tabindex="0" aria-label="可复制的图标代码"></pre><div class="detail-actions"><button type="button" class="button" id="copy-code"><span data-icon="copy"></span><span id="copy-code-label">复制 SVG</span></button><button type="button" class="button secondary" id="download-icon"><span data-icon="download"></span>下载 SVG</button></div><p class="detail-tip">仅有图标的按钮，请在按钮上添加 aria-label。整套下载采用默认 24 px / 1.8 px 规格。</p>`;
    hydrate(dialogContent);
    updateCode();
    showDialog();
    syncMotionControls();
    motion.play(dialogContent.querySelector('.detail-preview svg'), name);
  }
  function openDocs(type) {
    document.querySelector('#dialog-eyebrow').textContent = type === 'specs' ? 'THE DESIGN LANGUAGE' : 'READY FOR TOFI';
    if (type === 'specs') {
      dialogContent.innerHTML = `<h2 class="docs-title" id="dialog-title">一套语言，所有细节。</h2><p class="docs-intro">144 枚为 Tofi 重绘的圆润图标，覆盖 12 类产品场景；每枚配齐描边与实心，共 288 个 SVG。连续圆角、圆头笔画和饱满比例贯穿整套。</p><div class="docs-grid"><div class="spec-item"><b>24 × 24</b><span>基础坐标网格</span></div><div class="spec-item"><b>1.8 px</b><span>标准描边宽度</span></div><div class="spec-item"><b>round</b><span>圆形端点与连接</span></div><div class="spec-item"><b>1 color</b><span>通过 currentColor 继承</span></div></div><h3 class="docs-subtitle">使用约定</h3><ul class="docs-list"><li>推荐 UI 尺寸：16、20、24、32 px；默认 24 px。</li><li>图形以 2–22 坐标为主，光学延伸可进入 1–23。</li><li>图标采用英文 kebab-case 命名，并配有中文含义。</li><li>每枚图标配有语义动效，悬停、键盘聚焦或点击时播放一次；触屏可在详情中点“再播放”。</li><li>动效开关随时可关闭，并遵循系统的减少动态效果偏好。</li><li>状态应同时使用图形或文字表达，避免只依赖颜色。</li><li>Bot 共用双侧小天线与竖胶囊眼睛；实心记忆保留四个内部褶皱。</li><li>实心图形采用透明镂空；箭头、加号等开放图形采用加重笔画。所有格式从同一套路径导出。</li></ul><a class="docs-link" href="./tofi-contact-sheet.svg" target="_blank" rel="noopener">打开 144 枚图标的 SVG 网格图鉴 ↗</a>`;
    } else {
      dialogContent.innerHTML = `<h2 class="docs-title" id="dialog-title">画好了，也准备好用了。</h2><p class="docs-intro">选择任意图标复制代码，或者下载整套 SVG。Tofi 项目内已提供带类型提示的 React 组件。</p><h3 class="docs-subtitle">01 / 在 Tofi React 项目中</h3><pre class="code-block">${escape(`import { TofiIcon } from './icons';\n\n<button aria-label="创建 Bot">\n  <TofiIcon name="bot-add" size={20} animated />\n</button>\n\n<TofiIcon name="memory" variant="filled" title="长期记忆" />`)}</pre><h3 class="docs-subtitle">02 / 使用独立 SVG</h3><pre class="code-block">${escape('<img src="/icons/svg/bot.svg"\n     width="24" height="24" alt="智能伙伴" />')}</pre><p class="detail-tip">内联 SVG 或 React 组件可以继承 CSS color。img 引用的 SVG 不会继承父元素颜色。</p><h3 class="docs-subtitle">03 / 使用 SVG Sprite</h3><pre class="code-block">${escape('<svg width="24" height="24" aria-hidden="true">\n  <use href="/icons/tofi-sprite.svg#tofi-chat" />\n</svg>')}</pre><p class="detail-tip">Sprite 请通过同源 HTTP 服务使用。组件、素材与生成方式详见 ui/src/icons/README.md。</p><div class="detail-actions" style="margin-top:20px"><a class="button" href="./tofi-icons.zip" download><span data-icon="download"></span>下载完整图标库</a><a class="button secondary" href="./manifest.json" download>下载命名清单</a></div>`;
    }
    hydrate(dialogContent);
    showDialog();
  }

  document.addEventListener('click', event => {
    const target = event.target.closest('button, a');
    if (!target) return;
    if (target.id === 'motion-toggle') {
      state.motion = !state.motion;
      syncMotionControls();
    } else if (target.id === 'replay-motion') {
      motion.play(dialogContent.querySelector('.detail-preview svg'), state.selected);
    } else if (target.dataset.category) {
      state.category = target.dataset.category;
      render();
      scrollToCollection();
    } else if (target.dataset.name) {
      openIcon(target.dataset.name);
    } else if (target.dataset.variant) {
      state.variant = target.dataset.variant;
      if (state.variant === 'filled') {
        state.stroke = 1.8;
        document.querySelector('#stroke').value = '1.8';
        root.style.setProperty('--preview-stroke', '1.8');
      }
      document.querySelector('#stroke').disabled = state.variant === 'filled';
      document.querySelectorAll('[data-variant]').forEach(button => {
        const selected = button.dataset.variant === state.variant;
        button.classList.toggle('active', selected);
        button.setAttribute('aria-pressed', String(selected));
      });
      render();
    } else if (target.dataset.sizeChoice) {
      state.size = Number(target.dataset.sizeChoice);
      root.style.setProperty('--preview-size', `${state.size}px`);
      document.querySelectorAll('[data-size-choice]').forEach(button => {
        const selected = Number(button.dataset.sizeChoice) === state.size;
        button.classList.toggle('active', selected);
        button.setAttribute('aria-pressed', String(selected));
      });
    } else if (target.dataset.color) {
      state.color = target.dataset.color;
      root.style.setProperty('--icon-color', state.color === 'ink' ? 'var(--icon-ink)' : palette[state.color]);
      document.querySelectorAll('[data-color]').forEach(button => {
        const selected = button.dataset.color === state.color;
        button.classList.toggle('active', selected);
        button.setAttribute('aria-pressed', String(selected));
      });
    } else if (target.dataset.open) {
      openDocs(target.dataset.open);
    } else if (target.dataset.codeTab) {
      state.codeTab = target.dataset.codeTab;
      updateCode();
    } else if (target.id === 'copy-code') {
      void copy(selectedCode());
    } else if (target.id === 'download-icon') {
      download(state.selected);
    }
  });
  search.addEventListener('input', () => { state.query = search.value; render(); });
  document.querySelector('#stroke').addEventListener('change', event => {
    state.stroke = Number(event.target.value);
    root.style.setProperty('--preview-stroke', String(state.stroke));
  });
  document.querySelector('#reset-search').addEventListener('click', () => {
    state.query = '';
    state.category = 'all';
    search.value = '';
    render();
    search.focus();
  });
  document.querySelector('#close-dialog').addEventListener('click', () => dialog.close());
  dialog.addEventListener('click', event => {
    const bounds = dialog.getBoundingClientRect();
    if (event.target === dialog && (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom)) dialog.close();
  });
  dialog.addEventListener('close', () => {
    dialogContent.querySelectorAll('svg').forEach(svg => motion.cancel(svg));
    toast.classList.remove('visible');
    document.body.append(toast);
  });
  document.addEventListener('keydown', event => {
    if (event.key === '/' && !dialog.open && !event.ctrlKey && !event.metaKey && !event.altKey && !event.target.matches('input, textarea, select, [contenteditable]')) {
      event.preventDefault();
      scrollToCollection();
      search.focus({ preventScroll: true });
    }
  });
  document.querySelector('.theme-toggle').addEventListener('click', event => {
    const dark = root.dataset.theme !== 'dark';
    root.dataset.theme = dark ? 'dark' : 'light';
    const button = event.currentTarget;
    button.setAttribute('aria-label', dark ? '切换浅色模式' : '切换深色模式');
    button.setAttribute('title', dark ? '切换浅色模式' : '切换深色模式');
    button.innerHTML = svg(dark ? 'sun' : 'moon', 18);
  });
  function syncMotionControls() {
    const effective = state.motion && !reducedMotion.matches;
    motion.setEnabled(effective);
    root.dataset.motion = effective ? 'on' : 'off';
    const toggle = document.querySelector('#motion-toggle');
    toggle.setAttribute('aria-pressed', String(state.motion));
    toggle.disabled = reducedMotion.matches;
    toggle.textContent = reducedMotion.matches ? '动效 · 已减弱' : `动效 ${state.motion ? '开' : '关'}`;
    toggle.title = reducedMotion.matches ? '跟随系统的减少动态效果设置' : '悬停、键盘聚焦或点击时播放一次';
    const replay = document.querySelector('#replay-motion');
    if (replay) replay.disabled = !effective;
  }
  function animateControl(control) {
    if (!control || control.matches(':disabled,[aria-disabled="true"]')) return;
    control.querySelectorAll('svg[data-tofi-icon]').forEach(svg => motion.play(svg));
  }
  document.addEventListener('pointerover', event => {
    if (event.pointerType === 'touch') return;
    const control = event.target.closest('button, a');
    if (!control || (event.relatedTarget instanceof Node && control.contains(event.relatedTarget))) return;
    animateControl(control);
  });
  document.addEventListener('focusin', event => {
    const control = event.target.closest('button, a');
    if (control?.matches(':focus-visible')) animateControl(control);
  });
  document.addEventListener('click', event => animateControl(event.target.closest('button, a')));
  reducedMotion.addEventListener('change', syncMotionControls);
  document.addEventListener('visibilitychange', () => { if (document.hidden) motion.cancelAll(); });
  window.addEventListener('pagehide', () => motion.cancelAll());
  hydrate();
  render();
  syncMotionControls();
})();
